package publishing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/visoraft/visoraft/internal/identity"
	appsettings "github.com/visoraft/visoraft/internal/settings"
	"github.com/visoraft/visoraft/internal/taskconfig"
)

type approvedTask struct {
	Status           string
	ReviewStatus     string
	TargetPlatforms  []string
	StrategyID       *string
	AutoPublish      bool
	Title            string
	Description      string
	Tags             []string
	SourceURL        string
	StatementBrief   string
	StatementFull    string
	MetadataVersion  int64
	SettingsSnapshot []byte
}

func (s *PostgresStore) CoverImportSource(
	ctx context.Context,
	taskID string,
) (thumbnailURL string, requiresBilibili bool, hasCover bool, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT
			thumbnail_url,
			'bilibili'=ANY(target_platforms),
			EXISTS(
				SELECT 1
				FROM media_assets
				WHERE task_id=tasks.id
				  AND kind IN ('cover_processed','thumbnail')
				  AND status='available'
			)
		FROM tasks
		WHERE id=$1
	`, taskID).Scan(&thumbnailURL, &requiresBilibili, &hasCover)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, false, ErrNotFound
	}
	if err != nil {
		return "", false, false, fmt.Errorf("load cover import source: %w", err)
	}
	if requiresBilibili && !hasCover && strings.TrimSpace(thumbnailURL) == "" {
		return "", true, false, &ConflictError{
			Code:    "cover_source_missing",
			Message: "任务没有可导入的来源缩略图，请先补充封面",
		}
	}
	return thumbnailURL, requiresBilibili, hasCover, nil
}

func (s *PostgresStore) SaveImportedCover(
	ctx context.Context,
	taskID string,
	assetID string,
	bucket string,
	objectKey string,
	originalName string,
	contentType string,
	sizeBytes int64,
	checksum string,
	width int,
	height int,
	now time.Time,
) (string, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", fmt.Errorf("begin imported cover save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var taskExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1)
	`, taskID).Scan(&taskExists); err != nil {
		return "", fmt.Errorf("check imported cover task: %w", err)
	}
	if !taskExists {
		return "", ErrNotFound
	}
	mediaInfo, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"format_name":    strings.TrimPrefix(contentType, "image/"),
		"width":          width,
		"height":         height,
	})
	if err != nil {
		return "", fmt.Errorf("encode cover media info: %w", err)
	}
	var actualAssetID string
	err = tx.QueryRow(ctx, `
		INSERT INTO media_assets (
			id, task_id, kind, bucket, object_key, original_name,
			content_type, size_bytes, checksum_sha256, media_info,
			status, created_at
		) VALUES ($1,$2,'thumbnail',$3,$4,$5,$6,$7,$8,$9,'available',$10)
		ON CONFLICT (task_id, kind, object_key) DO UPDATE SET
			original_name=EXCLUDED.original_name,
			content_type=EXCLUDED.content_type,
			size_bytes=EXCLUDED.size_bytes,
			checksum_sha256=EXCLUDED.checksum_sha256,
			media_info=EXCLUDED.media_info,
			status='available',
			deleted_at=NULL
		RETURNING id::text
	`,
		assetID,
		taskID,
		bucket,
		objectKey,
		originalName,
		contentType,
		sizeBytes,
		checksum,
		mediaInfo,
		now,
	).Scan(&actualAssetID)
	if err != nil {
		return "", fmt.Errorf("upsert imported cover asset: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		WITH latest_job AS (
			SELECT id
			FROM publish_jobs
			WHERE task_id=$1
			ORDER BY created_at DESC
			LIMIT 1
		)
		UPDATE publish_jobs
		SET cover_asset_id=$2, updated_at=$3, version=version+1
		WHERE id=(SELECT id FROM latest_job)
	`, taskID, actualAssetID, now); err != nil {
		return "", fmt.Errorf("attach imported cover to publish job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		WITH latest_job AS (
			SELECT id
			FROM publish_jobs
			WHERE task_id=$1
			ORDER BY created_at DESC
			LIMIT 1
		)
		UPDATE platform_publications
		SET cover_asset_id=$2, updated_at=$3, version=version+1
		WHERE publish_job_id=(SELECT id FROM latest_job)
	`, taskID, actualAssetID, now); err != nil {
		return "", fmt.Errorf("attach imported cover to platform drafts: %w", err)
	}
	auditID, err := identity.NewUUID()
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			id, aggregate_type, aggregate_id, event_type,
			actor_type, actor_id, payload, occurred_at
		) VALUES (
			$1,'task',$2,'cover.imported',
			'system','cover-import',$3,$4
		)
	`, auditID, taskID, map[string]any{
		"asset_id":        actualAssetID,
		"object_key":      objectKey,
		"content_type":    contentType,
		"size_bytes":      sizeBytes,
		"checksum_sha256": checksum,
		"width":           width,
		"height":          height,
	}, now); err != nil {
		return "", fmt.Errorf("audit imported cover: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit imported cover: %w", err)
	}
	return actualAssetID, nil
}

func (s *PostgresStore) PrepareApprovedTask(
	ctx context.Context,
	taskID string,
	autoTriggered bool,
	now time.Time,
) (string, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", fmt.Errorf("begin publish preparation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	jobID, err := s.PrepareApprovedTaskTx(ctx, tx, taskID, autoTriggered, now)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit publish preparation: %w", err)
	}
	return jobID, nil
}

func (s *PostgresStore) PrepareApprovedTaskTx(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	autoTriggered bool,
	now time.Time,
) (string, error) {
	var task approvedTask
	err := tx.QueryRow(ctx, `
		SELECT
			t.status,
			t.review_status,
			t.target_platforms,
			t.posting_strategy_id::text,
			t.auto_publish,
			t.title,
			t.description,
			t.tags,
			t.source_url,
			t.repost_statement_brief,
			t.repost_statement_full,
			COALESCE(
				(SELECT max(version) FROM task_metadata_versions WHERE task_id=t.id),
				t.version
			),
			t.settings_snapshot
		FROM tasks t
		WHERE t.id=$1
		FOR UPDATE
	`, taskID).Scan(
		&task.Status,
		&task.ReviewStatus,
		&task.TargetPlatforms,
		&task.StrategyID,
		&task.AutoPublish,
		&task.Title,
		&task.Description,
		&task.Tags,
		&task.SourceURL,
		&task.StatementBrief,
		&task.StatementFull,
		&task.MetadataVersion,
		&task.SettingsSnapshot,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock approved task for publishing: %w", err)
	}
	if task.ReviewStatus != "approved" ||
		!slices.Contains([]string{"ready_to_publish", "publishing", "published"}, task.Status) {
		return "", &ConflictError{
			Code:    "task_not_approved",
			Message: "任务尚未通过审核，不能准备投稿",
		}
	}

	var snapshot appsettings.ConfigSnapshot
	if err := json.Unmarshal(task.SettingsSnapshot, &snapshot); err != nil {
		return "", fmt.Errorf("decode publishing settings snapshot: %w", err)
	}
	policy, err := taskconfig.Decode(task.SettingsSnapshot)
	if err != nil {
		return "", err
	}

	var strategy *PostingStrategy
	if policy.PostingStrategy != nil {
		if task.StrategyID == nil || *task.StrategyID != policy.PostingStrategy.ID {
			return "", errors.New("task posting strategy snapshot does not match task")
		}
		item := postingStrategyFromSnapshot(*policy.PostingStrategy)
		strategy = &item
	} else if task.StrategyID != nil {
		item, err := scanStrategy(tx.QueryRow(
			ctx,
			strategySelect+" WHERE id=$1 AND archived_at IS NULL",
			*task.StrategyID,
		))
		if errors.Is(err, ErrNotFound) {
			strategy = nil
		} else if err != nil {
			return "", err
		} else {
			strategy = &item
		}
	}

	var coverAssetID *string
	err = tx.QueryRow(ctx, `
		SELECT id::text
		FROM media_assets
		WHERE task_id=$1
		  AND kind IN ('cover_processed','thumbnail')
		  AND status='available'
		ORDER BY
			CASE kind WHEN 'cover_processed' THEN 0 ELSE 1 END,
			created_at DESC
		LIMIT 1
	`, taskID).Scan(&coverAssetID)
	if errors.Is(err, pgx.ErrNoRows) {
		coverAssetID = nil
	} else if err != nil {
		return "", fmt.Errorf("load publish cover asset: %w", err)
	}

	requiresTranscode := strategy != nil && strategy.TranscodePresetID != nil
	var mediaAssetID *string
	if requiresTranscode {
		err = tx.QueryRow(ctx, `
			SELECT output_asset_id::text
			FROM transcode_runs
			WHERE task_id=$1
			  AND preset_id=$2
			  AND status='succeeded'
			  AND output_asset_id IS NOT NULL
			ORDER BY completed_at DESC NULLS LAST, created_at DESC
			LIMIT 1
		`, taskID, *strategy.TranscodePresetID).Scan(&mediaAssetID)
	} else {
		err = tx.QueryRow(ctx, `
			SELECT id::text
			FROM media_assets
			WHERE task_id=$1
			  AND kind IN ('transcoded','source')
			  AND status='available'
			ORDER BY
				CASE kind WHEN 'transcoded' THEN 0 ELSE 1 END,
				created_at DESC
			LIMIT 1
		`, taskID).Scan(&mediaAssetID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		mediaAssetID = nil
	} else if err != nil {
		return "", fmt.Errorf("load publish media asset: %w", err)
	}

	moderationPassed := true
	if strategy != nil && strategy.RequireContentModeration {
		err = tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1
				FROM moderation_runs
				WHERE task_id=$1 AND status='passed' AND decision='pass'
			)
		`, taskID).Scan(&moderationPassed)
		if err != nil {
			return "", fmt.Errorf("check content moderation result: %w", err)
		}
	}

	blockers := make([]Blocker, 0)
	if strategy == nil {
		blockers = append(blockers, Blocker{
			Code:    "posting_strategy_missing",
			Message: "尚未选择有效的投稿策略",
			Action:  "select_posting_strategy",
		})
	} else if !strategy.Enabled {
		blockers = append(blockers, Blocker{
			Code:    "posting_strategy_disabled",
			Message: "当前投稿策略已停用",
			Action:  "enable_posting_strategy",
		})
	}
	if mediaAssetID == nil {
		if requiresTranscode {
			blockers = append(blockers, Blocker{
				Code:    "transcode_not_ready",
				Message: "投稿策略要求转码，但尚无成功的转码产物",
				Action:  "run_transcode",
			})
		} else {
			blockers = append(blockers, Blocker{
				Code:    "media_asset_missing",
				Message: "尚无可投稿的媒体文件",
				Action:  "retry_media_processing",
			})
		}
	}
	if coverAssetID == nil && slices.Contains(task.TargetPlatforms, PlatformBilibili) {
		blockers = append(blockers, Blocker{
			Code:     "bilibili_cover_missing",
			Platform: PlatformBilibili,
			Message:  "Bilibili 投稿需要封面，请先获取或上传封面",
			Action:   "prepare_cover",
		})
	}
	if !moderationPassed {
		blockers = append(blockers, Blocker{
			Code:    "content_moderation_not_passed",
			Message: "投稿策略要求内容审核，但尚无通过结果",
			Action:  "run_content_moderation",
		})
	}
	if strategy != nil {
		for _, platform := range task.TargetPlatforms {
			if !slices.Contains(strategy.TargetPlatforms, platform) {
				blockers = append(blockers, Blocker{
					Code:     "platform_not_in_strategy",
					Platform: platform,
					Message:  "任务平台未包含在投稿策略中",
					Action:   "edit_posting_strategy",
				})
				continue
			}
			accountID := strategy.AccountBindings[platform]
			if accountID == "" {
				blockers = append(blockers, Blocker{
					Code:     "platform_account_missing",
					Platform: platform,
					Message:  "尚未选择该平台的投稿账号",
					Action:   "bind_platform_account",
				})
			} else {
				var (
					accountReady    bool
					accountAuthMode string
				)
				err := tx.QueryRow(ctx, `
					SELECT status='ready', auth_mode
					FROM platform_accounts
					WHERE id=$1
					  AND platform=$2
					  AND archived_at IS NULL
				`, accountID, platform).Scan(&accountReady, &accountAuthMode)
				if errors.Is(err, pgx.ErrNoRows) {
					accountReady = false
				} else if err != nil {
					return "", fmt.Errorf("check publish account readiness: %w", err)
				}
				if !accountReady {
					blockers = append(blockers, Blocker{
						Code:     "platform_account_not_ready",
						Platform: platform,
						Message:  "投稿账号未通过校验或 Cookie 已失效",
						Action:   "check_platform_account",
					})
				} else if fixtureAccountBlocked(accountAuthMode, task.SourceURL) {
					blockers = append(blockers, Blocker{
						Code:     "fixture_account_real_source_forbidden",
						Platform: platform,
						Message:  "当前绑定的是本地测试账号，不会向平台投稿；真实来源必须使用 Cookie 认证账号",
						Action:   "bind_real_platform_account",
					})
				}
			}
			if strategy.CategoryBindings[platform] == "" {
				blockers = append(blockers, Blocker{
					Code:     "platform_category_missing",
					Platform: platform,
					Message:  "尚未选择该平台的视频分区",
					Action:   "select_platform_category",
				})
			}
		}
	}

	strategyID := ""
	if task.StrategyID != nil {
		strategyID = *task.StrategyID
	}
	mediaID := ""
	if mediaAssetID != nil {
		mediaID = *mediaAssetID
	}
	jobFingerprint := fingerprint(
		"publish-job",
		taskID,
		fmt.Sprint(task.MetadataVersion),
		strategyID,
		mediaID,
	)

	ready := len(blockers) == 0

	autoStart := autoTriggered &&
		task.AutoPublish &&
		snapshot.Publishing.AutoPublishAfterReview &&
		strategy != nil &&
		strategy.AutomationMode == AutomationAutomatic
	jobStatus := "blocked"
	if ready {
		jobStatus = "draft"
		if autoStart {
			jobStatus = "queued"
		}
	}

	jobID, err := identity.NewUUID()
	if err != nil {
		return "", err
	}
	var actualJobID string
	blockersRaw, err := json.Marshal(blockers)
	if err != nil {
		return "", fmt.Errorf("encode publish blockers: %w", err)
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO publish_jobs (
			id, task_id, strategy_id, status, auto_started, metadata_version,
			fingerprint, blockers, cover_asset_id, media_asset_id,
			scheduled_at, queued_at, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,
			CASE
				WHEN $4='queued' THEN $12::timestamptz
				ELSE NULL::timestamptz
			END,
			$12::timestamptz,$12::timestamptz
		)
		ON CONFLICT (fingerprint) DO UPDATE
		SET
			strategy_id=EXCLUDED.strategy_id,
			status=CASE
				WHEN publish_jobs.status IN ('publishing','published','partial_success')
				THEN publish_jobs.status
				ELSE EXCLUDED.status
			END,
			auto_started=publish_jobs.auto_started OR EXCLUDED.auto_started,
			blockers=EXCLUDED.blockers,
			cover_asset_id=EXCLUDED.cover_asset_id,
			media_asset_id=EXCLUDED.media_asset_id,
			scheduled_at=EXCLUDED.scheduled_at,
			queued_at=COALESCE(publish_jobs.queued_at, EXCLUDED.queued_at),
			updated_at=EXCLUDED.updated_at,
			version=publish_jobs.version+1
		RETURNING id::text
	`,
		jobID,
		taskID,
		task.StrategyID,
		jobStatus,
		autoStart,
		task.MetadataVersion,
		jobFingerprint,
		blockersRaw,
		coverAssetID,
		mediaAssetID,
		nextSchedule(strategy, now),
		now,
	).Scan(&actualJobID)
	if err != nil {
		return "", fmt.Errorf("create publish job: %w", err)
	}

	if strategy != nil && mediaAssetID != nil {
		statement := task.StatementFull
		if strategy.RepostStatementVersion == "brief_v1" {
			statement = task.StatementBrief
		}
		for _, platform := range task.TargetPlatforms {
			accountID := strategy.AccountBindings[platform]
			categoryID := strategy.CategoryBindings[platform]
			if !slices.Contains(strategy.TargetPlatforms, platform) ||
				accountID == "" ||
				categoryID == "" {
				continue
			}
			title := renderTemplate(
				strategy.TitleTemplates[platform],
				task.Title,
				task.Description,
				task.SourceURL,
				statement,
			)
			if strings.TrimSpace(strategy.TitleTemplates[platform]) == "" {
				title = task.Title
			}
			description := renderTemplate(
				strategy.DescriptionTemplates[platform],
				task.Title,
				task.Description,
				task.SourceURL,
				statement,
			)
			if strings.TrimSpace(strategy.DescriptionTemplates[platform]) == "" {
				description = task.Description
			}
			description = appendStatement(description, statement)
			tags := mergeTags(task.Tags, strategy.DefaultTags)
			publicationFingerprint := fingerprint(
				"platform-publication",
				jobFingerprint,
				platform,
				accountID,
				categoryID,
			)
			publicationID, err := identity.NewUUID()
			if err != nil {
				return "", err
			}
			status := "blocked"
			if ready {
				status = "draft"
				if autoStart {
					status = "queued"
				}
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO platform_publications (
					id, publish_job_id, task_id, platform, account_id,
					status, category_id, title, description, tags,
					cover_asset_id, media_asset_id, scheduled_at, fingerprint,
					created_at, updated_at
				) VALUES (
					$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15
				)
				ON CONFLICT (publish_job_id, platform) DO UPDATE SET
					account_id=EXCLUDED.account_id,
					status=CASE
						WHEN platform_publications.status='published'
						THEN platform_publications.status
						ELSE EXCLUDED.status
					END,
					category_id=EXCLUDED.category_id,
					title=EXCLUDED.title,
					description=EXCLUDED.description,
					tags=EXCLUDED.tags,
					cover_asset_id=EXCLUDED.cover_asset_id,
					media_asset_id=EXCLUDED.media_asset_id,
					scheduled_at=EXCLUDED.scheduled_at,
					updated_at=EXCLUDED.updated_at
			`,
				publicationID,
				actualJobID,
				taskID,
				platform,
				accountID,
				status,
				categoryID,
				title,
				description,
				tags,
				coverAssetID,
				*mediaAssetID,
				nextSchedule(strategy, now),
				publicationFingerprint,
				now,
			); err != nil {
				return "", fmt.Errorf("create %s publication draft: %w", platform, err)
			}
		}
	}

	stepID, err := identity.NewUUID()
	if err != nil {
		return "", err
	}
	blockerCode := ""
	blockerMessage := ""
	if len(blockers) > 0 {
		blockerCode = blockers[0].Code
		blockerMessage = blockers[0].Message
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_steps (
			id, task_id, kind, status, attempt, progress,
			error_code, error_message, updated_at
		) VALUES ($1,$2,'publish','queued',1,0,$3,$4,$5)
		ON CONFLICT (task_id, kind) DO UPDATE SET
			status=CASE
				WHEN task_steps.status='succeeded' THEN task_steps.status
				ELSE 'queued'
			END,
			progress=CASE
				WHEN task_steps.status='succeeded' THEN task_steps.progress
				ELSE 0
			END,
			error_code=$3,
			error_message=$4,
			finished_at=CASE
				WHEN task_steps.status='succeeded' THEN task_steps.finished_at
				ELSE NULL
			END,
			updated_at=$5
	`, stepID, taskID, blockerCode, blockerMessage, now); err != nil {
		return "", fmt.Errorf("prepare task publish step: %w", err)
	}

	if autoStart && ready {
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='publishing', updated_at=$2, version=version+1
			WHERE id=$1 AND status='ready_to_publish'
		`, taskID, now); err != nil {
			return "", fmt.Errorf("advance task to publishing: %w", err)
		}
	}
	return actualJobID, nil
}

