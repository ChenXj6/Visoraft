package moderation

import (
	"context"
	"testing"

	"github.com/visoraft/visoraft/internal/settings"
)

func TestHighestRiskAndConfiguredActions(t *testing.T) {
	config := settings.ModerationConfig{
		HighRiskAction:   "block",
		MediumRiskAction: "manual_review",
	}
	if got := HighestRisk("low", "HIGH", "medium"); got != "high" {
		t.Fatalf("unexpected highest risk %q", got)
	}
	if got := DecisionForRisk("high", config); got != DecisionBlock {
		t.Fatalf("unexpected high-risk decision %q", got)
	}
	if got := DecisionForRisk("medium", config); got != DecisionManualReview {
		t.Fatalf("unexpected medium-risk decision %q", got)
	}
	if got := DecisionForRisk("low", config); got != DecisionPass {
		t.Fatalf("unexpected low-risk decision %q", got)
	}
}

func TestFixtureProviderIsExplicitAndDeterministic(t *testing.T) {
	provider := NewFixtureProvider()
	result, err := provider.Moderate(context.Background(), Request{
		TaskID: "task",
		Config: settings.ModerationConfig{
			CheckText:        true,
			CheckImage:       true,
			CheckVideo:       true,
			TextService:      "fixture-text",
			ImageService:     "fixture-image",
			VideoService:     "fixture-video",
			HighRiskAction:   "block",
			MediumRiskAction: "manual_review",
		},
		Texts: []TextInput{{ID: "title", Content: "[fixture:review] title"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "fixture" ||
		result.RiskLevel != "medium" ||
		result.Decision != DecisionManualReview ||
		result.Text.Status != "completed" ||
		result.Image.Status != "completed" ||
		result.Video.Status != "completed" {
		t.Fatalf("unexpected fixture result: %#v", result)
	}
}
