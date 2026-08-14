package monitors

import (
	"strings"
	"testing"

	"github.com/visoraft/visoraft/internal/tasks"
)

func TestNormalizeListAcceptsCommonChineseSeparators(t *testing.T) {
	got := normalizeList([]string{"张哲瀚、龚俊；周也，赵薇", "龚俊"})
	want := []string{"张哲瀚", "龚俊", "周也", "赵薇"}
	if len(got) != len(want) {
		t.Fatalf("unexpected normalized list: %#v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("unexpected normalized list: %#v", got)
		}
	}
}

func validMonitorInput() CreateInput {
	return CreateInput{
		Name:                    "  产品发布监控  ",
		Enabled:                 true,
		MonitorType:             " SEARCH ",
		ChannelMode:             "search",
		Query:                   "  visoraft ",
		ChannelIDs:              []string{"channel-a,channel-b", "channel-a"},
		IncludeKeywords:         []string{"字幕，工作流"},
		ExcludeKeywords:         []string{"搬运"},
		ExcludeChannelIDs:       []string{},
		RegionCode:              " us ",
		LookbackDays:            7,
		MaxResults:              25,
		OrderBy:                 "date",
		VideoTypes:              []string{"VIDEO", "short"},
		MinDurationSeconds:      10,
		MaxDurationSeconds:      600,
		ScheduleType:            "automatic",
		ScheduleIntervalMinutes: 30,
		RateLimitRequests:       10,
		AutoAddToTasks:          true,
		TaskTemplate: TaskTemplate{
			TargetPlatforms:        []string{"BILIBILI", "acfun"},
			RepostStatementVersion: tasks.StatementFullV1,
		},
	}
}

func TestNormalizeAndValidateMonitorInput(t *testing.T) {
	input := validMonitorInput()
	normalizeInput(&input)
	if err := validateInput(input); err != nil {
		t.Fatalf("expected normalized input to validate: %v", err)
	}
	if input.Name != "产品发布监控" || input.MonitorType != "search" ||
		input.RegionCode != "US" {
		t.Fatalf("unexpected normalized scalar fields: %+v", input)
	}
	if len(input.ChannelIDs) != 2 || len(input.IncludeKeywords) != 2 {
		t.Fatalf("expected list splitting and deduplication: %+v", input)
	}
}

func TestValidateMonitorRejectsInvalidBoundaries(t *testing.T) {
	input := validMonitorInput()
	input.Name = ""
	input.MaxResults = 51
	input.MinDurationSeconds = 600
	input.MaxDurationSeconds = 60
	normalizeInput(&input)
	err := validateInput(input)
	validation, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected validation error, got %T %v", err, err)
	}
	for _, field := range []string{"name", "max_results", "duration"} {
		if _, exists := validation.Fields[field]; !exists {
			t.Fatalf("expected validation field %q in %+v", field, validation.Fields)
		}
	}
}

func TestValidateSeriesMonitorRequiresCompleteEpisodeRangeAndRequestBudget(t *testing.T) {
	input := validMonitorInput()
	input.MonitorType = "series"
	input.ChannelMode = "historical"
	input.Query = ""
	input.SeriesTitle = "还珠格格1"
	input.EpisodeStart = 1
	input.EpisodeEnd = 24
	input.RateLimitRequests = 48
	normalizeInput(&input)
	if err := validateInput(input); err != nil {
		t.Fatalf("expected series input to validate: %v", err)
	}

	input.RateLimitRequests = 1
	err := validateInput(input)
	validation, ok := err.(*ValidationError)
	if !ok || validation.Fields["rate_limit_requests"] == "" {
		t.Fatalf("expected request budget error, got %T %+v", err, err)
	}
}

func TestValidateSeriesMonitorSupportsMultipleGenericScopes(t *testing.T) {
	input := validMonitorInput()
	input.MonitorType = "series"
	input.ChannelMode = "historical"
	input.Query = "赵薇 苏有朋 林心如"
	input.SeriesTitle = "还珠格格"
	input.SeriesScopes = []SeriesScope{
		{Key: "part-1", Name: "第一部", Query: "MY FAIR PRINCESS I", EpisodeStart: 1, EpisodeEnd: 24},
		{Key: "part-2", Name: "第二部", Query: "MY FAIR PRINCESS II", EpisodeStart: 1, EpisodeEnd: 48},
	}
	input.RateLimitRequests = 144
	normalizeInput(&input)
	if err := validateInput(input); err != nil {
		t.Fatalf("expected multi-scope series input to validate: %v", err)
	}
	if input.EpisodeStart != 1 || input.EpisodeEnd != 48 || len(input.SeriesScopes) != 2 {
		t.Fatalf("unexpected normalized series bounds: %+v", input)
	}
}

func TestCandidatePassesAllFilters(t *testing.T) {
	monitor := Monitor{
		VideoTypes:         []string{"video"},
		IncludeKeywords:    []string{"workflow"},
		ExcludeKeywords:    []string{"spam"},
		ExcludeChannelIDs:  []string{"blocked"},
		MinViewCount:       100,
		MinLikeCount:       10,
		MinCommentCount:    2,
		MinDurationSeconds: 30,
		MaxDurationSeconds: 300,
	}
	candidate := Candidate{
		Title:           "A production workflow",
		Description:     "subtitle processing",
		ChannelID:       "allowed",
		VideoType:       "video",
		ViewCount:       1000,
		LikeCount:       100,
		CommentCount:    20,
		DurationSeconds: 120,
	}
	if passed, reason := candidatePasses(monitor, candidate); !passed {
		t.Fatalf("expected candidate to pass, reason=%s", reason)
	}
	candidate.Description = "spam content"
	if passed, reason := candidatePasses(monitor, candidate); passed ||
		!strings.Contains(reason, "排除关键词") {
		t.Fatalf("expected excluded keyword failure, passed=%v reason=%s", passed, reason)
	}
}

func TestItemCanEnqueueDuplicateDiscoveryWithoutTask(t *testing.T) {
	for _, decision := range []string{"accepted", "duplicate", "task_created", "task_failed"} {
		if !itemCanEnqueue(Item{Decision: decision}) {
			t.Fatalf("expected %s item without task to be enqueueable", decision)
		}
	}
	taskID := "existing-task"
	if itemCanEnqueue(Item{Decision: "duplicate", TaskID: &taskID}) {
		t.Fatal("item already linked to a task must not be enqueueable")
	}
	if itemCanEnqueue(Item{Decision: "filtered"}) {
		t.Fatal("filtered item must not be enqueueable")
	}
}

func TestParseISODuration(t *testing.T) {
	tests := map[string]int{
		"PT4S":       4,
		"PT1H2M3S":   3723,
		"P1DT2H3M4S": 93784,
		"invalid":    0,
	}
	for input, want := range tests {
		if got := parseISODuration(input); got != want {
			t.Fatalf("parseISODuration(%q)=%d want %d", input, got, want)
		}
	}
}
