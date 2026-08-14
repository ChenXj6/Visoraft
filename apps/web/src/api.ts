export type Platform = "acfun" | "bilibili";
export type StatementVersion = "brief_v1" | "full_v1";

export type TaskStep = {
  kind: string;
  status: string;
  attempt: number;
  progress: number;
  detail: {
    phase?: string;
    downloaded_bytes?: number;
    total_bytes?: number;
    total_bytes_is_estimate?: boolean;
    speed_bytes_per_second?: number;
    eta_seconds?: number;
    fragment_index?: number;
    fragment_count?: number;
    remote_task_id?: string;
    remote_status?: string;
    batch_index?: number;
    batch_count?: number;
    completed_batches?: number;
    batch_segment_count?: number;
    batch_split?: boolean;
    model_attempt?: number;
    model_attempts?: number;
    repairing_missing?: boolean;
    checkpoint_reused?: boolean;
    restored_items?: number;
    sample_count?: number;
    total_count?: number;
    decision?: {
      schema_version: number;
      disposition:
        | "generated_subtitles"
        | "existing_soft_chinese"
        | "existing_hardcoded_chinese";
      translation_skipped: boolean;
      burn_subtitles: boolean;
      detection: {
        schema_version: number;
        state: "disabled" | "found" | "not_found" | "uncertain" | "error";
        source: string;
        language: string;
        disposition: string;
        reason: string;
        confidence_percent: number;
        sample_count: number;
        hit_count: number;
        stable_pair_count: number;
        distinct_text_count: number;
        evidence: string[];
      };
    };
  };
  activity_state?: "active" | "delayed" | "stalled" | "telemetry_pending";
  heartbeat_age_seconds?: number;
  error_code?: string;
  error_message?: string;
  started_at?: string;
  finished_at?: string;
  updated_at: string;
};

export type MediaInfo = {
  schema_version?: number;
  format_name?: string;
  duration_seconds?: number;
  size_bytes?: number;
  bit_rate?: number;
  video_codec?: string;
  width?: number;
  height?: number;
  pixel_format?: string;
  frame_rate?: string;
  audio_codec?: string;
  sample_rate?: number;
  channels?: number;
  channel_layout?: string;
  stream_count?: number;
};

export type MediaAsset = {
  id: string;
  kind: string;
  bucket: string;
  object_key: string;
  original_name: string;
  content_type: string;
  size_bytes: number;
  checksum_sha256: string;
  media_info: MediaInfo;
  status: string;
  error_code?: string;
  error_message?: string;
  created_at: string;
  deleted_at?: string;
};

export type FileFolder = {
  task_id: string;
  title: string;
  status: string;
  archived: boolean;
  updated_at: string;
  file_count: number;
  available_count: number;
  deleted_count: number;
  total_bytes: number;
  files: MediaAsset[];
};

export type FileLibrary = {
  folder_count: number;
  file_count: number;
  available_count: number;
  deleted_count: number;
  total_bytes: number;
  folders: FileFolder[];
};

export type Task = {
  id: string;
  status: string;
  target_platforms: Platform[];
  source_url: string;
  cookie_profile_id?: string;
  posting_strategy_id?: string;
  auto_publish: boolean;
  publish_job_id?: string;
  publish_status: string;
  publish_mode: "" | "simulation" | "remote" | "mixed";
  publish_blockers: PublishingBlocker[];
  repost_statement_version: StatementVersion;
  repost_statement_brief: string;
  repost_statement_full: string;
  original_title: string;
  title: string;
  description: string;
  thumbnail_url: string;
  duration_seconds?: number;
  extractor: string;
  review_mode: "manual" | "automatic";
  review_status:
    | "not_started"
    | "running"
    | "pending"
    | "approved"
    | "rejected"
    | "changes_requested"
    | "abandoned";
  review_summary: Record<string, unknown>;
  settings_version: number;
  tags: string[];
  category: string;
  error_code?: string;
  error_message?: string;
  error_retryable: boolean;
  version: number;
  created_at: string;
  updated_at: string;
  archived_at?: string;
  archived_by?: string;
  steps: TaskStep[];
  assets: MediaAsset[];
};

export type CookieProfile = {
  id: string;
  name: string;
  kind: "upload" | "cookiecloud";
  status: "ready" | "syncing" | "error";
  server_url?: string;
  source_filename?: string;
  cookie_count: number;
  domain_count: number;
  has_usable_cookies: boolean;
  last_synced_at?: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
};

