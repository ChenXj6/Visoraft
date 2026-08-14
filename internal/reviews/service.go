package reviews

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/visoraft/visoraft/internal/events"
	"github.com/visoraft/visoraft/internal/identity"
	"github.com/visoraft/visoraft/internal/moderation"
	"github.com/visoraft/visoraft/internal/objectstorage"
	"github.com/visoraft/visoraft/internal/publishing"
	"github.com/visoraft/visoraft/internal/settings"
	"github.com/visoraft/visoraft/internal/taskconfig"
	"github.com/visoraft/visoraft/internal/tasks"
)

type subtitleArtifactStorage interface {
	Put(context.Context, string, string, string, []byte) error
}

type Service struct {
	pool          *pgxpool.Pool
	taskStore     *tasks.PostgresStore
	taskService   *tasks.Service
	publishStore  *publishing.PostgresStore
	storage       subtitleArtifactStorage
	storageBucket string
	now           func() time.Time
}

func (s *Service) ConfigureSubtitleArtifacts(
	storage *objectstorage.Client,
	bucket string,
) {
	s.storage = storage
	s.storageBucket = strings.TrimSpace(bucket)
}

func NewService(
	pool *pgxpool.Pool,
	taskStore *tasks.PostgresStore,
	taskService *tasks.Service,
	publishStores ...*publishing.PostgresStore,
) *Service {
	var publishStore *publishing.PostgresStore
	if len(publishStores) > 0 {
		publishStore = publishStores[0]
	}
	return &Service{
		pool:         pool,
		taskStore:    taskStore,
		taskService:  taskService,
		publishStore: publishStore,
		now:          time.Now,
	}
}

func (s *Service) Queue(ctx context.Context, limit int) ([]tasks.Task, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text
		FROM tasks
		WHERE status='awaiting_manual_review'
		  AND review_status IN ('pending','changes_requested')
		  AND archived_at IS NULL
		ORDER BY updated_at
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list review queue: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan review task id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review queue: %w", err)
	}

	result := make([]tasks.Task, 0, len(ids))
	for _, id := range ids {
		task, err := s.taskStore.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, task)
	}
	return result, nil
}

func (s *Service) Detail(ctx context.Context, taskID string) (Detail, error) {
	if !identity.IsUUID(taskID) {
		return Detail{}, tasks.ErrInvalidID
	}
	task, err := s.taskStore.Get(ctx, taskID)
	if err != nil {
		return Detail{}, err
	}
	runs, err := s.listRuns(ctx, taskID)
	if err != nil {
		return Detail{}, err
	}
	actions, err := s.listActions(ctx, taskID)
	if err != nil {
		return Detail{}, err
	}
	subtitles, err := s.listSubtitles(ctx, taskID)
	if err != nil {
		return Detail{}, err
	}
	moderationRuns, err := s.listModerationRuns(ctx, taskID)
	if err != nil {
		return Detail{}, err
	}
	return Detail{
		Task:           task,
		Runs:           runs,
		Actions:        actions,
		Subtitles:      subtitles,
		ModerationRuns: moderationRuns,
	}, nil
}

