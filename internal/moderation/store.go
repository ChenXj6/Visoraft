package moderation

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
)

type PostgresStore struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, now: time.Now}
}

func (s *PostgresStore) ApplyStarted(
	ctx context.Context,
	envelope events.Envelope,
	event Started,
) (bool, error) {
	if err := validateEventIdentity(envelope, event.TaskID, event.RunID, event.Attempt); err != nil {
		return false, err
	}
	return s.applyEvent(ctx, envelope, func(
		tx pgx.Tx,
		taskID string,
		now time.Time,
	) (bool, error) {
		tag, err := tx.Exec(ctx, `
			UPDATE moderation_runs
			SET
				status='running',
				started_at=COALESCE(started_at,$3),
				updated_at=$3
			WHERE id=$1 AND task_id=$2 AND attempt=$4
			  AND status IN ('queued','running')
		`, event.RunID, taskID, now, event.Attempt)
		if err != nil {
			return false, fmt.Errorf("start moderation run: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return false, s.ensureRunExists(ctx, tx, event.RunID, taskID)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET
				status='running',
				progress=10,
				started_at=COALESCE(started_at,$2),
				updated_at=$2,
				error_code='',
				error_message=''
			WHERE task_id=$1 AND kind='moderation'
			  AND status IN ('queued','running')
		`, taskID, now); err != nil {
			return false, fmt.Errorf("start moderation step: %w", err)
		}
		if err := s.insertAudit(
			ctx,
			tx,
			taskID,
			"moderation.started",
			envelope,
			map[string]any{
				"run_id":   event.RunID,
				"attempt":  event.Attempt,
				"provider": event.Provider,
			},
			now,
		); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s *PostgresStore) ApplyCompleted(
	ctx context.Context,
	envelope events.Envelope,
	result Result,
) (bool, error) {
	if err := validateEventIdentity(
		envelope,
		result.TaskID,
		result.RunID,
		result.Attempt,
	); err != nil {
		return false, err
	}
	if result.Decision != DecisionPass &&
		result.Decision != DecisionManualReview &&
		result.Decision != DecisionBlock {
		return false, fmt.Errorf("moderation result has invalid decision %q", result.Decision)
	}
	if normalizeRisk(result.RiskLevel) != result.RiskLevel {
		return false, fmt.Errorf("moderation result has invalid risk level %q", result.RiskLevel)
	}
	return s.applyEvent(ctx, envelope, func(
		tx pgx.Tx,
		taskID string,
		now time.Time,
	) (bool, error) {
		textRaw, imageRaw, videoRaw, err := encodeChannels(
			result.Text,
			result.Image,
			result.Video,
		)
		if err != nil {
			return false, err
		}
		runStatus := "passed"
		if result.Decision == DecisionBlock {
			runStatus = "rejected"
		}
		tag, err := tx.Exec(ctx, `
			UPDATE moderation_runs
			SET
				status=$3,
				text_result=$4,
				image_result=$5,
				video_result=$6,
				decision=$7,
				error_code='',
				error_message='',
				started_at=COALESCE(started_at,$8),
				completed_at=$8,
				updated_at=$8
			WHERE id=$1 AND task_id=$2 AND attempt=$9
			  AND status IN ('queued','running')
		`,
			result.RunID,
			taskID,
			runStatus,
			textRaw,
			imageRaw,
			videoRaw,
			result.Decision,
			now,
			result.Attempt,
		)
		if err != nil {
			return false, fmt.Errorf("complete moderation run: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return false, s.ensureRunExists(ctx, tx, result.RunID, taskID)
		}

		stepStatus := "succeeded"
		stepErrorCode := ""
		stepErrorMessage := ""
		if result.Decision == DecisionBlock {
			stepStatus = "failed"
			stepErrorCode = "content_moderation_blocked"
			stepErrorMessage = "内容审核判定为高风险，任务已停止"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET
				status=$2,
				progress=100,
				started_at=COALESCE(started_at,$3),
				finished_at=$3,
				updated_at=$3,
				error_code=$4,
				error_message=$5
			WHERE task_id=$1 AND kind='moderation'
		`,
			taskID,
			stepStatus,
			now,
			stepErrorCode,
			stepErrorMessage,
		); err != nil {
			return false, fmt.Errorf("finish moderation step: %w", err)
		}
		if result.Decision == DecisionBlock {
			if _, err := tx.Exec(ctx, `
				UPDATE tasks
				SET
					status='failed',
					error_code='content_moderation_blocked',
					error_message='内容审核未通过，任务不会进入投稿流程',
					error_retryable=false,
					updated_at=$2,
					version=version+1
				WHERE id=$1 AND status='processing'
			`, taskID, now); err != nil {
				return false, fmt.Errorf("block moderated task: %w", err)
			}
		}
		if err := s.insertAudit(
			ctx,
			tx,
			taskID,
			"moderation.completed",
			envelope,
			map[string]any{
				"run_id":     result.RunID,
				"attempt":    result.Attempt,
				"provider":   result.Provider,
				"risk_level": result.RiskLevel,
				"decision":   result.Decision,
			},
			now,
		); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s *PostgresStore) ApplyFailed(
	ctx context.Context,
	envelope events.Envelope,
	failure Failure,
) (bool, error) {
	if err := validateEventIdentity(
		envelope,
		failure.TaskID,
		failure.RunID,
		failure.Attempt,
	); err != nil {
		return false, err
	}
	if failure.Decision != DecisionManualReview &&
		failure.Decision != DecisionBlock {
		return false, fmt.Errorf("moderation failure has invalid decision %q", failure.Decision)
	}
	if failure.Code == "" || failure.Message == "" {
		return false, errors.New("moderation failure code and message are required")
	}
	return s.applyEvent(ctx, envelope, func(
		tx pgx.Tx,
		taskID string,
		now time.Time,
	) (bool, error) {
		textRaw, imageRaw, videoRaw, err := encodeChannels(
			failure.Text,
			failure.Image,
			failure.Video,
		)
		if err != nil {
			return false, err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE moderation_runs
			SET
				status='failed',
				text_result=$3,
				image_result=$4,
				video_result=$5,
				decision=$6,
				error_code=$7,
				error_message=$8,
				started_at=COALESCE(started_at,$9),
				completed_at=$9,
				updated_at=$9
			WHERE id=$1 AND task_id=$2 AND attempt=$10
			  AND status IN ('queued','running')
		`,
			failure.RunID,
			taskID,
			textRaw,
			imageRaw,
			videoRaw,
			failure.Decision,
			failure.Code,
			failure.Message,
			now,
			failure.Attempt,
		)
		if err != nil {
			return false, fmt.Errorf("fail moderation run: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return false, s.ensureRunExists(ctx, tx, failure.RunID, taskID)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_steps
			SET
				status='failed',
				progress=100,
				started_at=COALESCE(started_at,$2),
				finished_at=$2,
				updated_at=$2,
				error_code=$3,
				error_message=$4
			WHERE task_id=$1 AND kind='moderation'
		`, taskID, now, failure.Code, failure.Message); err != nil {
			return false, fmt.Errorf("fail moderation step: %w", err)
		}
		if failure.Decision == DecisionBlock {
			if _, err := tx.Exec(ctx, `
				UPDATE tasks
				SET
					status='failed',
					error_code=$2,
					error_message=$3,
					error_retryable=$4,
					updated_at=$5,
					version=version+1
				WHERE id=$1 AND status='processing'
			`,
				taskID,
				failure.Code,
				failure.Message,
				failure.Retryable,
				now,
			); err != nil {
				return false, fmt.Errorf("fail moderated task: %w", err)
			}
		}
		if err := s.insertAudit(
			ctx,
			tx,
			taskID,
			"moderation.failed",
			envelope,
			map[string]any{
				"run_id":    failure.RunID,
				"attempt":   failure.Attempt,
				"provider":  failure.Provider,
				"code":      failure.Code,
				"retryable": failure.Retryable,
				"decision":  failure.Decision,
			},
			now,
		); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s *PostgresStore) applyEvent(
	ctx context.Context,
	envelope events.Envelope,
	apply func(pgx.Tx, string, time.Time) (bool, error),
) (bool, error) {
	taskID := taskIDFromSubject(envelope.Subject)
	if taskID == "" {
		return false, errors.New("event subject does not contain a task ID")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin moderation event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		INSERT INTO consumed_messages(message_id, consumer, event_type, consumed_at)
		VALUES ($1,'workflow-consumer',$2,$3)
		ON CONFLICT (message_id) DO NOTHING
	`, envelope.ID, envelope.Type, s.now().UTC())
	if err != nil {
		return false, fmt.Errorf("record consumed moderation message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit duplicate moderation event: %w", err)
		}
		return false, nil
	}
	var exists bool
	if err := tx.QueryRow(
		ctx,
		"SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1)",
		taskID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check moderation task: %w", err)
	}
	if !exists {
		return false, fmt.Errorf("moderation task %s does not exist", taskID)
	}
	now := envelope.Time.UTC()
	if now.IsZero() {
		now = s.now().UTC()
	}
	applied, err := apply(tx, taskID, now)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit moderation event: %w", err)
	}
	return applied, nil
}

func (s *PostgresStore) ensureRunExists(
	ctx context.Context,
	tx pgx.Tx,
	runID string,
	taskID string,
) error {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM moderation_runs WHERE id=$1 AND task_id=$2
		)
	`, runID, taskID).Scan(&exists); err != nil {
		return fmt.Errorf("check moderation run: %w", err)
	}
	if !exists {
		return fmt.Errorf("moderation run %s does not exist", runID)
	}
	return nil
}

func (s *PostgresStore) insertAudit(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	eventType string,
	envelope events.Envelope,
	detail map[string]any,
	now time.Time,
) error {
	auditID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	detail["message_id"] = envelope.ID
	detail["source"] = envelope.Source
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("encode moderation audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (
			id, aggregate_type, aggregate_id, event_type,
			actor_type, actor_id, payload, occurred_at
		) VALUES ($1,'task',$2,$3,'worker',$4,$5,$6)
	`, auditID, taskID, eventType, envelope.Source, raw, now); err != nil {
		return fmt.Errorf("insert moderation audit: %w", err)
	}
	return nil
}

func validateEventIdentity(
	envelope events.Envelope,
	taskID string,
	runID string,
	attempt int,
) error {
	if !identity.IsUUID(taskID) || !identity.IsUUID(runID) {
		return errors.New("moderation event contains an invalid task or run ID")
	}
	if taskIDFromSubject(envelope.Subject) != taskID {
		return errors.New("moderation event task does not match its subject")
	}
	if attempt < 1 || attempt > 100 {
		return errors.New("moderation event contains an invalid attempt")
	}
	return nil
}

func encodeChannels(
	text ChannelResult,
	image ChannelResult,
	video ChannelResult,
) ([]byte, []byte, []byte, error) {
	textRaw, err := json.Marshal(text)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode text moderation result: %w", err)
	}
	imageRaw, err := json.Marshal(image)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode image moderation result: %w", err)
	}
	videoRaw, err := json.Marshal(video)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode video moderation result: %w", err)
	}
	return textRaw, imageRaw, videoRaw, nil
}

func taskIDFromSubject(subject string) string {
	const prefix = "task/"
	if len(subject) <= len(prefix) || subject[:len(prefix)] != prefix {
		return ""
	}
	return subject[len(prefix):]
}
