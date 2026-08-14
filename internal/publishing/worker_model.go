package publishing

import (
	"context"
	"errors"
	"time"
)

var ErrSubmissionUncertain = errors.New("platform submission result is uncertain")

type AssetReference struct {
	ID           string
	Bucket       string
	ObjectKey    string
	OriginalName string
	ContentType  string
	SizeBytes    int64
	Checksum     string
}

type ClaimedPublication struct {
	PlatformPublication
	Account     Account
	Media       AssetReference
	Cover       *AssetReference
	SourceURL   string
	MaxAttempts int
	RetryDelay  time.Duration
	Reconcile   bool
	ClaimMode   string
}

type UploadRequest struct {
	Publication PlatformPublication
	Account     Account
	SourceURL   string
	MediaPath   string
	CoverPath   string
	CookieJar   []byte
	OnStage     func(context.Context, string) error
}

type PublishResult struct {
	RemoteSubmissionID string
	RemoteURL          string
	RemoteStatus       string
	ResponseSummary    map[string]any
}

type ReconcileRequest struct {
	Publication PlatformPublication
	Account     Account
	SourceURL   string
	CookieJar   []byte
}

type PlatformPublisher interface {
	Platform() string
	AuthMode() string
	Version() string
	Publish(context.Context, UploadRequest) (PublishResult, error)
	Reconcile(context.Context, ReconcileRequest) (PublishResult, bool, error)
}

type AdapterError struct {
	Code      string
	Message   string
	Retryable bool
	Uncertain bool
}

func (e *AdapterError) Error() string {
	return e.Message
}