func postingStrategyFromSnapshot(
	snapshot taskconfig.PostingStrategySnapshot,
) PostingStrategy {
	return PostingStrategy{
		ID:                       snapshot.ID,
		Enabled:                  snapshot.Enabled,
		AutomationMode:           snapshot.AutomationMode,
		TargetPlatforms:          append([]string(nil), snapshot.TargetPlatforms...),
		AccountBindings:          snapshot.AccountBindings,
		CategoryBindings:         snapshot.CategoryBindings,
		TitleTemplates:           snapshot.TitleTemplates,
		DescriptionTemplates:     snapshot.DescriptionTemplates,
		DefaultTags:              append([]string(nil), snapshot.DefaultTags...),
		RepostStatementVersion:   snapshot.RepostStatementVersion,
		TranscodePresetID:        snapshot.TranscodePresetID,
		RequireContentModeration: snapshot.RequireContentModeration,
		ScheduleMode:             snapshot.ScheduleMode,
		ScheduleTime:             snapshot.ScheduleTime,
		Version:                  snapshot.Version,
	}
}

func (s *PostgresStore) PublishingDetail(
	ctx context.Context,
	taskID string,
) (Detail, error) {
	result := Detail{
		Publications: []PlatformPublication{},
		Attempts:     map[string][]PublicationAttempt{},
		Blockers:     []Blocker{},
	}
	job, err := scanJob(s.pool.QueryRow(ctx, jobSelect+`
		WHERE task_id=$1
		ORDER BY created_at DESC
		LIMIT 1
	`, taskID))
	if errors.Is(err, ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return Detail{}, err
	}
	result.Job = &job
	result.Blockers = append(result.Blockers, job.Blockers...)
	switch job.Status {
	case "blocked":
		result.NextAction = "resolve_blockers"
	case "draft":
		result.NextAction = "review_publish_draft"
	case "queued", "publishing", "reconciliation_required":
		result.NextAction = "track_platform_publications"
	case "partial_success", "failed":
		result.NextAction = "retry_failed_platforms"
	case "published":
		result.NextAction = "view_publish_results"
	case "cancelled":
		result.NextAction = "prepare_new_publish_job"
	default:
		result.NextAction = "open_publish_detail"
	}

	rows, err := s.pool.Query(ctx, publicationSelect+`
		WHERE publish_job_id=$1
		ORDER BY platform
	`, job.ID)
	if err != nil {
		return Detail{}, fmt.Errorf("list platform publication drafts: %w", err)
	}
	for rows.Next() {
		item, err := scanPublication(rows)
		if err != nil {
			rows.Close()
			return Detail{}, err
		}
		result.Publications = append(result.Publications, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Detail{}, fmt.Errorf("iterate platform publication drafts: %w", err)
	}
	rows.Close()

	for index := range result.Publications {
		publication := &result.Publications[index]
		if err := s.pool.QueryRow(ctx, `
			SELECT name, auth_mode
			FROM platform_accounts
			WHERE id=$1
		`, publication.AccountID).Scan(
			&publication.AccountName,
			&publication.AccountAuthMode,
		); err != nil {
			return Detail{}, fmt.Errorf("load publication account mode: %w", err)
		}
		publication.Simulation = publication.AccountAuthMode == "fixture" ||
			publication.RemoteStatus == "published_fixture"
		if value, ok := publication.ResponseSummary["fixture"].(bool); ok && value {
			publication.Simulation = true
		}
		attempts, err := s.listPublicationAttempts(ctx, publication.ID)
		if err != nil {
			return Detail{}, err
		}
		result.Attempts[publication.ID] = attempts
	}
	return result, nil
}

func (s *PostgresStore) UpdatePublicationDraft(
	ctx context.Context,
	taskID string,
	platform string,
	input DraftPlatformInput,
	now time.Time,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin publication draft update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		jobID       string
		jobStatus   string
		blockersRaw []byte
		sourceURL   string
	)
	err = tx.QueryRow(ctx, `
		SELECT job.id::text, job.status, job.blockers, task.source_url
		FROM publish_jobs job
		JOIN tasks task ON task.id=job.task_id
		WHERE job.task_id=$1
		ORDER BY job.created_at DESC
		LIMIT 1
		FOR UPDATE
	`, taskID).Scan(&jobID, &jobStatus, &blockersRaw, &sourceURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock publish job draft: %w", err)
	}
	if !slices.Contains([]string{"blocked", "draft", "failed", "partial_success"}, jobStatus) {
		return &ConflictError{
			Code:    "publication_draft_locked",
			Message: "当前投稿已进入队列或已经完成，不能再修改草稿",
		}
	}

	var (
		accountReady    bool
		accountAuthMode string
	)
	err = tx.QueryRow(ctx, `
		SELECT status='ready', auth_mode
		FROM platform_accounts
		WHERE id=$1 AND platform=$2 AND archived_at IS NULL
	`, input.AccountID, platform).Scan(&accountReady, &accountAuthMode)
	if errors.Is(err, pgx.ErrNoRows) {
		accountReady = false
	} else if err != nil {
		return fmt.Errorf("check publication account: %w", err)
	}
	if !accountReady {
		return &ValidationError{
			Fields: map[string]string{"account_id": "投稿账号不存在、平台不匹配或尚未通过校验"},
		}
	}
	if fixtureAccountBlocked(accountAuthMode, sourceURL) {
		return &ValidationError{
			Fields: map[string]string{
				"account_id": "本地测试账号不会连接真实平台，请选择已校验的 Cookie 认证账号",
			},
		}
	}
	var categoryReady bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM platform_categories
			WHERE platform=$1 AND category_id=$2 AND active=true
		)
	`, platform, input.CategoryID).Scan(&categoryReady); err != nil {
		return fmt.Errorf("check publication category: %w", err)
	}
	if !categoryReady {
		return &ValidationError{
			Fields: map[string]string{"category_id": "视频分区不存在或已失效，请刷新平台分区"},
		}
	}

	tag, err := tx.Exec(ctx, `
		UPDATE platform_publications
		SET
			account_id=$4,
			category_id=$5,
			title=$6,
			description=$7,
			tags=$8,
			status='draft',
			error_code='',
			error_message='',
			error_retryable=false,
			version=version+1,
			updated_at=$9
		WHERE publish_job_id=$1
		  AND platform=$2
		  AND version=$3
		  AND status IN ('blocked','draft','failed')
	`, jobID, platform, input.ExpectedVersion, input.AccountID, input.CategoryID,
		input.Title, input.Description, input.Tags, now)
	if err != nil {
		return fmt.Errorf("update platform publication draft: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrVersionConflict
	}

	var blockers []Blocker
	if err := json.Unmarshal(blockersRaw, &blockers); err != nil {
		return fmt.Errorf("decode publish blockers for draft update: %w", err)
	}
	filtered := blockers[:0]
	for _, blocker := range blockers {
		if blocker.Platform == platform &&
			slices.Contains(
				[]string{
					"platform_account_missing",
					"platform_account_not_ready",
					"fixture_account_real_source_forbidden",
					"platform_category_missing",
				},
				blocker.Code,
			) {
			continue
		}
		filtered = append(filtered, blocker)
	}
	nextStatus := "blocked"
	if len(filtered) == 0 {
		nextStatus = "draft"
	}
	nextBlockersRaw, err := json.Marshal(filtered)
	if err != nil {
		return fmt.Errorf("encode updated publish blockers: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE publish_jobs
		SET
			status=$2,
			blockers=$3,
			version=version+1,
			updated_at=$4
		WHERE id=$1
	`, jobID, nextStatus, nextBlockersRaw, now); err != nil {
		return fmt.Errorf("update publish job after draft edit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit publication draft update: %w", err)
	}
	return nil
}

