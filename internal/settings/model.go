package settings

import "time"

const (
	SecretModelGlobal              = "model.global.api_key"
	SecretModelSubtitleTranslation = "model.subtitle_translation.api_key"
	SecretModelSubtitleQC          = "model.subtitle_qc.api_key"
	SecretModelSmartSegmentation   = "model.smart_segmentation.api_key"
	SecretSubtitleASR              = "subtitle.asr.api_key"
	SecretYouTubeAPI               = "youtube.api_key"
	SecretYouTubeProxyPassword     = "youtube.proxy_password"
	SecretAliyunAccessKeyID        = "aliyun.access_key_id"
	SecretAliyunAccessKeySecret    = "aliyun.access_key_secret"
)

var AllowedSecretKeys = map[string]struct{}{
	SecretModelGlobal:              {},
	SecretModelSubtitleTranslation: {},
	SecretModelSubtitleQC:          {},
	SecretModelSmartSegmentation:   {},
	SecretSubtitleASR:              {},
	SecretYouTubeAPI:               {},
	SecretYouTubeProxyPassword:     {},
	SecretAliyunAccessKeyID:        {},
	SecretAliyunAccessKeySecret:    {},
}

type AutomaticReviewRules struct {
	RequireMedia           bool `json:"require_media"`
	RequireTitle           bool `json:"require_title"`
	MinimumDescription     int  `json:"minimum_description_length"`
	MaximumDurationSeconds int  `json:"maximum_duration_seconds"`
	RequireSubtitleQC      bool `json:"require_subtitle_qc"`
	MinimumSubtitleQCScore int  `json:"minimum_subtitle_qc_score"`
}

type ReviewConfig struct {
	Mode              string               `json:"mode"`
	AutomaticFallback string               `json:"automatic_fallback"`
	Rules             AutomaticReviewRules `json:"rules"`
}

type ModelEndpoint struct {
	Enabled        bool    `json:"enabled,omitempty"`
	Mode           string  `json:"mode,omitempty"`
	Provider       string  `json:"provider"`
	BaseURL        string  `json:"base_url"`
	Model          string  `json:"model"`
	Thinking       bool    `json:"thinking"`
	Temperature    float64 `json:"temperature"`
	TimeoutSeconds int     `json:"timeout_seconds"`
}

type ModelConfig struct {
	Global              ModelEndpoint `json:"global"`
	SubtitleTranslation ModelEndpoint `json:"subtitle_translation"`
	SubtitleQC          ModelEndpoint `json:"subtitle_qc"`
	SmartSegmentation   ModelEndpoint `json:"smart_segmentation"`
}

type ASRConfig struct {
	Enabled             bool   `json:"enabled"`
	Provider            string `json:"provider"`
	BaseURL             string `json:"base_url"`
	Model               string `json:"model"`
	Language            string `json:"language"`
	Prompt              string `json:"prompt"`
	TimeoutSeconds      int    `json:"timeout_seconds"`
	MaxRetries          int    `json:"max_retries"`
	VADEnabled          bool   `json:"vad_enabled"`
	ChunkSeconds        int    `json:"chunk_seconds"`
	ChunkOverlapSeconds int    `json:"chunk_overlap_seconds"`
}

type SubtitlePostprocessConfig struct {
	TimeOffsetSeconds        float64 `json:"time_offset_seconds"`
	MinimumCueSeconds        float64 `json:"minimum_cue_seconds"`
	MergeGapSeconds          float64 `json:"merge_gap_seconds"`
	MinimumTextLength        int     `json:"minimum_text_length"`
	MaximumCharactersPerLine int     `json:"maximum_characters_per_line"`
	MaximumLines             int     `json:"maximum_lines"`
	NormalizePunctuation     bool    `json:"normalize_punctuation"`
	FilterFillerWords        bool    `json:"filter_filler_words"`
}

type SegmentationConfig struct {
	Enabled                bool    `json:"enabled"`
	MinimumCueSeconds      float64 `json:"minimum_cue_seconds"`
	MaximumCueSeconds      float64 `json:"maximum_cue_seconds"`
	MaximumCPS             float64 `json:"maximum_cps"`
	BatchWindowSeconds     int     `json:"batch_window_seconds"`
	MaximumBatchCharacters int     `json:"maximum_batch_characters"`
	MaxRetries             int     `json:"max_retries"`
}

type TranslationConfig struct {
	Enabled           bool `json:"enabled"`
	BatchSize         int  `json:"batch_size"`
	MaxRetries        int  `json:"max_retries"`
	RetryDelaySeconds int  `json:"retry_delay_seconds"`
}

