package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/visoraft/visoraft/internal/events"
	"github.com/visoraft/visoraft/internal/identity"
	"github.com/visoraft/visoraft/internal/taskconfig"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) CurrentTaskConfiguration(
	ctx context.Context,
) (TaskConfiguration, error) {
	var result TaskConfiguration
	err := s.pool.QueryRow(ctx, `
		SELECT
			version,
			review_config->>'mode',
			jsonb_build_object(
				'review', review_config,
				'models', model_config,
				'subtitle', subtitle_config,
				'prompts', prompt_config,
				'youtube', youtube_config,
				'automation', automation_config,
				'transcode', transcode_config,
				'moderation', moderation_config,
				'publishing', publishing_config
			)
		FROM application_settings
		WHERE singleton=true
	`).Scan(&result.Version, &result.ReviewMode, &result.Snapshot)
	if err != nil {
		return TaskConfiguration{}, fmt.Errorf("load task configuration: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT key, ciphertext, version
		FROM setting_secrets
		ORDER BY key
	`)
	if err != nil {
		return TaskConfiguration{}, fmt.Errorf("load task secret configuration: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var snapshot SecretSnapshot
		if err := rows.Scan(&snapshot.Key, &snapshot.Ciphertext, &snapshot.Version); err != nil {
			return TaskConfiguration{}, fmt.Errorf("scan task secret configuration: %w", err)
		}
		result.SecretSnapshots = append(result.SecretSnapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return TaskConfiguration{}, fmt.Errorf("iterate task secret configuration: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) Create(ctx context.Context, newTask NewTask) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin create task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	task := newTask.Task
	if _, err := tx.Exec(ctx, `
		INSERT INTO tasks (
			id, status, target_platforms, source_url, cookie_profile_id,
			posting_strategy_id, auto_publish,
			repost_statement_version, repost_statement_brief, repost_statement_full,
			original_title, title, description, thumbnail_url, extractor,
			review_mode, review_status, review_summary, settings_version, settings_snapshot,
			tags, category, error_retryable, version, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,
			$19,$20,$21,$22,$23,$24,$25,$26
		)
	`,
		task.ID,
		task.Status,
		task.TargetPlatforms,
		task.SourceURL,
		task.CookieProfileID,
		task.PostingStrategyID,
		task.AutoPublish,
		task.StatementVersion,
		task.StatementBrief,
		task.StatementFull,
		task.OriginalTitle,
		task.Title,
		task.Description,
		task.ThumbnailURL,
		task.Extractor,
		task.ReviewMode,
		task.ReviewStatus,
		"{}",
		task.SettingsVersion,
		newTask.SettingsSnapshot,
		task.Tags,
		task.Category,
		task.ErrorRetryable,
		task.Version,
		task.CreatedAt,
		task.UpdatedAt,
	); err != nil {
		return fmt.Errorf("insert task: %w", err)
	}

	for _, snapshot := range newTask.SecretSnapshots {
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_secret_snapshots (
				task_id, key, ciphertext, source_version, created_at
			) VALUES ($1,$2,$3,$4,$5)
		`, task.ID, snapshot.Key, snapshot.Ciphertext, snapshot.Version, task.CreatedAt); err != nil {
			return fmt.Errorf("insert task secret snapshot %s: %w", snapshot.Key, err)
		}
	}

	step := task.Steps[0]
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_steps (
			id, task_id, kind, status, attempt, progress, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
	`,
		newTask.MetadataStepID,
		task.ID,
		step.Kind,
		step.Status,
		step.Attempt,
		step.Progress,
		step.UpdatedAt,
	); err != nil {
		return fmt.Errorf("insert metadata step: %w", err)
	}

	auditPayload, err := json.Marshal(map[string]any{
		"status":              task.Status,
		"target_platforms":    task.TargetPlatforms,
		"statement_version":   task.StatementVersion,
		"cookie_profile_id":   task.CookieProfileID,
		"posting_strategy_id": task.PostingStrategyID,
		"auto_publish":        task.AutoPublish,
		"review_mode":         task.ReviewMode,
		"settings_version":    task.SettingsVersion,
	})
	if err != nil {
		return fmt.Errorf("marshal task audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			id, aggregate_type, aggregate_id, event_type,
			actor_type, actor_id, payload, occurred_at
		) VALUES ($1,'task',$2,'task.created','user','local-user',$3,$4)
	`, newTask.AuditID, task.ID, auditPayload, task.CreatedAt); err != nil {
		return fmt.Errorf("insert task audit: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (
			id, aggregate_id, event_type, payload, status,
			attempts, available_at, created_at
		) VALUES ($1,$2,$3,$4,'pending',0,$5,$5)
	`, newTask.OutboxID, task.ID, newTask.EventType, newTask.Envelope, task.CreatedAt); err != nil {
		return fmt.Errorf("insert metadata outbox command: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit create task: %w", err)
	}
	return nil
}

const taskSelect = `
	SELECT
		t.id::text,
		t.status,
		t.target_platforms,
		t.source_url,
		t.cookie_profile_id::text,
		t.posting_strategy_id::text,
		t.auto_publish,
		(
			SELECT job.id::text
			FROM publish_jobs job
			WHERE job.task_id=t.id
			ORDER BY job.created_at DESC
			LIMIT 1
		),
		COALESCE((
			SELECT job.status
			FROM publish_jobs job
			WHERE job.task_id=t.id
			ORDER BY job.created_at DESC
			LIMIT 1
		), ''),
		COALESCE((
			SELECT job.blockers
			FROM publish_jobs job
			WHERE job.task_id=t.id
			ORDER BY job.created_at DESC
			LIMIT 1
		), '[]'::jsonb),
		COALESCE((
			SELECT CASE
				WHEN count(*) = 0 THEN ''
				WHEN bool_and(account.auth_mode='fixture') THEN 'simulation'
				WHEN bool_and(account.auth_mode='cookie') THEN 'remote'
				ELSE 'mixed'
			END
			FROM platform_publications publication
			JOIN platform_accounts account ON account.id=publication.account_id
			WHERE publication.publish_job_id=(
				SELECT job.id
				FROM publish_jobs job
				WHERE job.task_id=t.id
				ORDER BY job.created_at DESC
				LIMIT 1
			)
		), ''),
		t.repost_statement_version,
		t.repost_statement_brief,
		t.repost_statement_full,
		t.original_title,
		t.title,
		t.description,
		t.thumbnail_url,
		t.duration_seconds,
		t.extractor,
		t.review_mode,
		t.review_status,
		t.review_summary,
		t.settings_version,
		t.tags,
		t.category,
		t.error_code,
		t.error_message,
		t.error_retryable,
		t.version,
		t.created_at,
		t.updated_at,
		t.archived_at,
		t.archived_by,
		t.paused_at,
		t.paused_from_status,
		t.paused_step_kind,
		COALESCE((
			SELECT jsonb_agg(
				jsonb_build_object(
					'kind', ts.kind,
					'status', ts.status,
					'attempt', ts.attempt,
					'progress', ts.progress,
					'detail', ts.detail,
					'error_code', ts.error_code,
					'error_message', ts.error_message,
					'started_at', ts.started_at,
					'finished_at', ts.finished_at,
					'updated_at', ts.updated_at
				)
				ORDER BY ts.updated_at, ts.kind
			)
			FROM task_steps ts
			WHERE ts.task_id = t.id
		), '[]'::jsonb),
		COALESCE((
			SELECT jsonb_agg(
				jsonb_build_object(
					'id', ma.id,
					'kind', ma.kind,
					'bucket', ma.bucket,
					'object_key', ma.object_key,
					'original_name', ma.original_name,
					'content_type', ma.content_type,
					'size_bytes', ma.size_bytes,
					'checksum_sha256', ma.checksum_sha256,
					'media_info', ma.media_info,
					'status', ma.status,
					'error_code', ma.error_code,
					'error_message', ma.error_message,
					'created_at', ma.created_at,
					'deleted_at', ma.deleted_at
				)
				ORDER BY ma.created_at, ma.kind
			)
			FROM media_assets ma
			WHERE ma.task_id = t.id
		), '[]'::jsonb)
	FROM tasks t
`

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(row scanner) (Task, error) {
	var task Task
	var stepsJSON []byte
	var assetsJSON []byte
	var publishBlockersJSON []byte
	if err := row.Scan(
		&task.ID,
		&task.Status,
		&task.TargetPlatforms,
		&task.SourceURL,
		&task.CookieProfileID,
		&task.PostingStrategyID,
		&task.AutoPublish,
		&task.PublishJobID,
		&task.PublishStatus,
		&publishBlockersJSON,
		&task.PublishMode,
		&task.StatementVersion,
		&task.StatementBrief,
		&task.StatementFull,
		&task.OriginalTitle,
		&task.Title,
		&task.Description,
		&task.ThumbnailURL,
		&task.DurationSeconds,
		&task.Extractor,
		&task.ReviewMode,
		&task.ReviewStatus,
		&task.ReviewSummary,
		&task.SettingsVersion,
		&task.Tags,
		&task.Category,
		&task.ErrorCode,
		&task.ErrorMessage,
		&task.ErrorRetryable,
		&task.Version,
		&task.CreatedAt,
		&task.UpdatedAt,
		&task.ArchivedAt,
		&task.ArchivedBy,
		&task.PausedAt,
		&task.PausedFromStatus,
		&task.PausedStepKind,
		&stepsJSON,
		&assetsJSON,
	); err != nil {
		return Task{}, err
	}
	if err := json.Unmarshal(stepsJSON, &task.Steps); err != nil {
		return Task{}, fmt.Errorf("decode task steps: %w", err)
	}
	annotateStepActivity(task.Steps, time.Now().UTC())
	if err := json.Unmarshal(assetsJSON, &task.Assets); err != nil {
		return Task{}, fmt.Errorf("decode media assets: %w", err)
	}
	if err := json.Unmarshal(publishBlockersJSON, &task.PublishBlockers); err != nil {
		return Task{}, fmt.Errorf("decode publish blockers: %w", err)
	}
	return task, nil
}

func annotateStepActivity(steps []Step, now time.Time) {
	for index := range steps {
		step := &steps[index]
		if step.Kind != StepDownload || step.Status != "running" {
			continue
		}
		age := now.Sub(step.UpdatedAt)
		if age < 0 {
			age = 0
		}
		step.HeartbeatAgeSeconds = int64(age / time.Second)
		phase, _ := step.Detail["phase"].(string)
		if strings.TrimSpace(phase) == "" {
			step.ActivityState = "telemetry_pending"
			continue
		}
		switch {
		case age <= 15*time.Second:
			step.ActivityState = "active"
		case age <= 60*time.Second:
			step.ActivityState = "delayed"
		default:
			step.ActivityState = "stalled"
		}
	}
}

func (s *PostgresStore) List(ctx context.Context, limit int, scope string) ([]Task, error) {
	where := " WHERE t.archived_at IS NULL"
	switch scope {
	case "archived":
		where = " WHERE t.archived_at IS NOT NULL"
	case "all":
		where = ""
	}
	rows, err := s.pool.Query(
		ctx,
		taskSelect+where+" ORDER BY COALESCE(t.archived_at,t.created_at) DESC LIMIT $1",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	result := make([]Task, 0, limit)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		result = append(result, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) Get(ctx context.Context, id string) (Task, error) {
	task, err := scanTask(s.pool.QueryRow(ctx, taskSelect+" WHERE t.id=$1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, fmt.Errorf("get task: %w", err)
	}
	return task, nil
}

func (s *PostgresStore) Summary(ctx context.Context) (Summary, error) {
	var summary Summary
	err := s.pool.QueryRow(ctx, `
		SELECT
			count(*) AS total,
			count(*) FILTER (WHERE status IN (
				'queued','fetching_metadata','metadata_ready','downloading',
				'processing','ready_to_publish','publishing'
			)) AS active,
			count(*) FILTER (WHERE status='awaiting_manual_review') AS awaiting_manual_review,
			count(*) FILTER (WHERE status IN ('published','reconciled')) AS published,
			count(*) FILTER (WHERE status='failed') AS failed
		FROM tasks
		WHERE archived_at IS NULL
	`).Scan(
		&summary.Total,
		&summary.Active,
		&summary.AwaitingManualReview,
		&summary.Published,
		&summary.Failed,
	)
	if err != nil {
		return Summary{}, fmt.Errorf("query task summary: %w", err)
	}
	return summary, nil
}

func (s *PostgresStore) ArchivePreview(
	ctx context.Context,
) (ArchivePreview, error) {
	var preview ArchivePreview
	err := s.pool.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (
				WHERE status IN (
					'awaiting_manual_review','ready_to_publish',
					'published','reconciled','failed','cancelled','abandoned'
				)
			),
			count(*) FILTER (
				WHERE status NOT IN (
					'awaiting_manual_review','ready_to_publish',
					'published','reconciled','failed','cancelled','abandoned'
				)
			),
			count(*) FILTER (WHERE status IN ('published','reconciled'))
		FROM tasks
		WHERE archived_at IS NULL
	`).Scan(
		&preview.TotalTasks,
		&preview.ArchivableTasks,
		&preview.RunningTasks,
		&preview.PublishedTasks,
	)
	if err != nil {
		return ArchivePreview{}, fmt.Errorf("query task archive preview: %w", err)
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
			count(*),
			COALESCE(sum(asset.size_bytes),0)
		FROM media_assets asset
		JOIN tasks task ON task.id=asset.task_id
		WHERE task.archived_at IS NULL
		  AND asset.status IN ('available','failed')
	`).Scan(&preview.AssetCount, &preview.AssetBytes); err != nil {
		return ArchivePreview{}, fmt.Errorf("query task archive asset preview: %w", err)
	}
	return preview, nil
}

func (s *PostgresStore) ArchiveCandidates(
	ctx context.Context,
	limit int,
) ([]ArchiveCandidate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, version
		FROM tasks
		WHERE archived_at IS NULL
		ORDER BY created_at
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list task archive candidates: %w", err)
	}
	defer rows.Close()
	result := make([]ArchiveCandidate, 0, limit)
	for rows.Next() {
		var candidate ArchiveCandidate
		if err := rows.Scan(&candidate.ID, &candidate.Version); err != nil {
			return nil, fmt.Errorf("scan task archive candidate: %w", err)
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task archive candidates: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) CookieProfileExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM cookie_profiles
			WHERE id=$1 AND octet_length(encrypted_cookie_jar) > 0
		)
	`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check cookie profile: %w", err)
	}
	return exists, nil
}

