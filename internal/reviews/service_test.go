package reviews

import (
	"context"
	"reflect"
	"testing"

	"github.com/visoraft/visoraft/internal/moderation"
	"github.com/visoraft/visoraft/internal/settings"
)

type subtitleArtifactStorageFixture struct {
	objects map[string][]byte
}

func (s *subtitleArtifactStorageFixture) Put(
	_ context.Context,
	_ string,
	objectKey string,
	_ string,
	content []byte,
) error {
	s.objects[objectKey] = append([]byte(nil), content...)
	return nil
}

func TestEvaluateRulesPassesCompleteTask(t *testing.T) {
	duration := 120
	qcScore := 94.5
	results := evaluateRules(
		settings.AutomaticReviewRules{
			RequireMedia:           true,
			RequireTitle:           true,
			MinimumDescription:     10,
			MaximumDurationSeconds: 180,
			RequireSubtitleQC:      true,
			MinimumSubtitleQCScore: 90,
		},
		1,
		"验收标题",
		"这是一段足够长的任务简介",
		&duration,
		&qcScore,
	)

	if len(results) != 5 {
		t.Fatalf("expected five rule results, got %d", len(results))
	}
	for _, result := range results {
		if !result.Passed {
			t.Fatalf("expected %s to pass: %+v", result.Key, result)
		}
	}
}

func TestEvaluateRulesReportsEveryFailure(t *testing.T) {
	duration := 300
	qcScore := 40.0
	results := evaluateRules(
		settings.AutomaticReviewRules{
			RequireMedia:           true,
			RequireTitle:           true,
			MinimumDescription:     20,
			MaximumDurationSeconds: 60,
			RequireSubtitleQC:      true,
			MinimumSubtitleQCScore: 80,
		},
		0,
		" ",
		"过短",
		&duration,
		&qcScore,
	)

	got := make([]string, 0, len(results))
	for _, result := range results {
		if result.Passed {
			t.Fatalf("expected %s to fail: %+v", result.Key, result)
		}
		got = append(got, result.Key)
	}
	want := []string{
		"media_present",
		"title_present",
		"description_length",
		"duration_limit",
		"subtitle_qc",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected failed rule order: got %v want %v", got, want)
	}
}

func TestModerationRulePassesOnlyExplicitPass(t *testing.T) {
	result, forceManual := evaluateModerationRule(ModerationRun{
		ID:        "run",
		Provider:  "aliyun",
		Status:    "passed",
		RiskLevel: "none",
		Decision:  moderation.DecisionPass,
	}, true)
	if !result.Passed || forceManual {
		t.Fatalf("unexpected pass rule: %+v force_manual=%t", result, forceManual)
	}
}

func TestModerationRuleForcesManualOnProviderFallback(t *testing.T) {
	result, forceManual := evaluateModerationRule(ModerationRun{
		ID:        "run",
		Provider:  "aliyun",
		Status:    "failed",
		RiskLevel: "none",
		Decision:  moderation.DecisionManualReview,
	}, true)
	if result.Passed || !forceManual {
		t.Fatalf("unexpected manual rule: %+v force_manual=%t", result, forceManual)
	}
}

func TestModerationRuleDoesNotConvertBlockToManual(t *testing.T) {
	result, forceManual := evaluateModerationRule(ModerationRun{
		ID:        "run",
		Provider:  "aliyun",
		Status:    "rejected",
		RiskLevel: "high",
		Decision:  moderation.DecisionBlock,
	}, true)
	if result.Passed || forceManual {
		t.Fatalf("unexpected block rule: %+v force_manual=%t", result, forceManual)
	}
}

func TestNormalizeTagsSplitsAndDeduplicates(t *testing.T) {
	got := normalizeTags([]string{"科技,字幕", "字幕，自动化", "  科技  "})
	want := []string{"科技", "字幕", "自动化"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected normalized tags: got %v want %v", got, want)
	}
}

func TestNormalizeSubtitleSegmentsAndQualityReport(t *testing.T) {
	segments, fields := normalizeSubtitleSegments([]Segment{
		{Index: 99, Start: 0, End: 1, Text: "  第一条  "},
		{Index: 42, Start: 0.5, End: 0.7, Text: "字符速率很高字符速率很高"},
	})
	if len(fields) != 0 {
		t.Fatalf("expected valid subtitle segments, got fields: %+v", fields)
	}
	if segments[0].Index != 1 || segments[1].Index != 2 ||
		segments[0].Text != "第一条" {
		t.Fatalf("unexpected normalized segments: %+v", segments)
	}
	report := subtitleQualityReport(segments, 90, 0.7)
	if report["passed"] != false ||
		report["overlap_count"] != 1 ||
		report["high_cps_count"] != 1 {
		t.Fatalf("expected overlap/high-CPS QC failure, got %+v", report)
	}
}

func TestNormalizeSubtitleSegmentsRejectsInvalidTimingAndText(t *testing.T) {
	_, fields := normalizeSubtitleSegments([]Segment{
		{Start: 2, End: 1, Text: " "},
	})
	for _, key := range []string{"segments.0.text", "segments.0.end"} {
		if _, exists := fields[key]; !exists {
			t.Fatalf("expected validation field %q in %+v", key, fields)
		}
	}
}

func TestEditedSubtitleArtifactsAreVersionedAndRenderable(t *testing.T) {
	storage := &subtitleArtifactStorageFixture{objects: map[string][]byte{}}
	service := &Service{storage: storage, storageBucket: "media"}
	segments := []Segment{
		{Index: 1, Start: 1.25, End: 2.5, Text: "第一条字幕"},
		{Index: 2, Start: 3, End: 4.125, Text: "第二条字幕"},
	}
	artifacts, err := service.uploadSubtitleArtifacts(
		context.Background(),
		"00000000-0000-4000-8000-000000000001",
		"translated",
		3,
		segments,
		map[string]any{"score": 100, "passed": true},
	)
	if err != nil {
		t.Fatalf("upload subtitle artifacts: %v", err)
	}
	if len(artifacts) != 3 || len(storage.objects) != 3 {
		t.Fatalf("expected three persisted artifacts, got %+v", artifacts)
	}
	vttKey := "tasks/00000000-0000-4000-8000-000000000001/subtitles/review/translated-v3.vtt"
	wantVTT := "WEBVTT\n\n1\n00:00:01.250 --> 00:00:02.500\n第一条字幕\n\n" +
		"2\n00:00:03.000 --> 00:00:04.125\n第二条字幕\n\n"
	if got := string(storage.objects[vttKey]); got != wantVTT {
		t.Fatalf("unexpected edited VTT:\n%s", got)
	}
	for _, artifact := range artifacts {
		if len(artifact.checksum) != 64 || artifact.objectKey == "" {
			t.Fatalf("artifact lacks checksum or key: %+v", artifact)
		}
	}
}
