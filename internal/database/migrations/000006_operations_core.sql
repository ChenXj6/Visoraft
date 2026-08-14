CREATE TABLE application_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    version bigint NOT NULL DEFAULT 1,
    review_config jsonb NOT NULL,
    model_config jsonb NOT NULL,
    subtitle_config jsonb NOT NULL,
    prompt_config jsonb NOT NULL,
    youtube_config jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

INSERT INTO application_settings (
    singleton,
    version,
    review_config,
    model_config,
    subtitle_config,
    prompt_config,
    youtube_config,
    created_at,
    updated_at
) VALUES (
    true,
    1,
    '{
      "mode": "manual",
      "automatic_fallback": "manual",
      "rules": {
        "require_media": true,
        "require_title": true,
        "minimum_description_length": 0,
        "maximum_duration_seconds": 0,
        "require_subtitle_qc": false,
        "minimum_subtitle_qc_score": 80
      }
    }'::jsonb,
    '{
      "global": {
        "enabled": false,
        "provider": "openai_compatible",
        "base_url": "https://api.openai.com/v1",
        "model": "",
        "thinking": false,
        "temperature": 0.2,
        "timeout_seconds": 60
      },
      "subtitle_translation": {
        "mode": "inherit",
        "provider": "openai_compatible",
        "base_url": "",
        "model": "",
        "thinking": false,
        "temperature": 0.2,
        "timeout_seconds": 90
      },
      "subtitle_qc": {
        "mode": "inherit",
        "provider": "openai_compatible",
        "base_url": "",
        "model": "",
        "thinking": false,
        "temperature": 0,
        "timeout_seconds": 90
      },
      "smart_segmentation": {
        "mode": "inherit",
        "provider": "openai_compatible",
        "base_url": "",
        "model": "",
        "thinking": false,
        "temperature": 0.1,
        "timeout_seconds": 90
      }
    }'::jsonb,
    '{
      "enabled": false,
      "source_strategy": "youtube_then_asr",
      "source_language": "auto",
      "target_language": "zh",
      "download_auto_subtitles": true,
      "existing_chinese": {
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
      },
      "asr": {
        "enabled": false,
        "provider": "openai_compatible",
        "base_url": "https://api.openai.com/v1",
        "model": "whisper-1",
        "language": "",
        "prompt": "",
        "timeout_seconds": 600,
        "max_retries": 2,
        "vad_enabled": false,
        "chunk_seconds": 900,
        "chunk_overlap_seconds": 2
      },
      "postprocess": {
        "time_offset_seconds": 0,
        "minimum_cue_seconds": 0.7,
        "merge_gap_seconds": 0.15,
        "minimum_text_length": 1,
        "maximum_characters_per_line": 24,
        "maximum_lines": 2,
        "normalize_punctuation": true,
        "filter_filler_words": false
      },
      "segmentation": {
        "enabled": false,
        "minimum_cue_seconds": 1,
        "maximum_cue_seconds": 7,
        "maximum_cps": 18,
        "batch_window_seconds": 180,
        "maximum_batch_characters": 6000,
        "max_retries": 2
      },
      "translation": {
        "enabled": false,
        "batch_size": 20,
        "max_retries": 2,
        "retry_delay_seconds": 2
      },
      "qc": {
        "enabled": false,
        "threshold": 80,
        "sample_max_items": 80,
        "maximum_characters": 12000
      },
      "keep_original": true,
      "embed_in_video": false,
      "style": {
        "font_name": "Noto Sans CJK SC",
        "font_size": 42,
        "prefer_single_line": false,
        "maximum_lines": 2
      }
    }'::jsonb,
    '{
      "subtitle_translation": {
        "mode": "builtin",
        "text": ""
      },
      "subtitle_translation_strict": {
        "mode": "builtin",
        "text": ""
      },
      "subtitle_qc": {
        "mode": "builtin",
        "text": ""
      },
      "metadata_translation": {
        "mode": "builtin",
        "text": ""
      },
      "metadata_description_retry": {
        "mode": "builtin",
        "text": ""
      },
      "smart_segmentation": {
        "mode": "builtin",
        "text": ""
      }
    }'::jsonb,
    '{
      "provider": "google",
      "api_base_url": "https://www.googleapis.com/youtube/v3",
      "proxy_enabled": false,
      "proxy_url": "",
      "proxy_username": "",
      "request_timeout_seconds": 30,
      "fixture_media_url": "http://fixture-provider:8090/media/sample.wav"
    }'::jsonb,
    now(),
    now()
) ON CONFLICT (singleton) DO NOTHING;

CREATE TABLE setting_secrets (
    key text PRIMARY KEY,
    ciphertext bytea NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    archived_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT setting_secrets_key_check CHECK (
        key IN (
            'model.global.api_key',
            'model.subtitle_translation.api_key',
            'model.subtitle_qc.api_key',
            'model.smart_segmentation.api_key',
            'subtitle.asr.api_key',
            'youtube.api_key',
            'youtube.proxy_password'
        )
    )
);

