package tasks

import (
	"testing"
	"time"
)

func TestNormalizeDownloadProgressPreservesTelemetry(t *testing.T) {
	eta := 42
	progress := DownloadProgress{
		TaskID:               "00000000-0000-4000-8000-000000000001",
		Attempt:              1,
		Progress:             76,
		Phase:                " downloading ",
		DownloadedBytes:      150_000_000,
		TotalBytes:           120_000_000,
		TotalBytesIsEstimate: true,
		SpeedBytesPerSecond:  512_000,
		ETASeconds:           &eta,
	}

	if err := normalizeDownloadProgress(&progress); err != nil {
		t.Fatalf("normalize download progress: %v", err)
	}
	if progress.Phase != "downloading" {
		t.Fatalf("unexpected phase %q", progress.Phase)
	}
	if progress.DownloadedBytes != 150_000_000 ||
		!progress.TotalBytesIsEstimate ||
		progress.SpeedBytesPerSecond != 512_000 {
		t.Fatalf("telemetry was not preserved: %+v", progress)
	}
}

func TestNormalizeDownloadProgressRejectsNegativeMetrics(t *testing.T) {
	progress := DownloadProgress{
		Progress:        50,
		Phase:           "downloading",
		DownloadedBytes: -1,
	}

	if err := normalizeDownloadProgress(&progress); err == nil {
		t.Fatal("expected negative byte count to be rejected")
	}
}

func TestAnnotateStepActivitySeparatesLegacyAndStalledDownloads(t *testing.T) {
	now := time.Date(2026, 7, 29, 7, 0, 0, 0, time.UTC)
	steps := []Step{
		{
			Kind:      StepDownload,
			Status:    "running",
			Detail:    map[string]any{"phase": "downloading"},
			UpdatedAt: now.Add(-90 * time.Second),
		},
		{
			Kind:      StepDownload,
			Status:    "running",
			Detail:    map[string]any{},
			UpdatedAt: now.Add(-10 * time.Minute),
		},
	}

	annotateStepActivity(steps, now)

	if steps[0].ActivityState != "stalled" ||
		steps[0].HeartbeatAgeSeconds != 90 {
		t.Fatalf("expected stalled telemetry, got %+v", steps[0])
	}
	if steps[1].ActivityState != "telemetry_pending" {
		t.Fatalf("legacy download must not be falsely marked stalled: %+v", steps[1])
	}
}
