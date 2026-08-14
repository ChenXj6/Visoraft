package publishing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/visoraft/visoraft/internal/objectstorage"
)

type Worker struct {
	store      *PostgresStore
	cookies    CookieJarProvider
	storage    *objectstorage.Client
	publishers map[string]PlatformPublisher
	owner      string
	poll       time.Duration
	logger     *slog.Logger
	now        func() time.Time
}

func NewWorker(
	store *PostgresStore,
	cookies CookieJarProvider,
	storage *objectstorage.Client,
	owner string,
	poll time.Duration,
	logger *slog.Logger,
	publishers ...PlatformPublisher,
) *Worker {
	if poll < 100*time.Millisecond {
		poll = time.Second
	}
	registry := make(map[string]PlatformPublisher, len(publishers))
	for _, publisher := range publishers {
		if publisher == nil {
			continue
		}
		registry[publisher.Platform()+":"+publisher.AuthMode()] = publisher
	}
	return &Worker{
		store:      store,
		cookies:    cookies,
		storage:    storage,
		publishers: registry,
		owner:      owner,
		poll:       poll,
		logger:     logger,
		now:        time.Now,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if err := w.processBatch(ctx); err != nil {
				w.logger.Error("publisher batch failed", "error", err)
			}
			timer.Reset(w.poll)
		}
	}
}

func (w *Worker) processBatch(ctx context.Context) error {
	concurrency, err := w.store.PublishingConcurrency(ctx)
	if err != nil {
		return err
	}
	items, err := w.store.ClaimPublications(
		ctx,
		w.owner,
		concurrency,
		w.now().UTC(),
	)
	if err != nil {
		return err
	}
	var wait sync.WaitGroup
	wait.Add(len(items))
	for _, item := range items {
		item := item
		go func() {
			defer wait.Done()
			if err := w.processOne(ctx, item); err != nil {
				w.logger.Error(
					"platform publication execution failed",
					"publication_id", item.ID,
					"task_id", item.TaskID,
					"platform", item.Platform,
					"attempt", item.Attempt,
					"error", err,
				)
			}
		}()
	}
	wait.Wait()
	return nil
}

func (w *Worker) processOne(
	ctx context.Context,
	item ClaimedPublication,
) error {
	publisher, exists := w.publishers[item.Platform+":"+item.Account.AuthMode]
	if !exists {
		return w.recordFailure(ctx, item, "", &AdapterError{
			Code:      "platform_adapter_unavailable",
			Message:   "投稿服务没有装载匹配该账号认证方式的平台适配器",
			Retryable: false,
		})
	}
	cookieJar, err := w.cookieJar(ctx, item)
	if err != nil {
		return w.recordFailure(ctx, item, "", &AdapterError{
			Code:      "platform_cookie_unavailable",
			Message:   "投稿账号 Cookie 不可用，请同步或重新登录",
			Retryable: false,
		})
	}

	stage := "publish"
	if item.ClaimMode == "reconcile" {
		stage = "reconcile"
	}
	attemptID, err := w.store.BeginPublicationAttempt(
		ctx,
		item,
		stage,
		w.now().UTC(),
	)
	if err != nil {
		return err
	}

	if fixtureAccountBlocked(item.Account.AuthMode, item.SourceURL) {
		return w.recordFailure(ctx, item, attemptID, &AdapterError{
			Code:      "fixture_account_real_source_forbidden",
			Message:   "本地验收账号不会连接真实平台；请在投稿草稿中切换到已校验的 Cookie 认证账号",
			Retryable: false,
		})
	}

	if item.ClaimMode == "reconcile" {
		result, found, err := publisher.Reconcile(ctx, ReconcileRequest{
			Publication: item.PlatformPublication,
			Account:     item.Account,
			SourceURL:   item.SourceURL,
			CookieJar:   cookieJar,
		})
		if err != nil {
			return w.recordFailure(ctx, item, attemptID, classifyAdapterError(err))
		}
		if !found {
			return w.recordFailure(ctx, item, attemptID, &AdapterError{
				Code:      "remote_submission_not_confirmed",
				Message:   "平台暂未返回可确认的投稿结果，将继续对账",
				Retryable: true,
				Uncertain: true,
			})
		}
		return w.completeOrReconcile(
			ctx,
			item,
			attemptID,
			result,
			publisher.Version(),
		)
	}

	tempDir, err := os.MkdirTemp("", "visoraft-publish-*")
	if err != nil {
		return w.recordFailure(ctx, item, attemptID, &AdapterError{
			Code:      "publish_temp_directory_failed",
			Message:   "无法创建投稿临时目录",
			Retryable: true,
		})
	}
	defer func() {
		if removeErr := os.RemoveAll(tempDir); removeErr != nil {
			w.logger.Warn(
				"failed to remove publisher temp directory",
				"path", tempDir,
				"error", removeErr,
			)
		}
	}()
	if err := os.Chmod(tempDir, 0o700); err != nil {
		return w.recordFailure(ctx, item, attemptID, &AdapterError{
			Code:      "publish_temp_permissions_failed",
			Message:   "无法限制投稿临时目录权限",
			Retryable: true,
		})
	}

	mediaPath, err := w.materializeAsset(ctx, tempDir, "media", item.Media)
	if err != nil {
		return w.recordFailure(ctx, item, attemptID, &AdapterError{
			Code:      "publish_media_materialization_failed",
			Message:   "无法从对象存储读取并校验投稿媒体",
			Retryable: true,
		})
	}
	coverPath := ""
	if item.Cover != nil {
		coverPath, err = w.materializeAsset(ctx, tempDir, "cover", *item.Cover)
		if err != nil {
			return w.recordFailure(ctx, item, attemptID, &AdapterError{
				Code:      "publish_cover_materialization_failed",
				Message:   "无法从对象存储读取并校验投稿封面",
				Retryable: true,
			})
		}
	}
	if err := w.store.SetPublicationStage(
		ctx,
		item.ID,
		w.owner,
		"uploading",
		"upload",
		w.now().UTC(),
	); err != nil {
		return err
	}
	result, err := publisher.Publish(ctx, UploadRequest{
		Publication: item.PlatformPublication,
		Account:     item.Account,
		SourceURL:   item.SourceURL,
		MediaPath:   mediaPath,
		CoverPath:   coverPath,
		CookieJar:   cookieJar,
		OnStage: func(stageCtx context.Context, stage string) error {
			status := "uploading"
			if stage == "submitting" {
				status = "submitting"
			}
			return w.store.SetPublicationStage(
				stageCtx,
				item.ID,
				w.owner,
				status,
				stage,
				w.now().UTC(),
			)
		},
	})
	if err != nil {
		return w.recordFailure(ctx, item, attemptID, classifyAdapterError(err))
	}
	return w.completeOrReconcile(
		ctx,
		item,
		attemptID,
		result,
		publisher.Version(),
	)
}

