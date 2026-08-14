package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/visoraft/visoraft/internal/events"
	"github.com/visoraft/visoraft/internal/messaging"
	"github.com/visoraft/visoraft/internal/settings"
)

const (
	workerQueueName = "visoraft.moderation.v1"
	workerSource    = "visoraft/moderation-worker"
)

type processingConfigLoader interface {
	ProcessingConfig(context.Context, string) (settings.ProcessingConfig, error)
}

type objectPresigner interface {
	PresignGet(string, string, time.Duration) (string, error)
}

type eventPublisher interface {
	Publish(context.Context, string, string, []byte) error
}

type providerFactory func(
	settings.ModerationConfig,
	map[string]string,
) (Provider, error)

type Worker struct {
	rabbitMQURL string
	exchange    string
	settings    processingConfigLoader
	storage     objectPresigner
	logger      *slog.Logger
	providers   providerFactory
}

func NewWorker(
	rabbitMQURL string,
	exchange string,
	settingsService processingConfigLoader,
	storage objectPresigner,
	logger *slog.Logger,
) *Worker {
	return &Worker{
		rabbitMQURL: rabbitMQURL,
		exchange:    exchange,
		settings:    settingsService,
		storage:     storage,
		logger:      logger,
		providers:   defaultProviderFactory,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		err := w.runSession(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.logger.Warn("moderation worker session ended", "error", err)
		if !waitForWorker(ctx, 3*time.Second) {
			return ctx.Err()
		}
	}
	return ctx.Err()
}

func (w *Worker) runSession(ctx context.Context) error {
	connection, err := amqp.DialConfig(w.rabbitMQURL, amqp.Config{
		Heartbeat: 10 * time.Second,
		Locale:    "en_US",
	})
	if err != nil {
		return fmt.Errorf("connect moderation worker to RabbitMQ: %w", err)
	}
	defer connection.Close()

	channel, err := connection.Channel()
	if err != nil {
		return fmt.Errorf("open moderation worker channel: %w", err)
	}
	defer channel.Close()
	if err := channel.ExchangeDeclare(
		w.exchange,
		"topic",
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("declare moderation exchange: %w", err)
	}
	deadExchange := w.exchange + ".deadletter"
	if err := channel.ExchangeDeclare(
		deadExchange,
		"topic",
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("declare moderation dead-letter exchange: %w", err)
	}
	deadQueueName := workerQueueName + ".dlq"
	if _, err := channel.QueueDeclare(
		deadQueueName,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("declare moderation dead-letter queue: %w", err)
	}
	if err := channel.QueueBind(
		deadQueueName,
		RequestedV1,
		deadExchange,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("bind moderation dead-letter queue: %w", err)
	}
	queue, err := channel.QueueDeclare(
		workerQueueName,
		true,
		false,
		false,
		false,
		amqp.Table{"x-dead-letter-exchange": deadExchange},
	)
	if err != nil {
		return fmt.Errorf("declare moderation worker queue: %w", err)
	}
	if err := channel.QueueBind(
		queue.Name,
		RequestedV1,
		w.exchange,
		false,
		nil,
	); err != nil {
		return fmt.Errorf("bind moderation worker queue: %w", err)
	}
	if err := channel.Qos(1, 0, false); err != nil {
		return fmt.Errorf("configure moderation worker QoS: %w", err)
	}
	deliveries, err := channel.Consume(
		queue.Name,
		"moderation-worker",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("consume moderation commands: %w", err)
	}
	publisher, err := messaging.NewPublisher(w.rabbitMQURL, w.exchange)
	if err != nil {
		return err
	}
	defer publisher.Close()
	w.logger.Info("moderation worker connected", "queue", queue.Name)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case delivery, ok := <-deliveries:
			if !ok {
				return errors.New("moderation delivery channel closed")
			}
			if err := w.handle(ctx, delivery.Body, publisher); err != nil {
				w.logger.Error(
					"moderation command failed",
					"error", err,
					"delivery_tag", delivery.DeliveryTag,
				)
				if nackErr := delivery.Nack(false, true); nackErr != nil {
					return fmt.Errorf("nack moderation command: %w", nackErr)
				}
				return err
			}
			if err := delivery.Ack(false); err != nil {
				return fmt.Errorf("ack moderation command: %w", err)
			}
		}
	}
}