func (s *PostgresStore) PostingStrategy(
	ctx context.Context,
	id string,
) (PostingStrategyReference, error) {
	var result PostingStrategyReference
	err := s.pool.QueryRow(ctx, `
		SELECT
			strategy.id::text,
			strategy.enabled,
			strategy.automation_mode,
			strategy.target_platforms,
			jsonb_build_object(
				'id', strategy.id::text,
				'enabled', strategy.enabled,
				'automation_mode', strategy.automation_mode,
				'target_platforms', strategy.target_platforms,
				'account_bindings', strategy.account_bindings,
				'category_bindings', strategy.category_bindings,
				'title_templates', strategy.title_templates,
				'description_templates', strategy.description_templates,
				'default_tags', strategy.default_tags,
				'repost_statement_version', strategy.repost_statement_version,
				'transcode_preset_id', strategy.transcode_preset_id::text,
				'transcode_preset', CASE
					WHEN strategy.transcode_preset_id IS NULL THEN NULL
					ELSE jsonb_build_object(
						'id', strategy.transcode_preset_id::text,
						'available', (
							preset.id IS NOT NULL
							AND preset.enabled
							AND preset.archived_at IS NULL
						),
						'encoder_mode', COALESCE(preset.encoder_mode, ''),
						'video_codec', COALESCE(preset.video_codec, ''),
						'audio_codec', COALESCE(preset.audio_codec, ''),
						'container', COALESCE(preset.container, ''),
						'cpu_preset', COALESCE(preset.cpu_preset, ''),
						'high_resolution_cpu_preset',
							COALESCE(preset.high_resolution_cpu_preset, ''),
						'maximum_height', COALESCE(preset.maximum_height, 0),
						'video_bitrate_kbps',
							COALESCE(preset.video_bitrate_kbps, 0),
						'audio_bitrate_kbps',
							COALESCE(preset.audio_bitrate_kbps, 0),
						'burn_subtitles', COALESCE(preset.burn_subtitles, false),
						'custom_arguments',
							COALESCE(preset.custom_arguments, '{}'::text[])
					)
				END,
				'require_content_moderation',
					strategy.require_content_moderation,
				'schedule_mode', strategy.schedule_mode,
				'schedule_time', to_char(strategy.schedule_time, 'HH24:MI'),
				'version', strategy.version
			)
		FROM posting_strategies strategy
		LEFT JOIN transcode_presets preset
		  ON preset.id=strategy.transcode_preset_id
		WHERE strategy.id=$1 AND strategy.archived_at IS NULL
	`, id).Scan(
		&result.ID,
		&result.Enabled,
		&result.AutomationMode,
		&result.TargetPlatforms,
		&result.Snapshot,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return PostingStrategyReference{}, nil
	}
	if err != nil {
		return PostingStrategyReference{}, fmt.Errorf("load task posting strategy: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) SetCookieProfile(
	ctx context.Context,
	taskID string,
	cookieProfileID *string,
	now time.Time,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin set task cookie profile: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status     string
		archivedAt *time.Time
	)
	err = tx.QueryRow(
		ctx,
		"SELECT status, archived_at FROM tasks WHERE id=$1 FOR UPDATE",
		taskID,
	).Scan(&status, &archivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock task for cookie profile: %w", err)
	}
	if archivedAt != nil {
		return &ConflictError{
			Code:    "task_archived",
			Message: "回收站中的任务不能修改，请先恢复",
		}
	}
	if status != StatusFailed && status != StatusCancelled && status != StatusAwaitingReview {
		return &ConflictError{
			Code:    "task_cookie_profile_locked",
			Message: "任务正在执行，暂时不能更换 Cookie 配置",
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET cookie_profile_id=$2, updated_at=$3, version=version+1
		WHERE id=$1
	`, taskID, cookieProfileID, now); err != nil {
		return fmt.Errorf("update task cookie profile: %w", err)
	}
	if err := s.insertUserAudit(
		ctx,
		tx,
		taskID,
		"task.cookie-profile.updated",
		map[string]any{"cookie_profile_id": cookieProfileID},
		now,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit task cookie profile: %w", err)
	}
	return nil
}

func (s *PostgresStore) Cancel(ctx context.Context, taskID string, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin cancel task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status     string
		archivedAt *time.Time
	)
	err = tx.QueryRow(
		ctx,
		"SELECT status, archived_at FROM tasks WHERE id=$1 FOR UPDATE",
		taskID,
	).Scan(&status, &archivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock task for cancellation: %w", err)
	}
	if archivedAt != nil {
		return &ConflictError{
			Code:    "task_archived",
			Message: "回收站中的任务不能取消，请先恢复",
		}
	}
	if status == "published" || status == "reconciled" || status == "abandoned" {
		return &ConflictError{
			Code:    "task_not_cancellable",
			Message: "当前任务状态不允许取消",
		}
	}

	var publicationRequiresResolution bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM platform_publications
			WHERE task_id=$1
			  AND status IN ('submitting','reconciliation_required','published')
		)
	`, taskID).Scan(&publicationRequiresResolution); err != nil {
		return fmt.Errorf("check task publications before cancellation: %w", err)
	}
	if publicationRequiresResolution {
		return &ConflictError{
			Code:    "task_publication_resolution_required",
			Message: "平台可能已经收到投稿，请先完成平台对账后再取消任务",
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE publication_attempts
		SET status='cancelled',
		    error_code='cancelled_by_operator',
		    error_message='任务已由操作员取消',
		    completed_at=COALESCE(completed_at,$2)
		WHERE publication_id IN (
			SELECT id FROM platform_publications WHERE task_id=$1
		)
		  AND status='running'
	`, taskID, now); err != nil {
		return fmt.Errorf("cancel task publication attempts: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE platform_publications
		SET status='cancelled',
		    error_code='cancelled_by_operator',
		    error_message='任务已由操作员取消',
		    error_retryable=false,
		    locked_at=NULL,
		    locked_by='',
		    completed_at=COALESCE(completed_at,$2),
		    updated_at=$2,
		    version=version+1
		WHERE task_id=$1
		  AND status IN ('draft','blocked','queued','preparing','uploading')
	`, taskID, now); err != nil {
		return fmt.Errorf("cancel task platform publications: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE publish_jobs
		SET status='cancelled',
		    completed_at=COALESCE(completed_at,$2),
		    updated_at=$2,
		    version=version+1
		WHERE task_id=$1
		  AND status IN ('draft','blocked','queued','publishing','partial_success','failed')
	`, taskID, now); err != nil {
		return fmt.Errorf("cancel task publish jobs: %w", err)
	}

	if status == StatusCancelled {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE task_steps
		SET status='cancelled',
		    finished_at=COALESCE(finished_at,$2),
		    updated_at=$2
		WHERE task_id=$1 AND status IN ('queued','running')
	`, taskID, now); err != nil {
		return fmt.Errorf("cancel task steps: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status='cancelled',
		    paused_at=NULL,
		    paused_from_status='',
		    paused_step_kind='',
		    error_code='',
		    error_message='',
		    error_retryable=false,
		    updated_at=$2,
		    version=version+1
		WHERE id=$1
	`, taskID, now); err != nil {
		return fmt.Errorf("cancel task: %w", err)
	}
	if err := s.insertUserAudit(
		ctx,
		tx,
		taskID,
		"task.cancelled",
		map[string]any{"previous_status": status},
		now,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cancel task: %w", err)
	}
	return nil
}

func (s *PostgresStore) Pause(ctx context.Context, taskID string, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin pause task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status     string
		archivedAt *time.Time
		pausedAt   *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT status, archived_at, paused_at
		FROM tasks
		WHERE id=$1
		FOR UPDATE
	`, taskID).Scan(&status, &archivedAt, &pausedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock task for pause: %w", err)
	}
	if archivedAt != nil {
		return &ConflictError{Code: "task_archived", Message: "回收站中的任务不能暂停，请先恢复"}
	}
	if pausedAt != nil {
		return tx.Commit(ctx)
	}
	if status == StatusFailed || status == StatusCancelled || status == "abandoned" ||
		status == StatusPublished || status == "reconciled" {
		return &ConflictError{Code: "task_not_pausable", Message: "当前任务已经停止，不能暂停"}
	}

	var unsafePublication bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM platform_publications
			WHERE task_id=$1
			  AND status IN ('preparing','uploading','submitting','reconciliation_required')
		)
	`, taskID).Scan(&unsafePublication); err != nil {
		return fmt.Errorf("check task publications before pause: %w", err)
	}
	if status == StatusPublishing || unsafePublication {
		return &ConflictError{
			Code:    "task_publication_in_progress",
			Message: "平台投稿已经开始，当前阶段不能安全暂停；请等待本次提交结束后再操作",
		}
	}

	var stepKind string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE((
			SELECT kind
			FROM task_steps
			WHERE task_id=$1 AND status IN ('running','queued')
			ORDER BY CASE status WHEN 'running' THEN 0 ELSE 1 END, updated_at DESC
			LIMIT 1
		), '')
	`, taskID).Scan(&stepKind); err != nil {
		return fmt.Errorf("load task pause checkpoint: %w", err)
	}
	if stepKind == "" {
		switch status {
		case StatusAwaitingReview:
			stepKind = StepReview
		case StatusReadyToPublish:
			stepKind = StepPublish
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET paused_at=$2,
		    paused_from_status=status,
		    paused_step_kind=$3,
		    updated_at=$2,
		    version=version+1
		WHERE id=$1
	`, taskID, now, stepKind); err != nil {
		return fmt.Errorf("pause task: %w", err)
	}
	if err := s.insertUserAudit(ctx, tx, taskID, "task.paused", map[string]any{
		"status": status,
		"step":   stepKind,
	}, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit pause task: %w", err)
	}
	return nil
}

func (s *PostgresStore) Resume(ctx context.Context, taskID string, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin resume task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status       string
		archivedAt   *time.Time
		pausedAt     *time.Time
		pausedStatus string
		stepKind     string
	)
	err = tx.QueryRow(ctx, `
		SELECT status, archived_at, paused_at, paused_from_status, paused_step_kind
		FROM tasks
		WHERE id=$1
		FOR UPDATE
	`, taskID).Scan(&status, &archivedAt, &pausedAt, &pausedStatus, &stepKind)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock task for resume: %w", err)
	}
	if archivedAt != nil {
		return &ConflictError{Code: "task_archived", Message: "回收站中的任务不能继续，请先恢复"}
	}
	if pausedAt == nil {
		return tx.Commit(ctx)
	}
	if stepKind == StepDownload {
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET detail=jsonb_set(
			        COALESCE(detail, '{}'::jsonb),
			        '{phase}',
			        '"resume_requested"'::jsonb,
			        true
			    ),
			    updated_at=$2
			WHERE task_id=$1 AND kind='download' AND status='running'
		`, taskID, now); err != nil {
			return fmt.Errorf("mark download resume requested: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET paused_at=NULL,
		    paused_from_status='',
		    paused_step_kind='',
		    updated_at=$2,
		    version=version+1
		WHERE id=$1
	`, taskID, now); err != nil {
		return fmt.Errorf("resume task: %w", err)
	}
	if err := s.insertUserAudit(ctx, tx, taskID, "task.resumed", map[string]any{
		"status":        status,
		"paused_status": pausedStatus,
		"step":          stepKind,
	}, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit resume task: %w", err)
	}
	return nil
}

func (s *PostgresStore) Retry(ctx context.Context, taskID string, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin retry task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status     string
		archivedAt *time.Time
	)
	err = tx.QueryRow(
		ctx,
		"SELECT status, archived_at FROM tasks WHERE id=$1 FOR UPDATE",
		taskID,
	).Scan(&status, &archivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock task for retry: %w", err)
	}
	if archivedAt != nil {
		return &ConflictError{
			Code:    "task_archived",
			Message: "回收站中的任务不能重试，请先恢复",
		}
	}
	if status != StatusFailed && status != StatusCancelled {
		return &ConflictError{
			Code:    "task_not_retryable",
			Message: "只有失败或已取消任务可以重新执行",
		}
	}

	var stepKind string
	var currentAttempt int
	err = tx.QueryRow(ctx, `
		SELECT kind, attempt
		FROM task_steps
		WHERE task_id=$1 AND status IN ('failed', 'cancelled')
		ORDER BY updated_at DESC
		LIMIT 1
		FOR UPDATE
	`, taskID).Scan(&stepKind, &currentAttempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return &ConflictError{
			Code:    "failed_step_missing",
			Message: "任务没有可重试的失败步骤",
		}
	}
	if err != nil {
		return fmt.Errorf("load failed task step: %w", err)
	}

	var eventType string
	var nextStatus string
	var stepKindsToReset []string
	switch stepKind {
	case StepMetadata:
		eventType = MetadataRequestedV1
		nextStatus = StatusQueued
		stepKindsToReset = []string{StepMetadata}
	case StepDownload:
		eventType = DownloadRequestedV1
		nextStatus = StatusMetadataReady
		stepKindsToReset = []string{StepDownload, StepMediaInspect}
	case StepMediaInspect:
		eventType = DownloadRequestedV1
		nextStatus = StatusMetadataReady
		stepKindsToReset = []string{StepDownload, StepMediaInspect}
	case StepSubtitles:
		eventType = SubtitleRequestedV1
		nextStatus = StatusProcessing
		stepKindsToReset = []string{StepSubtitles}
	case StepTranscode:
		eventType = TranscodeRequestedV1
		nextStatus = StatusProcessing
		stepKindsToReset = []string{StepTranscode}
	default:
		return &ConflictError{
			Code:    "step_not_retryable",
			Message: "当前失败步骤暂不支持重试",
		}
	}

	var sourceURL string
	var cookieProfileID *string
	var settingsSnapshot []byte
	if err := tx.QueryRow(ctx, `
		SELECT source_url, cookie_profile_id::text, settings_snapshot
		FROM tasks
		WHERE id=$1
	`, taskID).Scan(&sourceURL, &cookieProfileID, &settingsSnapshot); err != nil {
		return fmt.Errorf("load retry source: %w", err)
	}

	nextAttempt := currentAttempt + 1
	commandData := map[string]any{
		"task_id":           taskID,
		"source_url":        sourceURL,
		"cookie_profile_id": cookieProfileID,
		"attempt":           nextAttempt,
	}
	if stepKind == StepTranscode {
		runID, err := identity.NewUUID()
		if err != nil {
			return err
		}
		commandData["run_id"] = runID
		var inputAssetID string
		if err := tx.QueryRow(ctx, `
			SELECT id::text
			FROM media_assets
			WHERE task_id=$1 AND kind='source' AND status='available'
			ORDER BY created_at DESC
			LIMIT 1
		`, taskID).Scan(&inputAssetID); err != nil {
			return fmt.Errorf("load transcode retry input: %w", err)
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
			return fmt.Errorf("load legacy transcode retry preset: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO transcode_runs (
				id, task_id, preset_id, status, attempt, input_asset_id,
				command_summary, progress, created_at, updated_at
			) VALUES ($1,$2,$3,'queued',$4,$5,'{}'::jsonb,0,$6,$6)
		`, runID, taskID, presetID, nextAttempt, inputAssetID, now); err != nil {
			return fmt.Errorf("insert transcode retry run: %w", err)
		}
	}
	command, err := events.New(
		eventType,
		"visoraft/control-api",
		"task/"+taskID,
		now,
		commandData,
	)
	if err != nil {
		return err
	}
	rawCommand, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("marshal retry command: %w", err)
	}
	outboxID, err := identity.NewUUID()
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE task_steps
		SET status='queued',
		    attempt=$3,
		    progress=0,
		    error_code='',
		    error_message='',
		    started_at=NULL,
		    finished_at=NULL,
		    updated_at=$2
		WHERE task_id=$1 AND kind=ANY($4::text[])
	`, taskID, now, nextAttempt, stepKindsToReset); err != nil {
		return fmt.Errorf("reset failed task step: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status=$2,
		    error_code='',
		    error_message='',
		    error_retryable=false,
		    updated_at=$3,
		    version=version+1
		WHERE id=$1
	`, taskID, nextStatus, now); err != nil {
		return fmt.Errorf("reset failed task: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (
			id, aggregate_id, event_type, payload, status,
			attempts, available_at, created_at
		) VALUES ($1,$2,$3,$4,'pending',0,$5,$5)
	`, outboxID, taskID, eventType, rawCommand, now); err != nil {
		return fmt.Errorf("enqueue retry command: %w", err)
	}
	if err := s.insertUserAudit(
		ctx,
		tx,
		taskID,
		"task.retry.requested",
		map[string]any{
			"step":    stepKind,
			"attempt": nextAttempt,
		},
		now,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit retry task: %w", err)
	}
	return nil
}

func (s *PostgresStore) DeleteAssets(ctx context.Context, taskID string, now time.Time) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin asset cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		taskStatus string
		archivedAt *time.Time
	)
	err = tx.QueryRow(
		ctx,
		"SELECT status, archived_at FROM tasks WHERE id=$1 FOR UPDATE",
		taskID,
	).Scan(&taskStatus, &archivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock task for asset cleanup: %w", err)
	}
	if archivedAt == nil && (taskStatus == "published" || taskStatus == "reconciled") {
		return &ConflictError{
			Code:    "published_assets_protected",
			Message: "已发布任务的媒体文件不能直接清理",
		}
	}

	type cleanupAsset struct {
		ID        string
		Bucket    string
		ObjectKey string
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, bucket, object_key
		FROM media_assets
		WHERE task_id=$1 AND status IN ('available','failed')
		ORDER BY created_at
		FOR UPDATE
	`, taskID)
	if err != nil {
		return fmt.Errorf("load assets for cleanup: %w", err)
	}
	assets := make([]cleanupAsset, 0)
	for rows.Next() {
		var asset cleanupAsset
		if err := rows.Scan(&asset.ID, &asset.Bucket, &asset.ObjectKey); err != nil {
			rows.Close()
			return fmt.Errorf("scan asset for cleanup: %w", err)
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate assets for cleanup: %w", err)
	}
	rows.Close()
	if len(assets) == 0 {
		return &ConflictError{
			Code:    "assets_not_available",
			Message: "这条任务没有可清理的媒体文件",
		}
	}

	assetIDs := make([]string, 0, len(assets))
	commandAssets := make([]map[string]string, 0, len(assets))
	for _, asset := range assets {
		assetIDs = append(assetIDs, asset.ID)
		commandAssets = append(commandAssets, map[string]string{
			"asset_id":   asset.ID,
			"bucket":     asset.Bucket,
			"object_key": asset.ObjectKey,
		})
	}

	command, err := events.New(
		AssetsDeleteRequestedV1,
		"visoraft/control-api",
		"task/"+taskID,
		now,
		map[string]any{
			"task_id": taskID,
			"assets":  commandAssets,
		},
	)
	if err != nil {
		return err
	}
	rawCommand, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("marshal asset cleanup command: %w", err)
	}
	outboxID, err := identity.NewUUID()
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE media_assets
		SET status='deleting', error_code='', error_message=''
		WHERE task_id=$1 AND id=ANY($2::uuid[])
	`, taskID, assetIDs); err != nil {
		return fmt.Errorf("mark assets deleting: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET updated_at=$2, version=version+1
		WHERE id=$1
	`, taskID, now); err != nil {
		return fmt.Errorf("touch task for asset cleanup: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (
			id, aggregate_id, event_type, payload, status,
			attempts, available_at, created_at
		) VALUES ($1,$2,$3,$4,'pending',0,$5,$5)
	`, outboxID, taskID, AssetsDeleteRequestedV1, rawCommand, now); err != nil {
		return fmt.Errorf("enqueue asset cleanup: %w", err)
	}
	if err := s.insertUserAudit(
		ctx,
		tx,
		taskID,
		"assets.delete.requested",
		map[string]any{"asset_ids": assetIDs},
		now,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit asset cleanup: %w", err)
	}
	return nil
}

func (s *PostgresStore) Archive(
	ctx context.Context,
	taskID string,
	input ArchiveInput,
	now time.Time,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin task archive: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status     string
		version    int64
		archivedAt *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT status, version, archived_at
		FROM tasks
		WHERE id=$1
		FOR UPDATE
	`, taskID).Scan(&status, &version, &archivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock task for archive: %w", err)
	}
	if version != input.ExpectedVersion {
		return &ConflictError{
			Code:    "task_version_conflict",
			Message: "任务已经更新，请刷新后重试",
		}
	}
	if archivedAt != nil {
		return &ConflictError{
			Code:    "task_already_archived",
			Message: "任务已经在回收站中",
		}
	}
	if !archivableTaskStatus(status) {
		return &ConflictError{
			Code:    "task_archive_requires_stop",
			Message: "运行中的任务不能删除，请先取消并等待步骤安全停止",
		}
	}
	var executionActive bool
	if err := tx.QueryRow(ctx, `
		SELECT
			EXISTS(
				SELECT 1
				FROM task_steps
				WHERE task_id=$1 AND status='running'
			)
			OR EXISTS(
				SELECT 1
				FROM platform_publications
				WHERE task_id=$1
				  AND status IN (
					'queued','preparing','uploading','submitting',
					'reconciliation_required'
				  )
			)
	`, taskID).Scan(&executionActive); err != nil {
		return fmt.Errorf("check task archive activity: %w", err)
	}
	if executionActive {
		return &ConflictError{
			Code:    "task_archive_execution_active",
			Message: "后台步骤或平台对账仍在运行，请稍后再删除",
		}
	}

	assetIDs := []string{}
	if input.DeleteAssets {
		assetIDs, err = queueAssetDeletionTx(ctx, tx, taskID, now)
		if err != nil {
			return err
		}
	}
	commandTag, err := tx.Exec(ctx, `
		UPDATE tasks
		SET archived_at=$3,
		    archived_by='local-operator',
		    updated_at=$3,
		    version=version+1
		WHERE id=$1 AND version=$2 AND archived_at IS NULL
	`, taskID, input.ExpectedVersion, now)
	if err != nil {
		return fmt.Errorf("archive task: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return &ConflictError{
			Code:    "task_version_conflict",
			Message: "任务已经更新，请刷新后重试",
		}
	}
	if err := s.insertUserAudit(
		ctx,
		tx,
		taskID,
		"task.archived",
		map[string]any{
			"previous_status": status,
			"delete_assets":   input.DeleteAssets,
			"asset_ids":       assetIDs,
			"reason":          input.Reason,
		},
		now,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit task archive: %w", err)
	}
	return nil
}

func (s *PostgresStore) Restore(
	ctx context.Context,
	taskID string,
	input RestoreInput,
	now time.Time,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin task restore: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		version    int64
		archivedAt *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT version, archived_at
		FROM tasks
		WHERE id=$1
		FOR UPDATE
	`, taskID).Scan(&version, &archivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock task for restore: %w", err)
	}
	if version != input.ExpectedVersion {
		return &ConflictError{
			Code:    "task_version_conflict",
			Message: "任务已经更新，请刷新后重试",
		}
	}
	if archivedAt == nil {
		return &ConflictError{
			Code:    "task_not_archived",
			Message: "任务不在回收站中",
		}
	}
	var deletingAssets int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM media_assets
		WHERE task_id=$1 AND status='deleting'
	`, taskID).Scan(&deletingAssets); err != nil {
		return fmt.Errorf("check restoring task assets: %w", err)
	}
	if deletingAssets > 0 {
		return &ConflictError{
			Code:    "task_asset_cleanup_in_progress",
			Message: "媒体文件仍在清理，完成后才能恢复任务",
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET archived_at=NULL,
		    archived_by='',
		    updated_at=$3,
		    version=version+1
		WHERE id=$1 AND version=$2
	`, taskID, input.ExpectedVersion, now); err != nil {
		return fmt.Errorf("restore task: %w", err)
	}
	if err := s.insertUserAudit(
		ctx,
		tx,
		taskID,
		"task.restored",
		map[string]any{"reason": input.Reason},
		now,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit task restore: %w", err)
	}
	return nil
}

func (s *PostgresStore) Purge(
	ctx context.Context,
	taskID string,
	input PurgeInput,
	now time.Time,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin task purge: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		status     string
		title      string
		sourceURL  string
		version    int64
		archivedAt *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT status, title, source_url, version, archived_at
		FROM tasks
		WHERE id=$1
		FOR UPDATE
	`, taskID).Scan(&status, &title, &sourceURL, &version, &archivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock task for purge: %w", err)
	}
	if version != input.ExpectedVersion {
		return &ConflictError{
			Code:    "task_version_conflict",
			Message: "任务已经更新，请刷新后重试",
		}
	}
	if archivedAt == nil {
		return &ConflictError{
			Code:    "task_purge_requires_archive",
			Message: "请先把任务移入回收站",
		}
	}
	var remainingAssets int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM media_assets
		WHERE task_id=$1 AND status <> 'deleted'
	`, taskID).Scan(&remainingAssets); err != nil {
		return fmt.Errorf("check task purge assets: %w", err)
	}
	if remainingAssets > 0 {
		return &ConflictError{
			Code:    "task_purge_assets_remaining",
			Message: "仍有媒体文件未清理；为防止对象存储孤儿，暂不能永久删除记录",
		}
	}
	if err := s.insertUserAudit(
		ctx,
		tx,
		taskID,
		"task.purged",
		map[string]any{
			"status":      status,
			"title":       title,
			"source_url":  sourceURL,
			"reason":      input.Reason,
			"archived_at": archivedAt,
		},
		now,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM outbox_messages
		WHERE aggregate_id=$1
	`, taskID); err != nil {
		return fmt.Errorf("delete task outbox messages: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM publish_jobs
		WHERE task_id=$1
	`, taskID); err != nil {
		return fmt.Errorf("delete task publish jobs: %w", err)
	}
	commandTag, err := tx.Exec(ctx, "DELETE FROM tasks WHERE id=$1", taskID)
	if err != nil {
		return fmt.Errorf("purge task: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit task purge: %w", err)
	}
	return nil
}

func queueAssetDeletionTx(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	now time.Time,
) ([]string, error) {
	type cleanupAsset struct {
		ID        string
		Bucket    string
		ObjectKey string
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, bucket, object_key
		FROM media_assets
		WHERE task_id=$1 AND status IN ('available','failed')
		ORDER BY created_at
		FOR UPDATE
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("load archive assets for cleanup: %w", err)
	}
	assets := make([]cleanupAsset, 0)
	for rows.Next() {
		var asset cleanupAsset
		if err := rows.Scan(&asset.ID, &asset.Bucket, &asset.ObjectKey); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan archive asset for cleanup: %w", err)
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate archive assets for cleanup: %w", err)
	}
	rows.Close()
	if len(assets) == 0 {
		return []string{}, nil
	}

	assetIDs := make([]string, 0, len(assets))
	commandAssets := make([]map[string]string, 0, len(assets))
	for _, asset := range assets {
		assetIDs = append(assetIDs, asset.ID)
		commandAssets = append(commandAssets, map[string]string{
			"asset_id":   asset.ID,
			"bucket":     asset.Bucket,
			"object_key": asset.ObjectKey,
		})
	}
	command, err := events.New(
		AssetsDeleteRequestedV1,
		"visoraft/control-api",
		"task/"+taskID,
		now,
		map[string]any{
			"task_id": taskID,
			"assets":  commandAssets,
		},
	)
	if err != nil {
		return nil, err
	}
	rawCommand, err := json.Marshal(command)
	if err != nil {
		return nil, fmt.Errorf("marshal archive asset cleanup command: %w", err)
	}
	outboxID, err := identity.NewUUID()
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE media_assets
		SET status='deleting', error_code='', error_message=''
		WHERE task_id=$1 AND id=ANY($2::uuid[])
	`, taskID, assetIDs); err != nil {
		return nil, fmt.Errorf("mark archive assets deleting: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (
			id, aggregate_id, event_type, payload, status,
			attempts, available_at, created_at
		) VALUES ($1,$2,$3,$4,'pending',0,$5,$5)
	`, outboxID, taskID, AssetsDeleteRequestedV1, rawCommand, now); err != nil {
		return nil, fmt.Errorf("enqueue archive asset cleanup: %w", err)
	}
	return assetIDs, nil
}

func archivableTaskStatus(status string) bool {
	switch status {
	case "awaiting_manual_review",
		"ready_to_publish",
		"published",
		"reconciled",
		"failed",
		"cancelled",
		"abandoned":
		return true
	default:
		return false
	}
}

func (s *PostgresStore) ApplyMetadataStarted(ctx context.Context, envelope events.Envelope) error {
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='running', progress=5, started_at=COALESCE(started_at,$2),
			    updated_at=$2, error_code='', error_message=''
			WHERE task_id=$1 AND kind='metadata' AND status IN ('queued','running')
		`, taskID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='fetching_metadata', updated_at=$2, version=version+1,
			    error_code='', error_message='', error_retryable=false
			WHERE id=$1 AND status IN ('queued','fetching_metadata')
		`, taskID, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "metadata.started", envelope, now)
	})
}

func (s *PostgresStore) ApplyMetadataCompleted(ctx context.Context, envelope events.Envelope, metadata Metadata) error {
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps AS step
			SET status='succeeded', progress=100, finished_at=$2, updated_at=$2,
			    error_code='', error_message=''
			FROM tasks AS task
			WHERE step.task_id=$1
			  AND step.kind='metadata'
			  AND task.id=step.task_id
			  AND task.status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now); err != nil {
			return err
		}
		commandTag, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='metadata_ready',
			    original_title=$2,
			    title=CASE WHEN title='' THEN $2 ELSE title END,
			    description=CASE WHEN description='' THEN $3 ELSE description END,
			    thumbnail_url=$4,
			    duration_seconds=$5,
			    extractor=$6,
			    error_code='',
			    error_message='',
			    error_retryable=false,
			    updated_at=$7,
			    version=version+1
			WHERE id=$1 AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, metadata.Title, metadata.Description, metadata.ThumbnailURL, metadata.DurationSeconds, metadata.Extractor, now)
		if err != nil {
			return err
		}
		if commandTag.RowsAffected() == 0 {
			return s.insertWorkflowAudit(ctx, tx, taskID, "metadata.completed.ignored", envelope, now)
		}

		var sourceURL string
		var cookieProfileID *string
		if err := tx.QueryRow(ctx, `
			SELECT source_url, cookie_profile_id::text
			FROM tasks
			WHERE id=$1
		`, taskID).Scan(&sourceURL, &cookieProfileID); err != nil {
			return fmt.Errorf("load download source: %w", err)
		}

		downloadStepID, err := identity.NewUUID()
		if err != nil {
			return err
		}
		inspectStepID, err := identity.NewUUID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_steps (
				id, task_id, kind, status, attempt, progress, updated_at
			) VALUES
				($1,$3,'download','queued',1,0,$4),
				($2,$3,'media_inspect','queued',1,0,$4)
			ON CONFLICT (task_id, kind) DO NOTHING
		`, downloadStepID, inspectStepID, taskID, now); err != nil {
			return fmt.Errorf("create download and media inspection steps: %w", err)
		}

		downloadCommand, err := events.New(
			DownloadRequestedV1,
			"visoraft/workflow-consumer",
			"task/"+taskID,
			now,
			map[string]any{
				"task_id":           taskID,
				"source_url":        sourceURL,
				"cookie_profile_id": cookieProfileID,
				"attempt":           1,
			},
		)
		if err != nil {
			return err
		}
		rawCommand, err := json.Marshal(downloadCommand)
		if err != nil {
			return fmt.Errorf("marshal download command: %w", err)
		}
		outboxID, err := identity.NewUUID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_messages (
				id, aggregate_id, event_type, payload, status,
				attempts, available_at, created_at
			) VALUES ($1,$2,$3,$4,'pending',0,$5,$5)
		`, outboxID, taskID, DownloadRequestedV1, rawCommand, now); err != nil {
			return fmt.Errorf("enqueue download command: %w", err)
		}

		return s.insertWorkflowAudit(ctx, tx, taskID, "metadata.completed", envelope, now)
	})
}

func (s *PostgresStore) ApplyDownloadStarted(ctx context.Context, envelope events.Envelope) error {
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps AS step
			SET status='running', progress=GREATEST(progress, 1),
			    started_at=COALESCE(started_at,$2), updated_at=$2,
			    detail=jsonb_set(COALESCE(detail, '{}'::jsonb), '{phase}', '"starting"'::jsonb, true),
			    error_code='', error_message=''
			FROM tasks AS task
			WHERE step.task_id=$1
			  AND step.kind='download'
			  AND task.id=step.task_id
			  AND task.status NOT IN ('cancelled','abandoned','published','reconciled')
			  AND step.status IN ('queued','running')
		`, taskID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='downloading', updated_at=$2, version=version+1,
			    error_code='', error_message='', error_retryable=false
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "download.started", envelope, now)
	})
}

