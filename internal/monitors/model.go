package monitors

import "time"

type TaskTemplate struct {
	TargetPlatforms        []string `json:"target_platforms"`
	CookieProfileID        *string  `json:"cookie_profile_id,omitempty"`
	RepostStatementVersion string   `json:"repost_statement_version"`
	PostingStrategyID      *string  `json:"posting_strategy_id,omitempty"`
	AutoPublish            bool     `json:"auto_publish"`
}

type SeriesScope struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Query        string `json:"query"`
	EpisodeStart int    `json:"episode_start"`
	EpisodeEnd   int    `json:"episode_end"`
}

type Monitor struct {
	ID                      string        `json:"id"`
	Name                    string        `json:"name"`
	Enabled                 bool          `json:"enabled"`
	MonitorType             string        `json:"monitor_type"`
	ChannelMode             string        `json:"channel_mode"`
	Query                   string        `json:"query"`
	SeriesTitle             string        `json:"series_title"`
	SeriesScopes            []SeriesScope `json:"series_scopes"`
	EpisodeStart            int           `json:"episode_start"`
	EpisodeEnd              int           `json:"episode_end"`
	ChannelIDs              []string      `json:"channel_ids"`
	IncludeKeywords         []string      `json:"include_keywords"`
	ExcludeKeywords         []string      `json:"exclude_keywords"`
	ExcludeChannelIDs       []string      `json:"exclude_channel_ids"`
	RegionCode              string        `json:"region_code"`
	CategoryID              string        `json:"category_id"`
	LookbackDays            int           `json:"lookback_days"`
	MaxResults              int           `json:"max_results"`
	OrderBy                 string        `json:"order_by"`
	VideoTypes              []string      `json:"video_types"`
	MinViewCount            int64         `json:"min_view_count"`
	MinLikeCount            int64         `json:"min_like_count"`
	MinCommentCount         int64         `json:"min_comment_count"`
	MinDurationSeconds      int           `json:"min_duration_seconds"`
	MaxDurationSeconds      int           `json:"max_duration_seconds"`
	PublishedAfter          *string       `json:"published_after,omitempty"`
	PublishedBefore         *string       `json:"published_before,omitempty"`
	ScheduleType            string        `json:"schedule_type"`
	ScheduleIntervalMinutes int           `json:"schedule_interval_minutes"`
	RateLimitRequests       int           `json:"rate_limit_requests"`
	AutoAddToTasks          bool          `json:"auto_add_to_tasks"`
	TaskTemplate            TaskTemplate  `json:"task_template"`
	State                   string        `json:"state"`
	LastRunAt               *time.Time    `json:"last_run_at,omitempty"`
	NextRunAt               *time.Time    `json:"next_run_at,omitempty"`
	LastError               string        `json:"last_error"`
	Version                 int64         `json:"version"`
	CreatedAt               time.Time     `json:"created_at"`
	UpdatedAt               time.Time     `json:"updated_at"`
	ArchivedAt              *time.Time    `json:"archived_at,omitempty"`
}

type CreateInput struct {
	Name                    string        `json:"name"`
	Enabled                 bool          `json:"enabled"`
	MonitorType             string        `json:"monitor_type"`
	ChannelMode             string        `json:"channel_mode"`
	Query                   string        `json:"query"`
	SeriesTitle             string        `json:"series_title"`
	SeriesScopes            []SeriesScope `json:"series_scopes"`
	EpisodeStart            int           `json:"episode_start"`
	EpisodeEnd              int           `json:"episode_end"`
	ChannelIDs              []string      `json:"channel_ids"`
	IncludeKeywords         []string      `json:"include_keywords"`
	ExcludeKeywords         []string      `json:"exclude_keywords"`
	ExcludeChannelIDs       []string      `json:"exclude_channel_ids"`
	RegionCode              string        `json:"region_code"`
	CategoryID              string        `json:"category_id"`
	LookbackDays            int           `json:"lookback_days"`
	MaxResults              int           `json:"max_results"`
	OrderBy                 string        `json:"order_by"`
	VideoTypes              []string      `json:"video_types"`
	MinViewCount            int64         `json:"min_view_count"`
	MinLikeCount            int64         `json:"min_like_count"`
	MinCommentCount         int64         `json:"min_comment_count"`
	MinDurationSeconds      int           `json:"min_duration_seconds"`
	MaxDurationSeconds      int           `json:"max_duration_seconds"`
	PublishedAfter          *string       `json:"published_after,omitempty"`
	PublishedBefore         *string       `json:"published_before,omitempty"`
	ScheduleType            string        `json:"schedule_type"`
	ScheduleIntervalMinutes int           `json:"schedule_interval_minutes"`
	RateLimitRequests       int           `json:"rate_limit_requests"`
	AutoAddToTasks          bool          `json:"auto_add_to_tasks"`
	TaskTemplate            TaskTemplate  `json:"task_template"`
}

