CREATE TABLE cookie_profiles (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    kind text NOT NULL,
    status text NOT NULL,
    encrypted_cookie_jar bytea NOT NULL DEFAULT '\x',
    cloud_server_url text NOT NULL DEFAULT '',
    encrypted_cloud_credentials bytea NOT NULL DEFAULT '\x',
    source_filename text NOT NULL DEFAULT '',
    cookie_count integer NOT NULL DEFAULT 0,
    domain_count integer NOT NULL DEFAULT 0,
    last_synced_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT cookie_profiles_kind_check CHECK (kind IN ('upload', 'cookiecloud')),
    CONSTRAINT cookie_profiles_status_check CHECK (status IN ('ready', 'syncing', 'error')),
    CONSTRAINT cookie_profiles_count_check CHECK (cookie_count >= 0 AND domain_count >= 0),
    CONSTRAINT cookie_profiles_name_check CHECK (length(btrim(name)) BETWEEN 1 AND 80)
);

CREATE INDEX cookie_profiles_updated_at_idx ON cookie_profiles(updated_at DESC);

ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS source_url text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS repost_statement_version text NOT NULL DEFAULT 'full_v1',
    ADD COLUMN IF NOT EXISTS repost_statement_brief text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS repost_statement_full text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cookie_profile_id uuid REFERENCES cookie_profiles(id) ON DELETE SET NULL;

DO $$
BEGIN
    IF to_regclass('public.source_records') IS NOT NULL THEN
        EXECUTE $migration$
            UPDATE tasks AS task
            SET source_url = source.source_url,
                repost_statement_version = source.repost_statement_version,
                repost_statement_brief = '转载来源：' || source.source_url,
                repost_statement_full = '【转载说明】本内容转载自：' || source.source_url ||
                    '。转载声明仅说明来源，不代表取得版权许可。'
            FROM source_records AS source
            WHERE source.task_id = task.id
        $migration$;
    END IF;
END
$$;

DROP TABLE IF EXISTS source_records;

ALTER TABLE tasks
    DROP CONSTRAINT IF EXISTS tasks_source_url_check,
    DROP CONSTRAINT IF EXISTS tasks_statement_version_check;

ALTER TABLE tasks
    ADD CONSTRAINT tasks_source_url_check CHECK (length(btrim(source_url)) > 0),
    ADD CONSTRAINT tasks_statement_version_check CHECK (
        repost_statement_version IN ('brief_v1', 'full_v1')
    );