func (s *PostgresStore) ApplyDownloadProgress(
	ctx context.Context,
	envelope events.Envelope,
	progress DownloadProgress,
) error {
	if err := normalizeDownloadProgress(&progress); err != nil {
		return err
	}
	detailRaw, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("encode download progress detail: %w", err)
	}
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if progress.TaskID != "" && progress.TaskID != taskID {
			return fmt.Errorf("download progress task id does not match subject")
		}
		_, err := tx.Exec(ctx, `
			UPDATE task_steps AS step
			SET progress=GREATEST(progress,$2), detail=$4::jsonb, updated_at=$3
			FROM tasks AS task
			WHERE step.task_id=$1
			  AND step.kind='download'
			  AND task.id=step.task_id
			  AND task.status='downloading'
			  AND step.status='running'
		`, taskID, progress.Progress, now, detailRaw)
		return err
	})
}

func normalizeDownloadProgress(progress *DownloadProgress) error {
	if progress.Progress < 1 {
		progress.Progress = 1
	}
	if progress.Progress > 99 {
		progress.Progress = 99
	}
	progress.Phase = strings.TrimSpace(progress.Phase)
	if progress.Phase == "" {
		progress.Phase = "downloading"
	}
	if len(progress.Phase) > 64 {
		return fmt.Errorf("download progress phase is too long")
	}
	if progress.Attempt < 1 {
		progress.Attempt = 1
	}
	if progress.DownloadedBytes < 0 ||
		progress.TotalBytes < 0 ||
		progress.SpeedBytesPerSecond < 0 ||
		progress.FragmentIndex < 0 ||
		progress.FragmentCount < 0 {
		return fmt.Errorf("download progress metrics are invalid")
	}
	if progress.ETASeconds != nil && *progress.ETASeconds < 0 {
		return fmt.Errorf("download progress ETA is invalid")
	}
	return nil
}

