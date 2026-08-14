package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/visoraft/visoraft/internal/events"
	"github.com/visoraft/visoraft/internal/moderation"
	"github.com/visoraft/visoraft/internal/pipeline"
	"github.com/visoraft/visoraft/internal/tasks"
)

const (
	metadataStartedV1       = "io.visoraft.media.metadata.started.v1"
	metadataCompletedV1     = "io.visoraft.media.metadata.completed.v1"
	metadataFailedV1        = "io.visoraft.media.metadata.failed.v1"
	downloadStartedV1       = "io.visoraft.media.download.started.v1"
	downloadProgressV1      = "io.visoraft.media.download.progress.v1"
	downloadCompletedV1     = "io.visoraft.media.download.completed.v1"
	downloadFailedV1        = "io.visoraft.media.download.failed.v1"
	downloadCancelledV1     = "io.visoraft.media.download.cancelled.v1"
	mediaInspectStartedV1   = "io.visoraft.media.inspect.started.v1"
	mediaInspectCompletedV1 = "io.visoraft.media.inspect.completed.v1"
	mediaInspectFailedV1    = "io.visoraft.media.inspect.failed.v1"
	assetsDeletedV1         = "io.visoraft.media.assets.deleted.v1"
	assetsDeleteFailedV1    = "io.visoraft.media.assets.delete.failed.v1"
	subtitleStartedV1       = "io.visoraft.subtitle.process.started.v1"
	subtitleProgressV1      = "io.visoraft.subtitle.process.progress.v1"
	subtitleCompletedV1     = "io.visoraft.subtitle.process.completed.v1"
	subtitleFailedV1        = "io.visoraft.subtitle.process.failed.v1"
	transcodeStartedV1      = "io.visoraft.media.transcode.started.v1"
	transcodeProgressV1     = "io.visoraft.media.transcode.progress.v1"
	transcodeCompletedV1    = "io.visoraft.media.transcode.completed.v1"
	transcodeFailedV1       = "io.visoraft.media.transcode.failed.v1"
	transcodeCancelledV1    = "io.visoraft.media.transcode.cancelled.v1"
)

type Consumer struct {
	rabbitMQURL string
	exchange    string
	store       *tasks.PostgresStore
	moderation  *moderation.PostgresStore
	pipeline    *pipeline.Service
	logger      *slog.Logger
}

func NewConsumer(
	rabbitMQURL string,
	exchange string,
	store *tasks.PostgresStore,
	moderationStore *moderation.PostgresStore,
	pipelineService *pipeline.Service,
	logger *slog.Logger,
) *Consumer {
	return &Consumer{
		rabbitMQURL: rabbitMQURL,
		exchange:    exchange,
		store:       store,
		moderation:  moderationStore,
		pipeline:    pipelineService,
		logger:      logger,
	}
}

func (c *Consumer) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		err := c.runSession(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.logger.Warn("workflow consumer session ended", "error", err)
		if !wait(ctx, 3*time.Second) {
			return ctx.Err()
		}
	}
	return ctx.Err()
}