func (w *Worker) handle(
	ctx context.Context,
	raw []byte,
	publisher eventPublisher,
) error {
	envelope, err := events.Decode(raw)
	if err != nil {
		return err
	}
	if envelope.Type != RequestedV1 {
		return fmt.Errorf("unsupported moderation command %s", envelope.Type)
	}
	var command Command
	if err := json.Unmarshal(envelope.Data, &command); err != nil {
		return fmt.Errorf("decode moderation command: %w", err)
	}
	if command.TaskID == "" || command.RunID == "" || command.Attempt < 1 {
		return errors.New("moderation command is missing task, run, or attempt")
	}
	processing, err := w.settings.ProcessingConfig(ctx, command.TaskID)
	if err != nil {
		return fmt.Errorf("load moderation processing config: %w", err)
	}
	if !processing.Moderation.Enabled {
		return w.publishFailure(ctx, publisher, command, processing.Moderation, &ProviderError{
			Code:      "moderation_disabled",
			Message:   "content moderation was queued but is disabled in the task snapshot",
			Retryable: false,
		})
	}
	provider, err := w.providers(processing.Moderation, processing.Secrets)
	if err != nil {
		return w.publishFailure(ctx, publisher, command, processing.Moderation, &ProviderError{
			Code:      "moderation_provider_unavailable",
			Message:   "content moderation provider could not be initialized",
			Retryable: false,
		})
	}
	request, err := w.buildRequest(command.TaskID, processing)
	if err != nil {
		return w.publishFailure(ctx, publisher, command, processing.Moderation, &ProviderError{
			Code:      "moderation_input_unavailable",
			Message:   err.Error(),
			Retryable: false,
		})
	}
	if err := w.publish(ctx, publisher, StartedV1, command.TaskID, Started{
		TaskID:   command.TaskID,
		RunID:    command.RunID,
		Attempt:  command.Attempt,
		Provider: processing.Moderation.Provider,
	}); err != nil {
		return err
	}
	result, err := provider.Moderate(ctx, request)
	if err != nil {
		var providerError *ProviderError
		if !errors.As(err, &providerError) {
			providerError = &ProviderError{
				Code:      "moderation_provider_failed",
				Message:   "content moderation provider failed",
				Retryable: true,
			}
		}
		return w.publishFailure(
			ctx,
			publisher,
			command,
			processing.Moderation,
			providerError,
		)
	}
	result.TaskID = command.TaskID
	result.RunID = command.RunID
	result.Attempt = command.Attempt
	if result.Provider == "" {
		result.Provider = processing.Moderation.Provider
	}
	if err := w.publish(
		ctx,
		publisher,
		CompletedV1,
		command.TaskID,
		result,
	); err != nil {
		return err
	}
	w.logger.Info(
		"moderation command completed",
		"task_id", command.TaskID,
		"run_id", command.RunID,
		"decision", result.Decision,
		"risk_level", result.RiskLevel,
	)
	return nil
}

