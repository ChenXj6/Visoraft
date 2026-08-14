package cookieprofiles

import (
	"context"
	"errors"
	"time"
)

const (
	KindUpload      = "upload"
	KindCookieCloud = "cookiecloud"

	StatusReady   = "ready"
	StatusSyncing = "syncing"
	StatusError   = "error"
)

var ErrNotFound = errors.New("cookie profile not found")

type ValidationError struct {
	Fields map[string]string `json:"fields"`
}

func (e *ValidationError) Error() string {
	return "cookie profile input is invalid"
}

type Profile struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Kind             string     `json:"kind"`
	Status           string     `json:"status"`
	ServerURL        string     `json:"server_url,omitempty"`
	SourceFilename   string     `json:"source_filename,omitempty"`
	CookieCount      int        `json:"cookie_count"`
	DomainCount      int        `json:"domain_count"`
	HasUsableCookies bool       `json:"has_usable_cookies"`
	LastSyncedAt     *time.Time `json:"last_synced_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type record struct {
	Profile
	EncryptedCookieJar        []byte
	EncryptedCloudCredentials []byte
}

type CookieCloudInput struct {
	Name      string `json:"name"`
	ServerURL string `json:"server_url"`
	UUID      string `json:"uuid"`
	Password  string `json:"password"`
}

type cloudCredentials struct {
	UUID     string `json:"uuid"`
	Password string `json:"password"`
}

type Store interface {
	Create(ctx context.Context, value record) error
	List(ctx context.Context) ([]Profile, error)
	Get(ctx context.Context, id string) (record, error)
	MarkSyncing(ctx context.Context, id string, now time.Time) error
	CompleteSync(
		ctx context.Context,
		id string,
		encryptedJar []byte,
		cookieCount int,
		domainCount int,
		now time.Time,
	) error
	FailSync(ctx context.Context, id string, message string, now time.Time) error
	Delete(ctx context.Context, id string) error
}
