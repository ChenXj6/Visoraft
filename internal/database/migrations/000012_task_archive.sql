ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS archived_at timestamptz,
    ADD COLUMN IF NOT EXISTS archived_by text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS tasks_active_created_idx
    ON tasks(created_at DESC)
    WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS tasks_archived_at_idx
    ON tasks(archived_at DESC)
    WHERE archived_at IS NOT NULL;