func (s *PostgresStore) EnqueuePublishJob(
	ctx context.Context,
	taskID string,
	now time.Time,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin publish enqueue: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var jobID, status, sourceURL string
	var blockersRaw []byte
	err = tx.QueryRow(ctx, `
		SELECT job.id::text, job.status, job.blockers, task.source_url
		FROM publish_jobs job
		JOIN tasks task ON task.id=job.task_id
		WHERE job.task_id=$1
		ORDER BY job.created_at DESC
		LIMIT 1
		FOR UPDATE
	`, taskID).Scan(&jobID, &status, &blockersRaw, &sourceURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock publish job for enqueue: %w", err)
	}
	var blockers []Blocker
	if err := json.Unmarshal(blockersRaw, &blockers); err != nil {
		return fmt.Errorf("decode publish blockers before enqueue: %w", err)
	}
	if len(blockers) > 0 || status == "blocked" {
		return &ConflictError{
			Code:    "publish_job_blocked",
			Message: "请先处理投稿页列出的阻断项",
		}
	}
	if !slices.Contains([]string{"draft", "failed", "partial_success"}, status) {
		return &ConflictError{
			Code:    "publish_job_not_queueable",
			Message: "当前投稿状态不能重复进入队列",
		}
	}
	var fixturePlatform string
	err = tx.QueryRow(ctx, `
		SELECT publication.platform
		FROM platform_publications publication
		JOIN platform_accounts account ON account.id=publication.account_id
		WHERE publication.publish_job_id=$1
		  AND publication.status <> 'published'
		  AND account.auth_mode='fixture'
		ORDER BY publication.platform
		LIMIT 1
	`, jobID).Scan(&fixturePlatform)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check fixture account before enqueue: %w", err)
	}
	if fixturePlatform != "" && !isFixtureSourceURL(sourceURL) {
		return &ConflictError{
			Code:    "fixture_account_real_source_forbidden",
			Message: "真实来源任务仍绑定本地测试账号；请改用已校验的 Cookie 认证账号",
		}
	}
	var publicationCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM platform_publications
		WHERE publish_job_id=$1 AND status <> 'published'
	`, jobID).Scan(&publicationCount); err != nil {
		return fmt.Errorf("count queueable platform publications: %w", err)
	}
	if publicationCount == 0 {
		return &ConflictError{
			Code:    "platform_publication_missing",
			Message: "没有可进入队列的平台投稿草稿",
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE platform_publications
		SET
			status='queued',
			error_code='',
			error_message='',
			error_retryable=false,
			updated_at=$2,
			version=version+1
		WHERE publish_job_id=$1
		  AND status IN ('draft','blocked','failed')
	`, jobID, now); err != nil {
		return fmt.Errorf("enqueue platform publications: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE publish_jobs
		SET
			status='queued',
			queued_at=$2,
			updated_at=$2,
			version=version+1
		WHERE id=$1
	`, jobID, now); err != nil {
		return fmt.Errorf("enqueue publish job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status='publishing', updated_at=$2, version=version+1
		WHERE id=$1 AND status IN ('ready_to_publish','publishing')
	`, taskID, now); err != nil {
		return fmt.Errorf("advance enqueued task: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_steps
		SET
			status='queued',
			progress=0,
			error_code='',
			error_message='',
			finished_at=NULL,
			updated_at=$2
		WHERE task_id=$1 AND kind='publish'
	`, taskID, now); err != nil {
		return fmt.Errorf("queue task publish step: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit publish enqueue: %w", err)
	}
	return nil
}

func (s *PostgresStore) ResolvePlatformPublication(
	ctx context.Context,
	taskID string,
	platform string,
	input ResolvePublicationInput,
	now time.Time,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin platform publication resolution: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		jobID         string
		publicationID string
		status        string
		version       int64
		attempt       int
		lockedAt      *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT
			job.id::text,
			publication.id::text,
			publication.status,
			publication.version,
			publication.attempt,
			publication.locked_at
		FROM publish_jobs job
		JOIN platform_publications publication
		  ON publication.publish_job_id=job.id
		WHERE job.id=(
			SELECT id
			FROM publish_jobs
			WHERE task_id=$1
			ORDER BY created_at DESC
			LIMIT 1
		)
		  AND publication.platform=$2
		FOR UPDATE OF job, publication
	`, taskID, platform).Scan(
		&jobID,
		&publicationID,
		&status,
		&version,
		&attempt,
		&lockedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock platform publication for resolution: %w", err)
	}
	if version != input.ExpectedVersion {
		return ErrVersionConflict
	}
	if status != "reconciliation_required" {
		return &ConflictError{
			Code:    "publication_recovery_not_required",
			Message: "该平台投稿当前不处于结果不确定状态",
		}
	}
	if lockedAt != nil {
		return &ConflictError{
			Code:    "publication_reconciliation_in_progress",
			Message: "发布 Worker 正在自动回查，请稍后刷新再处理",
		}
	}

	payload := map[string]any{
		"publication_id":       publicationID,
		"platform":             platform,
		"resolution":           input.Resolution,
		"note":                 input.Note,
		"previous_status":      status,
		"previous_attempt":     attempt,
		"remote_submission_id": input.RemoteSubmissionID,
		"remote_url":           input.RemoteURL,
	}
	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode publication resolution: %w", err)
	}

	switch input.Resolution {
	case "remote_published":
		if _, err := tx.Exec(ctx, `
			UPDATE platform_publications
			SET
				status='published',
				remote_submission_id=$2,
				remote_url=$3,
				remote_status='published_operator_confirmed',
				adapter_version=COALESCE(
					NULLIF(adapter_version,''),
					'operator-recovery-v1'
				),
				response_summary=response_summary || $4::jsonb,
				error_code='',
				error_message='',
				error_retryable=false,
				uncertain_since=NULL,
				locked_at=NULL,
				locked_by='',
				scheduled_at=NULL,
				completed_at=$5,
				updated_at=$5,
				version=version+1
			WHERE id=$1
		`, publicationID, input.RemoteSubmissionID, input.RemoteURL, payloadRaw, now); err != nil {
			return fmt.Errorf("confirm remote platform publication: %w", err)
		}
	case "remote_not_created":
		if _, err := tx.Exec(ctx, `
			UPDATE platform_publications
			SET
				status='queued',
				remote_submission_id='',
				remote_url='',
				remote_status='',
				response_summary=response_summary || $2::jsonb,
				error_code='',
				error_message='',
				error_retryable=false,
				uncertain_since=NULL,
				locked_at=NULL,
				locked_by='',
				scheduled_at=$3,
				completed_at=NULL,
				updated_at=$3,
				version=version+1
			WHERE id=$1
		`, publicationID, payloadRaw, now); err != nil {
			return fmt.Errorf("requeue confirmed missing platform publication: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE publication_attempts
		SET
			status='uncertain',
			error_code='resolved_by_operator',
			error_message='远端结果已由本地操作员核验',
			completed_at=$2
		WHERE publication_id=$1 AND status='running'
	`, publicationID, now); err != nil {
		return fmt.Errorf("close running publication attempts: %w", err)
	}
	if attempt < 1 {
		attempt = 1
	}
	attemptID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO publication_attempts (
			id, publication_id, attempt, stage, status,
			request_summary, response_summary, started_at, completed_at
		) VALUES (
			$1,$2,$3,'operator_resolution','succeeded',
			$4,$4,$5,$5
		)
	`, attemptID, publicationID, attempt, payloadRaw, now); err != nil {
		return fmt.Errorf("record publication operator resolution: %w", err)
	}

	auditID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	eventType := "platform_publication.remote_not_created_confirmed.v1"
	if input.Resolution == "remote_published" {
		eventType = "platform_publication.remote_published_confirmed.v1"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			id, aggregate_type, aggregate_id, event_type,
			actor_type, actor_id, payload, occurred_at
		) VALUES ($1,'task',$2,$3,'user','local-operator',$4,$5)
	`, auditID, taskID, eventType, payloadRaw, now); err != nil {
		return fmt.Errorf("record publication resolution audit: %w", err)
	}
	if err := s.recomputePublishJob(ctx, tx, jobID, taskID, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit platform publication resolution: %w", err)
	}
	return nil
}

func (s *PostgresStore) RetryPlatformPublication(
	ctx context.Context,
	taskID string,
	platform string,
	now time.Time,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin platform publication retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var jobID, sourceURL string
	err = tx.QueryRow(ctx, `
		SELECT job.id::text, task.source_url
		FROM publish_jobs job
		JOIN tasks task ON task.id=job.task_id
		WHERE job.task_id=$1
		ORDER BY job.created_at DESC
		LIMIT 1
		FOR UPDATE
	`, taskID).Scan(&jobID, &sourceURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock publish job for platform retry: %w", err)
	}
	var authMode string
	err = tx.QueryRow(ctx, `
		SELECT account.auth_mode
		FROM platform_publications publication
		JOIN platform_accounts account ON account.id=publication.account_id
		WHERE publication.publish_job_id=$1 AND publication.platform=$2
	`, jobID, platform).Scan(&authMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check platform account before retry: %w", err)
	}
	if fixtureAccountBlocked(authMode, sourceURL) {
		return &ConflictError{
			Code:    "fixture_account_real_source_forbidden",
			Message: "本地测试账号不会向平台投稿；请先把平台草稿切换为 Cookie 认证账号",
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE platform_publications
		SET
			status='queued',
			error_code='',
			error_message='',
			error_retryable=false,
			uncertain_since=NULL,
			updated_at=$3,
			version=version+1
		WHERE publish_job_id=$1
		  AND platform=$2
		  AND status='failed'
	`, jobID, platform, now)
	if err != nil {
		return fmt.Errorf("retry platform publication: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return &ConflictError{
			Code:    "platform_publication_not_retryable",
			Message: "该平台投稿当前不能直接重试；结果不确定时请先核验远端平台",
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE publish_jobs
		SET status='queued', completed_at=NULL, updated_at=$2, version=version+1
		WHERE id=$1
	`, jobID, now); err != nil {
		return fmt.Errorf("reopen publish job for retry: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status='publishing', updated_at=$2, version=version+1
		WHERE id=$1
	`, taskID, now); err != nil {
		return fmt.Errorf("reopen task for publication retry: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit platform publication retry: %w", err)
	}
	return nil
}

const jobSelect = `
	SELECT
		id::text,
		task_id::text,
		strategy_id::text,
		status,
		auto_started,
		metadata_version,
		fingerprint,
		blockers,
		cover_asset_id::text,
		media_asset_id::text,
		scheduled_at,
		queued_at,
		completed_at,
		created_at,
		updated_at,
		version
	FROM publish_jobs
`

func scanJob(row rowScanner) (PublishJob, error) {
	var item PublishJob
	var blockersRaw []byte
	if err := row.Scan(
		&item.ID,
		&item.TaskID,
		&item.StrategyID,
		&item.Status,
		&item.AutoStarted,
		&item.MetadataVersion,
		&item.Fingerprint,
		&blockersRaw,
		&item.CoverAssetID,
		&item.MediaAssetID,
		&item.ScheduledAt,
		&item.QueuedAt,
		&item.CompletedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
		&item.Version,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PublishJob{}, ErrNotFound
		}
		return PublishJob{}, fmt.Errorf("scan publish job: %w", err)
	}
	if err := json.Unmarshal(blockersRaw, &item.Blockers); err != nil {
		return PublishJob{}, fmt.Errorf("decode publish blockers: %w", err)
	}
	return item, nil
}

const publicationSelect = `
	SELECT
		id::text,
		publish_job_id::text,
		task_id::text,
		platform,
		account_id::text,
		status,
		category_id,
		title,
		description,
		tags,
		cover_asset_id::text,
		media_asset_id::text,
		scheduled_at,
		fingerprint,
		attempt,
		remote_submission_id,
		remote_url,
		remote_status,
		adapter_version,
		response_summary,
		error_code,
		error_message,
		error_retryable,
		uncertain_since,
		started_at,
		completed_at,
		created_at,
		updated_at,
		version
	FROM platform_publications
`

func scanPublication(row rowScanner) (PlatformPublication, error) {
	var item PlatformPublication
	var responseRaw []byte
	if err := row.Scan(
		&item.ID,
		&item.PublishJobID,
		&item.TaskID,
		&item.Platform,
		&item.AccountID,
		&item.Status,
		&item.CategoryID,
		&item.Title,
		&item.Description,
		&item.Tags,
		&item.CoverAssetID,
		&item.MediaAssetID,
		&item.ScheduledAt,
		&item.Fingerprint,
		&item.Attempt,
		&item.RemoteSubmissionID,
		&item.RemoteURL,
		&item.RemoteStatus,
		&item.AdapterVersion,
		&responseRaw,
		&item.ErrorCode,
		&item.ErrorMessage,
		&item.ErrorRetryable,
		&item.UncertainSince,
		&item.StartedAt,
		&item.CompletedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
		&item.Version,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PlatformPublication{}, ErrNotFound
		}
		return PlatformPublication{}, fmt.Errorf("scan platform publication: %w", err)
	}
	if err := json.Unmarshal(responseRaw, &item.ResponseSummary); err != nil {
		return PlatformPublication{}, fmt.Errorf("decode platform publication response: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) listPublicationAttempts(
	ctx context.Context,
	publicationID string,
) ([]PublicationAttempt, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			id::text, publication_id::text, attempt, stage, status,
			request_summary, response_summary, error_code, error_message,
			started_at, completed_at
		FROM publication_attempts
		WHERE publication_id=$1
		ORDER BY attempt DESC
	`, publicationID)
	if err != nil {
		return nil, fmt.Errorf("list publication attempts: %w", err)
	}
	defer rows.Close()
	result := make([]PublicationAttempt, 0)
	for rows.Next() {
		var item PublicationAttempt
		var requestRaw, responseRaw []byte
		if err := rows.Scan(
			&item.ID,
			&item.PublicationID,
			&item.Attempt,
			&item.Stage,
			&item.Status,
			&requestRaw,
			&responseRaw,
			&item.ErrorCode,
			&item.ErrorMessage,
			&item.StartedAt,
			&item.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan publication attempt: %w", err)
		}
		if err := json.Unmarshal(requestRaw, &item.RequestSummary); err != nil {
			return nil, fmt.Errorf("decode publication request summary: %w", err)
		}
		if err := json.Unmarshal(responseRaw, &item.ResponseSummary); err != nil {
			return nil, fmt.Errorf("decode publication response summary: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate publication attempts: %w", err)
	}
	return result, nil
}

func fingerprint(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func renderTemplate(
	template string,
	title string,
	description string,
	sourceURL string,
	statement string,
) string {
	replacer := strings.NewReplacer(
		"{{title}}", title,
		"{{description}}", description,
		"{{source_url}}", sourceURL,
		"{{repost_statement}}", statement,
	)
	return strings.TrimSpace(replacer.Replace(template))
}

func appendStatement(description string, statement string) string {
	description = strings.TrimSpace(description)
	statement = strings.TrimSpace(statement)
	if statement == "" || strings.Contains(description, statement) {
		return description
	}
	if description == "" {
		return statement
	}
	return description + "\n\n" + statement
}

func mergeTags(values ...[]string) []string {
	result := make([]string, 0)
	for _, group := range values {
		for _, raw := range group {
			value := strings.TrimSpace(raw)
			if value == "" || slices.Contains(result, value) {
				continue
			}
			result = append(result, value)
		}
	}
	return result
}

func nextSchedule(strategy *PostingStrategy, now time.Time) *time.Time {
	if strategy == nil ||
		strategy.ScheduleMode != "daily_time" ||
		strategy.ScheduleTime == nil {
		return nil
	}
	parts := strings.Split(*strategy.ScheduleTime, ":")
	if len(parts) != 2 {
		return nil
	}
	hour := 0
	minute := 0
	if _, err := fmt.Sscanf(parts[0], "%d", &hour); err != nil {
		return nil
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &minute); err != nil {
		return nil
	}
	candidate := time.Date(
		now.Year(),
		now.Month(),
		now.Day(),
		hour,
		minute,
		0,
		0,
		now.Location(),
	)
	if !candidate.After(now) {
		candidate = candidate.Add(24 * time.Hour)
	}
	return &candidate
}