func (w *Worker) completeOrReconcile(
	ctx context.Context,
	item ClaimedPublication,
	attemptID string,
	result PublishResult,
	adapterVersion string,
) error {
	if failure := validatePublishResult(item, result); failure != nil {
		return w.recordFailure(ctx, item, attemptID, failure)
	}
	now := w.now().UTC()
	if err := w.store.CompletePublication(
		ctx,
		item,
		w.owner,
		attemptID,
		result,
		adapterVersion,
		now,
	); err != nil {
		recoveryErr := w.store.MarkPublicationCompletionUncertain(
			ctx,
			item,
			w.owner,
			attemptID,
			result,
			adapterVersion,
			now,
		)
		if recoveryErr != nil {
			return fmt.Errorf(
				"persist platform completion: %w; mark reconciliation recovery: %v",
				err,
				recoveryErr,
			)
		}
		return fmt.Errorf(
			"persist platform completion: %w (remote result retained for reconciliation)",
			err,
		)
	}
	return nil
}

func (w *Worker) cookieJar(
	ctx context.Context,
	item ClaimedPublication,
) ([]byte, error) {
	if item.Account.AuthMode == "fixture" {
		return nil, nil
	}
	if item.Account.CookieProfileID == nil {
		return nil, errors.New("platform account cookie profile is missing")
	}
	return w.cookies.CookieJar(ctx, *item.Account.CookieProfileID)
}

func (w *Worker) materializeAsset(
	ctx context.Context,
	tempDir string,
	baseName string,
	asset AssetReference,
) (string, error) {
	extension := safeExtension(asset.OriginalName)
	targetPath := filepath.Join(tempDir, baseName+extension)
	file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create materialized asset: %w", err)
	}
	response, err := w.storage.Get(ctx, asset.Bucket, asset.ObjectKey, "")
	if err != nil {
		_ = file.Close()
		return "", err
	}
	hasher := sha256.New()
	limit := asset.SizeBytes + 1
	if limit < 1 {
		limit = 1
	}
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(response.Body, limit))
	closeResponseErr := response.Body.Close()
	closeFileErr := file.Close()
	if copyErr != nil {
		return "", fmt.Errorf("copy materialized asset: %w", copyErr)
	}
	if closeResponseErr != nil {
		return "", fmt.Errorf("close object storage response: %w", closeResponseErr)
	}
	if closeFileErr != nil {
		return "", fmt.Errorf("close materialized asset: %w", closeFileErr)
	}
	if written != asset.SizeBytes {
		return "", fmt.Errorf(
			"materialized asset size mismatch: got %d want %d",
			written,
			asset.SizeBytes,
		)
	}
	checksum := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(checksum, asset.Checksum) {
		return "", errors.New("materialized asset checksum mismatch")
	}
	return targetPath, nil
}

func (w *Worker) recordFailure(
	ctx context.Context,
	item ClaimedPublication,
	attemptID string,
	failure *AdapterError,
) error {
	return w.store.FailPublication(
		ctx,
		item,
		w.owner,
		attemptID,
		failure,
		w.now().UTC(),
	)
}

func classifyAdapterError(err error) *AdapterError {
	var adapterError *AdapterError
	if errors.As(err, &adapterError) {
		return adapterError
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &AdapterError{
			Code:      "platform_request_timeout",
			Message:   "平台请求超时或服务正在关闭",
			Retryable: true,
			Uncertain: true,
		}
	}
	return &AdapterError{
		Code:      "platform_publish_failed",
		Message:   truncateMessage(err.Error()),
		Retryable: true,
	}
}

func safeExtension(filename string) string {
	extension := strings.ToLower(filepath.Ext(filepath.Base(filename)))
	if len(extension) < 2 || len(extension) > 10 {
		return ".bin"
	}
	for _, value := range extension[1:] {
		if (value < 'a' || value > 'z') && (value < '0' || value > '9') {
			return ".bin"
		}
	}
	return extension
}