type SubtitleQCConfig struct {
	Enabled           bool `json:"enabled"`
	Threshold         int  `json:"threshold"`
	SampleMaxItems    int  `json:"sample_max_items"`
	MaximumCharacters int  `json:"maximum_characters"`
}

type SubtitleStyle struct {
	FontName         string `json:"font_name"`
	FontSize         int    `json:"font_size"`
	PreferSingleLine bool   `json:"prefer_single_line"`
	MaximumLines     int    `json:"maximum_lines"`
}

// ExistingChineseSubtitleConfig controls the preflight that looks for
// reusable Chinese subtitles before paid ASR and translation are started.
// Version is frozen in every task snapshot so detection changes remain
// auditable and never alter an already-created task.
type ExistingChineseSubtitleConfig struct {
	Version                    int    `json:"version"`
	Enabled                    bool   `json:"enabled"`
	InspectPlatformSubtitles   bool   `json:"inspect_platform_subtitles"`
	InspectEmbeddedSubtitles   bool   `json:"inspect_embedded_subtitles"`
	InspectHardcodedSubtitles  bool   `json:"inspect_hardcoded_subtitles"`
	HardcodedAction            string `json:"hardcoded_action"`
	UncertainAction            string `json:"uncertain_action"`
	SampleCount                int    `json:"sample_count"`
	ConfidenceThresholdPercent int    `json:"confidence_threshold_percent"`
	CoverageThresholdPercent   int    `json:"coverage_threshold_percent"`
	MinimumDistinctTexts       int    `json:"minimum_distinct_texts"`
}

type SubtitleConfig struct {
	Enabled               bool                          `json:"enabled"`
	SourceStrategy        string                        `json:"source_strategy"`
	SourceLanguage        string                        `json:"source_language"`
	TargetLanguage        string                        `json:"target_language"`
	DownloadAutoSubtitles bool                          `json:"download_auto_subtitles"`
	ExistingChinese       ExistingChineseSubtitleConfig `json:"existing_chinese"`
	ASR                   ASRConfig                     `json:"asr"`
	Postprocess           SubtitlePostprocessConfig     `json:"postprocess"`
	Segmentation          SegmentationConfig            `json:"segmentation"`
	Translation           TranslationConfig             `json:"translation"`
	QC                    SubtitleQCConfig              `json:"qc"`
	KeepOriginal          bool                          `json:"keep_original"`
	EmbedInVideo          bool                          `json:"embed_in_video"`
	Style                 SubtitleStyle                 `json:"style"`
}

type PromptEntry struct {
	Mode string `json:"mode"`
	Text string `json:"text"`
}

type PromptConfig struct {
	SubtitleTranslation       PromptEntry `json:"subtitle_translation"`
	SubtitleTranslationStrict PromptEntry `json:"subtitle_translation_strict"`
	SubtitleQC                PromptEntry `json:"subtitle_qc"`
	MetadataTranslation       PromptEntry `json:"metadata_translation"`
	MetadataDescriptionRetry  PromptEntry `json:"metadata_description_retry"`
	SmartSegmentation         PromptEntry `json:"smart_segmentation"`
}

type YouTubeConfig struct {
	Provider              string `json:"provider"`
	APIBaseURL            string `json:"api_base_url"`
	ProxyEnabled          bool   `json:"proxy_enabled"`
	ProxyURL              string `json:"proxy_url"`
	ProxyUsername         string `json:"proxy_username"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds"`
	FixtureMediaURL       string `json:"fixture_media_url"`
}

// AutomationConfig controls the task pipeline after a monitor result or a
// manually-created task has entered the system. It is snapshotted per task so
// changing the global switch never changes an already-running task.
type AutomationConfig struct {
	Enabled              bool `json:"enabled"`
	TranslateTitle       bool `json:"translate_title"`
	TranslateDescription bool `json:"translate_description"`
	GenerateTags         bool `json:"generate_tags"`
	RecommendCategories  bool `json:"recommend_categories"`
	ProcessCover         bool `json:"process_cover"`
}