CREATE TABLE settings_revisions (
    version bigint PRIMARY KEY,
    snapshot jsonb NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    created_at timestamptz NOT NULL
);

INSERT INTO settings_revisions (version, snapshot, actor_type, actor_id, created_at)
SELECT
    version,
    jsonb_build_object(
        'review', review_config,
        'models', model_config,
        'subtitle', subtitle_config,
        'prompts', prompt_config,
        'youtube', youtube_config
    ),
    'system',
    'migration',
    created_at
FROM application_settings
ON CONFLICT (version) DO NOTHING;

ALTER TABLE tasks
    ADD COLUMN review_mode text NOT NULL DEFAULT 'manual',
    ADD COLUMN review_status text NOT NULL DEFAULT 'not_started',
    ADD COLUMN review_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN settings_version bigint NOT NULL DEFAULT 1,
    ADD COLUMN settings_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN tags text[] NOT NULL DEFAULT '{}'::text[],
    ADD COLUMN category text NOT NULL DEFAULT '';

ALTER TABLE tasks
    ADD CONSTRAINT tasks_review_mode_check
        CHECK (review_mode IN ('manual', 'automatic')),
    ADD CONSTRAINT tasks_review_status_check
        CHECK (
            review_status IN (
                'not_started',
                'running',
                'pending',
                'approved',
                'rejected',
                'changes_requested',
                'abandoned'
            )
        );

CREATE INDEX tasks_review_queue_idx
    ON tasks(review_status, updated_at DESC);

CREATE TABLE task_secret_snapshots (
    task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    key text NOT NULL,
    ciphertext bytea NOT NULL,
    source_version bigint NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (task_id, key)
);

CREATE TABLE task_metadata_versions (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    version integer NOT NULL,
    title text NOT NULL,
    description text NOT NULL,
    tags text[] NOT NULL,
    category text NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    change_reason text NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE(task_id, version)
);

CREATE TABLE review_runs (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    mode text NOT NULL,
    policy_version bigint NOT NULL,
    status text NOT NULL,
    decision text NOT NULL DEFAULT '',
    rule_results jsonb NOT NULL DEFAULT '[]'::jsonb,
    summary text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    CONSTRAINT review_runs_mode_check CHECK (mode IN ('manual', 'automatic')),
    CONSTRAINT review_runs_status_check
        CHECK (status IN ('running', 'pending', 'completed')),
    CONSTRAINT review_runs_decision_check
        CHECK (decision IN ('', 'approved', 'rejected', 'manual_required', 'changes_requested'))
);

CREATE INDEX review_runs_task_idx
    ON review_runs(task_id, started_at DESC);

CREATE TABLE review_actions (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    review_run_id uuid REFERENCES review_runs(id) ON DELETE SET NULL,
    action text NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    reason text NOT NULL,
    metadata_version integer,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL,
    CONSTRAINT review_actions_action_check
        CHECK (
            action IN (
                'approve',
                'request_changes',
                'resubmit',
                'abandon',
                'subtitle_edit',
                'automatic_approve',
                'automatic_reject',
                'automatic_fallback'
            )
        )
);

CREATE INDEX review_actions_task_idx
    ON review_actions(task_id, created_at DESC);

CREATE TABLE subtitle_documents (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind text NOT NULL,
    language text NOT NULL,
    version integer NOT NULL,
    segments jsonb NOT NULL,
    qc_report jsonb NOT NULL DEFAULT '{}'::jsonb,
    source text NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE(task_id, kind, version),
    CONSTRAINT subtitle_documents_kind_check
        CHECK (kind IN ('original', 'translated')),
    CONSTRAINT subtitle_documents_source_check
        CHECK (source IN ('youtube_manual', 'youtube_auto', 'asr', 'fixture', 'edited'))
);

CREATE INDEX subtitle_documents_task_idx
    ON subtitle_documents(task_id, kind, version DESC);

