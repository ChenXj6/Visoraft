ALTER TABLE tasks
    ADD COLUMN origin_kind text NOT NULL DEFAULT 'manual',
    ADD COLUMN origin_monitor_id uuid,
    ADD COLUMN origin_monitor_name text NOT NULL DEFAULT '',
    ADD COLUMN origin_series_title text NOT NULL DEFAULT '',
    ADD COLUMN origin_series_scope_key text NOT NULL DEFAULT '',
    ADD COLUMN origin_series_scope_name text NOT NULL DEFAULT '',
    ADD COLUMN origin_episode_number integer NOT NULL DEFAULT 0,
    ADD CONSTRAINT tasks_origin_kind_check CHECK (origin_kind IN ('manual', 'monitor')),
    ADD CONSTRAINT tasks_origin_episode_check CHECK (origin_episode_number >= 0);

WITH source AS (
    SELECT DISTINCT ON (item.task_id)
           item.task_id,
           item.monitor_id,
           monitor.name AS monitor_name,
           monitor.series_title,
           item.series_scope_key,
           item.series_scope_name,
           item.episode_number
    FROM youtube_monitor_items AS item
    JOIN youtube_monitors AS monitor ON monitor.id = item.monitor_id
    WHERE item.task_id IS NOT NULL
    ORDER BY item.task_id, item.created_at ASC, item.id ASC
)
UPDATE tasks AS task
SET origin_kind = 'monitor',
    origin_monitor_id = source.monitor_id,
    origin_monitor_name = source.monitor_name,
    origin_series_title = source.series_title,
    origin_series_scope_key = source.series_scope_key,
    origin_series_scope_name = source.series_scope_name,
    origin_episode_number = source.episode_number
FROM source
WHERE task.id = source.task_id
  AND task.origin_kind = 'manual';

CREATE INDEX tasks_origin_monitor_idx
    ON tasks(origin_monitor_id, origin_series_scope_key, origin_episode_number, created_at)
    WHERE origin_kind = 'monitor';

CREATE TABLE local_library_settings (
    singleton boolean PRIMARY KEY DEFAULT true,
    requested_host_path text NOT NULL DEFAULT '',
    auto_sync boolean NOT NULL DEFAULT true,
    version bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT local_library_settings_singleton_check CHECK (singleton)
);

INSERT INTO local_library_settings (singleton)
VALUES (true)
ON CONFLICT (singleton) DO NOTHING;

CREATE TABLE local_library_entries (
    asset_id uuid PRIMARY KEY REFERENCES media_assets(id) ON DELETE CASCADE,
    task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    relative_path text NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    local_size_bytes bigint NOT NULL DEFAULT 0,
    materialized_at timestamptz,
    last_verified_at timestamptz,
    missing_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT local_library_entries_path_check CHECK (
        length(btrim(relative_path)) > 0
        AND relative_path NOT LIKE '/%'
        AND relative_path NOT LIKE '\\%'
    ),
    CONSTRAINT local_library_entries_status_check CHECK (
        status IN ('pending', 'syncing', 'available', 'missing', 'removed', 'error')
    )
);

CREATE INDEX local_library_entries_task_idx
    ON local_library_entries(task_id, created_at);

CREATE INDEX local_library_entries_sync_idx
    ON local_library_entries(status, updated_at)
    WHERE status IN ('pending', 'error');