func (c *Consumer) runSession(ctx context.Context) error {
	connection, err := amqp.DialConfig(c.rabbitMQURL, amqp.Config{
		Heartbeat: 10 * time.Second,
		Locale:    "en_US",
	})
	if err != nil {
		return fmt.Errorf("connect rabbitmq: %w", err)
	}
	defer connection.Close()

	channel, err := connection.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	defer channel.Close()

	if err := channel.ExchangeDeclare(c.exchange, "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange: %w", err)
	}
	if err := channel.ExchangeDeclare(c.exchange+".deadletter", "topic", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare dead-letter exchange: %w", err)
	}

	const queueName = "visoraft.workflow-results.v1"
	const deadQueueName = "visoraft.workflow-results.v1.dlq"
	if _, err := channel.QueueDeclare(deadQueueName, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare workflow dead-letter queue: %w", err)
	}
	if err := channel.QueueBind(deadQueueName, "#", c.exchange+".deadletter", false, nil); err != nil {
		return fmt.Errorf("bind workflow dead-letter queue: %w", err)
	}
	queue, err := channel.QueueDeclare(queueName, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange": c.exchange + ".deadletter",
	})
	if err != nil {
		return fmt.Errorf("declare workflow queue: %w", err)
	}
	for _, routingKey := range []string{
		metadataStartedV1,
		metadataCompletedV1,
		metadataFailedV1,
		downloadStartedV1,
		downloadProgressV1,
		downloadCompletedV1,
		downloadFailedV1,
		downloadCancelledV1,
		mediaInspectStartedV1,
		mediaInspectCompletedV1,
		mediaInspectFailedV1,
		assetsDeletedV1,
		assetsDeleteFailedV1,
		subtitleStartedV1,
		subtitleProgressV1,
		subtitleCompletedV1,
		subtitleFailedV1,
		transcodeStartedV1,
		transcodeProgressV1,
		transcodeCompletedV1,
		transcodeFailedV1,
		transcodeCancelledV1,
		moderation.StartedV1,
		moderation.CompletedV1,
		moderation.FailedV1,
	} {
		if err := channel.QueueBind(queue.Name, routingKey, c.exchange, false, nil); err != nil {
			return fmt.Errorf("bind workflow queue: %w", err)
		}
	}
	if err := channel.Qos(8, 0, false); err != nil {
		return fmt.Errorf("configure workflow qos: %w", err)
	}

	deliveries, err := channel.Consume(queue.Name, "workflow-consumer", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("start workflow consumer: %w", err)
	}
	c.logger.Info("workflow consumer connected", "queue", queue.Name)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case delivery, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("workflow delivery channel closed")
			}
			if err := c.handle(ctx, delivery.Body); err != nil {
				if errors.Is(err, tasks.ErrNotFound) {
					c.logger.Warn(
						"discarding workflow event for deleted task",
						"error", err,
						"delivery_tag", delivery.DeliveryTag,
					)
					if ackErr := delivery.Ack(false); ackErr != nil {
						return fmt.Errorf("ack stale workflow event: %w", ackErr)
					}
					continue
				}
				c.logger.Error("workflow event failed", "error", err, "delivery_tag", delivery.DeliveryTag)
				if nackErr := delivery.Nack(false, true); nackErr != nil {
					return fmt.Errorf("nack workflow event: %w", nackErr)
				}
				return err
			}
			if err := delivery.Ack(false); err != nil {
				return fmt.Errorf("ack workflow event: %w", err)
			}
		}
	}
}

