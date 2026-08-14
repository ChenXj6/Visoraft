package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/visoraft/visoraft/internal/events"
	"github.com/visoraft/visoraft/internal/identity"
	"github.com/visoraft/visoraft/internal/moderation"
	"github.com/visoraft/visoraft/internal/reviews"
	"github.com/visoraft/visoraft/internal/settings"
	"github.com/visoraft/visoraft/internal/taskconfig"
	"github.com/visoraft/visoraft/internal/tasks"
)

type checkpoint string

const (
	checkpointMediaReady      checkpoint = "media_ready"
	checkpointSubtitlesReady  checkpoint = "subtitles_ready"
	checkpointTranscodeReady  checkpoint = "transcode_ready"
	checkpointModerationReady checkpoint = "moderation_ready"
)

type Service struct {
	pool    *pgxpool.Pool
	reviews *reviews.Service
	now     func() time.Time
}

func NewService(pool *pgxpool.Pool, reviewService *reviews.Service) *Service {
	return &Service{
		pool:    pool,
		reviews: reviewService,
		now:     time.Now,
	}
}

func (s *Service) AfterMediaReady(ctx context.Context, taskID string) error {
	return s.advance(ctx, taskID, checkpointMediaReady)
}

func (s *Service) AfterSubtitlesReady(ctx context.Context, taskID string) error {
	return s.advance(ctx, taskID, checkpointSubtitlesReady)
}

func (s *Service) AfterTranscodeReady(ctx context.Context, taskID string) error {
	return s.advance(ctx, taskID, checkpointTranscodeReady)
}

func (s *Service) AfterModerationReady(ctx context.Context, taskID string) error {
	return s.advance(ctx, taskID, checkpointModerationReady)
}

func (s *Service) advance(
	ctx context.Context,
	taskID string,
	reached checkpoint,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin pipeline advance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	var snapshotRaw []byte
	err = tx.QueryRow(ctx, `
		SELECT status, settings_snapshot
		FROM tasks
		WHERE id=$1
		FOR UPDATE
	`, taskID).Scan(&status, &snapshotRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return tasks.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock task for pipeline advance: %w", err)
	}
	if status != tasks.StatusProcessing {
		return nil
	}

	var snapshot settings.ConfigSnapshot
	if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
		return fmt.Errorf("decode task pipeline snapshot: %w", err)
	}
	policy, err := taskconfig.Decode(snapshotRaw)
	if err != nil {
		return err
	}
	hardcodedChinese := false
	if reached == checkpointSubtitlesReady {
		var decisionRaw []byte
		err := tx.QueryRow(ctx, `
			SELECT COALESCE(detail -> 'decision', '{}'::jsonb)
			FROM task_steps
			WHERE task_id=$1 AND kind='subtitles'
			ORDER BY attempt DESC
			LIMIT 1
		`, taskID).Scan(&decisionRaw)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("load subtitle pipeline decision: %w", err)
		}
		var decision struct {
			SchemaVersion int    `json:"schema_version"`
			Disposition   string `json:"disposition"`
			BurnSubtitles bool   `json:"burn_subtitles"`
		}
		if len(decisionRaw) > 0 {
			if err := json.Unmarshal(decisionRaw, &decision); err != nil {
				return fmt.Errorf("decode subtitle pipeline decision: %w", err)
			}
		}
		hardcodedChinese = decision.SchemaVersion == 1 &&
			decision.Disposition == "existing_hardcoded_chinese" &&
			!decision.BurnSubtitles
		if hardcodedChinese {
			snapshot.Transcode.BurnSubtitles = false
		}
	}
	next := nextStage(snapshot, policy, reached)
	if hardcodedChinese && snapshot.Transcode.Enabled {
		next = tasks.StepTranscode
	}
	if next == "" {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit pipeline review advance: %w", err)
		}
		_, err := s.reviews.Evaluate(ctx, taskID)
		return err
	}
	if err := s.enqueueStageTx(ctx, tx, taskID, next, snapshot, policy); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit pipeline stage: %w", err)
	}
	return nil
}

func nextStage(
	snapshot settings.ConfigSnapshot,
	policy taskconfig.Policy,
	reached checkpoint,
) string {
	switch reached {
	case checkpointMediaReady:
		if snapshot.Subtitle.Enabled &&
			snapshot.Transcode.Enabled &&
			snapshot.Transcode.BurnSubtitles {
			return tasks.StepSubtitles
		}
		if snapshot.Transcode.Enabled {
			return tasks.StepTranscode
		}
		if snapshot.Subtitle.Enabled {
			return tasks.StepSubtitles
		}
		return terminalStage(snapshot, policy)
	case checkpointSubtitlesReady:
		if snapshot.Transcode.Enabled && snapshot.Transcode.BurnSubtitles {
			return tasks.StepTranscode
		}
		return terminalStage(snapshot, policy)
	case checkpointTranscodeReady:
		if snapshot.Subtitle.Enabled && !snapshot.Transcode.BurnSubtitles {
			return tasks.StepSubtitles
		}
		return terminalStage(snapshot, policy)
	case checkpointModerationReady:
		return ""
	}
	return ""
}

