ALTER TABLE application_settings
    ADD COLUMN automation_config jsonb NOT NULL DEFAULT '{
      "enabled": false,
      "translate_title": true,
      "translate_description": true,
      "generate_tags": true,
      "recommend_categories": true,
      "process_cover": true
    }'::jsonb,
    ADD COLUMN transcode_config jsonb NOT NULL DEFAULT '{
      "enabled": true,
      "encoder_mode": "auto",
      "video_codec": "h264",
      "audio_codec": "aac",
      "container": "mp4",
      "cpu_preset": "medium",
      "high_resolution_cpu_preset": "veryfast",
      "maximum_height": 0,
      "video_bitrate_kbps": 0,
      "audio_bitrate_kbps": 192,
      "burn_subtitles": false,
      "custom_arguments_enabled": false,
      "custom_arguments": []
    }'::jsonb,
    ADD COLUMN moderation_config jsonb NOT NULL DEFAULT '{
      "enabled": false,
      "provider": "aliyun",
      "region": "cn-shanghai",
      "check_text": true,
      "check_image": true,
      "check_video": true,
      "text_service": "pgc_detection",
      "image_service": "baselineCheck",
      "video_service": "videoDetection",
      "high_risk_action": "block",
      "medium_risk_action": "manual_review",
      "failure_action": "manual_review",
      "request_timeout_seconds": 30,
      "video_poll_seconds": 5,
      "video_maximum_wait_seconds": 900
    }'::jsonb,
    ADD COLUMN publishing_config jsonb NOT NULL DEFAULT '{
      "auto_publish_after_review": false,
      "maximum_concurrent_uploads": 1,
      "maximum_attempts": 3,
      "retry_delay_seconds": 30,
      "reconcile_uncertain_results": true
    }'::jsonb;

ALTER TABLE setting_secrets
    DROP CONSTRAINT setting_secrets_key_check;

ALTER TABLE setting_secrets
    ADD CONSTRAINT setting_secrets_key_check CHECK (
        key IN (
            'model.global.api_key',
            'model.subtitle_translation.api_key',
            'model.subtitle_qc.api_key',
            'model.smart_segmentation.api_key',
            'subtitle.asr.api_key',
            'youtube.api_key',
            'youtube.proxy_password',
            'aliyun.access_key_id',
            'aliyun.access_key_secret'
        )
    );

CREATE TABLE platform_accounts (
    id uuid PRIMARY KEY,
    platform text NOT NULL,
    name text NOT NULL,
    auth_mode text NOT NULL DEFAULT 'cookie',
    cookie_profile_id uuid REFERENCES cookie_profiles(id) ON DELETE RESTRICT,
    status text NOT NULL DEFAULT 'unchecked',
    remote_user_id text NOT NULL DEFAULT '',
    remote_display_name text NOT NULL DEFAULT '',
    adapter_version text NOT NULL DEFAULT '',
    last_checked_at timestamptz,
    last_error_code text NOT NULL DEFAULT '',
    last_error_message text NOT NULL DEFAULT '',
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    CONSTRAINT platform_accounts_platform_check
        CHECK (platform IN ('acfun', 'bilibili')),
    CONSTRAINT platform_accounts_auth_mode_check
        CHECK (auth_mode IN ('cookie', 'fixture')),
    CONSTRAINT platform_accounts_status_check
        CHECK (status IN ('unchecked', 'checking', 'ready', 'expired', 'error', 'archived')),
    CONSTRAINT platform_accounts_cookie_check
        CHECK (
            (auth_mode = 'fixture' AND cookie_profile_id IS NULL)
            OR (auth_mode = 'cookie' AND cookie_profile_id IS NOT NULL)
        )
);

CREATE UNIQUE INDEX platform_accounts_name_active_idx
    ON platform_accounts(platform, lower(name))
    WHERE archived_at IS NULL;

CREATE INDEX platform_accounts_status_idx
    ON platform_accounts(platform, status, updated_at DESC)
    WHERE archived_at IS NULL;

CREATE TABLE platform_login_sessions (
    id uuid PRIMARY KEY,
    platform text NOT NULL,
    account_name text NOT NULL,
    status text NOT NULL,
    qr_url text NOT NULL DEFAULT '',
    encrypted_session bytea NOT NULL DEFAULT ''::bytea,
    error_code text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    completed_account_id uuid REFERENCES platform_accounts(id) ON DELETE SET NULL,
    CONSTRAINT platform_login_sessions_platform_check
        CHECK (platform IN ('acfun', 'bilibili')),
    CONSTRAINT platform_login_sessions_status_check
        CHECK (status IN ('pending', 'scanned', 'confirmed', 'expired', 'failed', 'cancelled'))
);

CREATE INDEX platform_login_sessions_active_idx
    ON platform_login_sessions(status, expires_at);