func (c *Consumer) handle(ctx context.Context, body []byte) error {
	envelope, err := events.Decode(body)
	if err != nil {
		return err
	}

	switch envelope.Type {
	case metadataStartedV1:
		err = c.store.ApplyMetadataStarted(ctx, envelope)
	case metadataCompletedV1:
		var metadata tasks.Metadata
		if decodeErr := json.Unmarshal(envelope.Data, &metadata); decodeErr != nil {
			return fmt.Errorf("decode metadata result: %w", decodeErr)
		}
		err = c.store.ApplyMetadataCompleted(ctx, envelope, metadata)
	case metadataFailedV1:
		var failure tasks.WorkflowFailure
		if decodeErr := json.Unmarshal(envelope.Data, &failure); decodeErr != nil {
			return fmt.Errorf("decode metadata failure: %w", decodeErr)
		}
		err = c.store.ApplyMetadataFailed(ctx, envelope, failure)
	case downloadStartedV1:
		err = c.store.ApplyDownloadStarted(ctx, envelope)
	case downloadProgressV1:
		var progress tasks.DownloadProgress
		if decodeErr := json.Unmarshal(envelope.Data, &progress); decodeErr != nil {
			return fmt.Errorf("decode download progress: %w", decodeErr)
		}
		err = c.store.ApplyDownloadProgress(ctx, envelope, progress)
	case downloadCompletedV1:
		var result tasks.DownloadResult
		if decodeErr := json.Unmarshal(envelope.Data, &result); decodeErr != nil {
			return fmt.Errorf("decode download result: %w", decodeErr)
		}
		err = c.store.ApplyDownloadCompleted(ctx, envelope, result)
		if err == nil {
			err = c.pipeline.AfterMediaReady(ctx, result.TaskID)
		}
	case downloadFailedV1:
		var failure tasks.WorkflowFailure
		if decodeErr := json.Unmarshal(envelope.Data, &failure); decodeErr != nil {
			return fmt.Errorf("decode download failure: %w", decodeErr)
		}
		err = c.store.ApplyDownloadFailed(ctx, envelope, failure)
	case downloadCancelledV1:
		var event tasks.WorkflowCancellation
		if decodeErr := json.Unmarshal(envelope.Data, &event); decodeErr != nil {
			return fmt.Errorf("decode download cancellation: %w", decodeErr)
		}
		err = c.store.ApplyDownloadCancelled(ctx, envelope, event)
	case mediaInspectStartedV1:
		err = c.store.ApplyMediaInspectStarted(ctx, envelope)
	case mediaInspectCompletedV1:
		var result tasks.MediaInspectResult
		if decodeErr := json.Unmarshal(envelope.Data, &result); decodeErr != nil {
			return fmt.Errorf("decode media inspection result: %w", decodeErr)
		}
		err = c.store.ApplyMediaInspectCompleted(ctx, envelope, result)
	case mediaInspectFailedV1:
		var failure tasks.WorkflowFailure
		if decodeErr := json.Unmarshal(envelope.Data, &failure); decodeErr != nil {
			return fmt.Errorf("decode media inspection failure: %w", decodeErr)
		}
		err = c.store.ApplyMediaInspectFailed(ctx, envelope, failure)
	case assetsDeletedV1:
		var result tasks.AssetDeletionResult
		if decodeErr := json.Unmarshal(envelope.Data, &result); decodeErr != nil {
			return fmt.Errorf("decode asset deletion result: %w", decodeErr)
		}
		err = c.store.ApplyAssetsDeleted(ctx, envelope, result)
	case assetsDeleteFailedV1:
		var failure tasks.AssetDeletionFailure
		if decodeErr := json.Unmarshal(envelope.Data, &failure); decodeErr != nil {
			return fmt.Errorf("decode asset deletion failure: %w", decodeErr)
		}
		err = c.store.ApplyAssetsDeleteFailed(ctx, envelope, failure)
	case subtitleStartedV1:
		err = c.store.ApplySubtitleStarted(ctx, envelope)
	case subtitleProgressV1:
		var progress tasks.SubtitleProgress
		if decodeErr := json.Unmarshal(envelope.Data, &progress); decodeErr != nil {
			return fmt.Errorf("decode subtitle progress: %w", decodeErr)
		}
		err = c.store.ApplySubtitleProgress(ctx, envelope, progress)
	case subtitleCompletedV1:
		var result tasks.SubtitleProcessingResult
		if decodeErr := json.Unmarshal(envelope.Data, &result); decodeErr != nil {
			return fmt.Errorf("decode subtitle processing result: %w", decodeErr)
		}
		if validationErr := tasks.ValidateSubtitleProcessingResult(&result); validationErr != nil {
			c.logger.Error(
				"subtitle result contract rejected",
				"task_id", result.TaskID,
				"error", validationErr,
			)
			err = c.store.ApplySubtitleFailed(ctx, envelope, tasks.WorkflowFailure{
				Code:      "subtitle_result_invalid",
				Message:   "字幕处理结果校验失败：" + validationErr.Error(),
				Retryable: true,
			})
			break
		}
		err = c.store.ApplySubtitleCompleted(ctx, envelope, result)
		if err == nil {
			err = c.pipeline.AfterSubtitlesReady(ctx, result.TaskID)
		}
	case subtitleFailedV1:
		var failure tasks.WorkflowFailure
		if decodeErr := json.Unmarshal(envelope.Data, &failure); decodeErr != nil {
			return fmt.Errorf("decode subtitle processing failure: %w", decodeErr)
		}
		err = c.store.ApplySubtitleFailed(ctx, envelope, failure)
	case transcodeStartedV1:
		var event tasks.TranscodeProgress
		if decodeErr := json.Unmarshal(envelope.Data, &event); decodeErr != nil {
			return fmt.Errorf("decode transcode started event: %w", decodeErr)
		}
		err = c.store.ApplyTranscodeStarted(ctx, envelope, event)
	case transcodeProgressV1:
		var progress tasks.TranscodeProgress
		if decodeErr := json.Unmarshal(envelope.Data, &progress); decodeErr != nil {
			return fmt.Errorf("decode transcode progress: %w", decodeErr)
		}
		err = c.store.ApplyTranscodeProgress(ctx, envelope, progress)
	case transcodeCompletedV1:
		var result tasks.TranscodeResult
		if decodeErr := json.Unmarshal(envelope.Data, &result); decodeErr != nil {
			return fmt.Errorf("decode transcode result: %w", decodeErr)
		}
		err = c.store.ApplyTranscodeCompleted(ctx, envelope, result)
		if err == nil {
			err = c.pipeline.AfterTranscodeReady(ctx, result.TaskID)
		}
	case transcodeFailedV1:
		var failure tasks.TranscodeFailure
		if decodeErr := json.Unmarshal(envelope.Data, &failure); decodeErr != nil {
			return fmt.Errorf("decode transcode failure: %w", decodeErr)
		}
		err = c.store.ApplyTranscodeFailed(ctx, envelope, failure)
	case transcodeCancelledV1:
		var event tasks.TranscodeCancellation
		if decodeErr := json.Unmarshal(envelope.Data, &event); decodeErr != nil {
			return fmt.Errorf("decode transcode cancellation: %w", decodeErr)
		}
		err = c.store.ApplyTranscodeCancelled(ctx, envelope, event)
	case moderation.StartedV1:
		var event moderation.Started
		if decodeErr := json.Unmarshal(envelope.Data, &event); decodeErr != nil {
			return fmt.Errorf("decode moderation started event: %w", decodeErr)
		}
		_, err = c.moderation.ApplyStarted(ctx, envelope, event)
	case moderation.CompletedV1:
		var result moderation.Result
		if decodeErr := json.Unmarshal(envelope.Data, &result); decodeErr != nil {
			return fmt.Errorf("decode moderation completed event: %w", decodeErr)
		}
		var applied bool
		applied, err = c.moderation.ApplyCompleted(ctx, envelope, result)
		if err == nil && applied && result.Decision != moderation.DecisionBlock {
			err = c.pipeline.AfterModerationReady(ctx, result.TaskID)
		}
	case moderation.FailedV1:
		var failure moderation.Failure
		if decodeErr := json.Unmarshal(envelope.Data, &failure); decodeErr != nil {
			return fmt.Errorf("decode moderation failed event: %w", decodeErr)
		}
		var applied bool
		applied, err = c.moderation.ApplyFailed(ctx, envelope, failure)
		if err == nil &&
			applied &&
			failure.Decision == moderation.DecisionManualReview {
			err = c.pipeline.AfterModerationReady(ctx, failure.TaskID)
		}
	default:
		return fmt.Errorf("unsupported workflow event %s", envelope.Type)
	}
	if err != nil {
		return err
	}
	c.logger.Info("workflow event applied", "message_id", envelope.ID, "event_type", envelope.Type, "subject", envelope.Subject)
	return nil
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
