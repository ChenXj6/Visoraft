package publishing

import "time"

const (
	PlatformAcFun    = "acfun"
	PlatformBilibili = "bilibili"

	AccountStatusUnchecked = "unchecked"
	AccountStatusChecking  = "checking"
	AccountStatusReady     = "ready"
	AccountStatusExpired   = "expired"
	AccountStatusError     = "error"
	AccountStatusArchived  = "archived"

	AutomationManual    = "manual_after_review"
	AutomationAutomatic = "automatic_after_review"
)

type Account struct {
	ID                string     `json:"id"`
	Platform          string     `json:"platform"`
	Name              string     `json:"name"`
	AuthMode          string     `json:"auth_mode"`
	CookieProfileID   *string    `json:"cookie_profile_id,omitempty"`
	Status            string     `json:"status"`
	RemoteUserID      string     `json:"remote_user_id"`
	RemoteDisplayName string     `json:"remote_display_name"`
	AdapterVersion    string     `json:"adapter_version"`
	LastCheckedAt     *time.Time `json:"last_checked_at,omitempty"`
	LastErrorCode     string     `json:"last_error_code"`
	LastErrorMessage  string     `json:"last_error_message"`
	Version           int64      `json:"version"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type CreateAccountInput struct {
	Platform        string  `json:"platform"`
	Name            string  `json:"name"`
	AuthMode        string  `json:"auth_mode"`
	CookieProfileID *string `json:"cookie_profile_id,omitempty"`
}

type UpdateAccountInput struct {
	ExpectedVersion int64   `json:"expected_version"`
	Name            string  `json:"name"`
	CookieProfileID *string `json:"cookie_profile_id,omitempty"`
}

type AccountCheckResult struct {
	Account
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type Category struct {
	Platform    string         `json:"platform"`
	CategoryID  string         `json:"category_id"`
	ParentID    string         `json:"parent_id"`
	Name        string         `json:"name"`
	Path        string         `json:"path"`
	Active      bool           `json:"active"`
	SortOrder   int            `json:"sort_order"`
	Metadata    map[string]any `json:"metadata"`
	RefreshedAt time.Time      `json:"refreshed_at"`
}

type TranscodePreset struct {
	ID                      string    `json:"id"`
	Name                    string    `json:"name"`
	Enabled                 bool      `json:"enabled"`
	EncoderMode             string    `json:"encoder_mode"`
	VideoCodec              string    `json:"video_codec"`
	AudioCodec              string    `json:"audio_codec"`
	Container               string    `json:"container"`
	CPUPreset               string    `json:"cpu_preset"`
	HighResolutionCPUPreset string    `json:"high_resolution_cpu_preset"`
	MaximumHeight           int       `json:"maximum_height"`
	VideoBitrateKbps        int       `json:"video_bitrate_kbps"`
	AudioBitrateKbps        int       `json:"audio_bitrate_kbps"`
	BurnSubtitles           bool      `json:"burn_subtitles"`
	CustomArguments         []string  `json:"custom_arguments"`
	Version                 int64     `json:"version"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type TranscodePresetInput struct {
	Name                    string   `json:"name"`
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
	CustomArguments         []string `json:"custom_arguments"`
}

type UpdateTranscodePresetInput struct {
	ExpectedVersion int64 `json:"expected_version"`
	TranscodePresetInput
}

type PostingStrategy struct {
	ID                       string            `json:"id"`
	Name                     string            `json:"name"`
	Enabled                  bool              `json:"enabled"`
	AutomationMode           string            `json:"automation_mode"`
	TargetPlatforms          []string          `json:"target_platforms"`
	AccountBindings          map[string]string `json:"account_bindings"`
	CategoryBindings         map[string]string `json:"category_bindings"`
	TitleTemplates           map[string]string `json:"title_templates"`
	DescriptionTemplates     map[string]string `json:"description_templates"`
	DefaultTags              []string          `json:"default_tags"`
	RepostStatementVersion   string            `json:"repost_statement_version"`
	TranscodePresetID        *string           `json:"transcode_preset_id,omitempty"`
	RequireContentModeration bool              `json:"require_content_moderation"`
	ScheduleMode             string            `json:"schedule_mode"`
	ScheduleTime             *string           `json:"schedule_time,omitempty"`
	Version                  int64             `json:"version"`
	CreatedAt                time.Time         `json:"created_at"`
	UpdatedAt                time.Time         `json:"updated_at"`
}

type PostingStrategyInput struct {
	Name                     string            `json:"name"`
	Enabled                  bool              `json:"enabled"`
	AutomationMode           string            `json:"automation_mode"`
	TargetPlatforms          []string          `json:"target_platforms"`
	AccountBindings          map[string]string `json:"account_bindings"`
	CategoryBindings         map[string]string `json:"category_bindings"`
	TitleTemplates           map[string]string `json:"title_templates"`
	DescriptionTemplates     map[string]string `json:"description_templates"`
	DefaultTags              []string          `json:"default_tags"`
	RepostStatementVersion   string            `json:"repost_statement_version"`
	TranscodePresetID        *string           `json:"transcode_preset_id,omitempty"`
	RequireContentModeration bool              `json:"require_content_moderation"`
	ScheduleMode             string            `json:"schedule_mode"`
	ScheduleTime             *string           `json:"schedule_time,omitempty"`
}

type UpdatePostingStrategyInput struct {
	ExpectedVersion int64 `json:"expected_version"`
	PostingStrategyInput
}

type PublishJob struct {
	ID              string     `json:"id"`
	TaskID          string     `json:"task_id"`
	StrategyID      *string    `json:"strategy_id,omitempty"`
	Status          string     `json:"status"`
	AutoStarted     bool       `json:"auto_started"`
	MetadataVersion int64      `json:"metadata_version"`
	Fingerprint     string     `json:"fingerprint"`
	Blockers        []Blocker  `json:"blockers"`
	CoverAssetID    *string    `json:"cover_asset_id,omitempty"`
	MediaAssetID    *string    `json:"media_asset_id,omitempty"`
	ScheduledAt     *time.Time `json:"scheduled_at,omitempty"`
	QueuedAt        *time.Time `json:"queued_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Version         int64      `json:"version"`
}

type PlatformPublication struct {
	ID                 string         `json:"id"`
	PublishJobID       string         `json:"publish_job_id"`
	TaskID             string         `json:"task_id"`
	Platform           string         `json:"platform"`
	AccountID          string         `json:"account_id"`
	AccountName        string         `json:"account_name"`
	AccountAuthMode    string         `json:"account_auth_mode"`
	Simulation         bool           `json:"simulation"`
	Status             string         `json:"status"`
	CategoryID         string         `json:"category_id"`
	Title              string         `json:"title"`
	Description        string         `json:"description"`
	Tags               []string       `json:"tags"`
	CoverAssetID       *string        `json:"cover_asset_id,omitempty"`
	MediaAssetID       string         `json:"media_asset_id"`
	ScheduledAt        *time.Time     `json:"scheduled_at,omitempty"`
	Fingerprint        string         `json:"fingerprint"`
	Attempt            int            `json:"attempt"`
	RemoteSubmissionID string         `json:"remote_submission_id"`
	RemoteURL          string         `json:"remote_url"`
	RemoteStatus       string         `json:"remote_status"`
	AdapterVersion     string         `json:"adapter_version"`
	ResponseSummary    map[string]any `json:"response_summary"`
	ErrorCode          string         `json:"error_code"`
	ErrorMessage       string         `json:"error_message"`
	ErrorRetryable     bool           `json:"error_retryable"`
	UncertainSince     *time.Time     `json:"uncertain_since,omitempty"`
	StartedAt          *time.Time     `json:"started_at,omitempty"`
	CompletedAt        *time.Time     `json:"completed_at,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	Version            int64          `json:"version"`
}

type PublicationAttempt struct {
	ID              string         `json:"id"`
	PublicationID   string         `json:"publication_id"`
	Attempt         int            `json:"attempt"`
	Stage           string         `json:"stage"`
	Status          string         `json:"status"`
	RequestSummary  map[string]any `json:"request_summary"`
	ResponseSummary map[string]any `json:"response_summary"`
	ErrorCode       string         `json:"error_code"`
	ErrorMessage    string         `json:"error_message"`
	StartedAt       time.Time      `json:"started_at"`
	CompletedAt     *time.Time     `json:"completed_at,omitempty"`
}

type Blocker struct {
	Code     string `json:"code"`
	Platform string `json:"platform,omitempty"`
	Message  string `json:"message"`
	Action   string `json:"action"`
}

type Detail struct {
	Job          *PublishJob                     `json:"job,omitempty"`
	Publications []PlatformPublication           `json:"publications"`
	Attempts     map[string][]PublicationAttempt `json:"attempts"`
	Blockers     []Blocker                       `json:"blockers"`
	NextAction   string                          `json:"next_action"`
}

type DraftPlatformInput struct {
	ExpectedVersion int64    `json:"expected_version"`
	AccountID       string   `json:"account_id"`
	CategoryID      string   `json:"category_id"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Tags            []string `json:"tags"`
}

type ResolvePublicationInput struct {
	ExpectedVersion    int64  `json:"expected_version"`
	Resolution         string `json:"resolution"`
	RemoteSubmissionID string `json:"remote_submission_id"`
	RemoteURL          string `json:"remote_url"`
	Note               string `json:"note"`
}

type ValidationError struct {
	Fields map[string]string `json:"fields"`
}

func (e *ValidationError) Error() string {
	return "publishing input is invalid"
}

type ConflictError struct {
	Code    string
	Message string
}

func (e *ConflictError) Error() string {
	return e.Message
}
