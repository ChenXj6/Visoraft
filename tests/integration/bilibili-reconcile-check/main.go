package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/visoraft/visoraft/internal/config"
	"github.com/visoraft/visoraft/internal/cookieprofiles"
	"github.com/visoraft/visoraft/internal/database"
	"github.com/visoraft/visoraft/internal/publishing"
)

type output struct {
	TaskID                 string         `json:"task_id"`
	PublicationID          string         `json:"publication_id"`
	SubmittedRemoteID      string         `json:"submitted_remote_id"`
	Found                  bool           `json:"found_in_creator_center"`
	ReconciledRemoteID     string         `json:"reconciled_remote_id,omitempty"`
	ReconciledRemoteURL    string         `json:"reconciled_remote_url,omitempty"`
	ReconciledRemoteStatus string         `json:"reconciled_remote_status,omitempty"`
	ResponseSummary        map[string]any `json:"response_summary,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	taskID := os.Getenv("VISORAFT_TASK_ID")
	if taskID == "" {
		return errors.New("VISORAFT_TASK_ID is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	cfg, err := config.Load("bilibili-reconcile-check")
	if err != nil {
		return err
	}
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	var (
		publication publishing.PlatformPublication
		account     publishing.Account
		profileID   string
		sourceURL   string
	)
	err = pool.QueryRow(ctx, `
		SELECT
			p.id::text, p.publish_job_id::text, p.task_id::text, p.platform,
			p.account_id::text, p.status, p.category_id, p.title, p.description,
			p.tags, p.media_asset_id::text, p.fingerprint, p.attempt,
			p.remote_submission_id, p.remote_url, p.remote_status,
			a.id::text, a.platform, a.name, a.auth_mode,
			a.cookie_profile_id::text, a.status, t.source_url
		FROM platform_publications p
		JOIN platform_accounts a ON a.id=p.account_id
		JOIN tasks t ON t.id=p.task_id
		WHERE p.task_id=$1 AND p.platform='bilibili'
		ORDER BY p.created_at DESC
		LIMIT 1
	`, taskID).Scan(
		&publication.ID,
		&publication.PublishJobID,
		&publication.TaskID,
		&publication.Platform,
		&publication.AccountID,
		&publication.Status,
		&publication.CategoryID,
		&publication.Title,
		&publication.Description,
		&publication.Tags,
		&publication.MediaAssetID,
		&publication.Fingerprint,
		&publication.Attempt,
		&publication.RemoteSubmissionID,
		&publication.RemoteURL,
		&publication.RemoteStatus,
		&account.ID,
		&account.Platform,
		&account.Name,
		&account.AuthMode,
		&profileID,
		&account.Status,
		&sourceURL,
	)
	if err != nil {
		return fmt.Errorf("load Bilibili publication: %w", err)
	}
	account.CookieProfileID = &profileID

	box, err := cookieprofiles.NewSecretBox(cfg.CookieEncryptionKey)
	if err != nil {
		return err
	}
	cookies := cookieprofiles.NewService(
		cookieprofiles.NewPostgresStore(pool),
		box,
		cookieprofiles.NewHTTPCookieCloudClient(),
	)
	jar, err := cookies.CookieJar(ctx, profileID)
	if err != nil {
		return err
	}
	result, found, err := publishing.NewBilibiliWebAdapter().Reconcile(
		ctx,
		publishing.ReconcileRequest{
			Publication: publication,
			Account:     account,
			SourceURL:   sourceURL,
			CookieJar:   jar,
		},
	)
	if err != nil {
		return err
	}
	value := output{
		TaskID:            taskID,
		PublicationID:     publication.ID,
		SubmittedRemoteID: publication.RemoteSubmissionID,
		Found:             found,
	}
	if found {
		value.ReconciledRemoteID = result.RemoteSubmissionID
		value.ReconciledRemoteURL = result.RemoteURL
		value.ReconciledRemoteStatus = result.RemoteStatus
		value.ResponseSummary = result.ResponseSummary
	}
	return json.NewEncoder(os.Stdout).Encode(value)
}
