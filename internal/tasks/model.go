package tasks

import "time"

const (
	StatusQueued           = "queued"
	StatusFetchingMetadata = "fetching_metadata"
	StatusMetadataReady    = "metadata_ready"
	StatusDownloading      = "downloading"
	StatusProcessing       = "processing"
	StatusAwaitingReview   = "awaiting_manual_review"
	StatusReadyToPublish   = "ready_to_publish"
	StatusPublishing       = "publishing"
	StatusPublished        = "published"
	StatusFailed           = "failed"
	StatusCancelled        = "cancelled"

	StepMetadata     = "metadata"
	StepDownload     = "download"
	StepMediaInspect = "media_inspect"
	StepSubtitles    = "subtitles"
	StepModeration   = "moderation"
	StepTranscode    = "transcode"
	StepReview       = "review"
	StepPublish      = "publish"

	StatementBriefV1 = "brief_v1"
	StatementFullV1  = "full_v1"
)

type CreateInput struct {
	SourceURL              string   `json:"source_url"`
	TargetPlatforms        []string `json:"target_platforms"`
	CookieProfileID        *string  `json:"cookie_profile_id,omitempty"`
	RepostStatementVersion string   `json:"repost_statement_version"`
	PostingStrategyID      *string  `json:"posting_strategy_id,omitempty"`
	AutoPublish            bool     `json:"auto_publish"`
}

type Step struct {
	Kind                string         `json:"kind"`
	Status              string         `json:"status"`
	Attempt             int            `json:"attempt"`
	Progress            int            `json:"progress"`
	Detail              map[string]any `json:"detail"`
	ActivityState       string         `json:"activity_state,omitempty"`
	HeartbeatAgeSeconds int64          `json:"heartbeat_age_seconds,omitempty"`
	ErrorCode           string         `json:"error_code,omitempty"`
	ErrorMessage        string         `json:"error_message,omitempty"`
	StartedAt           *time.Time     `json:"started_at,omitempty"`
	FinishedAt          *time.Time     `json:"finished_at,omitempty"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

type MediaAsset struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	Bucket         string     `json:"bucket"`
	ObjectKey      string     `json:"object_key"`
	OriginalName   string     `json:"original_name"`
	ContentType    string     `json:"content_type"`
	SizeBytes      int64      `json:"size_bytes"`
	ChecksumSHA256 string     `json:"checksum_sha256"`
	MediaInfo      MediaInfo  `json:"media_info"`
	Status         string     `json:"status"`
	ErrorCode      string     `json:"error_code,omitempty"`
	ErrorMessage   string     `json:"error_message,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
}

type MediaInfo struct {
	SchemaVersion   int      `json:"schema_version"`
	FormatName      string   `json:"format_name"`
	DurationSeconds *float64 `json:"duration_seconds,omitempty"`
	SizeBytes       *int64   `json:"size_bytes,omitempty"`
	BitRate         *int64   `json:"bit_rate,omitempty"`
	VideoCodec      string   `json:"video_codec"`
	Width           *int     `json:"width,omitempty"`
	Height          *int     `json:"height,omitempty"`
	PixelFormat     string   `json:"pixel_format"`
	FrameRate       string   `json:"frame_rate"`
	AudioCodec      string   `json:"audio_codec"`
	SampleRate      *int     `json:"sample_rate,omitempty"`
	Channels        *int     `json:"channels,omitempty"`
	ChannelLayout   string   `json:"channel_layout"`
	StreamCount     int      `json:"stream_count"`
}