func (s *PostgresStore) ApplyMediaInspectStarted(
	ctx context.Context,
	envelope events.Envelope,
) error {
	var started struct {
		Attempt int `json:"attempt"`
	}
	if err := json.Unmarshal(envelope.Data, &started); err != nil {
		return fmt.Errorf("decode media inspection start: %w", err)
	}
	if started.Attempt < 1 {
		started.Attempt = 1
	}
	stepID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_steps (
				id, task_id, kind, status, attempt, progress, started_at, updated_at
			)
			SELECT $1, task.id, 'media_inspect', 'running', $3, 10, $4, $4
			FROM tasks AS task
			WHERE task.id=$2
			  AND task.status NOT IN ('cancelled','abandoned','published','reconciled')
			ON CONFLICT (task_id, kind) DO UPDATE SET
				status='running',
				attempt=GREATEST(task_steps.attempt, EXCLUDED.attempt),
				progress=10,
				started_at=COALESCE(task_steps.started_at, EXCLUDED.started_at),
				finished_at=NULL,
				updated_at=EXCLUDED.updated_at,
				error_code='',
				error_message=''
		`, stepID, taskID, started.Attempt, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='processing', updated_at=$2, version=version+1,
			    error_code='', error_message='', error_retryable=false
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "media.inspect.started", envelope, now)
	})
}

func (s *PostgresStore) ApplyMediaInspectCompleted(
	ctx context.Context,
	envelope events.Envelope,
	result MediaInspectResult,
) error {
	if err := validateMediaInfo(result.MediaInfo); err != nil {
		return err
	}
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps AS step
			SET status='succeeded', progress=100, finished_at=$2, updated_at=$2,
			    error_code='', error_message=''
			FROM tasks AS task
			WHERE step.task_id=$1
			  AND step.kind='media_inspect'
			  AND task.id=step.task_id
			  AND task.status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='processing', updated_at=$2, version=version+1
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "media.inspect.completed", envelope, now)
	})
}

func (s *PostgresStore) ApplyMediaInspectFailed(
	ctx context.Context,
	envelope events.Envelope,
	failure WorkflowFailure,
) error {
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='failed', finished_at=$2, updated_at=$2,
			    error_code=$3, error_message=$4
			WHERE task_id=$1 AND kind='media_inspect'
		`, taskID, now, failure.Code, failure.Message); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='cancelled', finished_at=COALESCE(finished_at,$2), updated_at=$2
			WHERE task_id=$1
			  AND kind IN ('download','media_inspect')
			  AND status IN ('queued','running')
		`, taskID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='failed', error_code=$2, error_message=$3,
			    error_retryable=$4, updated_at=$5, version=version+1
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, failure.Code, failure.Message, failure.Retryable, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "media.inspect.failed", envelope, now)
	})
}

func (s *PostgresStore) ApplyDownloadCompleted(
	ctx context.Context,
	envelope events.Envelope,
	result DownloadResult,
) error {
	if err := validateMediaInfo(result.MediaInfo); err != nil {
		return err
	}
	mediaInfoJSON, err := json.Marshal(result.MediaInfo)
	if err != nil {
		return fmt.Errorf("marshal media inspection result: %w", err)
	}
	var inspectedDurationSeconds *int
	if result.MediaInfo.DurationSeconds != nil {
		rounded := int(math.Round(*result.MediaInfo.DurationSeconds))
		inspectedDurationSeconds = &rounded
	}
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if result.AssetID == "" ||
			result.Kind == "" ||
			result.Bucket == "" ||
			result.ObjectKey == "" ||
			result.OriginalName == "" ||
			result.ContentType == "" ||
			result.SizeBytes < 0 ||
			len(result.ChecksumSHA256) != 64 {
			return fmt.Errorf("download result is missing required asset fields")
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO media_assets (
				id, task_id, kind, bucket, object_key, original_name,
				content_type, size_bytes, checksum_sha256, media_info, status, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'available',$11)
			ON CONFLICT (task_id, kind, object_key) DO UPDATE SET
				original_name=EXCLUDED.original_name,
				content_type=EXCLUDED.content_type,
				size_bytes=EXCLUDED.size_bytes,
				checksum_sha256=EXCLUDED.checksum_sha256,
				media_info=EXCLUDED.media_info,
				status='available',
				deleted_at=NULL
		`,
			result.AssetID,
			taskID,
			result.Kind,
			result.Bucket,
			result.ObjectKey,
			result.OriginalName,
			result.ContentType,
			result.SizeBytes,
			result.ChecksumSHA256,
			string(mediaInfoJSON),
			now,
		); err != nil {
			return fmt.Errorf("upsert media asset: %w", err)
		}

		for _, asset := range result.AdditionalAssets {
			if asset.AssetID == "" ||
				(asset.Kind != "thumbnail" && asset.Kind != "cover_processed") ||
				asset.Bucket == "" ||
				asset.ObjectKey == "" ||
				asset.OriginalName == "" ||
				(asset.ContentType != "image/jpeg" && asset.ContentType != "image/png") ||
				asset.SizeBytes <= 0 ||
				asset.SizeBytes > 10<<20 ||
				len(asset.ChecksumSHA256) != 64 ||
				asset.MediaInfo.Width == nil ||
				asset.MediaInfo.Height == nil ||
				*asset.MediaInfo.Width < 480 ||
				*asset.MediaInfo.Height < 270 {
				return fmt.Errorf("download result has an invalid additional cover asset")
			}
			assetMediaInfo, err := json.Marshal(asset.MediaInfo)
			if err != nil {
				return fmt.Errorf("marshal additional cover media info: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO media_assets (
					id, task_id, kind, bucket, object_key, original_name,
					content_type, size_bytes, checksum_sha256, media_info,
					status, created_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'available',$11)
				ON CONFLICT (task_id, kind, object_key) DO UPDATE SET
					original_name=EXCLUDED.original_name,
					content_type=EXCLUDED.content_type,
					size_bytes=EXCLUDED.size_bytes,
					checksum_sha256=EXCLUDED.checksum_sha256,
					media_info=EXCLUDED.media_info,
					status='available',
					deleted_at=NULL
			`,
				asset.AssetID,
				taskID,
				asset.Kind,
				asset.Bucket,
				asset.ObjectKey,
				asset.OriginalName,
				asset.ContentType,
				asset.SizeBytes,
				asset.ChecksumSHA256,
				assetMediaInfo,
				now,
			); err != nil {
				return fmt.Errorf("upsert additional cover asset: %w", err)
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE task_steps AS step
			SET status='succeeded', progress=100, finished_at=$2, updated_at=$2,
			    detail=CASE
			        WHEN step.kind='download'
			        THEN step.detail || '{"phase":"completed"}'::jsonb
			        ELSE step.detail
			    END,
			    error_code='', error_message=''
			FROM tasks AS task
			WHERE step.task_id=$1
			  AND step.kind IN ('download','media_inspect')
			  AND task.id=step.task_id
			  AND task.status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='processing',
			    duration_seconds=COALESCE(duration_seconds,$3),
			    error_code='',
			    error_message='',
			    error_retryable=false,
			    updated_at=$2,
			    version=version+1
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now, inspectedDurationSeconds); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "download.completed", envelope, now)
	})
}

func (s *PostgresStore) ApplyTranscodeStarted(
	ctx context.Context,
	envelope events.Envelope,
	event TranscodeProgress,
) error {
	if !identity.IsUUID(event.RunID) || event.Attempt < 1 {
		return fmt.Errorf("transcode started event has an invalid run")
	}
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if event.TaskID != "" && event.TaskID != taskID {
			return fmt.Errorf("transcode started task id does not match subject")
		}
		tag, err := tx.Exec(ctx, `
			UPDATE transcode_runs
			SET status='running',
			    progress=GREATEST(progress,1),
			    started_at=COALESCE(started_at,$4),
			    updated_at=$4,
			    error_code='',
			    error_message=''
			WHERE id=$1 AND task_id=$2 AND attempt=$3
			  AND status IN ('queued','running')
		`, event.RunID, taskID, event.Attempt, now)
		if err != nil {
			return fmt.Errorf("start transcode run: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("transcode run is missing or not startable")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='running',
			    progress=GREATEST(progress,1),
			    started_at=COALESCE(started_at,$3),
			    updated_at=$3,
			    error_code='',
			    error_message=''
			WHERE task_id=$1 AND kind='transcode' AND attempt=$2
			  AND status IN ('queued','running')
		`, taskID, event.Attempt, now); err != nil {
			return fmt.Errorf("start transcode step: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='processing',
			    updated_at=$2,
			    version=version+1,
			    error_code='',
			    error_message='',
			    error_retryable=false
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "transcode.started", envelope, now)
	})
}