func terminalStage(
	snapshot settings.ConfigSnapshot,
	policy taskconfig.Policy,
) string {
	requiredByStrategy := policy.PostingStrategy != nil &&
		policy.PostingStrategy.RequireContentModeration
	if snapshot.Moderation.Enabled || requiredByStrategy {
		return tasks.StepModeration
	}
	return ""
}

func (s *Service) enqueueStageTx(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	stepKind string,
	snapshot settings.ConfigSnapshot,
	policy taskconfig.Policy,
) error {
	var existingStatus string
	err := tx.QueryRow(ctx, `
		SELECT status
		FROM task_steps
		WHERE task_id=$1 AND kind=$2
	`, taskID, stepKind).Scan(&existingStatus)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check %s pipeline step: %w", stepKind, err)
	}

	now := s.now().UTC()
	stepID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	outboxID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	eventType := ""
	commandData := map[string]any{
		"task_id": taskID,
		"attempt": 1,
	}
	switch stepKind {
	case tasks.StepSubtitles:
		eventType = tasks.SubtitleRequestedV1
	case tasks.StepModeration:
		if !snapshot.Moderation.Enabled {
			return errors.New(
				"posting strategy requires content moderation but the task snapshot has it disabled",
			)
		}
		eventType = moderation.RequestedV1
		runID, err := identity.NewUUID()
		if err != nil {
			return err
		}
		commandData["run_id"] = runID
		policySnapshot := map[string]any{
			"provider":                   snapshot.Moderation.Provider,
			"region":                     snapshot.Moderation.Region,
			"check_text":                 snapshot.Moderation.CheckText,
			"check_image":                snapshot.Moderation.CheckImage,
			"check_video":                snapshot.Moderation.CheckVideo,
			"text_service":               snapshot.Moderation.TextService,
			"image_service":              snapshot.Moderation.ImageService,
			"video_service":              snapshot.Moderation.VideoService,
			"high_risk_action":           snapshot.Moderation.HighRiskAction,
			"medium_risk_action":         snapshot.Moderation.MediumRiskAction,
			"failure_action":             snapshot.Moderation.FailureAction,
			"request_timeout_seconds":    snapshot.Moderation.RequestTimeoutSeconds,
			"video_poll_seconds":         snapshot.Moderation.VideoPollSeconds,
			"video_maximum_wait_seconds": snapshot.Moderation.VideoMaximumWaitSeconds,
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO moderation_runs (
				id, task_id, provider, status, attempt, policy_snapshot,
				created_at, updated_at
			) VALUES ($1,$2,$3,'queued',1,$4,$5,$5)
		`,
			runID,
			taskID,
			snapshot.Moderation.Provider,
			policySnapshot,
			now,
		); err != nil {
			return fmt.Errorf("insert moderation run: %w", err)
		}
	case tasks.StepTranscode:
		eventType = tasks.TranscodeRequestedV1
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
			return fmt.Errorf("load transcode input asset: %w", err)
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
			return fmt.Errorf("load legacy task transcode preset: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO transcode_runs (
				id, task_id, preset_id, status, attempt, input_asset_id,
				command_summary, progress, created_at, updated_at
			) VALUES (
				$1,$2,$3,'queued',1,$4,$5,0,$6,$6
			)
		`,
			runID,
			taskID,
			presetID,
			inputAssetID,
			map[string]any{
				"requested_encoder_mode": snapshot.Transcode.EncoderMode,
				"requested_video_codec":  snapshot.Transcode.VideoCodec,
				"requested_audio_codec":  snapshot.Transcode.AudioCodec,
				"requested_container":    snapshot.Transcode.Container,
				"burn_subtitles":         snapshot.Transcode.BurnSubtitles,
			},
			now,
		); err != nil {
			return fmt.Errorf("insert transcode run: %w", err)
		}
	default:
		return fmt.Errorf("unsupported pipeline step %s", stepKind)
	}
	command, err := events.New(
		eventType,
		"visoraft/workflow-consumer",
		"task/"+taskID,
		now,
		commandData,
	)
	if err != nil {
		return err
	}
	rawCommand, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("encode %s command: %w", stepKind, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_steps (
			id, task_id, kind, status, attempt, progress, updated_at
		) VALUES ($1,$2,$3,'queued',1,0,$4)
	`, stepID, taskID, stepKind, now); err != nil {
		return fmt.Errorf("insert %s step: %w", stepKind, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (
			id, aggregate_id, event_type, payload, status,
			attempts, available_at, created_at
		) VALUES ($1,$2,$3,$4,'pending',0,$5,$5)
	`, outboxID, taskID, eventType, rawCommand, now); err != nil {
		return fmt.Errorf("enqueue %s command: %w", stepKind, err)
	}
	auditID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			id, aggregate_type, aggregate_id, event_type,
			actor_type, actor_id, payload, occurred_at
		) VALUES (
			$1,'task',$2,$3,
			'system','pipeline',$4,$5
		)
	`,
		auditID,
		taskID,
		stepKind+".processing.queued",
		rawCommand,
		now,
	); err != nil {
		return fmt.Errorf("record %s queue audit: %w", stepKind, err)
	}
	return nil
}
