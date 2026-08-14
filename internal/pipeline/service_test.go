package pipeline

import (
	"testing"

	"github.com/visoraft/visoraft/internal/settings"
	"github.com/visoraft/visoraft/internal/taskconfig"
	"github.com/visoraft/visoraft/internal/tasks"
)

func TestNextStageWithoutBurningTranscodesBeforeSubtitles(t *testing.T) {
	snapshot := settings.ConfigSnapshot{
		Subtitle: settings.SubtitleConfig{Enabled: true},
		Transcode: settings.TranscodeConfig{
			Enabled:       true,
			BurnSubtitles: false,
		},
	}
	if actual := nextStage(snapshot, taskconfig.Policy{}, checkpointMediaReady); actual != tasks.StepTranscode {
		t.Fatalf("expected transcode after media, got %q", actual)
	}
	if actual := nextStage(snapshot, taskconfig.Policy{}, checkpointTranscodeReady); actual != tasks.StepSubtitles {
		t.Fatalf("expected subtitles after transcode, got %q", actual)
	}
	if actual := nextStage(snapshot, taskconfig.Policy{}, checkpointSubtitlesReady); actual != "" {
		t.Fatalf("expected review after subtitles, got %q", actual)
	}
}

func TestNextStageWithBurningCreatesSubtitlesBeforeTranscode(t *testing.T) {
	snapshot := settings.ConfigSnapshot{
		Subtitle: settings.SubtitleConfig{Enabled: true},
		Transcode: settings.TranscodeConfig{
			Enabled:       true,
			BurnSubtitles: true,
		},
	}
	if actual := nextStage(snapshot, taskconfig.Policy{}, checkpointMediaReady); actual != tasks.StepSubtitles {
		t.Fatalf("expected subtitles after media, got %q", actual)
	}
	if actual := nextStage(snapshot, taskconfig.Policy{}, checkpointSubtitlesReady); actual != tasks.StepTranscode {
		t.Fatalf("expected transcode after subtitles, got %q", actual)
	}
	if actual := nextStage(snapshot, taskconfig.Policy{}, checkpointTranscodeReady); actual != "" {
		t.Fatalf("expected review after transcode, got %q", actual)
	}
}

func TestNextStageSkipsDisabledProcessing(t *testing.T) {
	snapshot := settings.ConfigSnapshot{}
	if actual := nextStage(snapshot, taskconfig.Policy{}, checkpointMediaReady); actual != "" {
		t.Fatalf("expected review, got %q", actual)
	}
}

func TestNextStageAlwaysRunsModerationBeforeReview(t *testing.T) {
	snapshot := settings.ConfigSnapshot{
		Moderation: settings.ModerationConfig{Enabled: true},
	}
	if actual := nextStage(
		snapshot,
		taskconfig.Policy{},
		checkpointMediaReady,
	); actual != tasks.StepModeration {
		t.Fatalf("expected moderation after media, got %q", actual)
	}
	if actual := nextStage(
		snapshot,
		taskconfig.Policy{},
		checkpointModerationReady,
	); actual != "" {
		t.Fatalf("expected review after moderation, got %q", actual)
	}
}

func TestPostingStrategyCannotBypassRequiredModeration(t *testing.T) {
	policy := taskconfig.Policy{
		PostingStrategy: &taskconfig.PostingStrategySnapshot{
			RequireContentModeration: true,
		},
	}
	if actual := nextStage(
		settings.ConfigSnapshot{},
		policy,
		checkpointMediaReady,
	); actual != tasks.StepModeration {
		t.Fatalf("expected required moderation, got %q", actual)
	}
}
