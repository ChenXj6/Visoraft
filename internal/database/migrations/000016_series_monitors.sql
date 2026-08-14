ALTER TABLE youtube_monitors
    DROP CONSTRAINT IF EXISTS youtube_monitors_type_check;

ALTER TABLE youtube_monitors
    ADD CONSTRAINT youtube_monitors_type_check
        CHECK (monitor_type IN ('search', 'channel', 'series')),
    ADD COLUMN series_title text NOT NULL DEFAULT '',
    ADD COLUMN episode_start integer NOT NULL DEFAULT 0,
    ADD COLUMN episode_end integer NOT NULL DEFAULT 0;

ALTER TABLE youtube_monitors
    ADD CONSTRAINT youtube_monitors_episode_range_check
        CHECK (
            (monitor_type <> 'series' AND episode_start = 0 AND episode_end = 0)
            OR
            (monitor_type = 'series' AND episode_start >= 1 AND episode_end >= episode_start)
        );

ALTER TABLE youtube_monitor_items
    ADD COLUMN episode_number integer NOT NULL DEFAULT 0;

CREATE INDEX youtube_monitor_items_episode_idx
    ON youtube_monitor_items(monitor_id, episode_number)
    WHERE episode_number > 0;