CREATE TABLE platform_categories (
    platform text NOT NULL,
    category_id text NOT NULL,
    parent_id text NOT NULL DEFAULT '',
    name text NOT NULL,
    path text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    sort_order integer NOT NULL DEFAULT 0,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    refreshed_at timestamptz NOT NULL,
    PRIMARY KEY (platform, category_id),
    CONSTRAINT platform_categories_platform_check
        CHECK (platform IN ('acfun', 'bilibili'))
);

CREATE INDEX platform_categories_tree_idx
    ON platform_categories(platform, parent_id, sort_order, name);

CREATE TABLE transcode_presets (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    encoder_mode text NOT NULL,
    video_codec text NOT NULL,
    audio_codec text NOT NULL,
    container text NOT NULL,
    cpu_preset text NOT NULL,
    high_resolution_cpu_preset text NOT NULL,
    maximum_height integer NOT NULL DEFAULT 0,
    video_bitrate_kbps integer NOT NULL DEFAULT 0,
    audio_bitrate_kbps integer NOT NULL DEFAULT 192,
    burn_subtitles boolean NOT NULL DEFAULT false,
    custom_arguments text[] NOT NULL DEFAULT '{}'::text[],
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    CONSTRAINT transcode_presets_encoder_mode_check
        CHECK (encoder_mode IN ('auto', 'cpu', 'nvidia', 'intel', 'amd')),
    CONSTRAINT transcode_presets_codec_check
        CHECK (video_codec IN ('h264', 'hevc', 'copy') AND audio_codec IN ('aac', 'copy')),
    CONSTRAINT transcode_presets_container_check
        CHECK (container IN ('mp4', 'mkv')),
    CONSTRAINT transcode_presets_limits_check
        CHECK (
            maximum_height >= 0
            AND video_bitrate_kbps >= 0
            AND audio_bitrate_kbps >= 0
        )
);

CREATE UNIQUE INDEX transcode_presets_name_active_idx
    ON transcode_presets(lower(name))
    WHERE archived_at IS NULL;

CREATE TABLE posting_strategies (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    automation_mode text NOT NULL DEFAULT 'manual_after_review',
    target_platforms text[] NOT NULL,
    account_bindings jsonb NOT NULL DEFAULT '{}'::jsonb,
    category_bindings jsonb NOT NULL DEFAULT '{}'::jsonb,
    title_templates jsonb NOT NULL DEFAULT '{}'::jsonb,
    description_templates jsonb NOT NULL DEFAULT '{}'::jsonb,
    default_tags text[] NOT NULL DEFAULT '{}'::text[],
    repost_statement_version text NOT NULL DEFAULT 'full_v1',
    transcode_preset_id uuid REFERENCES transcode_presets(id) ON DELETE RESTRICT,
    require_content_moderation boolean NOT NULL DEFAULT false,
    schedule_mode text NOT NULL DEFAULT 'immediate',
    schedule_time time,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    CONSTRAINT posting_strategies_automation_mode_check
        CHECK (automation_mode IN ('manual_after_review', 'automatic_after_review')),
    CONSTRAINT posting_strategies_targets_check
        CHECK (
            cardinality(target_platforms) > 0
            AND target_platforms <@ ARRAY['acfun', 'bilibili']::text[]
        ),
    CONSTRAINT posting_strategies_statement_check
        CHECK (repost_statement_version IN ('brief_v1', 'full_v1')),
    CONSTRAINT posting_strategies_schedule_check
        CHECK (schedule_mode IN ('immediate', 'daily_time'))
);

CREATE UNIQUE INDEX posting_strategies_name_active_idx
    ON posting_strategies(lower(name))
    WHERE archived_at IS NULL;

ALTER TABLE tasks
    ADD COLUMN posting_strategy_id uuid REFERENCES posting_strategies(id) ON DELETE SET NULL,
    ADD COLUMN auto_publish boolean NOT NULL DEFAULT false;

CREATE TABLE transcode_runs (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    preset_id uuid REFERENCES transcode_presets(id) ON DELETE SET NULL,
    status text NOT NULL,
    attempt integer NOT NULL DEFAULT 1,
    input_asset_id uuid REFERENCES media_assets(id) ON DELETE SET NULL,
    output_asset_id uuid REFERENCES media_assets(id) ON DELETE SET NULL,
    resolved_encoder text NOT NULL DEFAULT '',
    command_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    progress smallint NOT NULL DEFAULT 0,
    error_code text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT transcode_runs_status_check
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled', 'skipped')),
    CONSTRAINT transcode_runs_progress_check
        CHECK (progress >= 0 AND progress <= 100)
);

CREATE INDEX transcode_runs_task_idx
    ON transcode_runs(task_id, created_at DESC);

