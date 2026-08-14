package outbox

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/visoraft/visoraft/internal/messaging"
)

type Dispatcher struct {
	store        *Store
	rabbitMQURL  string
	exchange     string
	pollInterval time.Duration
	logger       *slog.Logger
}

func NewDispatcher(
	store *Store,
	rabbitMQURL string,
	exchange string,
	pollInterval time.Duration,
	logger *slog.Logger,
) *Dispatcher {
	return &Dispatcher{
		store:        store,
		rabbitMQURL:  rabbitMQURL,
		exchange:     exchange,
		pollInterval: pollInterval,
		logger:       logger,
	}
}

func (d *Dispatcher) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		publisher, err := messaging.NewPublisher(d.rabbitMQURL, d.exchange)
		if err != nil {
			d.logger.Warn("rabbitmq publisher unavailable", "error", err)
			if !wait(ctx, 3*time.Second) {
				return ctx.Err()
			}
			continue
		}

		d.logger.Info("outbox publisher connected", "exchange", d.exchange)
		err = d.runSession(ctx, publisher)
		publisher.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		d.logger.Warn("outbox publisher session ended", "error", err)
		if !wait(ctx, 2*time.Second) {
			return ctx.Err()
		}
	}
	return ctx.Err()
}

func (d *Dispatcher) runSession(ctx context.Context, publisher *messaging.Publisher) error {
	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	for {
		if err := d.dispatchBatch(ctx, publisher); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *Dispatcher) dispatchBatch(ctx context.Context, publisher *messaging.Publisher) error {
	messages, err := d.store.Claim(ctx, 20)
	if err != nil {
		return err
	}
	for _, message := range messages {
		publishCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := publisher.Publish(publishCtx, message.EventType, message.ID, message.Payload)
		cancel()
		if err != nil {
			delay := retryDelay(message.Attempts)
			if rescheduleErr := d.store.Reschedule(ctx, message.ID, err.Error(), delay); rescheduleErr != nil {
				d.logger.Error("could not reschedule outbox message", "message_id", message.ID, "error", rescheduleErr)
			}
			return err
		}
		if err := d.store.MarkPublished(ctx, message.ID); err != nil {
			return err
		}
		d.logger.Info(
			"outbox message published",
			"message_id", message.ID,
			"event_type", message.EventType,
			"aggregate_id", message.AggregateID,
		)
	}
	return nil
}

func retryDelay(attempt int) time.Duration {
	seconds := math.Min(math.Pow(2, float64(attempt)), 60)
	return time.Duration(seconds) * time.Second
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