export type DashboardSummary = {
  total: number;
  active: number;
  awaiting_manual_review: number;
  published: number;
  failed: number;
};

export type SystemStatus = {
  service: string;
  version: string;
  started_at: string;
  database: string;
  pending_outbox: number;
  last_worker_event?: string;
  message_push_state: "deferred";
};

export type CreateTaskInput = {
  source_url: string;
  target_platforms: Platform[];
  cookie_profile_id?: string;
  repost_statement_version: StatementVersion;
  posting_strategy_id?: string;
  auto_publish: boolean;
};

export type CreateCookieCloudInput = {
  name: string;
  server_url: string;
  uuid: string;
  password: string;
};

export type BulkRetryResult = {
  succeeded: Task[];
  failed: { task_id: string; code: string; message: string }[];
};

export type TaskArchivePreview = {
  total_tasks: number;
  archivable_tasks: number;
  running_tasks: number;
  asset_count: number;
  asset_bytes: number;
  published_tasks: number;
};

export type ArchiveAllResult = {
  archived: Task[];
  failed: { task_id: string; code: string; message: string }[];
};

export type PurgeTaskResult = {
  task_id: string;
  purged_at: string;
};

export type ReviewRules = {
  require_media: boolean;
  require_title: boolean;
  minimum_description_length: number;
  maximum_duration_seconds: number;
  require_subtitle_qc: boolean;
  minimum_subtitle_qc_score: number;
};

export type ModelEndpoint = {
  enabled?: boolean;
  mode?: "inherit" | "override" | "disabled";
  provider: "openai_compatible" | "fixture";
  base_url: string;
  model: string;
  thinking: boolean;
  temperature: number;
  timeout_seconds: number;
};

export type PromptEntry = {
  mode: "builtin" | "append" | "replace";
  text: string;
};

export type AutomationConfig = {
  enabled: boolean;
  translate_title: boolean;
  translate_description: boolean;
  generate_tags: boolean;
  recommend_categories: boolean;
  process_cover: boolean;
};

export type TranscodeConfig = {
  enabled: boolean;
  encoder_mode: string;
  video_codec: string;
  audio_codec: string;
  container: string;
  cpu_preset: string;
  high_resolution_cpu_preset: string;
  maximum_height: number;
  video_bitrate_kbps: number;
  audio_bitrate_kbps: number;
  burn_subtitles: boolean;
  custom_arguments_enabled: boolean;
  custom_arguments: string[];
};

export type ModerationConfig = {
  enabled: boolean;
  provider: "fixture" | "aliyun";
  region: string;
  check_text: boolean;
  check_image: boolean;
  check_video: boolean;
  text_service: string;
  image_service: string;
  video_service: string;
  high_risk_action: "block" | "manual_review";
  medium_risk_action: "block" | "manual_review";
  failure_action: "block" | "manual_review";
  request_timeout_seconds: number;
  video_poll_seconds: number;
  video_maximum_wait_seconds: number;
};

export type PublishingConfig = {
  auto_publish_after_review: boolean;
  maximum_concurrent_uploads: number;
  maximum_attempts: number;
  retry_delay_seconds: number;
  reconcile_uncertain_results: boolean;
};