CREATE TABLE moderation_runs (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    provider text NOT NULL,
    status text NOT NULL,
    attempt integer NOT NULL DEFAULT 1,
    policy_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    text_result jsonb NOT NULL DEFAULT '{}'::jsonb,
    image_result jsonb NOT NULL DEFAULT '{}'::jsonb,
    video_result jsonb NOT NULL DEFAULT '{}'::jsonb,
    decision text NOT NULL DEFAULT '',
    error_code text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT moderation_runs_provider_check
        CHECK (provider IN ('aliyun', 'fixture', 'disabled')),
    CONSTRAINT moderation_runs_status_check
        CHECK (status IN ('queued', 'running', 'passed', 'rejected', 'failed', 'skipped')),
    CONSTRAINT moderation_runs_decision_check
        CHECK (decision IN ('', 'pass', 'manual_review', 'block'))
);

CREATE INDEX moderation_runs_task_idx
    ON moderation_runs(task_id, created_at DESC);

CREATE TABLE publish_jobs (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    strategy_id uuid REFERENCES posting_strategies(id) ON DELETE SET NULL,
    status text NOT NULL,
    auto_started boolean NOT NULL DEFAULT false,
    metadata_version bigint NOT NULL,
    fingerprint text NOT NULL,
    blockers jsonb NOT NULL DEFAULT '[]'::jsonb,
    cover_asset_id uuid REFERENCES media_assets(id) ON DELETE SET NULL,
    media_asset_id uuid REFERENCES media_assets(id) ON DELETE SET NULL,
    scheduled_at timestamptz,
    queued_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    CONSTRAINT publish_jobs_status_check
        CHECK (
            status IN (
                'draft',
                'blocked',
                'queued',
                'publishing',
                'partial_success',
                'published',
                'reconciliation_required',
                'failed',
                'cancelled'
            )
        )
);

CREATE UNIQUE INDEX publish_jobs_fingerprint_idx ON publish_jobs(fingerprint);
CREATE INDEX publish_jobs_task_idx ON publish_jobs(task_id, created_at DESC);
CREATE INDEX publish_jobs_queue_idx
    ON publish_jobs(status, scheduled_at, created_at)
    WHERE status IN ('queued', 'publishing', 'reconciliation_required');

CREATE TABLE platform_publications (
    id uuid PRIMARY KEY,
    publish_job_id uuid NOT NULL REFERENCES publish_jobs(id) ON DELETE CASCADE,
    task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    platform text NOT NULL,
    account_id uuid NOT NULL REFERENCES platform_accounts(id) ON DELETE RESTRICT,
    status text NOT NULL,
    category_id text NOT NULL,
    title text NOT NULL,
    description text NOT NULL,
    tags text[] NOT NULL DEFAULT '{}'::text[],
    cover_asset_id uuid REFERENCES media_assets(id) ON DELETE SET NULL,
    media_asset_id uuid NOT NULL REFERENCES media_assets(id) ON DELETE RESTRICT,
    scheduled_at timestamptz,
    fingerprint text NOT NULL,
    attempt integer NOT NULL DEFAULT 0,
    remote_submission_id text NOT NULL DEFAULT '',
    remote_url text NOT NULL DEFAULT '',
    remote_status text NOT NULL DEFAULT '',
    adapter_version text NOT NULL DEFAULT '',
    response_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    error_code text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    error_retryable boolean NOT NULL DEFAULT false,
    uncertain_since timestamptz,
    locked_at timestamptz,
    locked_by text NOT NULL DEFAULT '',
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    CONSTRAINT platform_publications_platform_check
        CHECK (platform IN ('acfun', 'bilibili')),
    CONSTRAINT platform_publications_status_check
        CHECK (
            status IN (
                'draft',
                'blocked',
                'queued',
                'preparing',
                'uploading',
                'submitting',
                'published',
                'failed',
                'reconciliation_required',
                'cancelled'
            )
        ),
    UNIQUE (publish_job_id, platform)
);

CREATE UNIQUE INDEX platform_publications_fingerprint_idx
    ON platform_publications(platform, fingerprint);
CREATE INDEX platform_publications_queue_idx
    ON platform_publications(status, scheduled_at, updated_at)
    WHERE status IN ('queued', 'preparing', 'uploading', 'submitting', 'reconciliation_required');

CREATE TABLE publication_attempts (
    id uuid PRIMARY KEY,
    publication_id uuid NOT NULL REFERENCES platform_publications(id) ON DELETE CASCADE,
    attempt integer NOT NULL,
    stage text NOT NULL,
    status text NOT NULL,
    request_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    response_summary jsonb NOT NULL DEFAULT '{}'::jsonb,
    error_code text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    UNIQUE (publication_id, attempt, stage),
    CONSTRAINT publication_attempts_status_check
        CHECK (status IN ('running', 'succeeded', 'failed', 'uncertain', 'cancelled'))
);

CREATE INDEX publication_attempts_publication_idx
    ON publication_attempts(publication_id, attempt DESC);