CREATE TABLE youtube_monitors (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    enabled boolean NOT NULL,
    monitor_type text NOT NULL,
    channel_mode text NOT NULL DEFAULT 'latest',
    query text NOT NULL DEFAULT '',
    channel_ids text[] NOT NULL DEFAULT '{}'::text[],
    include_keywords text[] NOT NULL DEFAULT '{}'::text[],
    exclude_keywords text[] NOT NULL DEFAULT '{}'::text[],
    exclude_channel_ids text[] NOT NULL DEFAULT '{}'::text[],
    region_code text NOT NULL DEFAULT 'US',
    category_id text NOT NULL DEFAULT '',
    lookback_days integer NOT NULL DEFAULT 7,
    max_results integer NOT NULL DEFAULT 20,
    order_by text NOT NULL DEFAULT 'viewCount',
    video_types text[] NOT NULL DEFAULT '{video,short}'::text[],
    min_view_count bigint NOT NULL DEFAULT 0,
    min_like_count bigint NOT NULL DEFAULT 0,
    min_comment_count bigint NOT NULL DEFAULT 0,
    min_duration_seconds integer NOT NULL DEFAULT 0,
    max_duration_seconds integer NOT NULL DEFAULT 0,
    published_after date,
    published_before date,
    schedule_type text NOT NULL DEFAULT 'manual',
    schedule_interval_minutes integer NOT NULL DEFAULT 120,
    rate_limit_requests integer NOT NULL DEFAULT 20,
    auto_add_to_tasks boolean NOT NULL DEFAULT false,
    task_template jsonb NOT NULL,
    state text NOT NULL DEFAULT 'idle',
    last_run_at timestamptz,
    next_run_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    CONSTRAINT youtube_monitors_type_check
        CHECK (monitor_type IN ('search', 'channel')),
    CONSTRAINT youtube_monitors_channel_mode_check
        CHECK (channel_mode IN ('search', 'historical', 'latest')),
    CONSTRAINT youtube_monitors_schedule_check
        CHECK (schedule_type IN ('manual', 'automatic')),
    CONSTRAINT youtube_monitors_state_check
        CHECK (state IN ('idle', 'running', 'paused', 'error')),
    CONSTRAINT youtube_monitors_lookback_check
        CHECK (lookback_days BETWEEN 1 AND 30),
    CONSTRAINT youtube_monitors_results_check
        CHECK (max_results BETWEEN 1 AND 50),
    CONSTRAINT youtube_monitors_interval_check
        CHECK (schedule_interval_minutes BETWEEN 1 AND 43200),
    CONSTRAINT youtube_monitors_rate_limit_check
        CHECK (rate_limit_requests BETWEEN 1 AND 100)
);

CREATE INDEX youtube_monitors_due_idx
    ON youtube_monitors(enabled, schedule_type, next_run_at)
    WHERE enabled AND schedule_type = 'automatic';

CREATE TABLE youtube_monitor_runs (
    id uuid PRIMARY KEY,
    monitor_id uuid NOT NULL REFERENCES youtube_monitors(id) ON DELETE CASCADE,
    trigger text NOT NULL,
    status text NOT NULL,
    config_snapshot jsonb NOT NULL,
    discovered_count integer NOT NULL DEFAULT 0,
    accepted_count integer NOT NULL DEFAULT 0,
    duplicate_count integer NOT NULL DEFAULT 0,
    task_count integer NOT NULL DEFAULT 0,
    quota_units integer NOT NULL DEFAULT 0,
    error_code text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    lease_owner text NOT NULL DEFAULT '',
    lease_expires_at timestamptz,
    CONSTRAINT youtube_monitor_runs_trigger_check
        CHECK (trigger IN ('manual', 'scheduled')),
    CONSTRAINT youtube_monitor_runs_status_check
        CHECK (status IN ('queued', 'running', 'completed', 'failed'))
);

CREATE INDEX youtube_monitor_runs_monitor_idx
    ON youtube_monitor_runs(monitor_id, started_at DESC);

CREATE TABLE youtube_monitor_items (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES youtube_monitor_runs(id) ON DELETE CASCADE,
    monitor_id uuid NOT NULL REFERENCES youtube_monitors(id) ON DELETE CASCADE,
    external_video_id text NOT NULL,
    source_url text NOT NULL,
    title text NOT NULL,
    channel_id text NOT NULL,
    channel_title text NOT NULL,
    published_at timestamptz,
    duration_seconds integer NOT NULL DEFAULT 0,
    view_count bigint NOT NULL DEFAULT 0,
    like_count bigint NOT NULL DEFAULT 0,
    comment_count bigint NOT NULL DEFAULT 0,
    video_type text NOT NULL,
    decision text NOT NULL,
    decision_reason text NOT NULL DEFAULT '',
    task_id uuid REFERENCES tasks(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT youtube_monitor_items_video_type_check
        CHECK (video_type IN ('video', 'short', 'live')),
    CONSTRAINT youtube_monitor_items_decision_check
        CHECK (decision IN ('accepted', 'filtered', 'duplicate', 'task_created', 'task_failed'))
);

CREATE INDEX youtube_monitor_items_run_idx
    ON youtube_monitor_items(run_id, created_at);

CREATE TABLE youtube_monitor_seen (
    monitor_id uuid NOT NULL REFERENCES youtube_monitors(id) ON DELETE CASCADE,
    external_video_id text NOT NULL,
    first_run_id uuid NOT NULL REFERENCES youtube_monitor_runs(id) ON DELETE CASCADE,
    first_seen_at timestamptz NOT NULL,
    PRIMARY KEY (monitor_id, external_video_id)
);

CREATE TABLE youtube_video_ingestions (
    external_video_id text PRIMARY KEY,
    task_id uuid REFERENCES tasks(id) ON DELETE SET NULL,
    monitor_id uuid NOT NULL REFERENCES youtube_monitors(id) ON DELETE CASCADE,
    state text NOT NULL DEFAULT 'reserved',
    first_seen_at timestamptz NOT NULL,
    CONSTRAINT youtube_video_ingestions_state_check
        CHECK (state IN ('reserved', 'created'))
);
