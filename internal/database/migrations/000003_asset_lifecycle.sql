ALTER TABLE media_assets
    ADD COLUMN error_code text NOT NULL DEFAULT '',
    ADD COLUMN error_message text NOT NULL DEFAULT '';

CREATE INDEX media_assets_cleanup_idx
    ON media_assets(status, created_at)
    WHERE status IN ('deleting', 'failed');
