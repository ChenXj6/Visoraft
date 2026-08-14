ALTER TABLE youtube_monitors
    ADD COLUMN series_scopes jsonb NOT NULL DEFAULT '[]'::jsonb;

UPDATE youtube_monitors
SET series_scopes = jsonb_build_array(
    jsonb_build_object(
        'key', 'default',
        'name', '',
        'query', query,
        'episode_start', episode_start,
        'episode_end', episode_end
    )
)
WHERE monitor_type = 'series'
  AND series_scopes = '[]'::jsonb;

ALTER TABLE youtube_monitor_items
    ADD COLUMN series_scope_key text NOT NULL DEFAULT '',
    ADD COLUMN series_scope_name text NOT NULL DEFAULT '';

CREATE INDEX youtube_monitor_items_series_scope_idx
    ON youtube_monitor_items(monitor_id, series_scope_key, episode_number)
    WHERE episode_number > 0;