type Task struct {
	ID                string           `json:"id"`
	Status            string           `json:"status"`
	TargetPlatforms   []string         `json:"target_platforms"`
	SourceURL         string           `json:"source_url"`
	CookieProfileID   *string          `json:"cookie_profile_id,omitempty"`
	PostingStrategyID *string          `json:"posting_strategy_id,omitempty"`
	AutoPublish       bool             `json:"auto_publish"`
	PublishJobID      *string          `json:"publish_job_id,omitempty"`
	PublishStatus     string           `json:"publish_status"`
	PublishMode       string           `json:"publish_mode"`
	PublishBlockers   []map[string]any `json:"publish_blockers"`
	StatementVersion  string           `json:"repost_statement_version"`
	StatementBrief    string           `json:"repost_statement_brief"`
	StatementFull     string           `json:"repost_statement_full"`
	OriginalTitle     string           `json:"original_title"`
	Title             string           `json:"title"`
	Description       string           `json:"description"`
	ThumbnailURL      string           `json:"thumbnail_url"`
	DurationSeconds   *int             `json:"duration_seconds,omitempty"`
	Extractor         string           `json:"extractor"`
	ReviewMode        string           `json:"review_mode"`
	ReviewStatus      string           `json:"review_status"`
	ReviewSummary     map[string]any   `json:"review_summary"`
	SettingsVersion   int64            `json:"settings_version"`
	Tags              []string         `json:"tags"`
	Category          string           `json:"category"`
	ErrorCode         string           `json:"error_code,omitempty"`
	ErrorMessage      string           `json:"error_message,omitempty"`
	ErrorRetryable    bool             `json:"error_retryable"`
	Version           int64            `json:"version"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
	ArchivedAt        *time.Time       `json:"archived_at,omitempty"`
	ArchivedBy        string           `json:"archived_by,omitempty"`
	Steps             []Step           `json:"steps"`
	Assets            []MediaAsset     `json:"assets"`
}

type Summary struct {
	Total                int64 `json:"total"`
	Active               int64 `json:"active"`
	AwaitingManualReview int64 `json:"awaiting_manual_review"`
	Published            int64 `json:"published"`
	Failed               int64 `json:"failed"`
}

type FileFolder struct {
	TaskID         string       `json:"task_id"`
	Title          string       `json:"title"`
	Status         string       `json:"status"`
	Archived       bool         `json:"archived"`
	UpdatedAt      time.Time    `json:"updated_at"`
	FileCount      int          `json:"file_count"`
	AvailableCount int          `json:"available_count"`
	DeletedCount   int          `json:"deleted_count"`
	TotalBytes     int64        `json:"total_bytes"`
	Files          []MediaAsset `json:"files"`
}

type FileLibrary struct {
	FolderCount    int          `json:"folder_count"`
	FileCount      int          `json:"file_count"`
	AvailableCount int          `json:"available_count"`
	DeletedCount   int          `json:"deleted_count"`
	TotalBytes     int64        `json:"total_bytes"`
	Folders        []FileFolder `json:"folders"`
}

type NewTask struct {
	Task
	MetadataStepID   string
	OutboxID         string
	AuditID          string
	Envelope         []byte
	EventType        string
	SettingsSnapshot []byte
	SecretSnapshots  []SecretSnapshot
}

type SecretSnapshot struct {
	Key        string
	Ciphertext []byte
	Version    int64
}

type TaskConfiguration struct {
	Version         int64
	ReviewMode      string
	Snapshot        []byte
	SecretSnapshots []SecretSnapshot
}

type PostingStrategyReference struct {
	ID              string
	Enabled         bool
	AutomationMode  string
	TargetPlatforms []string
	Snapshot        []byte
}

type Metadata struct {
	Title           string `json:"title"`
	Description     string `json:"description"`
	ThumbnailURL    string `json:"thumbnail_url"`
	DurationSeconds *int   `json:"duration_seconds,omitempty"`
	Extractor       string `json:"extractor"`
}

type WorkflowFailure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type DownloadProgress struct {
	TaskID               string  `json:"task_id"`
	Attempt              int     `json:"attempt"`
	Progress             int     `json:"progress"`
	Phase                string  `json:"phase"`
	DownloadedBytes      int64   `json:"downloaded_bytes"`
	TotalBytes           int64   `json:"total_bytes"`
	TotalBytesIsEstimate bool    `json:"total_bytes_is_estimate"`
	SpeedBytesPerSecond  float64 `json:"speed_bytes_per_second"`
	ETASeconds           *int    `json:"eta_seconds"`
	FragmentIndex        int     `json:"fragment_index"`
	FragmentCount        int     `json:"fragment_count"`
}

type DownloadResult struct {
	TaskID           string                `json:"task_id"`
	AssetID          string                `json:"asset_id"`
	Kind             string                `json:"kind"`
	Bucket           string                `json:"bucket"`
	ObjectKey        string                `json:"object_key"`
	OriginalName     string                `json:"original_name"`
	ContentType      string                `json:"content_type"`
	SizeBytes        int64                 `json:"size_bytes"`
	ChecksumSHA256   string                `json:"checksum_sha256"`
	MediaInfo        MediaInfo             `json:"media_info"`
	AdditionalAssets []DownloadAssetResult `json:"additional_assets"`
}

type DownloadAssetResult struct {
	AssetID        string    `json:"asset_id"`
	Kind           string    `json:"kind"`
	Bucket         string    `json:"bucket"`
	ObjectKey      string    `json:"object_key"`
	OriginalName   string    `json:"original_name"`
	ContentType    string    `json:"content_type"`
	SizeBytes      int64     `json:"size_bytes"`
	ChecksumSHA256 string    `json:"checksum_sha256"`
	MediaInfo      MediaInfo `json:"media_info"`
}

type MediaInspectResult struct {
	TaskID    string    `json:"task_id"`
	Attempt   int       `json:"attempt"`
	MediaInfo MediaInfo `json:"media_info"`
}

type SubtitleAssetResult struct {
	AssetID        string `json:"asset_id"`
	Kind           string `json:"kind"`
	Bucket         string `json:"bucket"`
	ObjectKey      string `json:"object_key"`
	OriginalName   string `json:"original_name"`
	ContentType    string `json:"content_type"`
	SizeBytes      int64  `json:"size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256"`
}

type SubtitleDocumentResult struct {
	DocumentID string           `json:"document_id"`
	Kind       string           `json:"kind"`
	Language   string           `json:"language"`
	Source     string           `json:"source"`
	Segments   []map[string]any `json:"segments"`
	QCReport   map[string]any   `json:"qc_report"`
}

type ExistingSubtitleDetectionResult struct {
	SchemaVersion     int      `json:"schema_version"`
	State             string   `json:"state"`
	Source            string   `json:"source"`
	Language          string   `json:"language"`
	Disposition       string   `json:"disposition"`
	Reason            string   `json:"reason"`
	ConfidencePercent int      `json:"confidence_percent"`
	SampleCount       int      `json:"sample_count"`
	HitCount          int      `json:"hit_count"`
	StablePairCount   int      `json:"stable_pair_count"`
	DistinctTextCount int      `json:"distinct_text_count"`
	Evidence          []string `json:"evidence"`
}

type SubtitleProcessingDecision struct {
	SchemaVersion      int                             `json:"schema_version"`
	Disposition        string                          `json:"disposition"`
	TranslationSkipped bool                            `json:"translation_skipped"`
	BurnSubtitles      bool                            `json:"burn_subtitles"`
	Detection          ExistingSubtitleDetectionResult `json:"detection"`
}

type SubtitleProcessingResult struct {
	TaskID    string                     `json:"task_id"`
	Attempt   int                        `json:"attempt"`
	Assets    []SubtitleAssetResult      `json:"assets"`
	Documents []SubtitleDocumentResult   `json:"documents"`
	Decision  SubtitleProcessingDecision `json:"decision"`
}

type SubtitleProgress struct {
	TaskID            string `json:"task_id"`
	Attempt           int    `json:"attempt"`
	Progress          int    `json:"progress"`
	Phase             string `json:"phase"`
	RemoteTaskID      string `json:"remote_task_id,omitempty"`
	RemoteStatus      string `json:"remote_status,omitempty"`
	BatchIndex        int    `json:"batch_index,omitempty"`
	BatchCount        int    `json:"batch_count,omitempty"`
	CompletedBatches  int    `json:"completed_batches,omitempty"`
	BatchSegmentCount int    `json:"batch_segment_count,omitempty"`
	BatchSplit        bool   `json:"batch_split,omitempty"`
	ModelAttempt      int    `json:"model_attempt,omitempty"`
	ModelAttempts     int    `json:"model_attempts,omitempty"`
	RepairingMissing  bool   `json:"repairing_missing,omitempty"`
	CheckpointReused  bool   `json:"checkpoint_reused,omitempty"`
	RestoredItems     int    `json:"restored_items,omitempty"`
	SampleCount       int    `json:"sample_count,omitempty"`
	TotalCount        int    `json:"total_count,omitempty"`
}

type TranscodeProgress struct {
	TaskID   string `json:"task_id"`
	RunID    string `json:"run_id"`
	Attempt  int    `json:"attempt"`
	Progress int    `json:"progress"`
}

type TranscodeResult struct {
	TaskID          string         `json:"task_id"`
	RunID           string         `json:"run_id"`
	Attempt         int            `json:"attempt"`
	InputAssetID    string         `json:"input_asset_id"`
	AssetID         string         `json:"asset_id"`
	Kind            string         `json:"kind"`
	Bucket          string         `json:"bucket"`
	ObjectKey       string         `json:"object_key"`
	OriginalName    string         `json:"original_name"`
	ContentType     string         `json:"content_type"`
	SizeBytes       int64          `json:"size_bytes"`
	ChecksumSHA256  string         `json:"checksum_sha256"`
	MediaInfo       MediaInfo      `json:"media_info"`
	ResolvedEncoder string         `json:"resolved_encoder"`
	ResolvedAudio   string         `json:"resolved_audio_encoder"`
	CommandSummary  map[string]any `json:"command_summary"`
}

type TranscodeFailure struct {
	TaskID    string `json:"task_id"`
	RunID     string `json:"run_id"`
	Attempt   int    `json:"attempt"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type TranscodeCancellation struct {
	TaskID  string `json:"task_id"`
	RunID   string `json:"run_id"`
	Attempt int    `json:"attempt"`
	Message string `json:"message"`
}

type BulkRetryInput struct {
	TaskIDs []string `json:"task_ids"`
}

type BulkRetryFailure struct {
	TaskID  string `json:"task_id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type BulkRetryResult struct {
	Succeeded []Task             `json:"succeeded"`
	Failed    []BulkRetryFailure `json:"failed"`
}

type ArchiveInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	DeleteAssets    bool   `json:"delete_assets"`
	Reason          string `json:"reason"`
}

type RestoreInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
}

type PurgeInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	Confirmation    string `json:"confirmation"`
	Reason          string `json:"reason"`
}

type ArchiveAllInput struct {
	ExpectedCount int    `json:"expected_count"`
	DeleteAssets  bool   `json:"delete_assets"`
	Confirmation  string `json:"confirmation"`
	Reason        string `json:"reason"`
}

type ArchivePreview struct {
	TotalTasks      int64 `json:"total_tasks"`
	ArchivableTasks int64 `json:"archivable_tasks"`
	RunningTasks    int64 `json:"running_tasks"`
	AssetCount      int64 `json:"asset_count"`
	AssetBytes      int64 `json:"asset_bytes"`
	PublishedTasks  int64 `json:"published_tasks"`
}

type ArchiveFailure struct {
	TaskID  string `json:"task_id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ArchiveAllResult struct {
	Archived []Task           `json:"archived"`
	Failed   []ArchiveFailure `json:"failed"`
}

type ArchiveCandidate struct {
	ID      string
	Version int64
}

type PurgeResult struct {
	TaskID   string    `json:"task_id"`
	PurgedAt time.Time `json:"purged_at"`
}

type AssetDeletionResult struct {
	AssetIDs []string `json:"asset_ids"`
}

type AssetDeletionFailure struct {
	AssetIDs  []string `json:"asset_ids"`
	Code      string   `json:"code"`
	Message   string   `json:"message"`
	Retryable bool     `json:"retryable"`
}

type SetCookieProfileInput struct {
	CookieProfileID *string `json:"cookie_profile_id"`
}