func (s *PostgresStore) ApplyTranscodeProgress(
	ctx context.Context,
	envelope events.Envelope,
	event TranscodeProgress,
) error {
	if !identity.IsUUID(event.RunID) ||
		event.Attempt < 1 ||
		event.Progress < 1 ||
		event.Progress > 99 {
		return fmt.Errorf("transcode progress event is invalid")
	}
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if event.TaskID != "" && event.TaskID != taskID {
			return fmt.Errorf("transcode progress task id does not match subject")
		}
		tag, err := tx.Exec(ctx, `
			UPDATE transcode_runs
			SET progress=GREATEST(progress,$4), updated_at=$5
			WHERE id=$1 AND task_id=$2 AND attempt=$3 AND status='running'
		`, event.RunID, taskID, event.Attempt, event.Progress, now)
		if err != nil {
			return fmt.Errorf("update transcode run progress: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("transcode run is missing or not running")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET progress=GREATEST(progress,$3), updated_at=$4
			WHERE task_id=$1 AND kind='transcode' AND attempt=$2
			  AND status='running'
		`, taskID, event.Attempt, event.Progress, now); err != nil {
			return fmt.Errorf("update transcode step progress: %w", err)
		}
		return nil
	})
}

func (s *PostgresStore) ApplyTranscodeCompleted(
	ctx context.Context,
	envelope events.Envelope,
	result TranscodeResult,
) error {
	if !identity.IsUUID(result.RunID) ||
		!identity.IsUUID(result.InputAssetID) ||
		!identity.IsUUID(result.AssetID) ||
		result.Attempt < 1 ||
		result.Kind != "transcoded" ||
		result.Bucket == "" ||
		result.ObjectKey == "" ||
		result.OriginalName == "" ||
		result.ContentType == "" ||
		result.SizeBytes <= 0 ||
		len(result.ChecksumSHA256) != 64 ||
		result.ResolvedEncoder == "" {
		return fmt.Errorf("transcode result is missing required fields")
	}
	if err := validateMediaInfo(result.MediaInfo); err != nil {
		return err
	}
	mediaInfoJSON, err := json.Marshal(result.MediaInfo)
	if err != nil {
		return fmt.Errorf("marshal transcode media info: %w", err)
	}
	if result.CommandSummary == nil {
		result.CommandSummary = map[string]any{}
	}
	commandJSON, err := json.Marshal(result.CommandSummary)
	if err != nil {
		return fmt.Errorf("marshal transcode command summary: %w", err)
	}
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if result.TaskID != "" && result.TaskID != taskID {
			return fmt.Errorf("transcode result task id does not match subject")
		}
		if !strings.HasPrefix(result.ObjectKey, "tasks/"+taskID+"/transcoded/") {
			return fmt.Errorf("transcode output object key is outside the task prefix")
		}
		var inputExists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1
				FROM media_assets
				WHERE id=$1 AND task_id=$2 AND status='available'
			)
		`, result.InputAssetID, taskID).Scan(&inputExists); err != nil {
			return fmt.Errorf("check transcode input asset: %w", err)
		}
		if !inputExists {
			return fmt.Errorf("transcode input asset is missing")
		}
		var actualOutputAssetID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO media_assets (
				id, task_id, kind, bucket, object_key, original_name,
				content_type, size_bytes, checksum_sha256, media_info,
				status, created_at
			) VALUES ($1,$2,'transcoded',$3,$4,$5,$6,$7,$8,$9,'available',$10)
			ON CONFLICT (task_id, kind, object_key) DO UPDATE SET
				original_name=EXCLUDED.original_name,
				content_type=EXCLUDED.content_type,
				size_bytes=EXCLUDED.size_bytes,
				checksum_sha256=EXCLUDED.checksum_sha256,
				media_info=EXCLUDED.media_info,
				status='available',
				error_code='',
				error_message='',
				deleted_at=NULL
			RETURNING id::text
		`,
			result.AssetID,
			taskID,
			result.Bucket,
			result.ObjectKey,
			result.OriginalName,
			result.ContentType,
			result.SizeBytes,
			result.ChecksumSHA256,
			mediaInfoJSON,
			now,
		).Scan(&actualOutputAssetID); err != nil {
			return fmt.Errorf("upsert transcoded media asset: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE transcode_runs
			SET status='succeeded',
			    input_asset_id=$4,
			    output_asset_id=$5,
			    resolved_encoder=$6,
			    command_summary=$7,
			    progress=100,
			    error_code='',
			    error_message='',
			    completed_at=$8,
			    updated_at=$8
			WHERE id=$1 AND task_id=$2 AND attempt=$3
			  AND status IN ('queued','running')
		`,
			result.RunID,
			taskID,
			result.Attempt,
			result.InputAssetID,
			actualOutputAssetID,
			result.ResolvedEncoder,
			commandJSON,
			now,
		)
		if err != nil {
			return fmt.Errorf("complete transcode run: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("transcode run is missing or not completable")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='succeeded',
			    progress=100,
			    finished_at=$3,
			    updated_at=$3,
			    error_code='',
			    error_message=''
			WHERE task_id=$1 AND kind='transcode' AND attempt=$2
		`, taskID, result.Attempt, now); err != nil {
			return fmt.Errorf("complete transcode step: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='processing',
			    updated_at=$2,
			    version=version+1,
			    error_code='',
			    error_message='',
			    error_retryable=false
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "transcode.completed", envelope, now)
	})
}

func (s *PostgresStore) ApplyTranscodeFailed(
	ctx context.Context,
	envelope events.Envelope,
	failure TranscodeFailure,
) error {
	if !identity.IsUUID(failure.RunID) ||
		failure.Attempt < 1 ||
		failure.Code == "" ||
		failure.Message == "" {
		return fmt.Errorf("transcode failure event is invalid")
	}
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if failure.TaskID != "" && failure.TaskID != taskID {
			return fmt.Errorf("transcode failure task id does not match subject")
		}
		tag, err := tx.Exec(ctx, `
			UPDATE transcode_runs
			SET status='failed',
			    error_code=$4,
			    error_message=$5,
			    completed_at=$6,
			    updated_at=$6
			WHERE id=$1 AND task_id=$2 AND attempt=$3
			  AND status IN ('queued','running')
		`,
			failure.RunID,
			taskID,
			failure.Attempt,
			failure.Code,
			failure.Message,
			now,
		)
		if err != nil {
			return fmt.Errorf("fail transcode run: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("transcode run is missing or not failable")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='failed',
			    finished_at=$3,
			    updated_at=$3,
			    error_code=$4,
			    error_message=$5
			WHERE task_id=$1 AND kind='transcode' AND attempt=$2
		`, taskID, failure.Attempt, now, failure.Code, failure.Message); err != nil {
			return fmt.Errorf("fail transcode step: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='failed',
			    error_code=$2,
			    error_message=$3,
			    error_retryable=$4,
			    updated_at=$5,
			    version=version+1
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`,
			taskID,
			failure.Code,
			failure.Message,
			failure.Retryable,
			now,
		); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "transcode.failed", envelope, now)
	})
}

func (s *PostgresStore) ApplyTranscodeCancelled(
	ctx context.Context,
	envelope events.Envelope,
	event TranscodeCancellation,
) error {
	if !identity.IsUUID(event.RunID) || event.Attempt < 1 {
		return fmt.Errorf("transcode cancellation event is invalid")
	}
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if event.TaskID != "" && event.TaskID != taskID {
			return fmt.Errorf("transcode cancellation task id does not match subject")
		}
		if event.ControlState == "paused" {
			return s.requeuePausedTranscodeTx(ctx, tx, taskID, event, envelope, now)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE transcode_runs
			SET status='cancelled',
			    error_code='',
			    error_message=$4,
			    completed_at=$5,
			    updated_at=$5
			WHERE id=$1 AND task_id=$2 AND attempt=$3
			  AND status IN ('queued','running')
		`, event.RunID, taskID, event.Attempt, event.Message, now); err != nil {
			return fmt.Errorf("cancel transcode run: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='cancelled',
			    finished_at=$3,
			    updated_at=$3
			WHERE task_id=$1 AND kind='transcode' AND attempt=$2
			  AND status IN ('queued','running')
		`, taskID, event.Attempt, now); err != nil {
			return fmt.Errorf("cancel transcode step: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='cancelled',
			    updated_at=$2,
			    version=version+1,
			    error_code='',
			    error_message='',
			    error_retryable=false
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "transcode.cancelled", envelope, now)
	})
}

func (s *PostgresStore) ApplySubtitleStarted(
	ctx context.Context,
	envelope events.Envelope,
) error {
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='running', progress=5,
			    started_at=COALESCE(started_at,$2), updated_at=$2,
			    error_code='', error_message=''
			WHERE task_id=$1 AND kind='subtitles' AND status IN ('queued','running')
		`, taskID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='processing', updated_at=$2, version=version+1,
			    error_code='', error_message='', error_retryable=false
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "subtitle.processing.started", envelope, now)
	})
}

func (s *PostgresStore) ApplySubtitleProgress(
	ctx context.Context,
	envelope events.Envelope,
	progress SubtitleProgress,
) error {
	progress.Phase = strings.TrimSpace(progress.Phase)
	if progress.TaskID == "" ||
		progress.Attempt < 1 ||
		progress.Progress < 6 ||
		progress.Progress > 99 ||
		progress.Phase == "" ||
		len(progress.Phase) > 64 ||
		len(progress.RemoteTaskID) > 256 ||
		len(progress.RemoteStatus) > 64 {
		return fmt.Errorf("subtitle progress event is invalid")
	}
	detailRaw, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("encode subtitle progress: %w", err)
	}
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if progress.TaskID != taskID {
			return fmt.Errorf("subtitle progress task id does not match subject")
		}
		_, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET progress=GREATEST(progress,$3),
			    detail=$4::jsonb,
			    updated_at=$5
			WHERE task_id=$1 AND kind='subtitles' AND attempt=$2 AND status='running'
		`, taskID, progress.Attempt, progress.Progress, detailRaw, now)
		return err
	})
}

func (s *PostgresStore) ApplySubtitleCompleted(
	ctx context.Context,
	envelope events.Envelope,
	result SubtitleProcessingResult,
) error {
	if err := ValidateSubtitleProcessingResult(&result); err != nil {
		return err
	}
	completionDetail, err := json.Marshal(map[string]any{
		"phase":    "completed",
		"decision": result.Decision,
	})
	if err != nil {
		return fmt.Errorf("encode subtitle completion detail: %w", err)
	}
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		for _, asset := range result.Assets {
			if asset.AssetID == "" ||
				asset.Kind == "" ||
				asset.Bucket == "" ||
				asset.ObjectKey == "" ||
				asset.OriginalName == "" ||
				asset.ContentType == "" ||
				asset.SizeBytes < 0 ||
				len(asset.ChecksumSHA256) != 64 {
				return fmt.Errorf("subtitle result contains an invalid asset")
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO media_assets (
					id, task_id, kind, bucket, object_key, original_name,
					content_type, size_bytes, checksum_sha256, status, created_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'available',$10)
				ON CONFLICT (task_id, kind, object_key) DO UPDATE SET
					original_name=EXCLUDED.original_name,
					content_type=EXCLUDED.content_type,
					size_bytes=EXCLUDED.size_bytes,
					checksum_sha256=EXCLUDED.checksum_sha256,
					status='available',
					error_code='',
					error_message='',
					deleted_at=NULL
			`,
				asset.AssetID,
				taskID,
				asset.Kind,
				asset.Bucket,
				asset.ObjectKey,
				asset.OriginalName,
				asset.ContentType,
				asset.SizeBytes,
				asset.ChecksumSHA256,
				now,
			); err != nil {
				return fmt.Errorf("upsert subtitle asset: %w", err)
			}
		}

		for _, document := range result.Documents {
			if document.DocumentID == "" ||
				(document.Kind != "original" && document.Kind != "translated") ||
				document.Language == "" ||
				len(document.Segments) == 0 {
				return fmt.Errorf("subtitle result contains an invalid document")
			}
			segmentsRaw, err := json.Marshal(document.Segments)
			if err != nil {
				return fmt.Errorf("encode subtitle segments: %w", err)
			}
			qcRaw, err := json.Marshal(document.QCReport)
			if err != nil {
				return fmt.Errorf("encode subtitle qc report: %w", err)
			}
			var nextVersion int
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE(max(version),0) + 1
				FROM subtitle_documents
				WHERE task_id=$1 AND kind=$2
			`, taskID, document.Kind).Scan(&nextVersion); err != nil {
				return fmt.Errorf("calculate subtitle version: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO subtitle_documents (
					id, task_id, kind, language, version,
					segments, qc_report, source, created_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			`,
				document.DocumentID,
				taskID,
				document.Kind,
				document.Language,
				nextVersion,
				segmentsRaw,
				qcRaw,
				document.Source,
				now,
			); err != nil {
				return fmt.Errorf("insert subtitle document: %w", err)
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='succeeded', progress=100, finished_at=$2, updated_at=$2,
			    detail=$3::jsonb, error_code='', error_message=''
			WHERE task_id=$1 AND kind='subtitles'
		`, taskID, now, completionDetail); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='processing', updated_at=$2, version=version+1,
			    error_code='', error_message='', error_retryable=false
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "subtitle.processing.completed", envelope, now)
	})
}

func normalizeAndValidateSubtitleResult(result *SubtitleProcessingResult) error {
	if result.Decision.Disposition == "" && len(result.Documents) > 0 {
		result.Decision = SubtitleProcessingDecision{
			SchemaVersion: 1,
			Disposition:   "generated_subtitles",
		}
	}
	hardcodedChinese := result.Decision.Disposition == "existing_hardcoded_chinese" &&
		result.Decision.SchemaVersion == 1 &&
		result.Decision.TranslationSkipped &&
		!result.Decision.BurnSubtitles &&
		result.Decision.Detection.SchemaVersion == 1 &&
		result.Decision.Detection.State == "found" &&
		result.Decision.Detection.Source == "hardcoded" &&
		result.Decision.Detection.ConfidencePercent >= 50 &&
		result.Decision.Detection.SampleCount >= 8 &&
		result.Decision.Detection.StablePairCount > 0
	if len(result.Documents) == 0 && !hardcodedChinese {
		return fmt.Errorf("subtitle result contains no documents")
	}
	if hardcodedChinese && (len(result.Documents) != 0 || len(result.Assets) != 0) {
		return fmt.Errorf("hardcoded subtitle result must not contain generated subtitle artifacts")
	}
	if result.Decision.SchemaVersion != 1 {
		return fmt.Errorf("subtitle result contains an unsupported decision version")
	}
	if result.Decision.Disposition != "generated_subtitles" &&
		result.Decision.Disposition != "existing_soft_chinese" &&
		result.Decision.Disposition != "existing_hardcoded_chinese" {
		return fmt.Errorf("subtitle result contains an invalid decision")
	}
	if result.Decision.Disposition == "existing_soft_chinese" {
		detection := result.Decision.Detection
		validSource := detection.Source == "youtube_manual" ||
			detection.Source == "youtube_auto" ||
			detection.Source == "embedded"
		if detection.SchemaVersion != 1 ||
			detection.State != "found" ||
			!validSource ||
			detection.ConfidencePercent < 50 ||
			!result.Decision.TranslationSkipped ||
			result.Decision.BurnSubtitles ||
			len(result.Documents) == 0 {
			return fmt.Errorf("existing soft subtitle result lacks reusable subtitle evidence")
		}
	}
	return nil
}

// ValidateSubtitleProcessingResult validates the complete worker contract before
// the result enters a database transaction. Keeping this validation public lets
// the workflow consumer turn deterministic contract failures into a visible,
// retryable task failure instead of requeuing the same poison message forever.
func ValidateSubtitleProcessingResult(result *SubtitleProcessingResult) error {
	if err := normalizeAndValidateSubtitleResult(result); err != nil {
		return err
	}
	for _, asset := range result.Assets {
		if asset.AssetID == "" ||
			asset.Kind == "" ||
			asset.Bucket == "" ||
			asset.ObjectKey == "" ||
			asset.OriginalName == "" ||
			asset.ContentType == "" ||
			asset.SizeBytes < 0 ||
			len(asset.ChecksumSHA256) != 64 {
			return fmt.Errorf("subtitle result contains an invalid asset")
		}
	}
	for _, document := range result.Documents {
		if document.DocumentID == "" ||
			(document.Kind != "original" && document.Kind != "translated") ||
			document.Language == "" ||
			len(document.Segments) == 0 {
			return fmt.Errorf("subtitle result contains an invalid document")
		}
	}
	return nil
}

func (s *PostgresStore) ApplySubtitleFailed(
	ctx context.Context,
	envelope events.Envelope,
	failure WorkflowFailure,
) error {
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if failure.ControlState == "paused" {
			return s.requeuePausedSubtitleTx(ctx, tx, taskID, failure, envelope, now)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='failed', finished_at=$2, updated_at=$2,
			    detail=detail || '{"phase":"failed"}'::jsonb,
			    error_code=$3, error_message=$4
			WHERE task_id=$1 AND kind='subtitles'
		`, taskID, now, failure.Code, failure.Message); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='failed', error_code=$2, error_message=$3,
			    error_retryable=$4, updated_at=$5, version=version+1
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, failure.Code, failure.Message, failure.Retryable, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "subtitle.processing.failed", envelope, now)
	})
}

