package tasks

import (
	"testing"
	"time"
)

func TestValidateAndNormalizeMonitorOrigin(t *testing.T) {
	input := CreateInput{
		SourceURL:              "https://www.youtube.com/watch?v=video",
		TargetPlatforms:        []string{"bilibili"},
		RepostStatementVersion: StatementBriefV1,
		Origin: &TaskOrigin{
			Kind:            " MONITOR ",
			MonitorID:       "8102d777-6cd7-4197-9e26-85a241491c83",
			MonitorName:     " 节目监控 ",
			SeriesTitle:     " 节目 ",
			SeriesScopeKey:  " part-2 ",
			SeriesScopeName: " 第二部 ",
			EpisodeNumber:   13,
		},
	}
	normalized, err := validateAndNormalize(input, time.Now())
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if normalized.Origin == nil || normalized.Origin.Kind != "monitor" {
		t.Fatalf("monitor origin was not preserved: %#v", normalized.Origin)
	}
	if normalized.Origin.MonitorName != "节目监控" || normalized.Origin.SeriesScopeName != "第二部" {
		t.Fatalf("origin labels were not normalized: %#v", normalized.Origin)
	}
}

func TestValidateAndNormalizeRejectsInvalidMonitorOrigin(t *testing.T) {
	_, err := validateAndNormalize(CreateInput{
		SourceURL:              "https://www.youtube.com/watch?v=video",
		TargetPlatforms:        []string{"bilibili"},
		RepostStatementVersion: StatementBriefV1,
		Origin:                 &TaskOrigin{Kind: "monitor", MonitorID: "invalid", EpisodeNumber: -1},
	}, time.Now())
	validation, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected validation error, got %T %v", err, err)
	}
	if validation.Fields["origin"] == "" || validation.Fields["origin_episode_number"] == "" {
		t.Fatalf("missing origin validation fields: %#v", validation.Fields)
	}
}
