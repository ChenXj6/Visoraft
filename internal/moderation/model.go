package moderation

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/visoraft/visoraft/internal/settings"
)

const (
	RequestedV1 = "io.visoraft.moderation.requested.v1"
	StartedV1   = "io.visoraft.moderation.started.v1"
	CompletedV1 = "io.visoraft.moderation.completed.v1"
	FailedV1    = "io.visoraft.moderation.failed.v1"
)

const (
	DecisionPass         = "pass"
	DecisionManualReview = "manual_review"
	DecisionBlock        = "block"
)

type TextInput struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

type Command struct {
	TaskID  string `json:"task_id"`
	RunID   string `json:"run_id"`
	Attempt int    `json:"attempt"`
}

type Started struct {
	TaskID   string `json:"task_id"`
	RunID    string `json:"run_id"`
	Attempt  int    `json:"attempt"`
	Provider string `json:"provider"`
}

type Request struct {
	TaskID   string
	Config   settings.ModerationConfig
	Texts    []TextInput
	ImageURL string
	VideoURL string
}

type Finding struct {
	Label       string  `json:"label"`
	Description string  `json:"description,omitempty"`
	RiskLevel   string  `json:"risk_level,omitempty"`
	Confidence  float64 `json:"confidence,omitempty"`
	Location    string  `json:"location,omitempty"`
}

type ChannelResult struct {
	Status     string          `json:"status"`
	Service    string          `json:"service"`
	RiskLevel  string          `json:"risk_level"`
	RequestIDs []string        `json:"request_ids"`
	Findings   []Finding       `json:"findings"`
	Raw        json.RawMessage `json:"raw,omitempty"`
}

type Result struct {
	TaskID    string        `json:"task_id"`
	RunID     string        `json:"run_id"`
	Attempt   int           `json:"attempt"`
	Provider  string        `json:"provider"`
	Decision  string        `json:"decision"`
	RiskLevel string        `json:"risk_level"`
	Text      ChannelResult `json:"text_result"`
	Image     ChannelResult `json:"image_result"`
	Video     ChannelResult `json:"video_result"`
}

type Failure struct {
	TaskID    string        `json:"task_id"`
	RunID     string        `json:"run_id"`
	Attempt   int           `json:"attempt"`
	Provider  string        `json:"provider"`
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Retryable bool          `json:"retryable"`
	Decision  string        `json:"decision"`
	Text      ChannelResult `json:"text_result"`
	Image     ChannelResult `json:"image_result"`
	Video     ChannelResult `json:"video_result"`
}

type Provider interface {
	Moderate(context.Context, Request) (Result, error)
}

type ProviderError struct {
	Code      string
	Message   string
	Retryable bool
	Partial   Result
}

func (e *ProviderError) Error() string {
	return e.Message
}

func DecisionForRisk(
	riskLevel string,
	config settings.ModerationConfig,
) string {
	switch normalizeRisk(riskLevel) {
	case "high":
		return normalizeAction(config.HighRiskAction)
	case "medium":
		return normalizeAction(config.MediumRiskAction)
	default:
		return DecisionPass
	}
}

func FailureDecision(config settings.ModerationConfig) string {
	return normalizeAction(config.FailureAction)
}

func HighestRisk(values ...string) string {
	highest := "none"
	for _, value := range values {
		value = normalizeRisk(value)
		if riskRank(value) > riskRank(highest) {
			highest = value
		}
	}
	return highest
}

func normalizeRisk(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "high":
		return "high"
	case "medium":
		return "medium"
	case "low":
		return "low"
	default:
		return "none"
	}
}

func normalizeAction(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), DecisionBlock) {
		return DecisionBlock
	}
	return DecisionManualReview
}

func riskRank(value string) int {
	switch value {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func skipped(service string) ChannelResult {
	return ChannelResult{
		Status:     "skipped",
		Service:    service,
		RiskLevel:  "none",
		RequestIDs: []string{},
		Findings:   []Finding{},
	}
}
