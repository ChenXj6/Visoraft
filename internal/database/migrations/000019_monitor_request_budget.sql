ALTER TABLE youtube_monitors
    DROP CONSTRAINT IF EXISTS youtube_monitors_rate_limit_check;

ALTER TABLE youtube_monitors
    ADD CONSTRAINT youtube_monitors_rate_limit_check
    CHECK (rate_limit_requests BETWEEN 1 AND 250);
