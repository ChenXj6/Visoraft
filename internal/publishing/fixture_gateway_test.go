package publishing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFixtureUncertainScenarioSucceedsAfterOperatorRequeue(t *testing.T) {
	mediaPath := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(mediaPath, []byte("fixture-media"), 0o600); err != nil {
		t.Fatal(err)
	}

	gateway := NewFixtureGateway(PlatformAcFun)
	publication := PlatformPublication{
		Platform:    PlatformAcFun,
		Title:       "本地恢复验收 [fixture:uncertain]",
		Fingerprint: "0123456789abcdef0123456789abcdef",
		Attempt:     1,
	}
	_, err := gateway.Publish(context.Background(), UploadRequest{
		Publication: publication,
		SourceURL:   "http://fixture-provider:8090/media/sample.wav",
		MediaPath:   mediaPath,
	})
	var adapterError *AdapterError
	if !errors.As(err, &adapterError) || !adapterError.Uncertain {
		t.Fatalf("expected first attempt to be uncertain, got %v", err)
	}

	publication.Attempt = 2
	result, err := gateway.Publish(context.Background(), UploadRequest{
		Publication: publication,
		SourceURL:   "http://fixture-provider:8090/media/sample.wav",
		MediaPath:   mediaPath,
	})
	if err != nil {
		t.Fatalf("expected requeued attempt to succeed, got %v", err)
	}
	if result.RemoteSubmissionID != publication.Fingerprint[:16] {
		t.Fatalf("unexpected remote id %q", result.RemoteSubmissionID)
	}
	if result.RemoteStatus != "published_fixture" {
		t.Fatalf("unexpected remote status %q", result.RemoteStatus)
	}
}
