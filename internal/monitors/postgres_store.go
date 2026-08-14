package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/visoraft/visoraft/internal/identity"
	"github.com/visoraft/visoraft/internal/tasks"
)

var ErrNotFound = errors.New("youtube monitor not found")
var ErrRunNotFound = errors.New("youtube monitor run not found")
var ErrVersionConflict = errors.New("youtube monitor version conflict")

type PostgresStore struct {
	pool *pgxpool.Pool
}

type ClaimedRun struct {
	Run     Run
	Monitor Monitor
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) CookieProfileExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM cookie_profiles
			WHERE id=$1 AND status='ready' AND octet_length(encrypted_cookie_jar) > 0
		)
	`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check monitor cookie profile: %w", err)
	}
	return exists, nil
}

func (s *PostgresStore) PostingStrategy(
	ctx context.Context,
	id string,
) (tasks.PostingStrategyReference, error) {
	var result tasks.PostingStrategyReference
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, enabled, automation_mode, target_platforms
		FROM posting_strategies
		WHERE id=$1 AND archived_at IS NULL
	`, id).Scan(
		&result.ID,
		&result.Enabled,
		&result.AutomationMode,
		&result.TargetPlatforms,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return tasks.PostingStrategyReference{}, nil
	}
	if err != nil {
		return tasks.PostingStrategyReference{}, fmt.Errorf(
			"load monitor posting strategy: %w",
			err,
		)
	}
	return result, nil
}