func (s *Service) Evaluate(ctx context.Context, taskID string) (tasks.Task, error) {
	if !identity.IsUUID(taskID) {
		return tasks.Task{}, tasks.ErrInvalidID
	}
	now := s.now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return tasks.Task{}, fmt.Errorf("begin review evaluation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status          string
		reviewMode      string
		reviewStatus    string
		settingsVersion int64
		snapshotRaw     []byte
		title           string
		description     string
		duration        *int
	)
	err = tx.QueryRow(ctx, `
		SELECT
			status,
			review_mode,
			review_status,
			settings_version,
			settings_snapshot,
			title,
			description,
			duration_seconds
		FROM tasks
		WHERE id=$1
		FOR UPDATE
	`, taskID).Scan(
		&status,
		&reviewMode,
		&reviewStatus,
		&settingsVersion,
		&snapshotRaw,
		&title,
		&description,
		&duration,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return tasks.Task{}, tasks.ErrNotFound
	}
	if err != nil {
		return tasks.Task{}, fmt.Errorf("lock task for review: %w", err)
	}
	if !slices.Contains([]string{"processing", "awaiting_manual_review"}, status) ||
		!slices.Contains([]string{"not_started", "running", "changes_requested"}, reviewStatus) {
		return tasks.Task{}, &ConflictError{
			Code:    "review_not_evaluable",
			Message: "当前任务不在可审核阶段",
		}
	}

	var snapshot settings.ConfigSnapshot
	if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
		return tasks.Task{}, fmt.Errorf("decode review settings snapshot: %w", err)
	}

	var mediaCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM media_assets
		WHERE task_id=$1 AND kind='source' AND status='available'
	`, taskID).Scan(&mediaCount); err != nil {
		return tasks.Task{}, fmt.Errorf("count review media: %w", err)
	}
	var qcScore *float64
	err = tx.QueryRow(ctx, `
		SELECT NULLIF(qc_report->>'score','')::double precision
		FROM subtitle_documents
		WHERE task_id=$1 AND qc_report ? 'score'
		ORDER BY
			CASE WHEN kind='translated' THEN 0 ELSE 1 END,
			version DESC,
			created_at DESC
		LIMIT 1
	`, taskID).Scan(&qcScore)
	if errors.Is(err, pgx.ErrNoRows) {
		qcScore = nil
	} else if err != nil {
		return tasks.Task{}, fmt.Errorf("load subtitle qc score: %w", err)
	}

	ruleResults := evaluateRules(
		snapshot.Review.Rules,
		mediaCount,
		title,
		description,
		duration,
		qcScore,
	)
	forceManualReview := false
	var moderationRunID string
	if snapshot.Moderation.Enabled {
		moderationRun, found, err := loadLatestModerationTx(ctx, tx, taskID)
		if err != nil {
			return tasks.Task{}, err
		}
		moderationRule, forceManual := evaluateModerationRule(
			moderationRun,
			found,
		)
		ruleResults = append(ruleResults, moderationRule)
		forceManualReview = forceManual
		if found {
			moderationRunID = moderationRun.ID
		}
	}
	rulesRaw, err := json.Marshal(ruleResults)
	if err != nil {
		return tasks.Task{}, fmt.Errorf("encode review rules: %w", err)
	}
	allPassed := true
	for _, result := range ruleResults {
		if !result.Passed {
			allPassed = false
			break
		}
	}

	runID, err := identity.NewUUID()
	if err != nil {
		return tasks.Task{}, err
	}
	activeRunID := runID
	stepID, err := identity.NewUUID()
	if err != nil {
		return tasks.Task{}, err
	}

	nextTaskStatus := "awaiting_manual_review"
	nextReviewStatus := "pending"
	runStatus := "pending"
	decision := ""
	summary := "等待人工审核"
	var completedAt *time.Time
	var automaticAction string

	if reviewMode == "automatic" {
		runStatus = "completed"
		completedAt = &now
		if forceManualReview {
			decision = "manual_required"
			summary = "内容安全审核要求人工复核，任务已进入人工审核队列"
			automaticAction = "automatic_moderation_fallback"
		} else if allPassed {
			nextTaskStatus = "ready_to_publish"
			nextReviewStatus = "approved"
			decision = "approved"
			summary = "自动审核规则全部通过"
			automaticAction = "automatic_approve"
		} else if snapshot.Review.AutomaticFallback == "reject" {
			nextTaskStatus = "failed"
			nextReviewStatus = "rejected"
			decision = "rejected"
			summary = "自动审核未通过，策略要求拒绝"
			automaticAction = "automatic_reject"
		} else {
			decision = "manual_required"
			summary = "自动审核未通过，已转入人工审核"
			automaticAction = "automatic_fallback"
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO review_runs (
			id, task_id, mode, policy_version, status, decision,
			rule_results, summary, started_at, completed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, runID, taskID, reviewMode, settingsVersion, runStatus, decision, rulesRaw, summary, now, completedAt); err != nil {
		return tasks.Task{}, fmt.Errorf("insert review run: %w", err)
	}
	if reviewMode == "automatic" && nextReviewStatus == "pending" {
		manualRunID, err := identity.NewUUID()
		if err != nil {
			return tasks.Task{}, err
		}
		activeRunID = manualRunID
		if _, err := tx.Exec(ctx, `
			INSERT INTO review_runs (
				id, task_id, mode, policy_version, status, decision,
				rule_results, summary, started_at
			) VALUES ($1,$2,'manual',$3,'pending','',$4,$5,$6)
		`, manualRunID, taskID, settingsVersion, rulesRaw, "等待人工审核", now); err != nil {
			return tasks.Task{}, fmt.Errorf("insert automatic fallback manual review run: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_steps (
			id, task_id, kind, status, attempt, progress,
			started_at, finished_at, updated_at
		) VALUES (
			$1,$2,'review',$3,1,$4,$5,$6,$5
		)
		ON CONFLICT (task_id, kind) DO UPDATE SET
			status=EXCLUDED.status,
			progress=EXCLUDED.progress,
			started_at=COALESCE(task_steps.started_at, EXCLUDED.started_at),
			finished_at=EXCLUDED.finished_at,
			updated_at=EXCLUDED.updated_at,
			error_code='',
			error_message=''
	`, stepID, taskID, mapReviewStepStatus(nextReviewStatus), reviewProgress(nextReviewStatus), now, completedAt); err != nil {
		return tasks.Task{}, fmt.Errorf("upsert review step: %w", err)
	}

	reviewSummaryRaw, _ := json.Marshal(map[string]any{
		"run_id":            activeRunID,
		"evaluation_id":     runID,
		"mode":              reviewMode,
		"decision":          decision,
		"all_passed":        allPassed,
		"rule_results":      ruleResults,
		"moderation_run_id": moderationRunID,
		"summary":           summary,
	})
	errorCode := ""
	errorMessage := ""
	errorRetryable := false
	if nextReviewStatus == "rejected" {
		errorCode = "automatic_review_rejected"
		errorMessage = summary
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET
			status=$2,
			review_status=$3,
			review_summary=$4,
			error_code=$5,
			error_message=$6,
			error_retryable=$7,
			updated_at=$8,
			version=version+1
		WHERE id=$1
	`, taskID, nextTaskStatus, nextReviewStatus, reviewSummaryRaw, errorCode, errorMessage, errorRetryable, now); err != nil {
		return tasks.Task{}, fmt.Errorf("update reviewed task: %w", err)
	}

	if automaticAction != "" {
		if err := insertAction(
			ctx,
			tx,
			taskID,
			&runID,
			automaticAction,
			"system",
			"automatic-review",
			summary,
			nil,
			map[string]any{"rule_results": ruleResults},
			now,
		); err != nil {
			return tasks.Task{}, err
		}
	}
	if err := insertAudit(ctx, tx, taskID, "review.evaluated", map[string]any{
		"run_id":               runID,
		"mode":                 reviewMode,
		"decision":             decision,
		"all_passed":           allPassed,
		"moderation_run_id":    moderationRunID,
		"forced_manual_review": forceManualReview,
	}, now); err != nil {
		return tasks.Task{}, err
	}
	if nextReviewStatus == "approved" && s.publishStore != nil {
		if _, err := s.publishStore.PrepareApprovedTaskTx(
			ctx,
			tx,
			taskID,
			true,
			now,
		); err != nil {
			return tasks.Task{}, fmt.Errorf("prepare approved task for publishing: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return tasks.Task{}, fmt.Errorf("commit review evaluation: %w", err)
	}
	return s.taskStore.Get(ctx, taskID)
}

func (s *Service) UpdateMetadata(
	ctx context.Context,
	taskID string,
	input MetadataInput,
) (Detail, error) {
	if !identity.IsUUID(taskID) {
		return Detail{}, tasks.ErrInvalidID
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.Category = strings.TrimSpace(input.Category)
	input.Reason = strings.TrimSpace(input.Reason)
	input.Tags = normalizeTags(input.Tags)
	fields := map[string]string{}
	if input.Title == "" {
		fields["title"] = "标题不能为空"
	}
	if len([]rune(input.Title)) > 200 {
		fields["title"] = "标题不能超过 200 个字符"
	}
	if len([]rune(input.Description)) > 20000 {
		fields["description"] = "简介不能超过 20000 个字符"
	}
	if len(input.Tags) > 20 {
		fields["tags"] = "标签不能超过 20 个"
	}
	if input.Reason == "" {
		fields["reason"] = "请填写修改原因"
	}
	if len(fields) > 0 {
		return Detail{}, &ValidationError{Fields: fields}
	}

	now := s.now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Detail{}, fmt.Errorf("begin metadata update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `
		SELECT status
		FROM tasks
		WHERE id=$1
		FOR UPDATE
	`, taskID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, tasks.ErrNotFound
	} else if err != nil {
		return Detail{}, fmt.Errorf("lock task metadata: %w", err)
	}
	if status != "awaiting_manual_review" {
		return Detail{}, &ConflictError{
			Code:    "metadata_not_editable",
			Message: "只有待人工审核任务可以修改元数据",
		}
	}
	var version int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(max(version),0) + 1
		FROM task_metadata_versions
		WHERE task_id=$1
	`, taskID).Scan(&version); err != nil {
		return Detail{}, fmt.Errorf("calculate metadata version: %w", err)
	}
	versionID, err := identity.NewUUID()
	if err != nil {
		return Detail{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_metadata_versions (
			id, task_id, version, title, description, tags, category,
			actor_type, actor_id, change_reason, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,'user','local-operator',$8,$9)
	`, versionID, taskID, version, input.Title, input.Description, input.Tags, input.Category, input.Reason, now); err != nil {
		return Detail{}, fmt.Errorf("insert metadata version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET title=$2, description=$3, tags=$4, category=$5,
		    updated_at=$6, version=version+1
		WHERE id=$1
	`, taskID, input.Title, input.Description, input.Tags, input.Category, now); err != nil {
		return Detail{}, fmt.Errorf("update task metadata: %w", err)
	}
	if err := insertAudit(ctx, tx, taskID, "review.metadata.updated", map[string]any{
		"metadata_version": version,
		"reason":           input.Reason,
	}, now); err != nil {
		return Detail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Detail{}, fmt.Errorf("commit metadata update: %w", err)
	}
	return s.Detail(ctx, taskID)
}

