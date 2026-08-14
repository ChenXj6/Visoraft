package tasks

import (
	"errors"
	"testing"
)

func TestValidateArchiveInputRequiresVersionAndReason(t *testing.T) {
	err := validateArchiveInput(0, "短")
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if validation.Fields["expected_version"] == "" {
		t.Fatalf("expected version error, got %+v", validation.Fields)
	}
	if validation.Fields["reason"] == "" {
		t.Fatalf("expected reason error, got %+v", validation.Fields)
	}
}

func TestValidateArchiveInputAcceptsAuditedRequest(t *testing.T) {
	if err := validateArchiveInput(4, "人工确认移入任务回收站"); err != nil {
		t.Fatalf("expected valid archive input, got %v", err)
	}
}

func TestArchivableTaskStatusExcludesRunningWork(t *testing.T) {
	for _, status := range []string{
		StatusQueued,
		StatusFetchingMetadata,
		StatusMetadataReady,
		StatusDownloading,
		StatusProcessing,
		StatusPublishing,
	} {
		if archivableTaskStatus(status) {
			t.Fatalf("expected %s to require cancellation", status)
		}
	}
	for _, status := range []string{
		StatusAwaitingReview,
		StatusReadyToPublish,
		StatusPublished,
		StatusFailed,
		StatusCancelled,
		"reconciled",
		"abandoned",
	} {
		if !archivableTaskStatus(status) {
			t.Fatalf("expected %s to be archivable", status)
		}
	}
}