export type ApplicationSettings = {
  version: number;
  review: {
    mode: "manual" | "automatic";
    automatic_fallback: "manual" | "reject";
    rules: ReviewRules;
  };
  models: {
    global: ModelEndpoint;
    subtitle_translation: ModelEndpoint;
    subtitle_qc: ModelEndpoint;
    smart_segmentation: ModelEndpoint;
  };
  subtitle: {
    enabled: boolean;
    source_strategy:
      | "youtube_then_asr"
      | "youtube_only"
      | "asr_only"
      | "youtube_manual_then_asr";
    source_language: string;
    target_language: string;
    download_auto_subtitles: boolean;
    existing_chinese: {
      version: 1;
      enabled: boolean;
      inspect_platform_subtitles: boolean;
      inspect_embedded_subtitles: boolean;
      inspect_hardcoded_subtitles: boolean;
      hardcoded_action: "skip_translation";
      uncertain_action: "continue_pipeline";
      sample_count: number;
      confidence_threshold_percent: number;
      coverage_threshold_percent: number;
      minimum_distinct_texts: number;
    };
    asr: {
      enabled: boolean;
      provider: "openai_compatible" | "voxtral" | "aliyun_paraformer" | "fixture";
      base_url: string;
      model: string;
      language: string;
      prompt: string;
      timeout_seconds: number;
      max_retries: number;
      vad_enabled: boolean;
      chunk_seconds: number;
      chunk_overlap_seconds: number;
    };
    postprocess: {
      time_offset_seconds: number;
      minimum_cue_seconds: number;
      merge_gap_seconds: number;
      minimum_text_length: number;
      maximum_characters_per_line: number;
      maximum_lines: number;
      normalize_punctuation: boolean;
      filter_filler_words: boolean;
    };
    segmentation: {
      enabled: boolean;
      minimum_cue_seconds: number;
      maximum_cue_seconds: number;
      maximum_cps: number;
      batch_window_seconds: number;
      maximum_batch_characters: number;
      max_retries: number;
    };
    translation: {
      enabled: boolean;
      batch_size: number;
      max_retries: number;
      retry_delay_seconds: number;
    };
    qc: {
      enabled: boolean;
      threshold: number;
      sample_max_items: number;
      maximum_characters: number;
    };
    keep_original: boolean;
    embed_in_video: boolean;
    style: {
      font_name: string;
      font_size: number;
      prefer_single_line: boolean;
      maximum_lines: number;
    };
  };
  prompts: {
    subtitle_translation: PromptEntry;
    subtitle_translation_strict: PromptEntry;
    subtitle_qc: PromptEntry;
    metadata_translation: PromptEntry;
    metadata_description_retry: PromptEntry;
    smart_segmentation: PromptEntry;
  };
  youtube: {
    provider: "google" | "fixture";
    api_base_url: string;
    proxy_enabled: boolean;
    proxy_url: string;
    proxy_username: string;
    request_timeout_seconds: number;
    fixture_media_url: string;
  };
  automation: AutomationConfig;
  transcode: TranscodeConfig;
  moderation: ModerationConfig;
  publishing: PublishingConfig;
  secret_configured: Record<string, boolean>;
  updated_at: string;
};

export type UpdateSettingsInput = Omit<
  ApplicationSettings,
  "version" | "secret_configured" | "updated_at"
> & {
  expected_version: number;
  secrets?: Record<string, string>;
  clear_secrets?: string[];
};

export type ConnectionTestResult = {
  target: string;
  ok: boolean;
  message: string;
  latency_ms: number;
  checked_at: string;
  provider: string;
  model?: string;
};

export type ReviewRuleResult = {
  key: string;
  label: string;
  passed: boolean;
  expected?: unknown;
  actual?: unknown;
  message: string;
};

export type ReviewRun = {
  id: string;
  task_id: string;
  mode: "manual" | "automatic";
  policy_version: number;
  status: string;
  decision: string;
  rule_results: ReviewRuleResult[];
  summary: string;
  started_at: string;
  completed_at?: string;
};

export type ModerationFinding = {
  label: string;
  description?: string;
  risk_level?: string;
  confidence?: number;
  location?: string;
};

export type ModerationChannelResult = {
  status: string;
  service: string;
  risk_level: string;
  request_ids: string[];
  findings: ModerationFinding[];
};

export type ModerationRun = {
  id: string;
  provider: string;
  status: string;
  attempt: number;
  policy_snapshot: Record<string, unknown>;
  text_result: ModerationChannelResult;
  image_result: ModerationChannelResult;
  video_result: ModerationChannelResult;
  risk_level: string;
  decision: string;
  error_code: string;
  error_message: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
};

export type SubtitleSegment = {
  index: number;
  start: number;
  end: number;
  text: string;
};

export type SubtitleDocument = {
  id: string;
  kind: "original" | "translated";
  language: string;
  version: number;
  segments: SubtitleSegment[];
  qc_report: Record<string, unknown>;
  source: string;
  created_at: string;
};

export type ReviewDetail = {
  task: Task;
  runs: ReviewRun[];
  actions: {
    id: string;
    action: string;
    reason: string;
    actor_type: string;
    actor_id: string;
    metadata_version?: number;
    payload: Record<string, unknown>;
    created_at: string;
  }[];
  subtitles: SubtitleDocument[];
  moderation_runs: ModerationRun[];
};

export type MonitorTaskTemplate = {
  target_platforms: Platform[];
  cookie_profile_id?: string;
  repost_statement_version: StatementVersion;
  posting_strategy_id?: string;
  auto_publish: boolean;
};

export type MonitorSeriesScope = {
  key: string;
  name: string;
  query: string;
  episode_start: number;
  episode_end: number;
};

