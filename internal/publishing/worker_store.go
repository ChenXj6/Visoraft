package publishing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/visoraft/visoraft/internal/identity"
)

type claimedID struct {
	ID   string
	Mode string
}

func (s *PostgresStore) ClaimPublications(
	ctx context.Context,
	owner string,
	limit int,
	now time.Time,
) ([]ClaimedPublication, error) {
	if limit < 1 {
		limit = 1
	} else if limit > 32 {
		limit = 32
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin publication claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		WITH picked AS (
			SELECT
				publication.id,
				CASE
					WHEN publication.status IN ('submitting','reconciliation_required')
					THEN 'reconcile'
					ELSE 'publish'
				END AS claim_mode
			FROM platform_publications publication
			WHERE (
				(
					publication.status='queued'
					AND (
						publication.scheduled_at IS NULL
						OR publication.scheduled_at <= $3
					)
				)
				OR (
					publication.status IN ('preparing','uploading')
					AND publication.locked_at < $3 - interval '15 minutes'
				)
				OR (
					publication.status='submitting'
					AND publication.locked_at < $3 - interval '15 minutes'
				)
				OR publication.status='reconciliation_required'
			)
			  AND (
				publication.status <> 'reconciliation_required'
				OR COALESCE(publication.scheduled_at, publication.updated_at) <= $3
			  )
			  AND (
				publication.locked_at IS NULL
				OR publication.locked_at < $3 - interval '15 minutes'
			  )
			ORDER BY
				CASE publication.status
					WHEN 'reconciliation_required' THEN 0
					WHEN 'submitting' THEN 1
					ELSE 2
				END,
				COALESCE(publication.scheduled_at, publication.created_at),
				publication.created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE platform_publications publication
		SET
			status=CASE
				WHEN picked.claim_mode='reconcile' THEN 'reconciliation_required'
				ELSE 'preparing'
			END,
			attempt=publication.attempt + CASE
				WHEN picked.claim_mode='publish' THEN 1
				ELSE 0
			END,
			started_at=CASE
				WHEN picked.claim_mode='publish' THEN COALESCE(publication.started_at,$3)
				ELSE publication.started_at
			END,
			locked_at=$3,
			locked_by=$1,
			updated_at=$3,
			version=publication.version+1
		FROM picked
		WHERE publication.id=picked.id
		RETURNING publication.id::text, picked.claim_mode
	`, owner, limit, now)
	if err != nil {
		return nil, fmt.Errorf("claim platform publications: %w", err)
	}
	ids := make([]claimedID, 0, limit)
	for rows.Next() {
		var item claimedID
		if err := rows.Scan(&item.ID, &item.Mode); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan claimed publication id: %w", err)
		}
		ids = append(ids, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate claimed publication ids: %w", err)
	}
	rows.Close()

	result := make([]ClaimedPublication, 0, len(ids))
	for _, claimed := range ids {
		item, err := s.loadClaimedPublication(ctx, tx, claimed.ID, claimed.Mode)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE publish_jobs
			SET
				status=CASE
					WHEN $2='reconcile' THEN 'reconciliation_required'
					ELSE 'publishing'
				END,
				updated_at=$3,
				version=version+1
			WHERE id=$1
			  AND status NOT IN ('published','cancelled')
		`, item.PublishJobID, claimed.Mode, now); err != nil {
			return nil, fmt.Errorf("mark claimed publish job active: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET
				status='running',
				progress=CASE WHEN $2='reconcile' THEN 80 ELSE 10 END,
				started_at=COALESCE(started_at,$3),
				finished_at=NULL,
				error_code='',
				error_message='',
				updated_at=$3
			WHERE task_id=$1 AND kind='publish'
		`, item.TaskID, claimed.Mode, now); err != nil {
			return nil, fmt.Errorf("mark claimed task publish step active: %w", err)
		}
		result = append(result, item)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit publication claim: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) loadClaimedPublication(
	ctx context.Context,
	tx pgx.Tx,
	id string,
	mode string,
) (ClaimedPublication, error) {
	publication, err := scanPublication(tx.QueryRow(
		ctx,
		publicationSelect+" WHERE id=$1",
		id,
	))
	if err != nil {
		return ClaimedPublication{}, err
	}
	account, err := scanAccount(tx.QueryRow(
		ctx,
		accountSelect+" WHERE id=$1 AND archived_at IS NULL",
		publication.AccountID,
	))
	if err != nil {
		return ClaimedPublication{}, err
	}
	var media AssetReference
	if err := tx.QueryRow(ctx, `
		SELECT id::text, bucket, object_key, original_name, content_type,
		       size_bytes, checksum_sha256
		FROM media_assets
		WHERE id=$1 AND status='available'
	`, publication.MediaAssetID).Scan(
		&media.ID,
		&media.Bucket,
		&media.ObjectKey,
		&media.OriginalName,
		&media.ContentType,
		&media.SizeBytes,
		&media.Checksum,
	); errors.Is(err, pgx.ErrNoRows) {
		return ClaimedPublication{}, &ConflictError{
			Code:    "publish_media_unavailable",
			Message: "投稿媒体文件不存在或已被清理",
		}
	} else if err != nil {
		return ClaimedPublication{}, fmt.Errorf("load claimed publication media: %w", err)
	}

	var cover *AssetReference
	if publication.CoverAssetID != nil {
		var value AssetReference
		err := tx.QueryRow(ctx, `
			SELECT id::text, bucket, object_key, original_name, content_type,
			       size_bytes, checksum_sha256
			FROM media_assets
			WHERE id=$1 AND status='available'
		`, *publication.CoverAssetID).Scan(
			&value.ID,
			&value.Bucket,
			&value.ObjectKey,
			&value.OriginalName,
			&value.ContentType,
			&value.SizeBytes,
			&value.Checksum,
		)
		if err == nil {
			cover = &value
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return ClaimedPublication{}, fmt.Errorf("load claimed publication cover: %w", err)
		}
	}

	var (
		sourceURL   string
		maxAttempts int
		retryDelay  int
		reconcile   bool
	)
	if err := tx.QueryRow(ctx, `
		SELECT
			source_url,
			COALESCE(
				NULLIF(settings_snapshot->'publishing'->>'maximum_attempts','')::integer,
				3
			),
			COALESCE(
				NULLIF(settings_snapshot->'publishing'->>'retry_delay_seconds','')::integer,
				30
			),
			COALESCE(
				(settings_snapshot->'publishing'->>'reconcile_uncertain_results')::boolean,
				true
			)
		FROM tasks
		WHERE id=$1
	`, publication.TaskID).Scan(
		&sourceURL,
		&maxAttempts,
		&retryDelay,
		&reconcile,
	); err != nil {
		return ClaimedPublication{}, fmt.Errorf("load claimed publication policy: %w", err)
	}
	return ClaimedPublication{
		PlatformPublication: publication,
		Account:             account,
		Media:               media,
		Cover:               cover,
		SourceURL:           sourceURL,
		MaxAttempts:         maxAttempts,
		RetryDelay:          time.Duration(retryDelay) * time.Second,
		Reconcile:           reconcile,
		ClaimMode:           mode,
	}, nil
}

func (s *PostgresStore) BeginPublicationAttempt(
	ctx context.Context,
	publication ClaimedPublication,
	stage string,
	now time.Time,
) (string, error) {
	id, err := identity.NewUUID()
	if err != nil {
		return "", err
	}
	requestRaw, err := json.Marshal(map[string]any{
		"platform":       publication.Platform,
		"account_id":     publication.AccountID,
		"category_id":    publication.CategoryID,
		"media_asset_id": publication.MediaAssetID,
		"has_cover":      publication.CoverAssetID != nil,
	})
	if err != nil {
		return "", fmt.Errorf("encode publication attempt summary: %w", err)
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO publication_attempts (
			id, publication_id, attempt, stage, status,
			request_summary, started_at
		) VALUES ($1,$2,$3,$4,'running',$5,$6)
		ON CONFLICT (publication_id, attempt, stage) DO UPDATE SET
			status='running',
			request_summary=EXCLUDED.request_summary,
			response_summary='{}'::jsonb,
			error_code='',
			error_message='',
			started_at=EXCLUDED.started_at,
			completed_at=NULL
		RETURNING id::text
	`,
		id,
		publication.ID,
		publication.Attempt,
		stage,
		requestRaw,
		now,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("begin publication attempt: %w", err)
	}
	return id, nil
}

func (s *PostgresStore) SetPublicationStage(
	ctx context.Context,
	publicationID string,
	owner string,
	status string,
	stage string,
	now time.Time,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin publication stage update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE platform_publications
		SET status=$3, updated_at=$4, version=version+1
		WHERE id=$1 AND locked_by=$2 AND status <> 'cancelled'
	`, publicationID, owner, status, now)
	if err != nil {
		return fmt.Errorf("update platform publication stage: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return &ConflictError{
			Code:    "publication_claim_lost",
			Message: "投稿任务锁已失效，当前执行已停止",
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE publication_attempts
		SET stage=$2
		WHERE publication_id=$1
		  AND attempt=(SELECT attempt FROM platform_publications WHERE id=$1)
		  AND status='running'
		  AND stage <> 'reconcile'
	`, publicationID, stage); err != nil {
		return fmt.Errorf("update publication attempt stage: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_steps
		SET
			status='running',
			progress=CASE
				WHEN $2='uploading' THEN 45
				WHEN $2='submitting' THEN 75
				ELSE progress
			END,
			updated_at=$3
		WHERE task_id=(
			SELECT task_id FROM platform_publications WHERE id=$1
		)
		  AND kind='publish'
	`, publicationID, status, now); err != nil {
		return fmt.Errorf("update task publish progress: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit publication stage update: %w", err)
	}
	return nil
}

func (s *PostgresStore) CompletePublication(
	ctx context.Context,
	publication ClaimedPublication,
	owner string,
	attemptID string,
	result PublishResult,
	adapterVersion string,
	now time.Time,
) error {
	responseRaw, err := json.Marshal(result.ResponseSummary)
	if err != nil {
		return fmt.Errorf("encode platform publish response summary: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin publication completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE platform_publications
		SET
			status='published',
			remote_submission_id=$3,
			remote_url=$4,
			remote_status=$5,
			adapter_version=$6,
			response_summary=$7,
			error_code='',
			error_message='',
			error_retryable=false,
			uncertain_since=NULL,
			locked_at=NULL,
			locked_by='',
			completed_at=$8,
			updated_at=$8,
			version=version+1
		WHERE id=$1 AND locked_by=$2
	`, publication.ID, owner, result.RemoteSubmissionID, result.RemoteURL,
		result.RemoteStatus, adapterVersion, responseRaw, now)
	if err != nil {
		return fmt.Errorf("complete platform publication: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return &ConflictError{
			Code:    "publication_claim_lost",
			Message: "投稿任务锁已失效，结果未覆盖其他执行者",
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE publication_attempts
		SET
			status='succeeded',
			response_summary=$2,
			error_code='',
			error_message='',
			completed_at=$3
		WHERE id=$1
	`, attemptID, responseRaw, now); err != nil {
		return fmt.Errorf("complete publication attempt: %w", err)
	}
	if err := s.recomputePublishJob(ctx, tx, publication.PublishJobID, publication.TaskID, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit publication completion: %w", err)
	}
	return nil
}

func (s *PostgresStore) MarkPublicationCompletionUncertain(
	ctx context.Context,
	publication ClaimedPublication,
	owner string,
	attemptID string,
	result PublishResult,
	adapterVersion string,
	now time.Time,
) error {
	summary := make(map[string]any, len(result.ResponseSummary)+1)
	for key, value := range result.ResponseSummary {
		summary[key] = value
	}
	summary["local_persistence_recovery"] = true
	responseRaw, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("encode uncertain platform response summary: %w", err)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin publication reconciliation recovery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE platform_publications
		SET
			status='reconciliation_required',
			remote_submission_id=$3,
			remote_url=$4,
			remote_status=$5,
			adapter_version=$6,
			response_summary=$7,
			error_code='local_persistence_after_publish',
			error_message='平台已返回投稿结果，但本地完成事务失败，正在自动对账',
			error_retryable=true,
			uncertain_since=$8,
			scheduled_at=$8,
			locked_at=NULL,
			locked_by='',
			completed_at=NULL,
			updated_at=$8,
			version=version+1
		WHERE id=$1 AND locked_by=$2
	`, publication.ID, owner, result.RemoteSubmissionID, result.RemoteURL,
		result.RemoteStatus, adapterVersion, responseRaw, now)
	if err != nil {
		return fmt.Errorf("mark publication reconciliation recovery: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return &ConflictError{
			Code:    "publication_claim_lost",
			Message: "投稿任务锁已失效，无法保存待对账结果",
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE publication_attempts
		SET
			status='uncertain',
			response_summary=$2,
			error_code='local_persistence_after_publish',
			error_message='平台已返回投稿结果，但本地完成事务失败，正在自动对账',
			completed_at=$3
		WHERE id=$1
	`, attemptID, responseRaw, now); err != nil {
		return fmt.Errorf("mark publication attempt uncertain: %w", err)
	}
	if err := s.recomputePublishJob(
		ctx,
		tx,
		publication.PublishJobID,
		publication.TaskID,
		now,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit publication reconciliation recovery: %w", err)
	}
	return nil
}

func (s *PostgresStore) FailPublication(
	ctx context.Context,
	publication ClaimedPublication,
	owner string,
	attemptID string,
	failure *AdapterError,
	now time.Time,
) error {
	if failure == nil {
		failure = &AdapterError{
			Code:      "platform_publish_failed",
			Message:   "平台投稿失败",
			Retryable: true,
		}
	}
	failure.Code = strings.TrimSpace(failure.Code)
	if failure.Code == "" {
		failure.Code = "platform_publish_failed"
	}
	failure.Message = truncateMessage(failure.Message)
	nextStatus := "failed"
	attemptStatus := "failed"
	var nextScheduledAt *time.Time
	var uncertainSince *time.Time
	if failure.Uncertain && publication.Reconcile {
		nextStatus = "reconciliation_required"
		attemptStatus = "uncertain"
		uncertainSince = &now
		next := now.Add(publication.RetryDelay)
		nextScheduledAt = &next
	} else if failure.Retryable && publication.Attempt < publication.MaxAttempts {
		nextStatus = "queued"
		next := now.Add(publication.RetryDelay)
		nextScheduledAt = &next
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin publication failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE platform_publications
		SET
			status=$3,
			scheduled_at=COALESCE($4, scheduled_at),
			error_code=$5,
			error_message=$6,
			error_retryable=$7,
			uncertain_since=$8,
			locked_at=NULL,
			locked_by='',
			updated_at=$9,
			version=version+1
		WHERE id=$1 AND locked_by=$2
	`, publication.ID, owner, nextStatus, nextScheduledAt, failure.Code,
		failure.Message, failure.Retryable, uncertainSince, now)
	if err != nil {
		return fmt.Errorf("fail platform publication: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return &ConflictError{
			Code:    "publication_claim_lost",
			Message: "投稿任务锁已失效，失败结果未覆盖其他执行者",
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE publication_attempts
		SET
			status=$2,
			error_code=$3,
			error_message=$4,
			completed_at=$5
		WHERE id=$1
	`, attemptID, attemptStatus, failure.Code, failure.Message, now); err != nil {
		return fmt.Errorf("fail publication attempt: %w", err)
	}
	if err := s.recomputePublishJob(ctx, tx, publication.PublishJobID, publication.TaskID, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit publication failure: %w", err)
	}
	return nil
}

func (s *PostgresStore) recomputePublishJob(
	ctx context.Context,
	tx pgx.Tx,
	jobID string,
	taskID string,
	now time.Time,
) error {
	var total, published, failed, active, reconcile int
	if err := tx.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE status='published'),
			count(*) FILTER (WHERE status='failed'),
			count(*) FILTER (WHERE status IN (
				'queued','preparing','uploading','submitting'
			)),
			count(*) FILTER (WHERE status='reconciliation_required')
		FROM platform_publications
		WHERE publish_job_id=$1
	`, jobID).Scan(&total, &published, &failed, &active, &reconcile); err != nil {
		return fmt.Errorf("summarize platform publications: %w", err)
	}
	jobStatus := "publishing"
	taskStatus := "publishing"
	stepStatus := "running"
	stepProgress := 50
	completed := false
	switch {
	case total > 0 && published == total:
		jobStatus = "published"
		taskStatus = "published"
		stepStatus = "succeeded"
		stepProgress = 100
		completed = true
	case reconcile > 0:
		jobStatus = "reconciliation_required"
		stepProgress = 80
	case active > 0:
		jobStatus = "publishing"
		if published > 0 {
			stepProgress = 75
		}
	case published > 0 && failed > 0:
		jobStatus = "partial_success"
		taskStatus = "failed"
		stepStatus = "failed"
		stepProgress = 100
		completed = true
	case failed == total && total > 0:
		jobStatus = "failed"
		taskStatus = "failed"
		stepStatus = "failed"
		stepProgress = 100
		completed = true
	}
	if _, err := tx.Exec(ctx, `
		UPDATE publish_jobs
		SET
			status=$2,
			completed_at=CASE
				WHEN $3 THEN $4::timestamptz
				ELSE NULL::timestamptz
			END,
			updated_at=$4::timestamptz,
			version=version+1
		WHERE id=$1
	`, jobID, jobStatus, completed, now); err != nil {
		return fmt.Errorf("update publish job summary: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		SET
			status=$2,
			error_code=CASE WHEN $2='failed' THEN 'platform_publish_failed' ELSE '' END,
			error_message=CASE
				WHEN $2='failed' THEN '至少一个平台投稿失败，请在投稿详情中处理'
				ELSE ''
			END,
			error_retryable=($2='failed'),
			updated_at=$3,
			version=version+1
		WHERE id=$1
	`, taskID, taskStatus, now); err != nil {
		return fmt.Errorf("update task publish summary: %w", err)
	}
	stepID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_steps (
			id, task_id, kind, status, attempt, progress,
			started_at, finished_at, updated_at
		) VALUES (
			$1,$2,'publish',$3,1,$4,$5::timestamptz,
			CASE
				WHEN $6 THEN $5::timestamptz
				ELSE NULL::timestamptz
			END,
			$5::timestamptz
		)
		ON CONFLICT (task_id, kind) DO UPDATE SET
			status=EXCLUDED.status,
			attempt=GREATEST(task_steps.attempt, EXCLUDED.attempt),
			progress=EXCLUDED.progress,
			started_at=COALESCE(task_steps.started_at, EXCLUDED.started_at),
			finished_at=EXCLUDED.finished_at,
			updated_at=EXCLUDED.updated_at,
			error_code=CASE WHEN EXCLUDED.status='failed' THEN 'platform_publish_failed' ELSE '' END,
			error_message=CASE
				WHEN EXCLUDED.status='failed' THEN '平台投稿失败，请查看独立平台结果'
				ELSE ''
			END
	`, stepID, taskID, stepStatus, stepProgress, now, completed); err != nil {
		return fmt.Errorf("update task publish step: %w", err)
	}
	return nil
}

func truncateMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "平台投稿失败"
	}
	runes := []rune(value)
	if len(runes) > 1000 {
		return string(runes[:1000])
	}
	return value
}

func (s *PostgresStore) PublishingConcurrency(ctx context.Context) (int, error) {
	var value int
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(
			NULLIF(publishing_config->>'maximum_concurrent_uploads','')::integer,
			1
		)
		FROM application_settings
		WHERE singleton=true
	`).Scan(&value); err != nil {
		return 0, fmt.Errorf("load publishing concurrency: %w", err)
	}
	if value < 1 {
		value = 1
	} else if value > 32 {
		value = 32
	}
	return value, nil
}