type UpdateInput struct {
	ExpectedVersion int64 `json:"expected_version"`
	CreateInput
}

type Run struct {
	ID              string         `json:"id"`
	MonitorID       string         `json:"monitor_id"`
	Trigger         string         `json:"trigger"`
	Status          string         `json:"status"`
	ConfigSnapshot  map[string]any `json:"config_snapshot"`
	DiscoveredCount int            `json:"discovered_count"`
	AcceptedCount   int            `json:"accepted_count"`
	DuplicateCount  int            `json:"duplicate_count"`
	TaskCount       int            `json:"task_count"`
	QuotaUnits      int            `json:"quota_units"`
	ErrorCode       string         `json:"error_code"`
	ErrorMessage    string         `json:"error_message"`
	StartedAt       time.Time      `json:"started_at"`
	CompletedAt     *time.Time     `json:"completed_at,omitempty"`
}

type Item struct {
	ID              string     `json:"id"`
	RunID           string     `json:"run_id"`
	ExternalVideoID string     `json:"external_video_id"`
	EpisodeNumber   int        `json:"episode_number"`
	SeriesScopeKey  string     `json:"series_scope_key"`
	SeriesScopeName string     `json:"series_scope_name"`
	SourceURL       string     `json:"source_url"`
	Title           string     `json:"title"`
	ChannelID       string     `json:"channel_id"`
	ChannelTitle    string     `json:"channel_title"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	DurationSeconds int        `json:"duration_seconds"`
	ViewCount       int64      `json:"view_count"`
	LikeCount       int64      `json:"like_count"`
	CommentCount    int64      `json:"comment_count"`
	VideoType       string     `json:"video_type"`
	Decision        string     `json:"decision"`
	DecisionReason  string     `json:"decision_reason"`
	TaskID          *string    `json:"task_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type History struct {
	Monitor Monitor `json:"monitor"`
	Runs    []Run   `json:"runs"`
	Items   []Item  `json:"items"`
}

type DeleteInput struct {
	HistoryMode string `json:"history_mode"`
}

type Candidate struct {
	ExternalVideoID string
	EpisodeNumber   int
	SeriesScopeKey  string
	SeriesScopeName string
	SourceURL       string
	Title           string
	Description     string
	ChannelID       string
	ChannelTitle    string
	PublishedAt     *time.Time
	DurationSeconds int
	ViewCount       int64
	LikeCount       int64
	CommentCount    int64
	VideoType       string
}

type EnqueueItemsInput struct {
	ItemIDs []string `json:"item_ids"`
}

type EnqueueItemResult struct {
	ItemID  string  `json:"item_id"`
	Status  string  `json:"status"`
	TaskID  *string `json:"task_id,omitempty"`
	Message string  `json:"message"`
}

type EnqueueItemsResult struct {
	RequestedCount int                 `json:"requested_count"`
	CreatedCount   int                 `json:"created_count"`
	DuplicateCount int                 `json:"duplicate_count"`
	FailedCount    int                 `json:"failed_count"`
	Items          []EnqueueItemResult `json:"items"`
}

type YouTubeCategory struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Provider string `json:"provider"`
}

type ValidationError struct {
	Fields map[string]string `json:"fields"`
}

func (e *ValidationError) Error() string {
	return "monitor input is invalid"
}

type ConflictError struct {
	Code    string
	Message string
}

func (e *ConflictError) Error() string {
	return e.Message
}