export type YouTubeMonitorInput = {
  name: string;
  enabled: boolean;
  monitor_type: "search" | "channel" | "series";
  channel_mode: "search" | "historical" | "latest";
  query: string;
  series_title: string;
  series_scopes: MonitorSeriesScope[];
  episode_start: number;
  episode_end: number;
  channel_ids: string[];
  include_keywords: string[];
  exclude_keywords: string[];
  exclude_channel_ids: string[];
  region_code: string;
  category_id: string;
  lookback_days: number;
  max_results: number;
  order_by: "viewCount" | "date" | "rating" | "relevance";
  video_types: ("video" | "short" | "live")[];
  min_view_count: number;
  min_like_count: number;
  min_comment_count: number;
  min_duration_seconds: number;
  max_duration_seconds: number;
  published_after?: string;
  published_before?: string;
  schedule_type: "manual" | "automatic";
  schedule_interval_minutes: number;
  rate_limit_requests: number;
  auto_add_to_tasks: boolean;
  task_template: MonitorTaskTemplate;
};

export type YouTubeMonitor = YouTubeMonitorInput & {
  id: string;
  state: "idle" | "running" | "paused" | "error";
  last_run_at?: string;
  next_run_at?: string;
  last_error: string;
  version: number;
  created_at: string;
  updated_at: string;
};

export type YouTubeMonitorRun = {
  id: string;
  monitor_id: string;
  trigger: "manual" | "scheduled";
  status: "queued" | "running" | "completed" | "failed";
  config_snapshot: Record<string, unknown>;
  discovered_count: number;
  accepted_count: number;
  duplicate_count: number;
  task_count: number;
  quota_units: number;
  error_code: string;
  error_message: string;
  started_at: string;
  completed_at?: string;
};

export type YouTubeMonitorHistory = {
  monitor: YouTubeMonitor;
  runs: YouTubeMonitorRun[];
  items: {
    id: string;
    run_id: string;
    external_video_id: string;
    episode_number: number;
    series_scope_key: string;
    series_scope_name: string;
    source_url: string;
    title: string;
    channel_title: string;
    duration_seconds: number;
    view_count: number;
    like_count: number;
    comment_count: number;
    video_type: string;
    decision: string;
    decision_reason: string;
    task_id?: string;
    created_at: string;
  }[];
};

export type MonitorEnqueueResult = {
  requested_count: number;
  created_count: number;
  duplicate_count: number;
  failed_count: number;
  items: {
    item_id: string;
    status: "created" | "duplicate" | "failed";
    task_id?: string;
    message: string;
  }[];
};

export type YouTubeCategory = {
  id: string;
  title: string;
  provider: "google" | "fixture";
};

export type PlatformAccount = {
  id: string;
  platform: Platform;
  name: string;
  auth_mode: string;
  cookie_profile_id?: string;
  status: string;
  remote_user_id: string;
  remote_display_name: string;
  adapter_version: string;
  last_checked_at?: string;
  last_error_code: string;
  last_error_message: string;
  version: number;
  created_at: string;
  updated_at: string;
};

export type CreatePlatformAccountInput = {
  platform: Platform;
  name: string;
  auth_mode: string;
  cookie_profile_id?: string;
};

export type PlatformAccountCheckResult = PlatformAccount & {
  ok: boolean;
  message: string;
};

export type PlatformCategory = {
  platform: Platform;
  category_id: string;
  parent_id: string;
  name: string;
  path: string;
  active: boolean;
  sort_order: number;
  metadata: Record<string, unknown>;
  refreshed_at: string;
};

export type TranscodePresetInput = {
  name: string;
  enabled: boolean;
  encoder_mode: string;
  video_codec: string;
  audio_codec: string;
  container: string;
  cpu_preset: string;
  high_resolution_cpu_preset: string;
  maximum_height: number;
  video_bitrate_kbps: number;
  audio_bitrate_kbps: number;
  burn_subtitles: boolean;
  custom_arguments: string[];
};

export type TranscodePreset = TranscodePresetInput & {
  id: string;
  version: number;
  created_at: string;
  updated_at: string;
};

export type PostingStrategyInput = {
  name: string;
  enabled: boolean;
  automation_mode: "manual_after_review" | "automatic_after_review";
  target_platforms: Platform[];
  account_bindings: Partial<Record<Platform, string>>;
  category_bindings: Partial<Record<Platform, string>>;
  title_templates: Partial<Record<Platform, string>>;
  description_templates: Partial<Record<Platform, string>>;
  default_tags: string[];
  repost_statement_version: StatementVersion;
  transcode_preset_id?: string;
  require_content_moderation: boolean;
  schedule_mode: string;
  schedule_time?: string;
};

