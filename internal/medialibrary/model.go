package medialibrary

import "time"

type Settings struct {
	HostPath          string    `json:"host_path"`
	RequestedHostPath string    `json:"requested_host_path"`
	AutoSync          bool      `json:"auto_sync"`
	Writable          bool      `json:"writable"`
	RestartRequired   bool      `json:"restart_required"`
	Version           int64     `json:"version"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type UpdateSettingsInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	HostPath        string `json:"host_path"`
	AutoSync        bool   `json:"auto_sync"`
}

type Asset struct {
	ID             string     `json:"id"`
	TaskID         string     `json:"task_id"`
	Kind           string     `json:"kind"`
	OriginalName   string     `json:"original_name"`
	ContentType    string     `json:"content_type"`
	SizeBytes      int64      `json:"size_bytes"`
	ChecksumSHA256 string     `json:"checksum_sha256"`
	AssetStatus    string     `json:"asset_status"`
	AssetDeletedAt *time.Time `json:"asset_deleted_at,omitempty"`
	LocalStatus    string     `json:"local_status"`
	RelativePath   string     `json:"relative_path"`
	AbsolutePath   string     `json:"absolute_path"`
	LocalSizeBytes int64      `json:"local_size_bytes"`
	MaterializedAt *time.Time `json:"materialized_at,omitempty"`
	LastVerifiedAt *time.Time `json:"last_verified_at,omitempty"`
	MissingAt      *time.Time `json:"missing_at,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type TaskFolder struct {
	TaskID         string    `json:"task_id"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	Archived       bool      `json:"archived"`
	EpisodeNumber  int       `json:"episode_number,omitempty"`
	SeriesScopeKey string    `json:"series_scope_key,omitempty"`
	SeriesScope    string    `json:"series_scope,omitempty"`
	RelativePath   string    `json:"relative_path"`
	AbsolutePath   string    `json:"absolute_path"`
	UpdatedAt      time.Time `json:"updated_at"`
	FileCount      int       `json:"file_count"`
	AvailableCount int       `json:"available_count"`
	MissingCount   int       `json:"missing_count"`
	PendingCount   int       `json:"pending_count"`
	TotalBytes     int64     `json:"total_bytes"`
	LocalBytes     int64     `json:"local_bytes"`
	Files          []Asset   `json:"files"`
}

type Collection struct {
	Key            string       `json:"key"`
	Kind           string       `json:"kind"`
	Title          string       `json:"title"`
	MonitorID      string       `json:"monitor_id,omitempty"`
	SeriesTitle    string       `json:"series_title,omitempty"`
	FolderCount    int          `json:"folder_count"`
	FileCount      int          `json:"file_count"`
	AvailableCount int          `json:"available_count"`
	MissingCount   int          `json:"missing_count"`
	PendingCount   int          `json:"pending_count"`
	TotalBytes     int64        `json:"total_bytes"`
	LocalBytes     int64        `json:"local_bytes"`
	Folders        []TaskFolder `json:"folders"`
}

type Library struct {
	Settings        Settings     `json:"settings"`
	CollectionCount int          `json:"collection_count"`
	FolderCount     int          `json:"folder_count"`
	FileCount       int          `json:"file_count"`
	AvailableCount  int          `json:"available_count"`
	MissingCount    int          `json:"missing_count"`
	PendingCount    int          `json:"pending_count"`
	TotalBytes      int64        `json:"total_bytes"`
	LocalBytes      int64        `json:"local_bytes"`
	Collections     []Collection `json:"collections"`
}

type record struct {
	Asset
	Bucket          string
	ObjectKey       string
	TaskTitle       string
	OriginalTitle   string
	TaskStatus      string
	TaskArchivedAt  *time.Time
	TaskUpdatedAt   time.Time
	OriginKind      string
	MonitorID       string
	MonitorName     string
	SeriesTitle     string
	SeriesScopeKey  string
	SeriesScopeName string
	EpisodeNumber   int
}

type ConflictError struct {
	Code    string
	Message string
}

func (e *ConflictError) Error() string { return e.Message }

type ValidationError struct {
	Fields map[string]string `json:"fields"`
}

func (e *ValidationError) Error() string { return "local library input is invalid" }
