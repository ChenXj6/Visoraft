CREATE TABLE tasks (
    id uuid PRIMARY KEY,
    status text NOT NULL,
    target_platforms text[] NOT NULL,
    source_url text NOT NULL,
    repost_statement_version text NOT NULL DEFAULT 'full_v1',
    repost_statement_brief text NOT NULL DEFAULT '',
    repost_statement_full text NOT NULL DEFAULT '',
    original_title text NOT NULL DEFAULT '',
    title text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    thumbnail_url text NOT NULL DEFAULT '',
    duration_seconds integer,
    extractor text NOT NULL DEFAULT '',
    error_code text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    error_retryable boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT tasks_status_check CHECK (
        status IN (
            'queued',
            'fetching_metadata',
            'metadata_ready',
            'downloading',
            'processing',
            'awaiting_manual_review',
            'ready_to_publish',
            'publishing',
            'published',
            'reconciled',
            'failed',
            'cancelled',
            'abandoned'
        )
    ),
    CONSTRAINT tasks_target_platforms_check CHECK (cardinality(target_platforms) > 0),
    CONSTRAINT tasks_source_url_check CHECK (length(btrim(source_url)) > 0),
    CONSTRAINT tasks_statement_version_check CHECK (
        repost_statement_version IN ('brief_v1', 'full_v1')
    )
);

CREATE INDEX tasks_created_at_idx ON tasks(created_at DESC);
CREATE INDEX tasks_status_idx ON tasks(status, updated_at DESC);

CREATE TABLE task_steps (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind text NOT NULL,
    status text NOT NULL,
    attempt integer NOT NULL DEFAULT 1,
    progress smallint NOT NULL DEFAULT 0,
    error_code text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    started_at timestamptz,
    finished_at timestamptz,
    updated_at timestamptz NOT NULL,
    UNIQUE(task_id, kind),
    CONSTRAINT task_steps_status_check CHECK (
        status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled', 'skipped')
    ),
    CONSTRAINT task_steps_progress_check CHECK (progress >= 0 AND progress <= 100)
);

CREATE TABLE audit_events (
    id uuid PRIMARY KEY,
    aggregate_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL
);

CREATE INDEX audit_events_aggregate_idx
    ON audit_events(aggregate_type, aggregate_id, occurred_at);

CREATE TABLE outbox_messages (
    id uuid PRIMARY KEY,
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL,
    locked_at timestamptz,
    published_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    CONSTRAINT outbox_status_check CHECK (status IN ('pending', 'publishing', 'published'))
);

CREATE INDEX outbox_pending_idx ON outbox_messages(status, available_at, created_at);

CREATE TABLE consumed_messages (
    message_id uuid PRIMARY KEY,
    consumer text NOT NULL,
    event_type text NOT NULL,
    consumed_at timestamptz NOT NULL
);
