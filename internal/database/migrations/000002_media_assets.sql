CREATE TABLE media_assets (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind text NOT NULL,
    bucket text NOT NULL,
    object_key text NOT NULL,
    original_name text NOT NULL,
    content_type text NOT NULL,
    size_bytes bigint NOT NULL,
    checksum_sha256 text NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL,
    deleted_at timestamptz,
    UNIQUE(task_id, kind, object_key),
    CONSTRAINT media_assets_size_check CHECK (size_bytes >= 0),
    CONSTRAINT media_assets_checksum_check CHECK (
        checksum_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT media_assets_status_check CHECK (
        status IN ('available', 'deleting', 'deleted', 'failed')
    )
);

CREATE INDEX media_assets_task_idx
    ON media_assets(task_id, status, created_at);

CREATE INDEX media_assets_object_idx
    ON media_assets(bucket, object_key);
