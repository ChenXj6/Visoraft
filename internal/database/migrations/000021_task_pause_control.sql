ALTER TABLE tasks
    ADD COLUMN paused_at timestamptz,
    ADD COLUMN paused_from_status text NOT NULL DEFAULT '',
    ADD COLUMN paused_step_kind text NOT NULL DEFAULT '';

CREATE INDEX tasks_paused_idx
    ON tasks(paused_at, updated_at DESC)
    WHERE paused_at IS NOT NULL;