func (s *Service) UpdateSubtitle(
	ctx context.Context,
	taskID string,
	documentID string,
	input SubtitleInput,
) (Detail, error) {
	if !identity.IsUUID(taskID) {
		return Detail{}, tasks.ErrInvalidID
	}
	if !identity.IsUUID(documentID) {
		return Detail{}, &ValidationError{
			Fields: map[string]string{"document_id": "字幕文档 ID 格式无效"},
		}
	}
	input.Reason = strings.TrimSpace(input.Reason)
	segments, fields := normalizeSubtitleSegments(input.Segments)
	if input.ExpectedVersion < 1 {
		fields["expected_version"] = "缺少有效的字幕版本"
	}
	if input.Reason == "" {
		fields["reason"] = "请填写字幕修改原因"
	}
	if len([]rune(input.Reason)) > 1000 {
		fields["reason"] = "字幕修改原因不能超过 1000 个字符"
	}
	if len(fields) > 0 {
		return Detail{}, &ValidationError{Fields: fields}
	}

	now := s.now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Detail{}, fmt.Errorf("begin subtitle update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status      string
		snapshotRaw []byte
	)
	err = tx.QueryRow(ctx, `
		SELECT status, settings_snapshot
		FROM tasks
		WHERE id=$1
		FOR UPDATE
	`, taskID).Scan(&status, &snapshotRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, tasks.ErrNotFound
	}
	if err != nil {
		return Detail{}, fmt.Errorf("lock task subtitle: %w", err)
	}
	if status != "awaiting_manual_review" {
		return Detail{}, &ConflictError{
			Code:    "subtitle_not_editable",
			Message: "只有待人工审核任务可以修订字幕",
		}
	}

	var (
		kind           string
		language       string
		currentVersion int
	)
	err = tx.QueryRow(ctx, `
		SELECT kind, language, version
		FROM subtitle_documents
		WHERE id=$1 AND task_id=$2
	`, documentID, taskID).Scan(&kind, &language, &currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, &ValidationError{
			Fields: map[string]string{"document_id": "字幕文档不存在或不属于当前任务"},
		}
	}
	if err != nil {
		return Detail{}, fmt.Errorf("load subtitle document: %w", err)
	}
	var latestVersion int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(max(version),0)
		FROM subtitle_documents
		WHERE task_id=$1 AND kind=$2
	`, taskID, kind).Scan(&latestVersion); err != nil {
		return Detail{}, fmt.Errorf("load latest subtitle version: %w", err)
	}
	if currentVersion != input.ExpectedVersion || currentVersion != latestVersion {
		return Detail{}, &ConflictError{
			Code:    "subtitle_version_conflict",
			Message: "字幕已被其他操作更新，请刷新后重试",
		}
	}

	nextVersion := latestVersion + 1

	var snapshot settings.ConfigSnapshot
	if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
		return Detail{}, fmt.Errorf("decode subtitle settings snapshot: %w", err)
	}
	qcReport := subtitleQualityReport(
		segments,
		snapshot.Subtitle.QC.Threshold,
		snapshot.Subtitle.Postprocess.MinimumCueSeconds,
	)
	segmentsRaw, err := json.Marshal(segments)
	if err != nil {
		return Detail{}, fmt.Errorf("encode subtitle segments: %w", err)
	}
	qcRaw, err := json.Marshal(qcReport)
	if err != nil {
		return Detail{}, fmt.Errorf("encode subtitle qc report: %w", err)
	}
	nextDocumentID, err := identity.NewUUID()
	if err != nil {
		return Detail{}, err
	}
	artifacts, err := s.uploadSubtitleArtifacts(
		ctx,
		taskID,
		kind,
		nextVersion,
		segments,
		qcReport,
	)
	if err != nil {
		return Detail{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO subtitle_documents (
			id, task_id, kind, language, version, segments,
			qc_report, source, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,'edited',$8)
	`, nextDocumentID, taskID, kind, language, nextVersion, segmentsRaw, qcRaw, now); err != nil {
		return Detail{}, fmt.Errorf("insert subtitle version: %w", err)
	}
	artifactKinds := []string{
		"subtitle_" + kind + "_vtt",
		"subtitle_" + kind + "_srt",
		"subtitle_" + kind + "_qc",
	}
	if _, err := tx.Exec(ctx, `
		UPDATE media_assets
		SET status='deleted', deleted_at=$3
		WHERE task_id=$1 AND kind=ANY($2::text[]) AND status='available'
	`, taskID, artifactKinds, now); err != nil {
		return Detail{}, fmt.Errorf("supersede previous subtitle artifacts: %w", err)
	}
	for _, artifact := range artifacts {
		assetID, err := identity.NewUUID()
		if err != nil {
			return Detail{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO media_assets (
				id, task_id, kind, bucket, object_key, original_name,
				content_type, size_bytes, checksum_sha256, status, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'available',$10)
		`,
			assetID,
			taskID,
			artifact.kind,
			s.storageBucket,
			artifact.objectKey,
			artifact.originalName,
			artifact.contentType,
			len(artifact.content),
			artifact.checksum,
			now,
		); err != nil {
			return Detail{}, fmt.Errorf("insert edited subtitle artifact: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET updated_at=$2, version=version+1
		WHERE id=$1
	`, taskID, now); err != nil {
		return Detail{}, fmt.Errorf("touch task after subtitle update: %w", err)
	}

	var activeRunID *string
	var rawRunID string
	err = tx.QueryRow(ctx, `
		SELECT id::text
		FROM review_runs
		WHERE task_id=$1
		  AND status IN ('pending','running')
		ORDER BY started_at DESC
		LIMIT 1
	`, taskID).Scan(&rawRunID)
	if err == nil {
		activeRunID = &rawRunID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, fmt.Errorf("load active review run for subtitle update: %w", err)
	}
	if err := insertAction(
		ctx,
		tx,
		taskID,
		activeRunID,
		"subtitle_edit",
		"user",
		"local-operator",
		input.Reason,
		nil,
		map[string]any{
			"previous_document_id": documentID,
			"document_id":          nextDocumentID,
			"kind":                 kind,
			"language":             language,
			"previous_version":     currentVersion,
			"version":              nextVersion,
			"qc_score":             qcReport["score"],
		},
		now,
	); err != nil {
		return Detail{}, err
	}
	if err := insertAudit(ctx, tx, taskID, "review.subtitle.updated", map[string]any{
		"previous_document_id": documentID,
		"document_id":          nextDocumentID,
		"kind":                 kind,
		"language":             language,
		"previous_version":     currentVersion,
		"version":              nextVersion,
		"reason":               input.Reason,
		"qc_score":             qcReport["score"],
	}, now); err != nil {
		return Detail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Detail{}, fmt.Errorf("commit subtitle update: %w", err)
	}
	return s.Detail(ctx, taskID)
}

func (s *Service) Act(
	ctx context.Context,
	taskID string,
	action string,
	input ActionInput,
) (Detail, error) {
	if !identity.IsUUID(taskID) {
		return Detail{}, tasks.ErrInvalidID
	}
	action = strings.TrimSpace(action)
	input.Reason = strings.TrimSpace(input.Reason)
	if !slices.Contains([]string{"approve", "request_changes", "resubmit", "reprocess_subtitles", "abandon"}, action) {
		return Detail{}, &ValidationError{
			Fields: map[string]string{"action": "未知的审核操作"},
		}
	}
	if action != "approve" && input.Reason == "" {
		return Detail{}, &ValidationError{
			Fields: map[string]string{"reason": "该操作必须填写原因"},
		}
	}

	now := s.now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Detail{}, fmt.Errorf("begin review action: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status, reviewStatus, sourceURL string
	var cookieProfileID *string
	var settingsSnapshot []byte
	if err := tx.QueryRow(ctx, `
		SELECT
			status,
			review_status,
			source_url,
			cookie_profile_id::text,
			settings_snapshot
		FROM tasks
		WHERE id=$1
		FOR UPDATE
	`, taskID).Scan(
		&status,
		&reviewStatus,
		&sourceURL,
		&cookieProfileID,
		&settingsSnapshot,
	); errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, tasks.ErrNotFound
	} else if err != nil {
		return Detail{}, fmt.Errorf("lock task review action: %w", err)
	}
	if status != "awaiting_manual_review" {
		return Detail{}, &ConflictError{
			Code:    "review_action_not_allowed",
			Message: "当前任务不在人工审核队列",
		}
	}
	if action == "resubmit" && reviewStatus != "changes_requested" {
		return Detail{}, &ConflictError{
			Code:    "review_resubmit_not_allowed",
			Message: "只有已退回修改的任务可以重新提交",
		}
	}
	if action != "resubmit" &&
		!slices.Contains([]string{"pending", "changes_requested"}, reviewStatus) {
		return Detail{}, &ConflictError{
			Code:    "review_action_not_allowed",
			Message: "当前审核状态不允许此操作",
		}
	}

	var runID *string
	var rawRunID string
	err = tx.QueryRow(ctx, `
		SELECT id::text
		FROM review_runs
		WHERE task_id=$1
		  AND status IN ('pending','running')
		ORDER BY started_at DESC
		LIMIT 1
	`, taskID).Scan(&rawRunID)
	if err == nil {
		runID = &rawRunID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, fmt.Errorf("load active review run: %w", err)
	}

	var metadataVersion *int
	var currentMetadataVersion int
	if err := tx.QueryRow(ctx, `
		SELECT max(version)
		FROM task_metadata_versions
		WHERE task_id=$1
	`, taskID).Scan(&metadataVersion); err != nil {
		return Detail{}, fmt.Errorf("load metadata version: %w", err)
	}
	if metadataVersion != nil {
		currentMetadataVersion = *metadataVersion
	}

	nextStatus := status
	nextReviewStatus := reviewStatus
	eventType := "review." + action
	switch action {
	case "approve":
		nextStatus = "ready_to_publish"
		nextReviewStatus = "approved"
	case "request_changes":
		nextReviewStatus = "changes_requested"
	case "resubmit":
		nextStatus = "processing"
		nextReviewStatus = "not_started"
	case "reprocess_subtitles":
		nextStatus = "processing"
		nextReviewStatus = "not_started"
	case "abandon":
		nextStatus = "abandoned"
		nextReviewStatus = "abandoned"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET
			status=$2,
			review_status=$3,
			review_summary=review_summary || jsonb_build_object(
				'last_action', $4::text,
				'last_reason', $5::text,
				'last_action_at', $6::timestamptz
			),
			updated_at=$6,
			version=version+1
		WHERE id=$1
	`, taskID, nextStatus, nextReviewStatus, action, input.Reason, now); err != nil {
		return Detail{}, fmt.Errorf("update task review action: %w", err)
	}
	rerunTranscode := false
	if action == "reprocess_subtitles" {
		var currentAttempt int
		if err := tx.QueryRow(ctx, `
			SELECT attempt
			FROM task_steps
			WHERE task_id=$1 AND kind='subtitles'
			FOR UPDATE
		`, taskID).Scan(&currentAttempt); err != nil {
			return Detail{}, fmt.Errorf("load subtitle attempt for reprocessing: %w", err)
		}
		nextAttempt := currentAttempt + 1
		command, err := events.New(
			tasks.SubtitleRequestedV1,
			"visoraft/control-api",
			"task/"+taskID,
			now,
			map[string]any{
				"task_id":           taskID,
				"source_url":        sourceURL,
				"cookie_profile_id": cookieProfileID,
				"attempt":           nextAttempt,
			},
		)
		if err != nil {
			return Detail{}, err
		}
		rawCommand, err := json.Marshal(command)
		if err != nil {
			return Detail{}, fmt.Errorf("encode subtitle reprocess command: %w", err)
		}
		outboxID, err := identity.NewUUID()
		if err != nil {
			return Detail{}, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='queued', attempt=$3, progress=0, detail='{}'::jsonb,
				error_code='', error_message='', started_at=NULL,
				finished_at=NULL, updated_at=$2
			WHERE task_id=$1 AND kind='subtitles'
		`, taskID, now, nextAttempt); err != nil {
			return Detail{}, fmt.Errorf("reset subtitle step for reprocessing: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM task_steps
			WHERE task_id=$1 AND kind=ANY($2::text[])
		`, taskID, []string{
			tasks.StepTranscode,
			tasks.StepModeration,
			tasks.StepReview,
			tasks.StepPublish,
		}); err != nil {
			return Detail{}, fmt.Errorf("reset downstream review steps: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE media_assets
			SET status='deleted', deleted_at=$2
			WHERE task_id=$1
			  AND status='available'
			  AND (kind='transcoded' OR kind LIKE 'subtitle_%')
		`, taskID, now); err != nil {
			return Detail{}, fmt.Errorf("hide superseded review assets: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_messages (
				id, aggregate_id, event_type, payload, status,
				attempts, available_at, created_at
			) VALUES ($1,$2,$3,$4,'pending',0,$5,$5)
		`, outboxID, taskID, tasks.SubtitleRequestedV1, rawCommand, now); err != nil {
			return Detail{}, fmt.Errorf("enqueue subtitle reprocessing: %w", err)
		}
	}
	if action == "resubmit" {
		var snapshot settings.ConfigSnapshot
		if err := json.Unmarshal(settingsSnapshot, &snapshot); err != nil {
			return Detail{}, fmt.Errorf("decode review resubmit settings: %w", err)
		}
		if snapshot.Transcode.Enabled && snapshot.Transcode.BurnSubtitles {
			if err := s.enqueueReviewTranscodeTx(
				ctx,
				tx,
				taskID,
				settingsSnapshot,
				now,
			); err != nil {
				return Detail{}, err
			}
			rerunTranscode = true
		}
	}
	if action == "approve" || action == "abandon" {
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status=$2, progress=$3, finished_at=$4, updated_at=$4
			WHERE task_id=$1 AND kind='review'
		`, taskID, mapReviewStepStatus(nextReviewStatus), reviewProgress(nextReviewStatus), now); err != nil {
			return Detail{}, fmt.Errorf("finish review step: %w", err)
		}
	} else if action == "request_changes" {
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='running', progress=50, finished_at=NULL, updated_at=$2
			WHERE task_id=$1 AND kind='review'
		`, taskID, now); err != nil {
			return Detail{}, fmt.Errorf("keep review step open for changes: %w", err)
		}
	}
	if action == "approve" || action == "request_changes" || action == "reprocess_subtitles" || action == "abandon" {
		if runID != nil {
			decision := "approved"
			if action == "request_changes" {
				decision = "changes_requested"
			} else if action == "reprocess_subtitles" {
				decision = "changes_requested"
			} else if action == "abandon" {
				decision = "rejected"
			}
			if _, err := tx.Exec(ctx, `
				UPDATE review_runs
				SET status='completed', decision=$2, summary=$3, completed_at=$4
				WHERE id=$1
			`, *runID, decision, input.Reason, now); err != nil {
				return Detail{}, fmt.Errorf("finish review run: %w", err)
			}
		}
	}
	if err := insertAction(
		ctx,
		tx,
		taskID,
		runID,
		action,
		"user",
		"local-operator",
		input.Reason,
		metadataVersion,
		map[string]any{
			"delete_assets":   input.DeleteAssets,
			"reprocess_stage": map[bool]string{true: "subtitles"}[action == "reprocess_subtitles"],
			"rerun_transcode": rerunTranscode,
		},
		now,
	); err != nil {
		return Detail{}, err
	}
	if err := insertAudit(ctx, tx, taskID, eventType, map[string]any{
		"reason":           input.Reason,
		"metadata_version": currentMetadataVersion,
		"delete_assets":    input.DeleteAssets,
	}, now); err != nil {
		return Detail{}, err
	}
	if action == "approve" && s.publishStore != nil {
		if _, err := s.publishStore.PrepareApprovedTaskTx(
			ctx,
			tx,
			taskID,
			true,
			now,
		); err != nil {
			return Detail{}, fmt.Errorf("prepare approved task for publishing: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Detail{}, fmt.Errorf("commit review action: %w", err)
	}

	if action == "resubmit" && !rerunTranscode {
		if _, err := s.Evaluate(ctx, taskID); err != nil {
			return Detail{}, err
		}
	}
	if action == "abandon" && input.DeleteAssets {
		if _, err := s.taskService.DeleteAssets(ctx, taskID); err != nil {
			var conflict *tasks.ConflictError
			if !errors.As(err, &conflict) || conflict.Code != "assets_not_available" {
				return Detail{}, fmt.Errorf("abandon task but asset cleanup failed: %w", err)
			}
		}
	}
	return s.Detail(ctx, taskID)
}

func (s *Service) enqueueReviewTranscodeTx(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	settingsSnapshot []byte,
	now time.Time,
) error {
	var currentAttempt int
	if err := tx.QueryRow(ctx, `
		SELECT attempt
		FROM task_steps
		WHERE task_id=$1 AND kind='transcode'
		FOR UPDATE
	`, taskID).Scan(&currentAttempt); err != nil {
		return fmt.Errorf("load transcode step for review resubmit: %w", err)
	}
	nextAttempt := currentAttempt + 1

	var inputAssetID string
	if err := tx.QueryRow(ctx, `
		SELECT id::text
		FROM media_assets
		WHERE task_id=$1 AND kind='source' AND status='available'
		ORDER BY created_at DESC
		LIMIT 1
	`, taskID).Scan(&inputAssetID); err != nil {
		return fmt.Errorf("load review transcode source asset: %w", err)
	}
	policy, err := taskconfig.Decode(settingsSnapshot)
	if err != nil {
		return err
	}
	var presetID *string
	if policy.PostingStrategy != nil {
		presetID = policy.PostingStrategy.TranscodePresetID
	} else if err := tx.QueryRow(ctx, `
		SELECT strategy.transcode_preset_id::text
		FROM tasks task
		LEFT JOIN posting_strategies strategy
		  ON strategy.id=task.posting_strategy_id
		WHERE task.id=$1
	`, taskID).Scan(&presetID); err != nil {
		return fmt.Errorf("load review transcode preset: %w", err)
	}

	runID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	outboxID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	command, err := events.New(
		tasks.TranscodeRequestedV1,
		"visoraft/control-api",
		"task/"+taskID,
		now,
		map[string]any{
			"task_id": taskID,
			"run_id":  runID,
			"attempt": nextAttempt,
		},
	)
	if err != nil {
		return err
	}
	rawCommand, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("encode review transcode command: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcode_runs (
			id, task_id, preset_id, status, attempt, input_asset_id,
			command_summary, progress, created_at, updated_at
		) VALUES ($1,$2,$3,'queued',$4,$5,$6,0,$7,$7)
	`,
		runID,
		taskID,
		presetID,
		nextAttempt,
		inputAssetID,
		map[string]any{
			"reason":         "review_subtitle_edit",
			"burn_subtitles": true,
		},
		now,
	); err != nil {
		return fmt.Errorf("insert review transcode run: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_steps
		SET status='queued', attempt=$3, progress=0, detail='{}'::jsonb,
			error_code='', error_message='', started_at=NULL,
			finished_at=NULL, updated_at=$2
		WHERE task_id=$1 AND kind='transcode'
	`, taskID, now, nextAttempt); err != nil {
		return fmt.Errorf("reset transcode step after subtitle edit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM task_steps
		WHERE task_id=$1 AND kind=ANY($2::text[])
	`, taskID, []string{
		tasks.StepModeration,
		tasks.StepReview,
		tasks.StepPublish,
	}); err != nil {
		return fmt.Errorf("reset downstream steps after subtitle edit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE media_assets
		SET status='deleted', deleted_at=$2
		WHERE task_id=$1 AND kind='transcoded' AND status='available'
	`, taskID, now); err != nil {
		return fmt.Errorf("supersede transcoded asset after subtitle edit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (
			id, aggregate_id, event_type, payload, status,
			attempts, available_at, created_at
		) VALUES ($1,$2,$3,$4,'pending',0,$5,$5)
	`, outboxID, taskID, tasks.TranscodeRequestedV1, rawCommand, now); err != nil {
		return fmt.Errorf("enqueue review transcode command: %w", err)
	}
	return nil
}

func evaluateRules(
	rules settings.AutomaticReviewRules,
	mediaCount int,
	title string,
	description string,
	duration *int,
	qcScore *float64,
) []RuleResult {
	results := make([]RuleResult, 0, 5)
	if rules.RequireMedia {
		results = append(results, RuleResult{
			Key:      "media_present",
			Label:    "媒体文件可用",
			Passed:   mediaCount > 0,
			Expected: "至少 1 个源媒体",
			Actual:   mediaCount,
			Message:  passMessage(mediaCount > 0, "媒体文件已登记", "没有可用媒体文件"),
		})
	}
	if rules.RequireTitle {
		passed := strings.TrimSpace(title) != ""
		results = append(results, RuleResult{
			Key:      "title_present",
			Label:    "标题完整",
			Passed:   passed,
			Expected: "非空标题",
			Actual:   len([]rune(strings.TrimSpace(title))),
			Message:  passMessage(passed, "标题已填写", "标题为空"),
		})
	}
	if rules.MinimumDescription > 0 {
		actual := len([]rune(strings.TrimSpace(description)))
		passed := actual >= rules.MinimumDescription
		results = append(results, RuleResult{
			Key:      "description_length",
			Label:    "简介长度",
			Passed:   passed,
			Expected: rules.MinimumDescription,
			Actual:   actual,
			Message:  passMessage(passed, "简介长度符合要求", "简介过短"),
		})
	}
	if rules.MaximumDurationSeconds > 0 {
		actual := 0
		if duration != nil {
			actual = *duration
		}
		passed := duration != nil && actual <= rules.MaximumDurationSeconds
		results = append(results, RuleResult{
			Key:      "duration_limit",
			Label:    "媒体时长",
			Passed:   passed,
			Expected: rules.MaximumDurationSeconds,
			Actual:   actual,
			Message:  passMessage(passed, "媒体时长符合限制", "媒体时长超限或未知"),
		})
	}
	if rules.RequireSubtitleQC {
		actual := 0.0
		if qcScore != nil {
			actual = math.Round(*qcScore*100) / 100
		}
		passed := qcScore != nil && actual >= float64(rules.MinimumSubtitleQCScore)
		results = append(results, RuleResult{
			Key:      "subtitle_qc",
			Label:    "字幕质检",
			Passed:   passed,
			Expected: rules.MinimumSubtitleQCScore,
			Actual:   actual,
			Message:  passMessage(passed, "字幕质检达到阈值", "字幕质检缺失或未达阈值"),
		})
	}
	return results
}

func evaluateModerationRule(
	run ModerationRun,
	found bool,
) (RuleResult, bool) {
	result := RuleResult{
		Key:      "content_moderation",
		Label:    "内容安全审核",
		Passed:   false,
		Expected: "内容审核完成且决策为通过",
		Actual: map[string]any{
			"status":     "missing",
			"decision":   "",
			"risk_level": "unknown",
		},
		Message: "尚未取得内容审核结果，必须转入人工复核",
	}
	if !found {
		return result, true
	}
	result.Actual = map[string]any{
		"provider":   run.Provider,
		"status":     run.Status,
		"decision":   run.Decision,
		"risk_level": run.RiskLevel,
		"run_id":     run.ID,
	}
	switch {
	case run.Status == "passed" && run.Decision == moderation.DecisionPass:
		result.Passed = true
		result.Message = "内容审核已完成，未发现需要阻断或人工复核的风险"
		return result, false
	case run.Decision == moderation.DecisionManualReview:
		result.Message = "内容审核要求人工复核，自动审核不会直接批准或拒绝"
		return result, true
	case run.Decision == moderation.DecisionBlock:
		result.Message = "内容审核判定为阻断，任务不得进入投稿流程"
		return result, false
	default:
		result.Message = "内容审核结果不完整，必须转入人工复核"
		return result, true
	}
}

func passMessage(passed bool, success, failure string) string {
	if passed {
		return success
	}
	return failure
}

func loadLatestModerationTx(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
) (ModerationRun, bool, error) {
	run, err := scanModerationRun(tx.QueryRow(ctx, `
		SELECT
			id::text,
			provider,
			status,
			attempt,
			policy_snapshot,
			text_result,
			image_result,
			video_result,
			decision,
			error_code,
			error_message,
			started_at,
			completed_at,
			created_at,
			updated_at
		FROM moderation_runs
		WHERE task_id=$1
		ORDER BY created_at DESC
		LIMIT 1
	`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ModerationRun{}, false, nil
	}
	if err != nil {
		return ModerationRun{}, false, fmt.Errorf(
			"load latest moderation result for review: %w",
			err,
		)
	}
	return run, true, nil
}

func mapReviewStepStatus(reviewStatus string) string {
	switch reviewStatus {
	case "approved":
		return "succeeded"
	case "rejected", "abandoned":
		return "failed"
	default:
		return "running"
	}
}

func reviewProgress(reviewStatus string) int {
	switch reviewStatus {
	case "approved":
		return 100
	case "rejected", "abandoned":
		return 100
	default:
		return 50
	}
}

func normalizeTags(raw []string) []string {
	result := make([]string, 0, len(raw))
	for _, value := range raw {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == '，' || r == '\n'
		}) {
			tag := strings.TrimSpace(part)
			if tag == "" || slices.Contains(result, tag) {
				continue
			}
			result = append(result, tag)
		}
	}
	return result
}

func normalizeSubtitleSegments(raw []Segment) ([]Segment, map[string]string) {
	fields := map[string]string{}
	if len(raw) == 0 {
		fields["segments"] = "字幕至少需要一个片段"
		return nil, fields
	}
	if len(raw) > 5000 {
		fields["segments"] = "字幕片段不能超过 5000 条"
		return nil, fields
	}
	result := make([]Segment, 0, len(raw))
	totalCharacters := 0
	previousStart := -1.0
	for index, item := range raw {
		prefix := fmt.Sprintf("segments.%d", index)
		item.Text = strings.TrimSpace(item.Text)
		if item.Text == "" {
			fields[prefix+".text"] = "字幕文本不能为空"
		}
		if len([]rune(item.Text)) > 2000 {
			fields[prefix+".text"] = "单条字幕不能超过 2000 个字符"
		}
		if math.IsNaN(item.Start) || math.IsInf(item.Start, 0) || item.Start < 0 {
			fields[prefix+".start"] = "开始时间必须是大于等于 0 的有限数值"
		}
		if math.IsNaN(item.End) || math.IsInf(item.End, 0) || item.End <= item.Start {
			fields[prefix+".end"] = "结束时间必须晚于开始时间"
		}
		if index > 0 && item.Start < previousStart {
			fields[prefix+".start"] = "字幕必须按开始时间排序"
		}
		item.Index = index + 1
		totalCharacters += len([]rune(item.Text))
		previousStart = item.Start
		result = append(result, item)
	}
	if totalCharacters > 200000 {
		fields["segments"] = "字幕总字符数不能超过 200000"
	}
	return result, fields
}

type subtitleArtifact struct {
	kind         string
	objectKey    string
	originalName string
	contentType  string
	content      []byte
	checksum     string
}

func (s *Service) uploadSubtitleArtifacts(
	ctx context.Context,
	taskID string,
	kind string,
	version int,
	segments []Segment,
	qcReport map[string]any,
) ([]subtitleArtifact, error) {
	if s.storage == nil || s.storageBucket == "" {
		return nil, errors.New("subtitle artifact storage is not configured")
	}
	qcContent, err := json.MarshalIndent(qcReport, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode edited subtitle QC artifact: %w", err)
	}
	qcContent = append(qcContent, '\n')
	baseName := fmt.Sprintf("%s-v%d", kind, version)
	artifacts := []subtitleArtifact{
		{
			kind:         "subtitle_" + kind + "_vtt",
			originalName: baseName + ".vtt",
			contentType:  "text/vtt; charset=utf-8",
			content:      []byte(renderReviewVTT(segments)),
		},
		{
			kind:         "subtitle_" + kind + "_srt",
			originalName: baseName + ".srt",
			contentType:  "application/x-subrip; charset=utf-8",
			content:      []byte(renderReviewSRT(segments)),
		},
		{
			kind:         "subtitle_" + kind + "_qc",
			originalName: baseName + ".qc.json",
			contentType:  "application/json",
			content:      qcContent,
		},
	}
	for index := range artifacts {
		artifact := &artifacts[index]
		artifact.objectKey = fmt.Sprintf(
			"tasks/%s/subtitles/review/%s",
			taskID,
			artifact.originalName,
		)
		sum := sha256.Sum256(artifact.content)
		artifact.checksum = fmt.Sprintf("%x", sum[:])
		if err := s.storage.Put(
			ctx,
			s.storageBucket,
			artifact.objectKey,
			artifact.contentType,
			artifact.content,
		); err != nil {
			return nil, fmt.Errorf("upload edited subtitle artifact: %w", err)
		}
	}
	return artifacts, nil
}

func renderReviewVTT(segments []Segment) string {
	var output strings.Builder
	output.WriteString("WEBVTT\n\n")
	for _, item := range segments {
		fmt.Fprintf(
			&output,
			"%d\n%s --> %s\n%s\n\n",
			item.Index,
			formatSubtitleTimestamp(item.Start, '.'),
			formatSubtitleTimestamp(item.End, '.'),
			item.Text,
		)
	}
	return output.String()
}

func renderReviewSRT(segments []Segment) string {
	var output strings.Builder
	for _, item := range segments {
		fmt.Fprintf(
			&output,
			"%d\n%s --> %s\n%s\n\n",
			item.Index,
			formatSubtitleTimestamp(item.Start, ','),
			formatSubtitleTimestamp(item.End, ','),
			item.Text,
		)
	}
	return output.String()
}

func formatSubtitleTimestamp(seconds float64, millisecondSeparator byte) string {
	totalMilliseconds := int64(math.Round(math.Max(0, seconds) * 1000))
	hours := totalMilliseconds / 3_600_000
	totalMilliseconds %= 3_600_000
	minutes := totalMilliseconds / 60_000
	totalMilliseconds %= 60_000
	wholeSeconds := totalMilliseconds / 1000
	milliseconds := totalMilliseconds % 1000
	return fmt.Sprintf(
		"%02d:%02d:%02d%c%03d",
		hours,
		minutes,
		wholeSeconds,
		millisecondSeparator,
		milliseconds,
	)
}

func subtitleQualityReport(
	segments []Segment,
	threshold int,
	minimumCueSeconds float64,
) map[string]any {
	if threshold < 0 {
		threshold = 0
	} else if threshold > 100 {
		threshold = 100
	}
	if minimumCueSeconds <= 0 {
		minimumCueSeconds = 0.7
	}
	issues := make([]map[string]any, 0)
	overlaps := 0
	highCPS := 0
	shortCues := 0
	previousEnd := 0.0
	for _, item := range segments {
		duration := math.Max(0.001, item.End-item.Start)
		characters := len([]rune(strings.ReplaceAll(item.Text, "\n", "")))
		cps := float64(characters) / duration
		if item.Start < previousEnd-0.01 {
			overlaps++
			issues = append(issues, map[string]any{
				"index":    item.Index,
				"severity": "error",
				"message":  "时间轴与前一条重叠",
			})
		}
		if cps > 22 {
			highCPS++
			issues = append(issues, map[string]any{
				"index":    item.Index,
				"severity": "warning",
				"message":  fmt.Sprintf("字符速率过高（%.1f CPS）", cps),
			})
		}
		if duration < minimumCueSeconds {
			shortCues++
		}
		previousEnd = math.Max(previousEnd, item.End)
	}
	score := math.Max(
		0,
		100-float64(overlaps*12+highCPS*4+shortCues*3),
	)
	score = math.Round(score*100) / 100
	return map[string]any{
		"score":           score,
		"threshold":       threshold,
		"passed":          score >= float64(threshold),
		"segment_count":   len(segments),
		"overlap_count":   overlaps,
		"high_cps_count":  highCPS,
		"short_cue_count": shortCues,
		"issues":          issues,
	}
}

func insertAction(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	runID *string,
	action string,
	actorType string,
	actorID string,
	reason string,
	metadataVersion *int,
	payload map[string]any,
	now time.Time,
) error {
	id, err := identity.NewUUID()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode review action payload: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO review_actions (
			id, task_id, review_run_id, action, actor_type, actor_id,
			reason, metadata_version, payload, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, id, taskID, runID, action, actorType, actorID, reason, metadataVersion, raw, now); err != nil {
		return fmt.Errorf("insert review action: %w", err)
	}
	return nil
}

func insertAudit(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	eventType string,
	payload map[string]any,
	now time.Time,
) error {
	id, err := identity.NewUUID()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode review audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			id, aggregate_type, aggregate_id, event_type,
			actor_type, actor_id, payload, occurred_at
		) VALUES ($1,'task',$2,$3,'user','local-operator',$4,$5)
	`, id, taskID, eventType, raw, now); err != nil {
		return fmt.Errorf("insert review audit: %w", err)
	}
	return nil
}

func (s *Service) listRuns(ctx context.Context, taskID string) ([]Run, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			id::text, task_id::text, mode, policy_version, status, decision,
			rule_results, summary, started_at, completed_at
		FROM review_runs
		WHERE task_id=$1
		ORDER BY started_at DESC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list review runs: %w", err)
	}
	defer rows.Close()
	result := make([]Run, 0)
	for rows.Next() {
		var item Run
		var rulesRaw []byte
		if err := rows.Scan(
			&item.ID,
			&item.TaskID,
			&item.Mode,
			&item.PolicyVersion,
			&item.Status,
			&item.Decision,
			&rulesRaw,
			&item.Summary,
			&item.StartedAt,
			&item.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan review run: %w", err)
		}
		if err := json.Unmarshal(rulesRaw, &item.RuleResults); err != nil {
			return nil, fmt.Errorf("decode review run rules: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) listActions(ctx context.Context, taskID string) ([]Action, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			id::text,
			task_id::text,
			review_run_id::text,
			action,
			actor_type,
			actor_id,
			reason,
			metadata_version,
			payload,
			created_at
		FROM review_actions
		WHERE task_id=$1
		ORDER BY created_at DESC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list review actions: %w", err)
	}
	defer rows.Close()
	result := make([]Action, 0)
	for rows.Next() {
		var item Action
		var payloadRaw []byte
		if err := rows.Scan(
			&item.ID,
			&item.TaskID,
			&item.ReviewRunID,
			&item.Action,
			&item.ActorType,
			&item.ActorID,
			&item.Reason,
			&item.MetadataVersion,
			&payloadRaw,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan review action: %w", err)
		}
		if err := json.Unmarshal(payloadRaw, &item.Payload); err != nil {
			return nil, fmt.Errorf("decode review action payload: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) listSubtitles(
	ctx context.Context,
	taskID string,
) ([]SubtitleDocument, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, kind, language, version, segments, qc_report, source, created_at
		FROM subtitle_documents
		WHERE task_id=$1
		ORDER BY kind, version DESC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list subtitle documents: %w", err)
	}
	defer rows.Close()
	result := make([]SubtitleDocument, 0)
	for rows.Next() {
		var item SubtitleDocument
		var segmentsRaw, qcRaw []byte
		if err := rows.Scan(
			&item.ID,
			&item.Kind,
			&item.Language,
			&item.Version,
			&segmentsRaw,
			&qcRaw,
			&item.Source,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan subtitle document: %w", err)
		}
		if err := json.Unmarshal(segmentsRaw, &item.Segments); err != nil {
			return nil, fmt.Errorf("decode subtitle segments: %w", err)
		}
		if err := json.Unmarshal(qcRaw, &item.QCReport); err != nil {
			return nil, fmt.Errorf("decode subtitle qc report: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) listModerationRuns(
	ctx context.Context,
	taskID string,
) ([]ModerationRun, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			id::text,
			provider,
			status,
			attempt,
			policy_snapshot,
			text_result,
			image_result,
			video_result,
			decision,
			error_code,
			error_message,
			started_at,
			completed_at,
			created_at,
			updated_at
		FROM moderation_runs
		WHERE task_id=$1
		ORDER BY created_at DESC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list moderation runs: %w", err)
	}
	defer rows.Close()
	result := make([]ModerationRun, 0)
	for rows.Next() {
		item, err := scanModerationRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate moderation runs: %w", err)
	}
	return result, nil
}

func scanModerationRun(
	row interface{ Scan(...any) error },
) (ModerationRun, error) {
	var item ModerationRun
	var policyRaw, textRaw, imageRaw, videoRaw []byte
	if err := row.Scan(
		&item.ID,
		&item.Provider,
		&item.Status,
		&item.Attempt,
		&policyRaw,
		&textRaw,
		&imageRaw,
		&videoRaw,
		&item.Decision,
		&item.ErrorCode,
		&item.ErrorMessage,
		&item.StartedAt,
		&item.CompletedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return ModerationRun{}, err
	}
	if err := json.Unmarshal(policyRaw, &item.PolicySnapshot); err != nil {
		return ModerationRun{}, fmt.Errorf(
			"decode moderation policy snapshot: %w",
			err,
		)
	}
	for _, value := range []struct {
		name   string
		raw    []byte
		target *moderation.ChannelResult
	}{
		{"text", textRaw, &item.Text},
		{"image", imageRaw, &item.Image},
		{"video", videoRaw, &item.Video},
	} {
		if err := json.Unmarshal(value.raw, value.target); err != nil {
			return ModerationRun{}, fmt.Errorf(
				"decode %s moderation result: %w",
				value.name,
				err,
			)
		}
	}
	item.RiskLevel = moderation.HighestRisk(
		item.Text.RiskLevel,
		item.Image.RiskLevel,
		item.Video.RiskLevel,
	)
	return item, nil
}
