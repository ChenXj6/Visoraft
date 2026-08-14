ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS media_info jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE media_assets
    ADD CONSTRAINT media_assets_media_info_object_check
    CHECK (jsonb_typeof(media_info) = 'object');