func (s *PostgresStore) ApplyDownloadFailed(
	ctx context.Context,
	envelope events.Envelope,
	failure WorkflowFailure,
) error {
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps AS step
			SET status='failed', finished_at=$2, updated_at=$2,
			    detail=step.detail || '{"phase":"failed"}'::jsonb,
			    error_code=$3, error_message=$4
			FROM tasks AS task
			WHERE step.task_id=$1
			  AND step.kind='download'
			  AND task.id=step.task_id
			  AND task.status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now, failure.Code, failure.Message); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='failed', error_code=$2, error_message=$3,
			    error_retryable=$4, updated_at=$5, version=version+1
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, failure.Code, failure.Message, failure.Retryable, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "download.failed", envelope, now)
	})
}

func (s *PostgresStore) ApplyDownloadCancelled(
	ctx context.Context,
	envelope events.Envelope,
	event WorkflowCancellation,
) error {
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if event.TaskID != "" && event.TaskID != taskID {
			return fmt.Errorf("download cancellation task id does not match subject")
		}
		var (
			taskStatus     string
			currentAttempt int
			currentPhase   string
		)
		if err := tx.QueryRow(ctx, `
			SELECT task.status, step.attempt, COALESCE(step.detail->>'phase', '')
			FROM tasks AS task
			JOIN task_steps AS step
			  ON step.task_id=task.id AND step.kind='download'
			WHERE task.id=$1
			FOR UPDATE OF task, step
		`, taskID).Scan(&taskStatus, &currentAttempt, &currentPhase); err != nil {
			return fmt.Errorf("lock download cancellation state: %w", err)
		}
		event, ignore := downloadCancellationDecision(
			taskStatus,
			currentAttempt,
			currentPhase,
			event,
		)
		if ignore {
			return s.insertWorkflowAudit(ctx, tx, taskID, "download.cancellation.ignored", envelope, now)
		}
		if event.ControlState == "paused" {
			return s.requeuePausedDownloadTx(ctx, tx, taskID, event, envelope, now)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='cancelled', finished_at=COALESCE(finished_at,$2), updated_at=$2,
			    detail=CASE
			        WHEN kind='download'
			        THEN detail || '{"phase":"cancelled"}'::jsonb
			        ELSE detail
			    END
			WHERE task_id=$1
			  AND kind IN ('download','media_inspect')
			  AND status IN ('queued','running')
		`, taskID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='cancelled', updated_at=$2,
			    error_code='', error_message='', error_retryable=false,
			    version=version+1
			WHERE id=$1
			  AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "download.cancelled", envelope, now)
	})
}

func downloadCancellationDecision(
	taskStatus string,
	currentAttempt int,
	currentPhase string,
	event WorkflowCancellation,
) (WorkflowCancellation, bool) {
	if terminalWorkflowStatus(taskStatus) ||
		(event.Attempt > 0 && event.Attempt < currentAttempt) {
		return event, true
	}
	// A quick resume can arrive while the old yt-dlp process is still exiting.
	// Treat that attempt's delayed cancellation as the pause acknowledgement,
	// never as a user cancellation, and preserve its checkpoint files.
	if currentPhase == "resume_requested" &&
		(event.Attempt == 0 || event.Attempt == currentAttempt) {
		event.ControlState = "paused"
	}
	return event, false
}

func (s *PostgresStore) requeuePausedDownloadTx(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	event WorkflowCancellation,
	envelope events.Envelope,
	now time.Time,
) error {
	checkpoint := map[string]any{"phase": "paused"}
	if event.DownloadedBytes > 0 || event.TotalBytes > 0 {
		checkpoint["downloaded_bytes"] = event.DownloadedBytes
		checkpoint["total_bytes"] = event.TotalBytes
		checkpoint["total_bytes_is_estimate"] = event.TotalBytesIsEstimate
		checkpoint["speed_bytes_per_second"] = event.SpeedBytesPerSecond
		checkpoint["eta_seconds"] = event.ETASeconds
		checkpoint["fragment_index"] = event.FragmentIndex
		checkpoint["fragment_count"] = event.FragmentCount
	}
	checkpointRaw, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("encode paused download checkpoint: %w", err)
	}
	var (
		status          string
		sourceURL       string
		cookieProfileID *string
		currentAttempt  int
	)
	if err := tx.QueryRow(ctx, `
		SELECT task.status, task.source_url, task.cookie_profile_id::text,
		       COALESCE(step.attempt, 0)
		FROM tasks task
		LEFT JOIN task_steps step
		  ON step.task_id=task.id AND step.kind='download'
		WHERE task.id=$1
		FOR UPDATE OF task
	`, taskID).Scan(&status, &sourceURL, &cookieProfileID, &currentAttempt); err != nil {
		return fmt.Errorf("load paused download checkpoint: %w", err)
	}
	if terminalWorkflowStatus(status) {
		return nil
	}
	nextAttempt := max(currentAttempt, event.Attempt) + 1
	if _, err := tx.Exec(ctx, `
		UPDATE task_steps
		SET status='queued', attempt=$3,
		    detail=COALESCE(detail, '{}'::jsonb) || $4::jsonb,
		    error_code='', error_message='', finished_at=NULL, updated_at=$2
		WHERE task_id=$1 AND kind='download'
	`, taskID, now, nextAttempt, checkpointRaw); err != nil {
		return fmt.Errorf("checkpoint paused download step: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_steps
		SET status='queued', attempt=$3, progress=0,
		    error_code='', error_message='', started_at=NULL, finished_at=NULL, updated_at=$2
		WHERE task_id=$1 AND kind='media_inspect' AND status <> 'succeeded'
	`, taskID, now, nextAttempt); err != nil {
		return fmt.Errorf("reset paused media inspection step: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status='metadata_ready', updated_at=$2, version=version+1
		WHERE id=$1
	`, taskID, now); err != nil {
		return fmt.Errorf("checkpoint paused download task: %w", err)
	}
	if err := enqueuePausedCommandTx(ctx, tx, taskID, DownloadRequestedV1, map[string]any{
		"task_id":           taskID,
		"source_url":        sourceURL,
		"cookie_profile_id": cookieProfileID,
		"attempt":           nextAttempt,
	}, now); err != nil {
		return err
	}
	return s.insertWorkflowAudit(ctx, tx, taskID, "download.paused", envelope, now)
}

func (s *PostgresStore) requeuePausedSubtitleTx(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	failure WorkflowFailure,
	envelope events.Envelope,
	now time.Time,
) error {
	var status string
	var currentAttempt int
	if err := tx.QueryRow(ctx, `
		SELECT task.status, COALESCE(step.attempt, 0)
		FROM tasks task
		LEFT JOIN task_steps step
		  ON step.task_id=task.id AND step.kind='subtitles'
		WHERE task.id=$1
		FOR UPDATE OF task
	`, taskID).Scan(&status, &currentAttempt); err != nil {
		return fmt.Errorf("load paused subtitle checkpoint: %w", err)
	}
	if terminalWorkflowStatus(status) {
		return nil
	}
	nextAttempt := max(currentAttempt, failure.Attempt) + 1
	if _, err := tx.Exec(ctx, `
		UPDATE task_steps
		SET status='queued', attempt=$3,
		    detail=jsonb_set(detail, '{phase}', '"paused"'::jsonb, true),
		    error_code='', error_message='', finished_at=NULL, updated_at=$2
		WHERE task_id=$1 AND kind='subtitles'
	`, taskID, now, nextAttempt); err != nil {
		return fmt.Errorf("checkpoint paused subtitle step: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status='processing', updated_at=$2, version=version+1
		WHERE id=$1
	`, taskID, now); err != nil {
		return fmt.Errorf("checkpoint paused subtitle task: %w", err)
	}
	if err := enqueuePausedCommandTx(ctx, tx, taskID, SubtitleRequestedV1, map[string]any{
		"task_id": taskID,
		"attempt": nextAttempt,
	}, now); err != nil {
		return err
	}
	return s.insertWorkflowAudit(ctx, tx, taskID, "subtitle.processing.paused", envelope, now)
}

func (s *PostgresStore) requeuePausedTranscodeTx(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	event TranscodeCancellation,
	envelope events.Envelope,
	now time.Time,
) error {
	var (
		status           string
		settingsSnapshot []byte
		currentAttempt   int
		inputAssetID     string
	)
	if err := tx.QueryRow(ctx, `
		SELECT task.status, task.settings_snapshot, COALESCE(step.attempt, 0),
		       COALESCE((
		           SELECT asset.id::text
		           FROM media_assets asset
		           WHERE asset.task_id=task.id AND asset.kind='source' AND asset.status='available'
		           ORDER BY asset.created_at DESC
		           LIMIT 1
		       ), '')
		FROM tasks task
		LEFT JOIN task_steps step
		  ON step.task_id=task.id AND step.kind='transcode'
		WHERE task.id=$1
		FOR UPDATE OF task
	`, taskID).Scan(&status, &settingsSnapshot, &currentAttempt, &inputAssetID); err != nil {
		return fmt.Errorf("load paused transcode checkpoint: %w", err)
	}
	if terminalWorkflowStatus(status) {
		return nil
	}
	if inputAssetID == "" {
		return fmt.Errorf("paused transcode source asset is missing")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE transcode_runs
		SET status='cancelled', error_message='任务已暂停', completed_at=$4, updated_at=$4
		WHERE id=$1 AND task_id=$2 AND attempt=$3
		  AND status IN ('queued','running')
	`, event.RunID, taskID, event.Attempt, now); err != nil {
		return fmt.Errorf("checkpoint paused transcode run: %w", err)
	}
	nextAttempt := max(currentAttempt, event.Attempt) + 1
	runID, err := identity.NewUUID()
	if err != nil {
		return err
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
		LEFT JOIN posting_strategies strategy ON strategy.id=task.posting_strategy_id
		WHERE task.id=$1
	`, taskID).Scan(&presetID); err != nil {
		return fmt.Errorf("load paused transcode preset: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcode_runs (
			id, task_id, preset_id, status, attempt, input_asset_id,
			command_summary, progress, created_at, updated_at
		) VALUES ($1,$2,$3,'queued',$4,$5,'{}'::jsonb,0,$6,$6)
	`, runID, taskID, presetID, nextAttempt, inputAssetID, now); err != nil {
		return fmt.Errorf("insert paused transcode continuation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_steps
		SET status='queued', attempt=$3,
		    detail=jsonb_set(detail, '{phase}', '"paused"'::jsonb, true),
		    error_code='', error_message='', finished_at=NULL, updated_at=$2
		WHERE task_id=$1 AND kind='transcode'
	`, taskID, now, nextAttempt); err != nil {
		return fmt.Errorf("checkpoint paused transcode step: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status='processing', updated_at=$2, version=version+1
		WHERE id=$1
	`, taskID, now); err != nil {
		return fmt.Errorf("checkpoint paused transcode task: %w", err)
	}
	if err := enqueuePausedCommandTx(ctx, tx, taskID, TranscodeRequestedV1, map[string]any{
		"task_id": taskID,
		"run_id":  runID,
		"attempt": nextAttempt,
	}, now); err != nil {
		return err
	}
	return s.insertWorkflowAudit(ctx, tx, taskID, "transcode.paused", envelope, now)
}

func enqueuePausedCommandTx(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	eventType string,
	data map[string]any,
	now time.Time,
) error {
	command, err := events.New(eventType, "visoraft/workflow-consumer", "task/"+taskID, now, data)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("marshal paused continuation command: %w", err)
	}
	outboxID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (
			id, aggregate_id, event_type, payload, status,
			attempts, available_at, created_at
		) VALUES ($1,$2,$3,$4,'pending',0,$5,$5)
	`, outboxID, taskID, eventType, raw, now); err != nil {
		return fmt.Errorf("enqueue paused continuation: %w", err)
	}
	return nil
}

func terminalWorkflowStatus(status string) bool {
	return status == StatusCancelled || status == StatusPublished || status == "reconciled" || status == "abandoned"
}

func (s *PostgresStore) ApplyAssetsDeleted(
	ctx context.Context,
	envelope events.Envelope,
	result AssetDeletionResult,
) error {
	if err := validateAssetIDs(result.AssetIDs); err != nil {
		return err
	}
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if _, err := tx.Exec(ctx, `
			UPDATE media_assets
			SET status='deleted', deleted_at=$3, error_code='', error_message=''
			WHERE task_id=$1 AND id=ANY($2::uuid[])
		`, taskID, result.AssetIDs, now); err != nil {
			return fmt.Errorf("mark assets deleted: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET updated_at=$2, version=version+1
			WHERE id=$1
		`, taskID, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "assets.deleted", envelope, now)
	})
}

func (s *PostgresStore) ApplyAssetsDeleteFailed(
	ctx context.Context,
	envelope events.Envelope,
	failure AssetDeletionFailure,
) error {
	if err := validateAssetIDs(failure.AssetIDs); err != nil {
		return err
	}
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if _, err := tx.Exec(ctx, `
			UPDATE media_assets
			SET status='failed', error_code=$3, error_message=$4
			WHERE task_id=$1 AND id=ANY($2::uuid[])
		`, taskID, failure.AssetIDs, failure.Code, failure.Message); err != nil {
			return fmt.Errorf("mark asset cleanup failed: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET updated_at=$2, version=version+1
			WHERE id=$1
		`, taskID, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "assets.delete.failed", envelope, now)
	})
}

func (s *PostgresStore) ApplyMetadataFailed(ctx context.Context, envelope events.Envelope, failure WorkflowFailure) error {
	return s.applyWorkflowEvent(ctx, envelope, func(tx pgx.Tx, taskID string, now time.Time) error {
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET status='failed', finished_at=$2, updated_at=$2,
			    error_code=$3, error_message=$4
			WHERE task_id=$1 AND kind='metadata'
		`, taskID, now, failure.Code, failure.Message); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks
			SET status='failed', error_code=$2, error_message=$3,
			    error_retryable=$4, updated_at=$5, version=version+1
			WHERE id=$1 AND status NOT IN ('cancelled','abandoned','published','reconciled')
		`, taskID, failure.Code, failure.Message, failure.Retryable, now); err != nil {
			return err
		}
		return s.insertWorkflowAudit(ctx, tx, taskID, "metadata.failed", envelope, now)
	})
}