// TranscodeConfig describes a policy, not a raw shell command. The media
// worker resolves these fields against its detected LGPL-safe FFmpeg
// capabilities before launching a process.
type TranscodeConfig struct {
	Enabled                 bool     `json:"enabled"`
	EncoderMode             string   `json:"encoder_mode"`
	VideoCodec              string   `json:"video_codec"`
	AudioCodec              string   `json:"audio_codec"`
	Container               string   `json:"container"`
	CPUPreset               string   `json:"cpu_preset"`
	HighResolutionCPUPreset string   `json:"high_resolution_cpu_preset"`
	MaximumHeight           int      `json:"maximum_height"`
	VideoBitrateKbps        int      `json:"video_bitrate_kbps"`
	AudioBitrateKbps        int      `json:"audio_bitrate_kbps"`
	BurnSubtitles           bool     `json:"burn_subtitles"`
	CustomArgumentsEnabled  bool     `json:"custom_arguments_enabled"`
	CustomArguments         []string `json:"custom_arguments"`
}

type ModerationConfig struct {
	Enabled                 bool   `json:"enabled"`
	Provider                string `json:"provider"`
	Region                  string `json:"region"`
	CheckText               bool   `json:"check_text"`
	CheckImage              bool   `json:"check_image"`
	CheckVideo              bool   `json:"check_video"`
	TextService             string `json:"text_service"`
	ImageService            string `json:"image_service"`
	VideoService            string `json:"video_service"`
	HighRiskAction          string `json:"high_risk_action"`
	MediumRiskAction        string `json:"medium_risk_action"`
	FailureAction           string `json:"failure_action"`
	RequestTimeoutSeconds   int    `json:"request_timeout_seconds"`
	VideoPollSeconds        int    `json:"video_poll_seconds"`
	VideoMaximumWaitSeconds int    `json:"video_maximum_wait_seconds"`
}

type PublishingConfig struct {
	AutoPublishAfterReview    bool `json:"auto_publish_after_review"`
	MaximumConcurrentUploads  int  `json:"maximum_concurrent_uploads"`
	MaximumAttempts           int  `json:"maximum_attempts"`
	RetryDelaySeconds         int  `json:"retry_delay_seconds"`
	ReconcileUncertainResults bool `json:"reconcile_uncertain_results"`
}

type ConfigSnapshot struct {
	Review     ReviewConfig     `json:"review"`
	Models     ModelConfig      `json:"models"`
	Subtitle   SubtitleConfig   `json:"subtitle"`
	Prompts    PromptConfig     `json:"prompts"`
	YouTube    YouTubeConfig    `json:"youtube"`
	Automation AutomationConfig `json:"automation"`
	Transcode  TranscodeConfig  `json:"transcode"`
	Moderation ModerationConfig `json:"moderation"`
	Publishing PublishingConfig `json:"publishing"`
}

type RuntimeAsset struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Bucket       string `json:"bucket"`
	ObjectKey    string `json:"object_key"`
	OriginalName string `json:"original_name"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes"`
	Checksum     string `json:"checksum_sha256"`
}

type TaskRuntime struct {
	SourceURL       string        `json:"source_url"`
	ThumbnailURL    string        `json:"thumbnail_url"`
	CookieProfileID *string       `json:"cookie_profile_id,omitempty"`
	SourceAsset     *RuntimeAsset `json:"source_asset,omitempty"`
	SubtitleAsset   *RuntimeAsset `json:"subtitle_asset,omitempty"`
	FinalMediaAsset *RuntimeAsset `json:"final_media_asset,omitempty"`
	CoverAsset      *RuntimeAsset `json:"cover_asset,omitempty"`
	Title           string        `json:"title"`
	Description     string        `json:"description"`
	Tags            []string      `json:"tags"`
	RepostStatement string        `json:"repost_statement"`
}

type Settings struct {
	Version int64 `json:"version"`
	ConfigSnapshot
	SecretConfigured map[string]bool `json:"secret_configured"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type UpdateInput struct {
	ExpectedVersion int64 `json:"expected_version"`
	ConfigSnapshot
	Secrets      map[string]string `json:"secrets,omitempty"`
	ClearSecrets []string          `json:"clear_secrets,omitempty"`
}

type ConnectionTestInput struct {
	Target string `json:"target"`
}

type ConnectionTestResult struct {
	Target    string        `json:"target"`
	OK        bool          `json:"ok"`
	Message   string        `json:"message"`
	Latency   time.Duration `json:"-"`
	LatencyMS int64         `json:"latency_ms"`
	CheckedAt time.Time     `json:"checked_at"`
	Provider  string        `json:"provider"`
	Model     string        `json:"model,omitempty"`
}

type ValidationError struct {
	Fields map[string]string `json:"fields"`
}

func (e *ValidationError) Error() string {
	return "settings input is invalid"
}
