UPDATE application_settings
SET subtitle_config = jsonb_set(
    subtitle_config,
    '{existing_chinese}',
    '{
      "version": 1,
      "enabled": true,
      "inspect_platform_subtitles": true,
      "inspect_embedded_subtitles": true,
      "inspect_hardcoded_subtitles": true,
      "hardcoded_action": "skip_translation",
      "uncertain_action": "continue_pipeline",
      "sample_count": 32,
      "confidence_threshold_percent": 85,
      "coverage_threshold_percent": 60,
      "minimum_distinct_texts": 3
    }'::jsonb,
    true
)
WHERE singleton = true
  AND NOT (subtitle_config ? 'existing_chinese');

ALTER TABLE subtitle_documents
    DROP CONSTRAINT subtitle_documents_source_check;

ALTER TABLE subtitle_documents
    ADD CONSTRAINT subtitle_documents_source_check
    CHECK (
        source IN (
            'youtube_manual',
            'youtube_auto',
            'embedded',
            'asr',
            'fixture',
            'model',
            'edited'
        )
    );