func (w *Worker) buildRequest(
	taskID string,
	processing settings.ProcessingConfig,
) (Request, error) {
	request := Request{
		TaskID: taskID,
		Config: processing.Moderation,
		Texts: []TextInput{
			{ID: "title", Content: processing.Runtime.Title},
			{ID: "description", Content: processing.Runtime.Description},
			{ID: "tags", Content: strings.Join(processing.Runtime.Tags, " ")},
			{ID: "repost_statement", Content: processing.Runtime.RepostStatement},
		},
	}
	if processing.Moderation.Provider == "fixture" {
		return request, nil
	}
	validFor := time.Duration(
		processing.Moderation.VideoMaximumWaitSeconds+
			processing.Moderation.RequestTimeoutSeconds,
	)*time.Second + 30*time.Minute
	if validFor < time.Hour {
		validFor = time.Hour
	}
	if validFor > 24*time.Hour {
		validFor = 24 * time.Hour
	}
	if processing.Moderation.CheckImage {
		if processing.Runtime.CoverAsset != nil {
			value, err := w.storage.PresignGet(
				processing.Runtime.CoverAsset.Bucket,
				processing.Runtime.CoverAsset.ObjectKey,
				validFor,
			)
			if err != nil {
				return Request{}, fmt.Errorf(
					"create public cover URL for content moderation: %w",
					err,
				)
			}
			request.ImageURL = value
		} else {
			value, err := publicHTTPURL(processing.Runtime.ThumbnailURL)
			if err != nil {
				return Request{}, err
			}
			request.ImageURL = value
		}
	}
	if processing.Moderation.CheckVideo {
		if processing.Runtime.FinalMediaAsset == nil {
			return Request{}, errors.New(
				"video moderation is enabled but the final media asset is unavailable",
			)
		}
		value, err := w.storage.PresignGet(
			processing.Runtime.FinalMediaAsset.Bucket,
			processing.Runtime.FinalMediaAsset.ObjectKey,
			validFor,
		)
		if err != nil {
			return Request{}, fmt.Errorf(
				"create public video URL for content moderation: %w",
				err,
			)
		}
		request.VideoURL = value
	}
	return request, nil
}

func (w *Worker) publishFailure(
	ctx context.Context,
	publisher eventPublisher,
	command Command,
	config settings.ModerationConfig,
	providerError *ProviderError,
) error {
	partial := providerError.Partial
	failure := Failure{
		TaskID:    command.TaskID,
		RunID:     command.RunID,
		Attempt:   command.Attempt,
		Provider:  firstNonEmpty(partial.Provider, config.Provider),
		Code:      providerError.Code,
		Message:   truncateMessage(providerError.Message),
		Retryable: providerError.Retryable,
		Decision:  FailureDecision(config),
		Text:      partial.Text,
		Image:     partial.Image,
		Video:     partial.Video,
	}
	if failure.Text.Status == "" {
		failure.Text = skipped(config.TextService)
	}
	if failure.Image.Status == "" {
		failure.Image = skipped(config.ImageService)
	}
	if failure.Video.Status == "" {
		failure.Video = skipped(config.VideoService)
	}
	if err := w.publish(
		ctx,
		publisher,
		FailedV1,
		command.TaskID,
		failure,
	); err != nil {
		return err
	}
	w.logger.Warn(
		"moderation command produced a failure decision",
		"task_id", command.TaskID,
		"run_id", command.RunID,
		"code", failure.Code,
		"retryable", failure.Retryable,
		"decision", failure.Decision,
	)
	return nil
}

func (w *Worker) publish(
	ctx context.Context,
	publisher eventPublisher,
	eventType string,
	taskID string,
	data any,
) error {
	envelope, err := events.New(
		eventType,
		workerSource,
		"task/"+taskID,
		time.Now().UTC(),
		data,
	)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode moderation event: %w", err)
	}
	publishCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return publisher.Publish(publishCtx, eventType, envelope.ID, raw)
}

func defaultProviderFactory(
	config settings.ModerationConfig,
	secrets map[string]string,
) (Provider, error) {
	switch config.Provider {
	case "fixture":
		return NewFixtureProvider(), nil
	case "aliyun":
		return NewAliyunProvider(
			secrets[settings.SecretAliyunAccessKeyID],
			secrets[settings.SecretAliyunAccessKeySecret],
			config,
		)
	default:
		return nil, fmt.Errorf(
			"unsupported moderation provider %q",
			config.Provider,
		)
	}
}

func publicHTTPURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New(
			"image moderation is enabled but no cover or thumbnail is available",
		)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", errors.New(
			"image moderation requires an HTTP or HTTPS cover URL",
		)
	}
	parsed.Fragment = ""
	return parsed.String(), nil
}

func truncateMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "content moderation failed"
	}
	runes := []rune(value)
	if len(runes) > 1000 {
		runes = runes[:1000]
	}
	return string(runes)
}

func waitForWorker(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
