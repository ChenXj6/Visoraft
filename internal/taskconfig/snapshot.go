package taskconfig

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/visoraft/visoraft/internal/settings"
)

var ErrPresetUnavailable = errors.New("task transcode preset is unavailable")

type TranscodePresetSnapshot struct {
	ID                      string   `json:"id"`
	Available               bool     `json:"available"`
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

func (p TranscodePresetSnapshot) Config() settings.TranscodeConfig {
	return settings.TranscodeConfig{
		Enabled:                 true,
		EncoderMode:             p.EncoderMode,
		VideoCodec:              p.VideoCodec,
		AudioCodec:              p.AudioCodec,
		Container:               p.Container,
		CPUPreset:               p.CPUPreset,
		HighResolutionCPUPreset: p.HighResolutionCPUPreset,
		MaximumHeight:           p.MaximumHeight,
		VideoBitrateKbps:        p.VideoBitrateKbps,
		AudioBitrateKbps:        p.AudioBitrateKbps,
		BurnSubtitles:           p.BurnSubtitles,
		CustomArgumentsEnabled:  len(p.CustomArguments) > 0,
		CustomArguments:         append([]string(nil), p.CustomArguments...),
	}
}

type PostingStrategySnapshot struct {
	ID                       string                   `json:"id"`
	Enabled                  bool                     `json:"enabled"`
	AutomationMode           string                   `json:"automation_mode"`
	TargetPlatforms          []string                 `json:"target_platforms"`
	AccountBindings          map[string]string        `json:"account_bindings"`
	CategoryBindings         map[string]string        `json:"category_bindings"`
	TitleTemplates           map[string]string        `json:"title_templates"`
	DescriptionTemplates     map[string]string        `json:"description_templates"`
	DefaultTags              []string                 `json:"default_tags"`
	RepostStatementVersion   string                   `json:"repost_statement_version"`
	TranscodePresetID        *string                  `json:"transcode_preset_id,omitempty"`
	TranscodePreset          *TranscodePresetSnapshot `json:"transcode_preset,omitempty"`
	RequireContentModeration bool                     `json:"require_content_moderation"`
	ScheduleMode             string                   `json:"schedule_mode"`
	ScheduleTime             *string                  `json:"schedule_time,omitempty"`
	Version                  int64                    `json:"version"`
}

type Policy struct {
	PostingStrategy *PostingStrategySnapshot `json:"posting_strategy,omitempty"`
}

type policyEnvelope struct {
	TaskPolicy Policy `json:"task_policy"`
}

func DecodeStrategy(raw []byte) (PostingStrategySnapshot, error) {
	var strategy PostingStrategySnapshot
	if len(raw) == 0 {
		return strategy, errors.New("posting strategy snapshot is empty")
	}
	if err := json.Unmarshal(raw, &strategy); err != nil {
		return strategy, fmt.Errorf("decode posting strategy snapshot: %w", err)
	}
	if strategy.ID == "" {
		return strategy, errors.New("posting strategy snapshot has no id")
	}
	return strategy, nil
}

func Decode(raw []byte) (Policy, error) {
	var envelope policyEnvelope
	if len(raw) == 0 {
		return envelope.TaskPolicy, nil
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Policy{}, fmt.Errorf("decode task policy snapshot: %w", err)
	}
	return envelope.TaskPolicy, nil
}

func Apply(settingsRaw, strategyRaw []byte) ([]byte, error) {
	if len(strategyRaw) == 0 {
		return append([]byte(nil), settingsRaw...), nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(settingsRaw, &root); err != nil {
		return nil, fmt.Errorf("decode task settings snapshot: %w", err)
	}
	strategy, err := DecodeStrategy(strategyRaw)
	if err != nil {
		return nil, err
	}
	if strategy.TranscodePresetID != nil {
		if strategy.TranscodePreset == nil ||
			strategy.TranscodePreset.ID != *strategy.TranscodePresetID ||
			!strategy.TranscodePreset.Available {
			return nil, ErrPresetUnavailable
		}
		transcodeRaw, err := json.Marshal(strategy.TranscodePreset.Config())
		if err != nil {
			return nil, fmt.Errorf("encode task transcode preset: %w", err)
		}
		root["transcode"] = transcodeRaw
	}
	policyRaw, err := json.Marshal(Policy{PostingStrategy: &strategy})
	if err != nil {
		return nil, fmt.Errorf("encode task policy snapshot: %w", err)
	}
	root["task_policy"] = policyRaw
	result, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("encode task settings snapshot: %w", err)
	}
	return result, nil
}
