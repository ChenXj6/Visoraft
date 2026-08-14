ALTER TABLE youtube_monitors
    ADD COLUMN IF NOT EXISTS archived_at timestamptz;

DROP INDEX IF EXISTS youtube_monitors_due_idx;

CREATE INDEX youtube_monitors_due_idx
    ON youtube_monitors(enabled, schedule_type, next_run_at)
    WHERE enabled
      AND schedule_type = 'automatic'
      AND archived_at IS NULL;