export type PostingStrategy = PostingStrategyInput & {
  id: string;
  version: number;
  created_at: string;
  updated_at: string;
};

export type PublishingBlocker = {
  code: string;
  platform?: string;
  message: string;
  action: string;
};

export type PublishJob = {
  id: string;
  task_id: string;
  strategy_id?: string;
  status: string;
  auto_started: boolean;
  metadata_version: number;
  fingerprint: string;
  blockers: PublishingBlocker[];
  cover_asset_id?: string;
  media_asset_id?: string;
  scheduled_at?: string;
  queued_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
  version: number;
};

export type PlatformPublication = {
  id: string;
  publish_job_id: string;
  task_id: string;
  platform: Platform;
  account_id: string;
  account_name: string;
  account_auth_mode: string;
  simulation: boolean;
  status: string;
  category_id: string;
  title: string;
  description: string;
  tags: string[];
  cover_asset_id?: string;
  media_asset_id: string;
  scheduled_at?: string;
  fingerprint: string;
  attempt: number;
  remote_submission_id: string;
  remote_url: string;
  remote_status: string;
  adapter_version: string;
  response_summary: Record<string, unknown>;
  error_code: string;
  error_message: string;
  error_retryable: boolean;
  uncertain_since?: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
  version: number;
};

export type PublicationAttempt = {
  id: string;
  publication_id: string;
  attempt: number;
  stage: string;
  status: string;
  request_summary: Record<string, unknown>;
  response_summary: Record<string, unknown>;
  error_code: string;
  error_message: string;
  started_at: string;
  completed_at?: string;
};

export type PublishingDetail = {
  job?: PublishJob;
  publications: PlatformPublication[];
  attempts: Record<string, PublicationAttempt[]>;
  blockers: PublishingBlocker[];
  next_action: string;
};

export type PublishingDraftInput = {
  expected_version: number;
  account_id: string;
  category_id: string;
  title: string;
  description: string;
  tags: string[];
};

export type PublishingResolutionInput = {
  expected_version: number;
  resolution: "remote_published" | "remote_not_created";
  remote_submission_id: string;
  remote_url: string;
  note: string;
};

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fields?: Record<string, string>;

  constructor(
    status: number,
    code: string,
    message: string,
    fields?: Record<string, string>
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.fields = fields;
  }
}

async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const hasJSONBody = Boolean(init?.body) && !(init?.body instanceof FormData);
  const response = await fetch(path, {
    ...init,
    headers: {
      Accept: "application/json",
      ...(hasJSONBody ? { "Content-Type": "application/json" } : {}),
      ...init?.headers
    }
  });

  if (response.status === 204) {
    return undefined as T;
  }
  const value = (await response.json().catch(() => null)) as
    | { error?: { code?: string; message?: string; fields?: Record<string, string> } }
    | T
    | null;
  if (!response.ok) {
    const problem =
      value && typeof value === "object" && "error" in value ? value.error : undefined;
    throw new ApiError(
      response.status,
      problem?.code ?? "request_failed",
      problem?.message ?? `请求失败（${response.status}）`,
      problem?.fields
    );
  }
  return value as T;
}