func (s *PostgresStore) List(ctx context.Context) ([]Monitor, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT to_jsonb(m)
		FROM youtube_monitors m
		WHERE archived_at IS NULL
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list youtube monitors: %w", err)
	}
	defer rows.Close()
	result := make([]Monitor, 0)
	for rows.Next() {
		item, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *PostgresStore) Get(ctx context.Context, id string) (Monitor, error) {
	item, err := scanMonitor(s.pool.QueryRow(ctx, `
		SELECT to_jsonb(m)
		FROM youtube_monitors m
		WHERE id=$1 AND archived_at IS NULL
	`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	return item, err
}

func (s *PostgresStore) Create(
	ctx context.Context,
	id string,
	input CreateInput,
	now time.Time,
) (Monitor, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return Monitor{}, fmt.Errorf("encode youtube monitor: %w", err)
	}
	_, err = s.pool.Exec(ctx, monitorInsertSQL, id, raw, now)
	if err != nil {
		return Monitor{}, fmt.Errorf("insert youtube monitor: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *PostgresStore) Update(
	ctx context.Context,
	id string,
	input UpdateInput,
	now time.Time,
) (Monitor, error) {
	raw, err := json.Marshal(input.CreateInput)
	if err != nil {
		return Monitor{}, fmt.Errorf("encode youtube monitor update: %w", err)
	}
	tag, err := s.pool.Exec(ctx, monitorUpdateSQL, id, raw, input.ExpectedVersion, now)
	if err != nil {
		return Monitor{}, fmt.Errorf("update youtube monitor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM youtube_monitors
				WHERE id=$1 AND archived_at IS NULL
			)
		`, id).Scan(&exists); err != nil {
			return Monitor{}, fmt.Errorf("check youtube monitor update: %w", err)
		}
		if !exists {
			return Monitor{}, ErrNotFound
		}
		return Monitor{}, ErrVersionConflict
	}
	return s.Get(ctx, id)
}

func (s *PostgresStore) SetEnabled(
	ctx context.Context,
	id string,
	enabled bool,
	now time.Time,
) (Monitor, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE youtube_monitors
		SET
			enabled=$2,
			state=CASE WHEN $2 THEN 'idle' ELSE 'paused' END,
			next_run_at=CASE
				WHEN $2 AND schedule_type='automatic'
					THEN $3::timestamptz + make_interval(mins => schedule_interval_minutes)
				ELSE NULL
			END,
			last_error='',
			version=version+1,
			updated_at=$3
		WHERE id=$1 AND archived_at IS NULL
	`, id, enabled, now)
	if err != nil {
		return Monitor{}, fmt.Errorf("set youtube monitor enabled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Monitor{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *PostgresStore) Delete(
	ctx context.Context,
	id string,
	historyMode string,
	now time.Time,
) error {
	var tag pgconn.CommandTag
	var err error
	if historyMode == "purge" {
		tag, err = s.pool.Exec(ctx, `DELETE FROM youtube_monitors WHERE id=$1`, id)
	} else {
		tag, err = s.pool.Exec(ctx, `
			UPDATE youtube_monitors
			SET enabled=false, state='paused', next_run_at=NULL,
			    archived_at=$2, updated_at=$2, version=version+1
			WHERE id=$1 AND archived_at IS NULL
		`, id, now)
	}
	if err != nil {
		return fmt.Errorf("delete youtube monitor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) CreateRun(
	ctx context.Context,
	monitor Monitor,
	trigger string,
	now time.Time,
) (Run, error) {
	runID, err := identity.NewUUID()
	if err != nil {
		return Run{}, err
	}
	raw, err := json.Marshal(monitor)
	if err != nil {
		return Run{}, fmt.Errorf("encode monitor run snapshot: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Run{}, fmt.Errorf("begin youtube monitor run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var enabled bool
	var state string
	err = tx.QueryRow(ctx, `
		SELECT enabled, state
		FROM youtube_monitors
		WHERE id=$1 AND archived_at IS NULL
		FOR UPDATE
	`, monitor.ID).Scan(&enabled, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("lock youtube monitor for run: %w", err)
	}
	if !enabled || state == "paused" {
		return Run{}, &ConflictError{
			Code:    "monitor_paused",
			Message: "监控已暂停，恢复后才能运行",
		}
	}
	if state == "running" {
		return Run{}, &ConflictError{
			Code:    "monitor_already_running",
			Message: "该监控已有运行实例",
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO youtube_monitor_runs (
			id, monitor_id, trigger, status, config_snapshot, started_at
		) VALUES ($1,$2,$3,'queued',$4,$5)
	`, runID, monitor.ID, trigger, raw, now); err != nil {
		return Run{}, fmt.Errorf("insert youtube monitor run: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE youtube_monitors
		SET state='running', last_error='', updated_at=$2, version=version+1
		WHERE id=$1
	`, monitor.ID, now); err != nil {
		return Run{}, fmt.Errorf("mark youtube monitor running: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, fmt.Errorf("commit youtube monitor run: %w", err)
	}
	return s.GetRun(ctx, runID)
}

func (s *PostgresStore) EnqueueDue(ctx context.Context, now time.Time) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin due monitor scan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT to_jsonb(m)
		FROM youtube_monitors m
		WHERE enabled
		  AND archived_at IS NULL
		  AND schedule_type='automatic'
		  AND state='idle'
		  AND next_run_at <= $1
		ORDER BY next_run_at
		FOR UPDATE SKIP LOCKED
		LIMIT 20
	`, now)
	if err != nil {
		return 0, fmt.Errorf("select due youtube monitors: %w", err)
	}
	monitors := make([]Monitor, 0)
	for rows.Next() {
		item, err := scanMonitor(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		monitors = append(monitors, item)
	}
	rows.Close()
	for _, monitor := range monitors {
		runID, err := identity.NewUUID()
		if err != nil {
			return 0, err
		}
		raw, err := json.Marshal(monitor)
		if err != nil {
			return 0, fmt.Errorf("encode due monitor snapshot: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO youtube_monitor_runs (
				id, monitor_id, trigger, status, config_snapshot, started_at
			) VALUES ($1,$2,'scheduled','queued',$3,$4)
		`, runID, monitor.ID, raw, now); err != nil {
			return 0, fmt.Errorf("enqueue due monitor: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE youtube_monitors
			SET
				state='running',
				next_run_at=$2::timestamptz + make_interval(mins => schedule_interval_minutes),
				updated_at=$2,
				version=version+1
			WHERE id=$1
		`, monitor.ID, now); err != nil {
			return 0, fmt.Errorf("advance due monitor: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit due monitor scan: %w", err)
	}
	return len(monitors), nil
}

func (s *PostgresStore) ClaimRun(
	ctx context.Context,
	owner string,
) (ClaimedRun, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ClaimedRun{}, fmt.Errorf("begin monitor run claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var runID string
	err = tx.QueryRow(ctx, `
		SELECT id::text
		FROM youtube_monitor_runs
		WHERE status='queued'
		ORDER BY started_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimedRun{}, ErrRunNotFound
	}
	if err != nil {
		return ClaimedRun{}, fmt.Errorf("select queued monitor run: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE youtube_monitor_runs
		SET status='running',
		    lease_owner=$2,
		    lease_expires_at=now() + interval '5 minutes'
		WHERE id=$1
	`, runID, owner); err != nil {
		return ClaimedRun{}, fmt.Errorf("claim monitor run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ClaimedRun{}, fmt.Errorf("commit monitor run claim: %w", err)
	}
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return ClaimedRun{}, err
	}
	monitor, err := s.Get(ctx, run.MonitorID)
	if err != nil {
		return ClaimedRun{}, err
	}
	return ClaimedRun{Run: run, Monitor: monitor}, nil
}

func (s *PostgresStore) RequeueExpiredRuns(
	ctx context.Context,
	now time.Time,
) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE youtube_monitor_runs
		SET status='queued', lease_owner='', lease_expires_at=NULL
		WHERE status='running'
		  AND lease_expires_at IS NOT NULL
		  AND lease_expires_at < $1
	`, now)
	if err != nil {
		return 0, fmt.Errorf("requeue expired monitor runs: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *PostgresStore) GetRun(ctx context.Context, id string) (Run, error) {
	item, err := scanRun(s.pool.QueryRow(ctx, `
		SELECT jsonb_build_object(
			'id', id,
			'monitor_id', monitor_id,
			'trigger', trigger,
			'status', status,
			'config_snapshot', config_snapshot,
			'discovered_count', discovered_count,
			'accepted_count', accepted_count,
			'duplicate_count', duplicate_count,
			'task_count', task_count,
			'quota_units', quota_units,
			'error_code', error_code,
			'error_message', error_message,
			'started_at', started_at,
			'completed_at', completed_at
		)
		FROM youtube_monitor_runs
		WHERE id=$1
	`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	return item, err
}

func (s *PostgresStore) RecordItem(
	ctx context.Context,
	run Run,
	monitor Monitor,
	candidate Candidate,
	decision string,
	reason string,
	taskID *string,
	now time.Time,
) error {
	itemID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO youtube_monitor_items (
			id, run_id, monitor_id, external_video_id, episode_number,
			series_scope_key, series_scope_name, source_url,
			title, channel_id, channel_title, published_at,
			duration_seconds, view_count, like_count, comment_count,
			video_type, decision, decision_reason, task_id, created_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21
		)
	`, itemID, run.ID, monitor.ID, candidate.ExternalVideoID, candidate.EpisodeNumber,
		candidate.SeriesScopeKey, candidate.SeriesScopeName, candidate.SourceURL,
		candidate.Title, candidate.ChannelID, candidate.ChannelTitle,
		candidate.PublishedAt, candidate.DurationSeconds, candidate.ViewCount,
		candidate.LikeCount, candidate.CommentCount, candidate.VideoType,
		decision, reason, taskID, now)
	if err != nil {
		return fmt.Errorf("record youtube monitor item: %w", err)
	}
	return nil
}

func (s *PostgresStore) ReserveIngestion(
	ctx context.Context,
	externalVideoID string,
	monitorID string,
	now time.Time,
) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO youtube_video_ingestions (
			external_video_id, monitor_id, state, first_seen_at
		) VALUES ($1,$2,'reserved',$3)
		ON CONFLICT (external_video_id) DO UPDATE
		SET monitor_id=EXCLUDED.monitor_id,
			state='reserved',
			first_seen_at=EXCLUDED.first_seen_at
		WHERE youtube_video_ingestions.state='created'
		  AND youtube_video_ingestions.task_id IS NULL
	`, externalVideoID, monitorID, now)
	if err != nil {
		return false, fmt.Errorf("reserve monitor ingestion: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *PostgresStore) Seen(
	ctx context.Context,
	monitorID string,
	externalVideoID string,
) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM youtube_monitor_seen
			WHERE monitor_id=$1 AND external_video_id=$2
		)
	`, monitorID, externalVideoID).Scan(&exists)
	return exists, err
}

func (s *PostgresStore) MarkSeen(
	ctx context.Context,
	monitorID string,
	externalVideoID string,
	runID string,
	now time.Time,
) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO youtube_monitor_seen (
			monitor_id, external_video_id, first_run_id, first_seen_at
		) VALUES ($1,$2,$3,$4)
		ON CONFLICT (monitor_id, external_video_id) DO NOTHING
	`, monitorID, externalVideoID, runID, now)
	return err
}

func (s *PostgresStore) FinalizeIngestion(
	ctx context.Context,
	externalVideoID string,
	taskID string,
	monitorID string,
	runID string,
	now time.Time,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin monitor ingestion finalization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE youtube_video_ingestions
		SET task_id=$2, state='created'
		WHERE external_video_id=$1 AND state='reserved'
	`, externalVideoID, taskID)
	if err != nil {
		return fmt.Errorf("finalize monitor ingestion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("monitor ingestion reservation was lost")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO youtube_monitor_seen (
			monitor_id, external_video_id, first_run_id, first_seen_at
		) VALUES ($1,$2,$3,$4)
		ON CONFLICT (monitor_id, external_video_id) DO NOTHING
	`, monitorID, externalVideoID, runID, now); err != nil {
		return fmt.Errorf("register monitor seen item: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit monitor ingestion finalization: %w", err)
	}
	return nil
}

func (s *PostgresStore) ReleaseIngestion(
	ctx context.Context,
	externalVideoID string,
) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM youtube_video_ingestions
		WHERE external_video_id=$1 AND state='reserved'
	`, externalVideoID)
	return err
}

func (s *PostgresStore) Ingestion(
	ctx context.Context,
	externalVideoID string,
) (*string, string, error) {
	var taskID *string
	var state string
	err := s.pool.QueryRow(ctx, `
		SELECT task_id::text, state
		FROM youtube_video_ingestions
		WHERE external_video_id=$1
	`, externalVideoID).Scan(&taskID, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "missing", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("load monitor ingestion: %w", err)
	}
	return taskID, state, nil
}

func (s *PostgresStore) ItemsByIDs(
	ctx context.Context,
	monitorID string,
	itemIDs []string,
) ([]Item, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT jsonb_build_object(
			'id', id,
			'run_id', run_id,
			'external_video_id', external_video_id,
			'episode_number', episode_number,
			'series_scope_key', series_scope_key,
			'series_scope_name', series_scope_name,
			'source_url', source_url,
			'title', title,
			'channel_id', channel_id,
			'channel_title', channel_title,
			'published_at', published_at,
			'duration_seconds', duration_seconds,
			'view_count', view_count,
			'like_count', like_count,
			'comment_count', comment_count,
			'video_type', video_type,
			'decision', decision,
			'decision_reason', decision_reason,
			'task_id', task_id,
			'created_at', created_at
		)
		FROM youtube_monitor_items
		WHERE monitor_id=$1 AND id=ANY($2::uuid[])
	`, monitorID, itemIDs)
	if err != nil {
		return nil, fmt.Errorf("load monitor items for tasks: %w", err)
	}
	defer rows.Close()
	result := make([]Item, 0, len(itemIDs))
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *PostgresStore) LinkItemTask(
	ctx context.Context,
	itemID string,
	decision string,
	reason string,
	taskID *string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin monitor item task link: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var runID string
	err = tx.QueryRow(ctx, `
		UPDATE youtube_monitor_items
		SET decision=$2, decision_reason=$3, task_id=$4
		WHERE id=$1
		RETURNING run_id::text
	`, itemID, decision, reason, taskID).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("monitor item was not found")
	}
	if err != nil {
		return fmt.Errorf("link monitor item task: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE youtube_monitor_runs AS run
		SET
			accepted_count=summary.accepted_count,
			duplicate_count=summary.duplicate_count,
			task_count=summary.task_count
		FROM (
			SELECT
				count(*) FILTER (WHERE decision IN ('accepted','task_created')) AS accepted_count,
				count(*) FILTER (WHERE decision='duplicate') AS duplicate_count,
				count(*) FILTER (WHERE decision='task_created') AS task_count
			FROM youtube_monitor_items
			WHERE run_id=$1
		) AS summary
		WHERE run.id=$1
	`, runID); err != nil {
		return fmt.Errorf("refresh monitor run task counts: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit monitor item task link: %w", err)
	}
	return nil
}

func (s *PostgresStore) CompleteRun(
	ctx context.Context,
	run ClaimedRun,
	quotaUnits int,
	runErr error,
	now time.Time,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin monitor run completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var discovered, accepted, duplicates, taskCount int
	if err := tx.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (
				WHERE decision IN ('accepted','task_created')
			),
			count(*) FILTER (WHERE decision='duplicate'),
			count(*) FILTER (WHERE decision='task_created')
		FROM youtube_monitor_items
		WHERE run_id=$1
	`, run.Run.ID).Scan(&discovered, &accepted, &duplicates, &taskCount); err != nil {
		return fmt.Errorf("summarize monitor run: %w", err)
	}
	status := "completed"
	errorCode := ""
	errorMessage := ""
	monitorState := "idle"
	if runErr != nil {
		status = "failed"
		errorCode = "monitor_execution_failed"
		errorMessage = runErr.Error()
		monitorState = "error"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE youtube_monitor_runs
		SET
			status=$2,
			discovered_count=$3,
			accepted_count=$4,
			duplicate_count=$5,
			task_count=$6,
			quota_units=$7,
			error_code=$8,
			error_message=$9,
			completed_at=$10,
			lease_expires_at=NULL
		WHERE id=$1
	`, run.Run.ID, status, discovered, accepted, duplicates, taskCount,
		quotaUnits, errorCode, errorMessage, now); err != nil {
		return fmt.Errorf("complete monitor run: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE youtube_monitors
		SET
			state=CASE WHEN enabled THEN $2 ELSE 'paused' END,
			last_run_at=$3,
			last_error=$4,
			next_run_at=CASE
				WHEN enabled AND schedule_type='automatic'
					THEN COALESCE(
						next_run_at,
						$3::timestamptz + make_interval(mins => schedule_interval_minutes)
					)
				ELSE NULL
			END,
			updated_at=$3,
			version=version+1
		WHERE id=$1
	`, run.Monitor.ID, monitorState, now, errorMessage); err != nil {
		return fmt.Errorf("complete monitor state: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit monitor run completion: %w", err)
	}
	return nil
}

func (s *PostgresStore) History(
	ctx context.Context,
	monitorID string,
	runLimit int,
) (History, error) {
	monitor, err := s.Get(ctx, monitorID)
	if err != nil {
		return History{}, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT jsonb_build_object(
			'id', id,
			'monitor_id', monitor_id,
			'trigger', trigger,
			'status', status,
			'config_snapshot', config_snapshot,
			'discovered_count', discovered_count,
			'accepted_count', accepted_count,
			'duplicate_count', duplicate_count,
			'task_count', task_count,
			'quota_units', quota_units,
			'error_code', error_code,
			'error_message', error_message,
			'started_at', started_at,
			'completed_at', completed_at
		)
		FROM youtube_monitor_runs
		WHERE monitor_id=$1
		ORDER BY started_at DESC
		LIMIT $2
	`, monitorID, runLimit)
	if err != nil {
		return History{}, fmt.Errorf("list monitor history: %w", err)
	}
	runs := make([]Run, 0)
	runIDs := make([]string, 0)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			rows.Close()
			return History{}, err
		}
		runs = append(runs, run)
		runIDs = append(runIDs, run.ID)
	}
	rows.Close()
	items := make([]Item, 0)
	if len(runIDs) > 0 {
		itemRows, err := s.pool.Query(ctx, `
			SELECT jsonb_build_object(
				'id', id,
				'run_id', run_id,
				'external_video_id', external_video_id,
				'episode_number', episode_number,
				'series_scope_key', series_scope_key,
				'series_scope_name', series_scope_name,
				'source_url', source_url,
				'title', title,
				'channel_id', channel_id,
				'channel_title', channel_title,
				'published_at', published_at,
				'duration_seconds', duration_seconds,
				'view_count', view_count,
				'like_count', like_count,
				'comment_count', comment_count,
				'video_type', video_type,
				'decision', decision,
				'decision_reason', decision_reason,
				'task_id', COALESCE(
					i.task_id,
					(
						SELECT ingestion.task_id
						FROM youtube_video_ingestions ingestion
						WHERE ingestion.external_video_id=i.external_video_id
						  AND ingestion.task_id IS NOT NULL
					)
				),
				'created_at', created_at
			)
			FROM youtube_monitor_items i
			WHERE run_id=ANY($1::uuid[])
			ORDER BY
				array_position($1::uuid[], i.run_id),
				i.created_at ASC,
				i.id ASC
		`, runIDs)
		if err != nil {
			return History{}, fmt.Errorf("list monitor history items: %w", err)
		}
		for itemRows.Next() {
			item, err := scanItem(itemRows)
			if err != nil {
				itemRows.Close()
				return History{}, err
			}
			items = append(items, item)
		}
		itemRows.Close()
	}
	return History{Monitor: monitor, Runs: runs, Items: items}, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMonitor(row rowScanner) (Monitor, error) {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return Monitor{}, err
	}
	var item Monitor
	if err := json.Unmarshal(raw, &item); err != nil {
		return Monitor{}, fmt.Errorf("decode youtube monitor: %w", err)
	}
	return item, nil
}

func scanRun(row rowScanner) (Run, error) {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return Run{}, err
	}
	var item Run
	if err := json.Unmarshal(raw, &item); err != nil {
		return Run{}, fmt.Errorf("decode youtube monitor run: %w", err)
	}
	return item, nil
}

func scanItem(row rowScanner) (Item, error) {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return Item{}, err
	}
	var item Item
	if err := json.Unmarshal(raw, &item); err != nil {
		return Item{}, fmt.Errorf("decode monitor item: %w", err)
	}
	return item, nil
}

const monitorInsertSQL = `
	WITH value AS (SELECT $2::jsonb AS v)
	INSERT INTO youtube_monitors (
		id, name, enabled, monitor_type, channel_mode, query,
		series_title, series_scopes, episode_start, episode_end,
		channel_ids, include_keywords, exclude_keywords, exclude_channel_ids,
		region_code, category_id, lookback_days, max_results, order_by,
		video_types, min_view_count, min_like_count, min_comment_count,
		min_duration_seconds, max_duration_seconds, published_after,
		published_before, schedule_type, schedule_interval_minutes,
		rate_limit_requests, auto_add_to_tasks, task_template, state,
		next_run_at, created_at, updated_at
	)
	SELECT
		$1,
		v->>'name',
		(v->>'enabled')::boolean,
		v->>'monitor_type',
		v->>'channel_mode',
		v->>'query',
		v->>'series_title',
		v->'series_scopes',
		(v->>'episode_start')::integer,
		(v->>'episode_end')::integer,
		ARRAY(SELECT jsonb_array_elements_text(v->'channel_ids')),
		ARRAY(SELECT jsonb_array_elements_text(v->'include_keywords')),
		ARRAY(SELECT jsonb_array_elements_text(v->'exclude_keywords')),
		ARRAY(SELECT jsonb_array_elements_text(v->'exclude_channel_ids')),
		v->>'region_code',
		v->>'category_id',
		(v->>'lookback_days')::integer,
		(v->>'max_results')::integer,
		v->>'order_by',
		ARRAY(SELECT jsonb_array_elements_text(v->'video_types')),
		(v->>'min_view_count')::bigint,
		(v->>'min_like_count')::bigint,
		(v->>'min_comment_count')::bigint,
		(v->>'min_duration_seconds')::integer,
		(v->>'max_duration_seconds')::integer,
		NULLIF(v->>'published_after','')::date,
		NULLIF(v->>'published_before','')::date,
		v->>'schedule_type',
		(v->>'schedule_interval_minutes')::integer,
		(v->>'rate_limit_requests')::integer,
		(v->>'auto_add_to_tasks')::boolean,
		v->'task_template',
		CASE WHEN (v->>'enabled')::boolean THEN 'idle' ELSE 'paused' END,
		CASE
			WHEN (v->>'enabled')::boolean AND v->>'schedule_type'='automatic'
				THEN $3::timestamptz + make_interval(
					mins => (v->>'schedule_interval_minutes')::integer
				)
			ELSE NULL
		END,
		$3,
		$3
	FROM value
`

const monitorUpdateSQL = `
	WITH value AS (SELECT $2::jsonb AS v)
	UPDATE youtube_monitors AS monitor
	SET
		name=v->>'name',
		enabled=(v->>'enabled')::boolean,
		monitor_type=v->>'monitor_type',
		channel_mode=v->>'channel_mode',
		query=v->>'query',
		series_title=v->>'series_title',
		series_scopes=v->'series_scopes',
		episode_start=(v->>'episode_start')::integer,
		episode_end=(v->>'episode_end')::integer,
		channel_ids=ARRAY(SELECT jsonb_array_elements_text(v->'channel_ids')),
		include_keywords=ARRAY(SELECT jsonb_array_elements_text(v->'include_keywords')),
		exclude_keywords=ARRAY(SELECT jsonb_array_elements_text(v->'exclude_keywords')),
		exclude_channel_ids=ARRAY(SELECT jsonb_array_elements_text(v->'exclude_channel_ids')),
		region_code=v->>'region_code',
		category_id=v->>'category_id',
		lookback_days=(v->>'lookback_days')::integer,
		max_results=(v->>'max_results')::integer,
		order_by=v->>'order_by',
		video_types=ARRAY(SELECT jsonb_array_elements_text(v->'video_types')),
		min_view_count=(v->>'min_view_count')::bigint,
		min_like_count=(v->>'min_like_count')::bigint,
		min_comment_count=(v->>'min_comment_count')::bigint,
		min_duration_seconds=(v->>'min_duration_seconds')::integer,
		max_duration_seconds=(v->>'max_duration_seconds')::integer,
		published_after=NULLIF(v->>'published_after','')::date,
		published_before=NULLIF(v->>'published_before','')::date,
		schedule_type=v->>'schedule_type',
		schedule_interval_minutes=(v->>'schedule_interval_minutes')::integer,
		rate_limit_requests=(v->>'rate_limit_requests')::integer,
		auto_add_to_tasks=(v->>'auto_add_to_tasks')::boolean,
		task_template=v->'task_template',
		state=CASE
			WHEN NOT (v->>'enabled')::boolean THEN 'paused'
			WHEN monitor.state='running' THEN 'running'
			ELSE 'idle'
		END,
		next_run_at=CASE
			WHEN (v->>'enabled')::boolean
			  AND v->>'schedule_type'='automatic'
				THEN $4::timestamptz + make_interval(
					mins => (v->>'schedule_interval_minutes')::integer
				)
			ELSE NULL
		END,
		last_error='',
		version=monitor.version+1,
		updated_at=$4
	FROM value
	WHERE monitor.id=$1
	  AND monitor.version=$3
	  AND monitor.archived_at IS NULL
`