func (s *PostgresStore) applyWorkflowEvent(
	ctx context.Context,
	envelope events.Envelope,
	apply func(pgx.Tx, string, time.Time) error,
) error {
	taskID := taskIDFromSubject(envelope.Subject)
	if taskID == "" {
		return fmt.Errorf("event subject does not contain a task id")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin workflow event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	commandTag, err := tx.Exec(ctx, `
		INSERT INTO consumed_messages(message_id, consumer, event_type, consumed_at)
		VALUES ($1,'workflow-consumer',$2,$3)
		ON CONFLICT (message_id) DO NOTHING
	`, envelope.ID, envelope.Type, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("record consumed message: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1)", taskID).Scan(&exists); err != nil {
		return fmt.Errorf("check workflow task: %w", err)
	}
	if !exists {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit stale workflow event: %w", err)
		}
		return fmt.Errorf("%w: workflow task %s does not exist", ErrNotFound, taskID)
	}

	now := envelope.Time.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := apply(tx, taskID, now); err != nil {
		return fmt.Errorf("apply workflow event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit workflow event: %w", err)
	}
	return nil
}

func (s *PostgresStore) insertWorkflowAudit(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	eventType string,
	envelope events.Envelope,
	now time.Time,
) error {
	auditID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	payloadValue := map[string]any{
		"message_id": envelope.ID,
		"source":     envelope.Source,
		"event_type": envelope.Type,
	}
	if strings.HasSuffix(eventType, ".failed") {
		var failure map[string]any
		if err := json.Unmarshal(envelope.Data, &failure); err == nil {
			for _, key := range []string{"attempt", "code", "message", "retryable", "phase"} {
				if value, ok := failure[key]; ok {
					payloadValue[key] = value
				}
			}
		}
	}
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_events (
			id, aggregate_type, aggregate_id, event_type,
			actor_type, actor_id, payload, occurred_at
		) VALUES ($1,'task',$2,$3,'worker',$4,$5,$6)
	`, auditID, taskID, eventType, envelope.Source, payload, now)
	return err
}

func (s *PostgresStore) insertUserAudit(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	eventType string,
	value any,
	now time.Time,
) error {
	auditID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal user audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			id, aggregate_type, aggregate_id, event_type,
			actor_type, actor_id, payload, occurred_at
		) VALUES ($1,'task',$2,$3,'user','local-user',$4,$5)
	`, auditID, taskID, eventType, payload, now); err != nil {
		return fmt.Errorf("insert user audit: %w", err)
	}
	return nil
}