export const api = {
  assetContentURL: (taskId: string, assetId: string) =>
    `/api/v1/tasks/${encodeURIComponent(taskId)}/assets/${encodeURIComponent(assetId)}/content`,
  dashboard: () => requestJSON<DashboardSummary>("/api/v1/dashboard"),
  files: () => requestJSON<FileLibrary>("/api/v1/files"),
  systemStatus: () => requestJSON<SystemStatus>("/api/v1/system/status"),
  tasks: () => requestJSON<{ items: Task[] }>("/api/v1/tasks"),
  tasksByScope: (scope: "active" | "archived" | "all") =>
    requestJSON<{ items: Task[] }>(
      `/api/v1/tasks?scope=${encodeURIComponent(scope)}`
    ),
  task: (id: string) => requestJSON<Task>(`/api/v1/tasks/${encodeURIComponent(id)}`),
  createTask: (input: CreateTaskInput) =>
    requestJSON<Task>("/api/v1/tasks", {
      method: "POST",
      body: JSON.stringify(input)
    }),
  cancelTask: (id: string) =>
    requestJSON<Task>(`/api/v1/tasks/${encodeURIComponent(id)}/cancel`, {
      method: "POST"
    }),
  retryTask: (id: string) =>
    requestJSON<Task>(`/api/v1/tasks/${encodeURIComponent(id)}/retry`, {
      method: "POST"
    }),
  retryTasks: (taskIds: string[]) =>
    requestJSON<BulkRetryResult>("/api/v1/tasks/bulk-retry", {
      method: "POST",
      body: JSON.stringify({ task_ids: taskIds })
    }),
  setTaskCookieProfile: (id: string, cookieProfileId?: string) =>
    requestJSON<Task>(`/api/v1/tasks/${encodeURIComponent(id)}/cookie-profile`, {
      method: "PUT",
      body: JSON.stringify({ cookie_profile_id: cookieProfileId || null })
    }),
  deleteTaskAssets: (id: string) =>
    requestJSON<Task>(`/api/v1/tasks/${encodeURIComponent(id)}/assets`, {
      method: "DELETE"
    }),
  taskArchivePreview: () =>
    requestJSON<TaskArchivePreview>("/api/v1/tasks/archive-preview"),
  archiveTask: (
    id: string,
    input: { expected_version: number; delete_assets: boolean; reason: string }
  ) =>
    requestJSON<Task>(`/api/v1/tasks/${encodeURIComponent(id)}/archive`, {
      method: "POST",
      body: JSON.stringify(input)
    }),
  archiveAllTasks: (input: {
    expected_count: number;
    delete_assets: boolean;
    confirmation: string;
    reason: string;
  }) =>
    requestJSON<ArchiveAllResult>("/api/v1/tasks/archive-all", {
      method: "POST",
      body: JSON.stringify(input)
    }),
  restoreTask: (
    id: string,
    input: { expected_version: number; reason: string }
  ) =>
    requestJSON<Task>(`/api/v1/tasks/${encodeURIComponent(id)}/restore`, {
      method: "POST",
      body: JSON.stringify(input)
    }),
  purgeTask: (
    id: string,
    input: { expected_version: number; confirmation: string; reason: string }
  ) =>
    requestJSON<PurgeTaskResult>(`/api/v1/tasks/${encodeURIComponent(id)}`, {
      method: "DELETE",
      body: JSON.stringify(input)
    }),
  cookieProfiles: () =>
    requestJSON<{ items: CookieProfile[] }>("/api/v1/cookie-profiles"),
  uploadCookieProfile: (name: string, file: File) => {
    const body = new FormData();
    body.set("name", name);
    body.set("file", file);
    return requestJSON<CookieProfile>("/api/v1/cookie-profiles/upload", {
      method: "POST",
      body
    });
  },
  createCookieCloudProfile: (input: CreateCookieCloudInput) =>
    requestJSON<CookieProfile>("/api/v1/cookie-profiles/cookiecloud", {
      method: "POST",
      body: JSON.stringify(input)
    }),
  syncCookieProfile: (id: string) =>
    requestJSON<CookieProfile>(
      `/api/v1/cookie-profiles/${encodeURIComponent(id)}/sync`,
      { method: "POST" }
    ),
  deleteCookieProfile: (id: string) =>
    requestJSON<void>(`/api/v1/cookie-profiles/${encodeURIComponent(id)}`, {
      method: "DELETE"
    }),
  settings: () => requestJSON<ApplicationSettings>("/api/v1/settings"),
  updateSettings: (input: UpdateSettingsInput) =>
    requestJSON<ApplicationSettings>("/api/v1/settings", {
      method: "PUT",
      body: JSON.stringify(input)
    }),
  testConnection: (target: string) =>
    requestJSON<ConnectionTestResult>("/api/v1/settings/test-connection", {
      method: "POST",
      body: JSON.stringify({ target })
    }),
  reviews: () => requestJSON<{ items: Task[] }>("/api/v1/reviews"),
  review: (id: string) =>
    requestJSON<ReviewDetail>(`/api/v1/reviews/${encodeURIComponent(id)}`),
  updateReviewMetadata: (
    id: string,
    input: {
      title: string;
      description: string;
      tags: string[];
      category: string;
      reason: string;
    }
  ) =>
    requestJSON<ReviewDetail>(
      `/api/v1/reviews/${encodeURIComponent(id)}/metadata`,
      { method: "PUT", body: JSON.stringify(input) }
    ),
  updateReviewSubtitle: (
    taskId: string,
    documentId: string,
    input: {
      expected_version: number;
      segments: SubtitleSegment[];
      reason: string;
    }
  ) =>
    requestJSON<ReviewDetail>(
      `/api/v1/reviews/${encodeURIComponent(taskId)}/subtitles/${encodeURIComponent(documentId)}`,
      { method: "PUT", body: JSON.stringify(input) }
    ),
  reviewAction: (
    id: string,
    action: "approve" | "request_changes" | "resubmit" | "reprocess_subtitles" | "abandon",
    input: { reason: string; delete_assets?: boolean }
  ) =>
    requestJSON<ReviewDetail>(
      `/api/v1/reviews/${encodeURIComponent(id)}/${action}`,
      { method: "POST", body: JSON.stringify(input) }
    ),
  youtubeMonitors: () =>
    requestJSON<{ items: YouTubeMonitor[] }>("/api/v1/youtube-monitors"),
  youtubeCategories: (region: string) =>
    requestJSON<{ items: YouTubeCategory[] }>(
      `/api/v1/youtube-categories?region=${encodeURIComponent(region)}`
    ),
  youtubeMonitor: (id: string) =>
    requestJSON<YouTubeMonitor>(
      `/api/v1/youtube-monitors/${encodeURIComponent(id)}`
    ),
  createYouTubeMonitor: (input: YouTubeMonitorInput) =>
    requestJSON<YouTubeMonitor>("/api/v1/youtube-monitors", {
      method: "POST",
      body: JSON.stringify(input)
    }),
  updateYouTubeMonitor: (
    id: string,
    input: YouTubeMonitorInput & { expected_version: number }
  ) =>
    requestJSON<YouTubeMonitor>(
      `/api/v1/youtube-monitors/${encodeURIComponent(id)}`,
      { method: "PUT", body: JSON.stringify(input) }
    ),
  pauseYouTubeMonitor: (id: string) =>
    requestJSON<YouTubeMonitor>(
      `/api/v1/youtube-monitors/${encodeURIComponent(id)}/pause`,
      { method: "POST" }
    ),
  resumeYouTubeMonitor: (id: string) =>
    requestJSON<YouTubeMonitor>(
      `/api/v1/youtube-monitors/${encodeURIComponent(id)}/resume`,
      { method: "POST" }
    ),
  runYouTubeMonitor: (id: string) =>
    requestJSON<YouTubeMonitorRun>(
      `/api/v1/youtube-monitors/${encodeURIComponent(id)}/run`,
      { method: "POST" }
    ),
  deleteYouTubeMonitor: (id: string, historyMode: "archive" | "purge") =>
    requestJSON<void>(`/api/v1/youtube-monitors/${encodeURIComponent(id)}`, {
      method: "DELETE",
      body: JSON.stringify({ history_mode: historyMode })
    }),
  youtubeMonitorHistory: (id: string) =>
    requestJSON<YouTubeMonitorHistory>(
      `/api/v1/youtube-monitors/${encodeURIComponent(id)}/history`
    ),
  enqueueYouTubeMonitorItems: (id: string, itemIds: string[]) =>
    requestJSON<MonitorEnqueueResult>(
      `/api/v1/youtube-monitors/${encodeURIComponent(id)}/tasks`,
      { method: "POST", body: JSON.stringify({ item_ids: itemIds }) }
    ),
  platformAccounts: (platform?: Platform) =>
    requestJSON<{ items: PlatformAccount[] }>(
      `/api/v1/platform-accounts${
        platform ? `?platform=${encodeURIComponent(platform)}` : ""
      }`
    ),
  platformAccount: (id: string) =>
    requestJSON<PlatformAccount>(
      `/api/v1/platform-accounts/${encodeURIComponent(id)}`
    ),
  createPlatformAccount: (input: CreatePlatformAccountInput) =>
    requestJSON<PlatformAccount>("/api/v1/platform-accounts", {
      method: "POST",
      body: JSON.stringify(input)
    }),
  updatePlatformAccount: (
    id: string,
    input: {
      expected_version: number;
      name: string;
      cookie_profile_id?: string;
    }
  ) =>
    requestJSON<PlatformAccount>(
      `/api/v1/platform-accounts/${encodeURIComponent(id)}`,
      { method: "PUT", body: JSON.stringify(input) }
    ),
  archivePlatformAccount: (id: string, expectedVersion: number) =>
    requestJSON<void>(
      `/api/v1/platform-accounts/${encodeURIComponent(id)}?expected_version=${expectedVersion}`,
      { method: "DELETE" }
    ),
  checkPlatformAccount: (id: string) =>
    requestJSON<PlatformAccountCheckResult>(
      `/api/v1/platform-accounts/${encodeURIComponent(id)}/check`,
      { method: "POST" }
    ),
  platformCategories: (platform: Platform) =>
    requestJSON<{ items: PlatformCategory[] }>(
      `/api/v1/platform-categories?platform=${encodeURIComponent(platform)}`
    ),
  refreshPlatformCategories: (accountId: string) =>
    requestJSON<{ items: PlatformCategory[] }>(
      "/api/v1/platform-categories/refresh",
      { method: "POST", body: JSON.stringify({ account_id: accountId }) }
    ),
  transcodePresets: () =>
    requestJSON<{ items: TranscodePreset[] }>("/api/v1/transcode-presets"),
  transcodePreset: (id: string) =>
    requestJSON<TranscodePreset>(
      `/api/v1/transcode-presets/${encodeURIComponent(id)}`
    ),
  createTranscodePreset: (input: TranscodePresetInput) =>
    requestJSON<TranscodePreset>("/api/v1/transcode-presets", {
      method: "POST",
      body: JSON.stringify(input)
    }),
  updateTranscodePreset: (
    id: string,
    input: TranscodePresetInput & { expected_version: number }
  ) =>
    requestJSON<TranscodePreset>(
      `/api/v1/transcode-presets/${encodeURIComponent(id)}`,
      { method: "PUT", body: JSON.stringify(input) }
    ),
  archiveTranscodePreset: (id: string, expectedVersion: number) =>
    requestJSON<void>(
      `/api/v1/transcode-presets/${encodeURIComponent(id)}?expected_version=${expectedVersion}`,
      { method: "DELETE" }
    ),
  postingStrategies: () =>
    requestJSON<{ items: PostingStrategy[] }>("/api/v1/posting-strategies"),
  postingStrategy: (id: string) =>
    requestJSON<PostingStrategy>(
      `/api/v1/posting-strategies/${encodeURIComponent(id)}`
    ),
  createPostingStrategy: (input: PostingStrategyInput) =>
    requestJSON<PostingStrategy>("/api/v1/posting-strategies", {
      method: "POST",
      body: JSON.stringify(input)
    }),
  updatePostingStrategy: (
    id: string,
    input: PostingStrategyInput & { expected_version: number }
  ) =>
    requestJSON<PostingStrategy>(
      `/api/v1/posting-strategies/${encodeURIComponent(id)}`,
      { method: "PUT", body: JSON.stringify(input) }
    ),
  archivePostingStrategy: (id: string, expectedVersion: number) =>
    requestJSON<void>(
      `/api/v1/posting-strategies/${encodeURIComponent(id)}?expected_version=${expectedVersion}`,
      { method: "DELETE" }
    ),
  publishing: (taskId: string) =>
    requestJSON<PublishingDetail>(
      `/api/v1/publishing/${encodeURIComponent(taskId)}`
    ),
  preparePublishing: (taskId: string) =>
    requestJSON<PublishingDetail>(
      `/api/v1/publishing/${encodeURIComponent(taskId)}/prepare`,
      { method: "POST" }
    ),
  updatePublishingDraft: (
    taskId: string,
    platform: Platform,
    input: PublishingDraftInput
  ) =>
    requestJSON<PublishingDetail>(
      `/api/v1/publishing/${encodeURIComponent(taskId)}/platforms/${platform}`,
      { method: "PUT", body: JSON.stringify(input) }
    ),
  enqueuePublishing: (taskId: string) =>
    requestJSON<PublishingDetail>(
      `/api/v1/publishing/${encodeURIComponent(taskId)}/enqueue`,
      { method: "POST" }
    ),
  retryPlatformPublishing: (taskId: string, platform: Platform) =>
    requestJSON<PublishingDetail>(
      `/api/v1/publishing/${encodeURIComponent(taskId)}/platforms/${platform}/retry`,
      { method: "POST" }
    ),
  resolvePlatformPublishing: (
    taskId: string,
    platform: Platform,
    input: PublishingResolutionInput
  ) =>
    requestJSON<PublishingDetail>(
      `/api/v1/publishing/${encodeURIComponent(taskId)}/platforms/${platform}/resolve`,
      { method: "POST", body: JSON.stringify(input) }
    )
};
