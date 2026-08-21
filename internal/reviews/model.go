package reviews

import (
	"time"

	"github.com/visoraft/visoraft/internal/moderation"
	"github.com/visoraft/visoraft/internal/tasks"
)

type RuleResult struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Passed   bool   `json:"passed"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
	Message  string `json:"message"`
}

type Run struct {
	ID            string       `json:"id"`
	TaskID        string       `json:"task_id"`
	Mode          string       `json:"mode"`
	PolicyVersion int64        `json:"policy_version"`
	Status        string       `json:"status"`
	Decision      string       `json:"decision"`
	RuleResults   []RuleResult `json:"rule_results"`
	Summary       string       `json:"summary"`
	StartedAt     time.Time    `json:"started_at"`
	CompletedAt   *time.Time   `json:"completed_at,omitempty"`
}

type Action struct {
	ID              string         `json:"id"`
	TaskID          string         `json:"task_id"`
	ReviewRunID     *string        `json:"review_run_id,omitempty"`
	Action          string         `json:"action"`
	ActorType       string         `json:"actor_type"`
	ActorID         string         `json:"actor_id"`
	Reason          string         `json:"reason"`
	MetadataVersion *int           `json:"metadata_version,omitempty"`
	Payload         map[string]any `json:"payload"`
	CreatedAt       time.Time      `json:"created_at"`
}

type SubtitleDocument struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Language  string         `json:"language"`
	Version   int            `json:"version"`
	Segments  []Segment      `json:"segments"`
	QCReport  map[string]any `json:"qc_report"`
	Source    string         `json:"source"`
	CreatedAt time.Time      `json:"created_at"`
}

type ModerationRun struct {
	ID             string                   `json:"id"`
	Provider       string                   `json:"provider"`
	Status         string                   `json:"status"`
	Attempt        int                      `json:"attempt"`
	PolicySnapshot map[string]any           `json:"policy_snapshot"`
	Text           moderation.ChannelResult `json:"text_result"`
	Image          moderation.ChannelResult `json:"image_result"`
	Video          moderation.ChannelResult `json:"video_result"`
	RiskLevel      string                   `json:"risk_level"`
	Decision       string                   `json:"decision"`
	ErrorCode      string                   `json:"error_code"`
	ErrorMessage   string                   `json:"error_message"`
	StartedAt      *time.Time               `json:"started_at,omitempty"`
	CompletedAt    *time.Time               `json:"completed_at,omitempty"`
	CreatedAt      time.Time                `json:"created_at"`
	UpdatedAt      time.Time                `json:"updated_at"`
}

type Segment struct {
	Index int     `json:"index"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type Detail struct {
	Task           tasks.Task         `json:"task"`
	Runs           []Run              `json:"runs"`
	Actions        []Action           `json:"actions"`
	Subtitles      []SubtitleDocument `json:"subtitles"`
	ModerationRuns []ModerationRun    `json:"moderation_runs"`
}

type MetadataInput struct {
	Title                  string   `json:"title"`
	Description            string   `json:"description"`
	Tags                   []string `json:"tags"`
	Category               string   `json:"category"`
	RepostStatementVersion string   `json:"repost_statement_version,omitempty"`
	Reason                 string   `json:"reason"`
}

type SubtitleInput struct {
	ExpectedVersion int       `json:"expected_version"`
	Segments        []Segment `json:"segments"`
	Reason          string    `json:"reason"`
}

type ActionInput struct {
	Reason       string `json:"reason"`
	DeleteAssets bool   `json:"delete_assets"`
}

type ValidationError struct {
	Fields map[string]string `json:"fields"`
}

func (e *ValidationError) Error() string {
	return "review input is invalid"
}

type ConflictError struct {
	Code    string
	Message string
}

func (e *ConflictError) Error() string {
	return e.Message
}