func taskIDFromSubject(subject string) string {
	const prefix = "task/"
	if len(subject) <= len(prefix) || subject[:len(prefix)] != prefix {
		return ""
	}
	return subject[len(prefix):]
}

func validateAssetIDs(assetIDs []string) error {
	if len(assetIDs) == 0 || len(assetIDs) > 100 {
		return fmt.Errorf("asset deletion event must contain 1 to 100 asset ids")
	}
	for _, assetID := range assetIDs {
		if !identity.IsUUID(assetID) {
			return fmt.Errorf("asset deletion event contains an invalid asset id")
		}
	}
	return nil
}

func validateMediaInfo(info MediaInfo) error {
	if info.SchemaVersion != 1 {
		return fmt.Errorf("media inspection result has an unsupported schema version")
	}
	if info.StreamCount < 1 || info.StreamCount > 64 {
		return fmt.Errorf("media inspection result has an invalid stream count")
	}
	if info.FormatName == "" || len(info.FormatName) > 200 ||
		len(info.VideoCodec) > 100 ||
		len(info.PixelFormat) > 100 ||
		len(info.FrameRate) > 50 ||
		len(info.AudioCodec) > 100 ||
		len(info.ChannelLayout) > 100 {
		return fmt.Errorf("media inspection result contains invalid text fields")
	}
	if info.DurationSeconds != nil &&
		(*info.DurationSeconds < 0 ||
			*info.DurationSeconds > 315_360_000 ||
			math.IsNaN(*info.DurationSeconds) ||
			math.IsInf(*info.DurationSeconds, 0)) {
		return fmt.Errorf("media inspection result has an invalid duration")
	}
	if info.SizeBytes != nil && *info.SizeBytes < 0 {
		return fmt.Errorf("media inspection result has an invalid size")
	}
	if info.BitRate != nil && *info.BitRate < 0 {
		return fmt.Errorf("media inspection result has an invalid bit rate")
	}
	if info.Width != nil && (*info.Width < 0 || *info.Width > 65535) {
		return fmt.Errorf("media inspection result has an invalid width")
	}
	if info.Height != nil && (*info.Height < 0 || *info.Height > 65535) {
		return fmt.Errorf("media inspection result has an invalid height")
	}
	if info.SampleRate != nil && (*info.SampleRate < 0 || *info.SampleRate > 768000) {
		return fmt.Errorf("media inspection result has an invalid sample rate")
	}
	if info.Channels != nil && (*info.Channels < 0 || *info.Channels > 1024) {
		return fmt.Errorf("media inspection result has an invalid channel count")
	}
	return nil
}
