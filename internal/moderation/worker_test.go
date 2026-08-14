package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/visoraft/visoraft/internal/events"
	"github.com/visoraft/visoraft/internal/settings"
)

type fakeConfigLoader struct {
	config settings.ProcessingConfig
	err    error
}

func (f fakeConfigLoader) ProcessingConfig(
	context.Context,
	string,
) (settings.ProcessingConfig, error) {
	return f.config, f.err
}

type fakePresigner struct {
	values []string
	calls  int
}

func (f *fakePresigner) PresignGet(
	string,
	string,
	time.Duration,
) (string, error) {
	if f.calls >= len(f.values) {
		return "", errors.New("unexpected presign call")
	}
	value := f.values[f.calls]
	f.calls++
	return value, nil
}

type publishedEvent struct {
	eventType string
	envelope  events.Envelope
}

type fakeEventPublisher struct {
	events []publishedEvent
}

func (f *fakeEventPublisher) Publish(
	_ context.Context,
	eventType string,
	_ string,
	raw []byte,
) error {
	envelope, err := events.Decode(raw)
	if err != nil {
		return err
	}
	f.events = append(f.events, publishedEvent{
		eventType: eventType,
		envelope:  envelope,
	})
	return nil
}

type capturingProvider struct {
	request Request
	result  Result
	err     error
}

func (p *capturingProvider) Moderate(
	_ context.Context,
	request Request,
) (Result, error) {
	p.request = request
	return p.result, p.err
}

func TestWorkerPublishesStartedAndCompletedWithPreparedInputs(t *testing.T) {
	taskID := "5a1a269c-c05a-4d86-af44-ef077e2a1982"
	runID := "fc4c4146-460c-43db-8bef-a9c24137e59b"
	config := settings.ProcessingConfig{
		ConfigSnapshot: settings.ConfigSnapshot{
			Moderation: settings.ModerationConfig{
				Enabled:                 true,
				Provider:                "aliyun",
				CheckText:               true,
				CheckImage:              true,
				CheckVideo:              true,
				TextService:             "pgc_detection",
				ImageService:            "baselineCheck",
				VideoService:            "videoDetection",
				HighRiskAction:          "block",
				MediumRiskAction:        "manual_review",
				FailureAction:           "manual_review",
				RequestTimeoutSeconds:   30,
				VideoMaximumWaitSeconds: 900,
			},
		},
		Runtime: settings.TaskRuntime{
			Title:           "标题",
			Description:     "简介",
			Tags:            []string{"标签一", "标签二"},
			RepostStatement: "转载说明",
			CoverAsset: &settings.RuntimeAsset{
				Bucket:    "media",
				ObjectKey: "cover.jpg",
			},
			FinalMediaAsset: &settings.RuntimeAsset{
				Bucket:    "media",
				ObjectKey: "video.mp4",
			},
		},
	}
	presigner := &fakePresigner{values: []string{
		"https://media.example.test/cover.jpg?token=one",
		"https://media.example.test/video.mp4?token=two",
	}}
	provider := &capturingProvider{result: Result{
		Provider:  "aliyun",
		Decision:  DecisionPass,
		RiskLevel: "none",
		Text:      completedChannel("pgc_detection"),
		Image:     completedChannel("baselineCheck"),
		Video:     completedChannel("videoDetection"),
	}}
	worker := NewWorker(
		"",
		"",
		fakeConfigLoader{config: config},
		presigner,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	worker.providers = func(
		settings.ModerationConfig,
		map[string]string,
	) (Provider, error) {
		return provider, nil
	}
	publisher := &fakeEventPublisher{}
	if err := worker.handle(
		context.Background(),
		moderationCommandEnvelope(t, taskID, runID),
		publisher,
	); err != nil {
		t.Fatal(err)
	}
	if len(publisher.events) != 2 ||
		publisher.events[0].eventType != StartedV1 ||
		publisher.events[1].eventType != CompletedV1 {
		t.Fatalf("unexpected events: %#v", publisher.events)
	}
	if provider.request.ImageURL == "" ||
		provider.request.VideoURL == "" ||
		len(provider.request.Texts) != 4 {
		t.Fatalf("provider input was incomplete: %#v", provider.request)
	}
	var result Result
	if err := json.Unmarshal(publisher.events[1].envelope.Data, &result); err != nil {
		t.Fatal(err)
	}
	if result.TaskID != taskID || result.RunID != runID || result.Attempt != 1 {
		t.Fatalf("result identity was not attached: %#v", result)
	}
}

func TestWorkerTurnsProviderFailureIntoConfiguredManualReview(t *testing.T) {
	taskID := "5a1a269c-c05a-4d86-af44-ef077e2a1982"
	runID := "fc4c4146-460c-43db-8bef-a9c24137e59b"
	config := settings.ProcessingConfig{
		ConfigSnapshot: settings.ConfigSnapshot{
			Moderation: settings.ModerationConfig{
				Enabled:       true,
				Provider:      "fixture",
				FailureAction: DecisionManualReview,
			},
		},
	}
	provider := &capturingProvider{err: &ProviderError{
		Code:      "provider_timeout",
		Message:   "provider timed out",
		Retryable: true,
		Partial: Result{
			Provider: "fixture",
			Text:     completedChannel("fixture-text"),
		},
	}}
	worker := NewWorker(
		"",
		"",
		fakeConfigLoader{config: config},
		&fakePresigner{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	worker.providers = func(
		settings.ModerationConfig,
		map[string]string,
	) (Provider, error) {
		return provider, nil
	}
	publisher := &fakeEventPublisher{}
	if err := worker.handle(
		context.Background(),
		moderationCommandEnvelope(t, taskID, runID),
		publisher,
	); err != nil {
		t.Fatal(err)
	}
	if len(publisher.events) != 2 ||
		publisher.events[1].eventType != FailedV1 {
		t.Fatalf("unexpected events: %#v", publisher.events)
	}
	var failure Failure
	if err := json.Unmarshal(publisher.events[1].envelope.Data, &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Decision != DecisionManualReview ||
		!failure.Retryable ||
		failure.Text.Status != "completed" {
		t.Fatalf("unexpected failure event: %#v", failure)
	}
}

func moderationCommandEnvelope(
	t *testing.T,
	taskID string,
	runID string,
) []byte {
	t.Helper()
	envelope, err := events.New(
		RequestedV1,
		"visoraft/workflow-consumer",
		"task/"+taskID,
		time.Now(),
		Command{TaskID: taskID, RunID: runID, Attempt: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
