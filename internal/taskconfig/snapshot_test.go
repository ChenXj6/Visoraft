package taskconfig

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/visoraft/visoraft/internal/settings"
)

func TestApplyFreezesStrategyAndOverridesTranscode(t *testing.T) {
	settingsRaw := []byte(`{
		"review":{"mode":"manual"},
		"transcode":{"enabled":false,"encoder_mode":"cpu","video_codec":"copy"}
	}`)
	presetID := "00000000-0000-4000-8000-000000000001"
	strategyRaw := []byte(`{
		"id":"00000000-0000-4000-8000-000000000002",
		"enabled":true,
		"automation_mode":"automatic_after_review",
		"target_platforms":["acfun","bilibili"],
		"account_bindings":{"acfun":"account-a"},
		"category_bindings":{"acfun":"category-a"},
		"title_templates":{},
		"description_templates":{},
		"default_tags":["转载"],
		"repost_statement_version":"full_v1",
		"transcode_preset_id":"00000000-0000-4000-8000-000000000001",
		"transcode_preset":{
			"id":"00000000-0000-4000-8000-000000000001",
			"available":true,
			"encoder_mode":"cpu",
			"video_codec":"h264",
			"audio_codec":"aac",
			"container":"mp4",
			"cpu_preset":"medium",
			"high_resolution_cpu_preset":"slow",
			"maximum_height":1080,
			"video_bitrate_kbps":4500,
			"audio_bitrate_kbps":192,
			"burn_subtitles":true,
			"custom_arguments":["-movflags","+faststart"]
		},
		"require_content_moderation":true,
		"schedule_mode":"immediate",
		"version":7
	}`)

	result, err := Apply(settingsRaw, strategyRaw)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	var config settings.ConfigSnapshot
	if err := json.Unmarshal(result, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !config.Transcode.Enabled ||
		config.Transcode.VideoCodec != "h264" ||
		!config.Transcode.BurnSubtitles ||
		!config.Transcode.CustomArgumentsEnabled {
		t.Fatalf("unexpected transcode override: %#v", config.Transcode)
	}
	policy, err := Decode(result)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if policy.PostingStrategy == nil ||
		policy.PostingStrategy.TranscodePresetID == nil ||
		*policy.PostingStrategy.TranscodePresetID != presetID ||
		!policy.PostingStrategy.RequireContentModeration ||
		policy.PostingStrategy.Version != 7 {
		t.Fatalf("unexpected frozen policy: %#v", policy.PostingStrategy)
	}
}

func TestApplyRejectsUnavailablePreset(t *testing.T) {
	_, err := Apply(
		[]byte(`{"transcode":{"enabled":false}}`),
		[]byte(`{
			"id":"strategy",
			"transcode_preset_id":"preset",
			"transcode_preset":{"id":"preset","available":false}
		}`),
	)
	if !errors.Is(err, ErrPresetUnavailable) {
		t.Fatalf("expected ErrPresetUnavailable, got %v", err)
	}
}

func TestApplyWithoutStrategyPreservesGlobalSnapshot(t *testing.T) {
	input := []byte(`{"transcode":{"enabled":true}}`)
	result, err := Apply(input, nil)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if string(result) != string(input) {
		t.Fatalf("expected snapshot to remain byte-for-byte stable")
	}
}
