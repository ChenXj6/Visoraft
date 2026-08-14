package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Message struct {
	ID          string
	AggregateID string
	EventType   string
	Payload     []byte
	Attempts    int
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Claim(ctx context.Context, limit int) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `
		WITH picked AS (
			SELECT message.id
			FROM outbox_messages message
			WHERE ((
				message.status='pending' AND message.available_at <= now()
			) OR (
				message.status='publishing' AND message.locked_at < now() - interval '2 minutes'
			))
			AND NOT EXISTS (
				SELECT 1
				FROM tasks task
				WHERE task.id=message.aggregate_id
				  AND task.paused_at IS NOT NULL
			)
			ORDER BY message.created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE outbox_messages o
		SET status='publishing', locked_at=now(), attempts=o.attempts+1
		FROM picked
		WHERE o.id=picked.id
		RETURNING o.id::text, o.aggregate_id::text, o.event_type, o.payload, o.attempts
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim outbox messages: %w", err)
	}
	defer rows.Close()

	messages := make([]Message, 0, limit)
	for rows.Next() {
		var message Message
		if err := rows.Scan(
			&message.ID,
			&message.AggregateID,
			&message.EventType,
			&message.Payload,
			&message.Attempts,
		); err != nil {
			return nil, fmt.Errorf("scan outbox message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox messages: %w", err)
	}
	return messages, nil
}

func (s *Store) MarkPublished(ctx context.Context, id string) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE outbox_messages
		SET status='published', published_at=now(), locked_at=NULL, last_error=''
		WHERE id=$1
	`, id); err != nil {
		return fmt.Errorf("mark outbox message published: %w", err)
	}
	return nil
}

func (s *Store) Reschedule(ctx context.Context, id, failure string, delay time.Duration) error {
	if len(failure) > 2000 {
		failure = failure[:2000]
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE outbox_messages
		SET status='pending',
		    available_at=now() + ($2 * interval '1 millisecond'),
		    locked_at=NULL,
		    last_error=$3
		WHERE id=$1
	`, id, delay.Milliseconds(), failure); err != nil {
		return fmt.Errorf("reschedule outbox message: %w", err)
	}
	return nil
}
