package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/visoraft/visoraft/internal/events"
	"github.com/visoraft/visoraft/internal/identity"
	appsettings "github.com/visoraft/visoraft/internal/settings"
	"github.com/visoraft/visoraft/internal/taskconfig"
)

const (
	MetadataRequestedV1     = "io.visoraft.media.metadata.requested.v1"
	DownloadRequestedV1     = "io.visoraft.media.download.requested.v1"
	AssetsDeleteRequestedV1 = "io.visoraft.media.assets.delete.requested.v1"
	SubtitleRequestedV1     = "io.visoraft.subtitle.process.requested.v1"
	TranscodeRequestedV1    = "io.visoraft.media.transcode.requested.v1"
)

var ErrNotFound = errors.New("task not found")
var ErrInvalidID = errors.New("task id is invalid")

type ConflictError struct {
	Code    string
	Message string
}

func (e *ConflictError) Error() string {
	return e.Message
}

type ValidationError struct {
	Fields map[string]string `json:"fields"`
}

func (e *ValidationError) Error() string {
	return "task input is invalid"
}

type Store interface {
	Create(context.Context, NewTask) error
	CurrentTaskConfiguration(context.Context) (TaskConfiguration, error)
	List(context.Context, int, string) ([]Task, error)
	Get(context.Context, string) (Task, error)
	Summary(context.Context) (Summary, error)
	ArchivePreview(context.Context) (ArchivePreview, error)
	ArchiveCandidates(context.Context, int) ([]ArchiveCandidate, error)
	CookieProfileExists(context.Context, string) (bool, error)
	PostingStrategy(context.Context, string) (PostingStrategyReference, error)
	SetCookieProfile(context.Context, string, *string, time.Time) error
	Cancel(context.Context, string, time.Time) error
	Retry(context.Context, string, time.Time) error
	DeleteAssets(context.Context, string, time.Time) error
	Archive(context.Context, string, ArchiveInput, time.Time) error
	Restore(context.Context, string, RestoreInput, time.Time) error
	Purge(context.Context, string, PurgeInput, time.Time) error
}

type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store) *Service {
	return &Service{store: store, now: time.Now}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Task, error) {
	now := s.now().UTC()
	normalized, err := validateAndNormalize(input, now)
	if err != nil {
		return Task{}, err
	}

	if normalized.CookieProfileID != nil {
		exists, err := s.store.CookieProfileExists(ctx, *normalized.CookieProfileID)
		if err != nil {
			return Task{}, err
		}
		if !exists {
			return Task{}, &ValidationError{
				Fields: map[string]string{"cookie_profile_id": "选择的 Cookie 配置不存在"},
			}
		}
	}
	var postingStrategySnapshot []byte
	var selectedStrategy *taskconfig.PostingStrategySnapshot
	if normalized.PostingStrategyID != nil {
		strategy, err := s.store.PostingStrategy(ctx, *normalized.PostingStrategyID)
		if err != nil {
			return Task{}, err
		}
		if strategy.ID == "" || !strategy.Enabled {
			return Task{}, &ValidationError{
				Fields: map[string]string{"posting_strategy_id": "投稿策略不存在或已停用"},
			}
		}
		for _, platform := range normalized.TargetPlatforms {
			if !slices.Contains(strategy.TargetPlatforms, platform) {
				return Task{}, &ValidationError{
					Fields: map[string]string{
						"target_platforms": "目标平台必须包含在所选投稿策略中",
					},
				}
			}
		}
		if normalized.AutoPublish && strategy.AutomationMode != "automatic_after_review" {
			return Task{}, &ValidationError{
				Fields: map[string]string{
					"auto_publish": "全自动任务必须选择“审核后自动投稿”策略",
				},
			}
		}
		decodedStrategy, err := taskconfig.DecodeStrategy(strategy.Snapshot)
		if err != nil {
			return Task{}, fmt.Errorf("decode selected posting strategy: %w", err)
		}
		if decodedStrategy.TranscodePresetID != nil &&
			(decodedStrategy.TranscodePreset == nil ||
				!decodedStrategy.TranscodePreset.Available) {
			return Task{}, &ValidationError{
				Fields: map[string]string{
					"posting_strategy_id": "投稿策略选择的转码预设不存在、已停用或已归档",
				},
			}
		}
		selectedStrategy = &decodedStrategy
		postingStrategySnapshot = strategy.Snapshot
	}

	taskID, err := identity.NewUUID()
	if err != nil {
		return Task{}, err
	}
	stepID, err := identity.NewUUID()
	if err != nil {
		return Task{}, err
	}
	outboxID, err := identity.NewUUID()
	if err != nil {
		return Task{}, err
	}
	auditID, err := identity.NewUUID()
	if err != nil {
		return Task{}, err
	}

	brief, full := BuildRepostStatements(normalized.SourceURL)
	configuration, err := s.store.CurrentTaskConfiguration(ctx)
	if err != nil {
		return Task{}, err
	}
	var snapshot appsettings.ConfigSnapshot
	if err := json.Unmarshal(configuration.Snapshot, &snapshot); err != nil {
		return Task{}, fmt.Errorf("decode current task configuration: %w", err)
	}
	if selectedStrategy != nil &&
		selectedStrategy.RequireContentModeration &&
		!snapshot.Moderation.Enabled {
		return Task{}, &ValidationError{
			Fields: map[string]string{
				"posting_strategy_id": "投稿策略要求内容审核，请先在设置中启用内容审核",
			},
		}
	}
	if snapshot.Moderation.Enabled && snapshot.Moderation.Provider == "aliyun" {
		configured := map[string]bool{}
		for _, secret := range configuration.SecretSnapshots {
			configured[secret.Key] = len(secret.Ciphertext) > 0
		}
		if !configured[appsettings.SecretAliyunAccessKeyID] ||
			!configured[appsettings.SecretAliyunAccessKeySecret] {
			return Task{}, &ValidationError{
				Fields: map[string]string{
					"moderation.credentials": "阿里云内容审核凭证未配置完整",
				},
			}
		}
	}
	configuration.Snapshot, err = taskconfig.Apply(
		configuration.Snapshot,
		postingStrategySnapshot,
	)
	if errors.Is(err, taskconfig.ErrPresetUnavailable) {
		return Task{}, &ValidationError{
			Fields: map[string]string{
				"posting_strategy_id": "投稿策略选择的转码预设当前不可用",
			},
		}
	}
	if err != nil {
		return Task{}, err
	}

	task := Task{
		ID:                taskID,
		Status:            StatusQueued,
		TargetPlatforms:   normalized.TargetPlatforms,
		SourceURL:         normalized.SourceURL,
		CookieProfileID:   normalized.CookieProfileID,
		PostingStrategyID: normalized.PostingStrategyID,
		AutoPublish:       normalized.AutoPublish,
		StatementVersion:  normalized.RepostStatementVersion,
		StatementBrief:    brief,
		StatementFull:     full,
		ReviewMode:        configuration.ReviewMode,
		ReviewStatus:      "not_started",
		ReviewSummary:     map[string]any{},
		SettingsVersion:   configuration.Version,
		Tags:              []string{},
		Version:           1,
		CreatedAt:         now,
		UpdatedAt:         now,
		Steps: []Step{{
			Kind:      StepMetadata,
			Status:    StatusQueued,
			Attempt:   1,
			Progress:  0,
			UpdatedAt: now,
		}},
	}

	envelope, err := events.New(
		MetadataRequestedV1,
		"visoraft/control-api",
		"task/"+taskID,
		now,
		map[string]any{
			"task_id":           taskID,
			"source_url":        normalized.SourceURL,
			"cookie_profile_id": normalized.CookieProfileID,
			"attempt":           1,
		},
	)
	if err != nil {
		return Task{}, err
	}
	rawEnvelope, err := json.Marshal(envelope)
	if err != nil {
		return Task{}, fmt.Errorf("marshal metadata command: %w", err)
	}

	if err := s.store.Create(ctx, NewTask{
		Task:             task,
		MetadataStepID:   stepID,
		OutboxID:         outboxID,
		AuditID:          auditID,
		Envelope:         rawEnvelope,
		EventType:        MetadataRequestedV1,
		SettingsSnapshot: configuration.Snapshot,
		SecretSnapshots:  configuration.SecretSnapshots,
	}); err != nil {
		return Task{}, err
	}

	return task, nil
}

func (s *Service) List(ctx context.Context, limit int, scope string) ([]Task, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = "active"
	}
	if scope != "active" && scope != "archived" && scope != "all" {
		return nil, &ValidationError{
			Fields: map[string]string{"scope": "scope 必须是 active、archived 或 all"},
		}
	}
	return s.store.List(ctx, limit, scope)
}

func (s *Service) Get(ctx context.Context, id string) (Task, error) {
	if !identity.IsUUID(id) {
		return Task{}, ErrInvalidID
	}
	return s.store.Get(ctx, id)
}

func (s *Service) Dashboard(ctx context.Context) (Summary, error) {
	return s.store.Summary(ctx)
}

func (s *Service) FileLibrary(ctx context.Context) (FileLibrary, error) {
	items, err := s.store.List(ctx, 1000, "all")
	if err != nil {
		return FileLibrary{}, err
	}
	return BuildFileLibrary(items), nil
}

func BuildFileLibrary(items []Task) FileLibrary {
	library := FileLibrary{Folders: make([]FileFolder, 0, len(items))}
	for _, task := range items {
		if len(task.Assets) == 0 {
			continue
		}
		title := strings.TrimSpace(task.Title)
		if title == "" {
			title = strings.TrimSpace(task.OriginalTitle)
		}
		if title == "" {
			title = "未命名任务"
		}
		folder := FileFolder{
			TaskID:    task.ID,
			Title:     title,
			Status:    task.Status,
			Archived:  task.ArchivedAt != nil,
			UpdatedAt: task.UpdatedAt,
			Files:     append([]MediaAsset(nil), task.Assets...),
		}
		for _, asset := range folder.Files {
			folder.FileCount++
			library.FileCount++
			if asset.DeletedAt != nil || asset.Status == "deleted" {
				folder.DeletedCount++
				library.DeletedCount++
				continue
			}
			folder.AvailableCount++
			library.AvailableCount++
			folder.TotalBytes += asset.SizeBytes
			library.TotalBytes += asset.SizeBytes
		}
		library.Folders = append(library.Folders, folder)
	}
	library.FolderCount = len(library.Folders)
	return library
}

func (s *Service) SetCookieProfile(
	ctx context.Context,
	id string,
	input SetCookieProfileInput,
) (Task, error) {
	if !identity.IsUUID(id) {
		return Task{}, ErrInvalidID
	}
	if input.CookieProfileID != nil {
		normalized := strings.TrimSpace(*input.CookieProfileID)
		if !identity.IsUUID(normalized) {
			return Task{}, &ValidationError{
				Fields: map[string]string{"cookie_profile_id": "Cookie 配置 ID 格式无效"},
			}
		}
		exists, err := s.store.CookieProfileExists(ctx, normalized)
		if err != nil {
			return Task{}, err
		}
		if !exists {
			return Task{}, &ValidationError{
				Fields: map[string]string{"cookie_profile_id": "选择的 Cookie 配置不存在"},
			}
		}
		input.CookieProfileID = &normalized
	}
	if err := s.store.SetCookieProfile(ctx, id, input.CookieProfileID, s.now().UTC()); err != nil {
		return Task{}, err
	}
	return s.store.Get(ctx, id)
}

func (s *Service) Cancel(ctx context.Context, id string) (Task, error) {
	if !identity.IsUUID(id) {
		return Task{}, ErrInvalidID
	}
	if err := s.store.Cancel(ctx, id, s.now().UTC()); err != nil {
		return Task{}, err
	}
	return s.store.Get(ctx, id)
}

func (s *Service) Retry(ctx context.Context, id string) (Task, error) {
	if !identity.IsUUID(id) {
		return Task{}, ErrInvalidID
	}
	if err := s.store.Retry(ctx, id, s.now().UTC()); err != nil {
		return Task{}, err
	}
	return s.store.Get(ctx, id)
}

func (s *Service) DeleteAssets(ctx context.Context, id string) (Task, error) {
	if !identity.IsUUID(id) {
		return Task{}, ErrInvalidID
	}
	if err := s.store.DeleteAssets(ctx, id, s.now().UTC()); err != nil {
		return Task{}, err
	}
	return s.store.Get(ctx, id)
}

func (s *Service) ArchivePreview(ctx context.Context) (ArchivePreview, error) {
	return s.store.ArchivePreview(ctx)
}

func (s *Service) Archive(
	ctx context.Context,
	id string,
	input ArchiveInput,
) (Task, error) {
	if !identity.IsUUID(id) {
		return Task{}, ErrInvalidID
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if err := validateArchiveInput(input.ExpectedVersion, input.Reason); err != nil {
		return Task{}, err
	}
	if err := s.store.Archive(ctx, id, input, s.now().UTC()); err != nil {
		return Task{}, err
	}
	return s.store.Get(ctx, id)
}

func (s *Service) ArchiveAll(
	ctx context.Context,
	input ArchiveAllInput,
) (ArchiveAllResult, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ExpectedCount < 1 {
		return ArchiveAllResult{}, &ValidationError{
			Fields: map[string]string{"expected_count": "没有可清空的任务"},
		}
	}
	expectedConfirmation := fmt.Sprintf("archive-all:%d", input.ExpectedCount)
	if strings.TrimSpace(input.Confirmation) != expectedConfirmation {
		return ArchiveAllResult{}, &ValidationError{
			Fields: map[string]string{"confirmation": "请重新确认本次清空范围"},
		}
	}
	if len([]rune(input.Reason)) < 4 || len([]rune(input.Reason)) > 500 {
		return ArchiveAllResult{}, &ValidationError{
			Fields: map[string]string{"reason": "操作原因需为 4 到 500 个字符"},
		}
	}
	preview, err := s.store.ArchivePreview(ctx)
	if err != nil {
		return ArchiveAllResult{}, err
	}
	if int64(input.ExpectedCount) != preview.TotalTasks {
		return ArchiveAllResult{}, &ConflictError{
			Code:    "task_archive_scope_changed",
			Message: "任务数量已经变化，请刷新影响范围后重试",
		}
	}
	if preview.TotalTasks > 500 {
		return ArchiveAllResult{}, &ConflictError{
			Code:    "task_archive_scope_too_large",
			Message: "单次最多清空 500 条任务，请先缩小范围",
		}
	}
	candidates, err := s.store.ArchiveCandidates(ctx, int(preview.TotalTasks))
	if err != nil {
		return ArchiveAllResult{}, err
	}
	result := ArchiveAllResult{
		Archived: make([]Task, 0, len(candidates)),
		Failed:   make([]ArchiveFailure, 0),
	}
	for _, candidate := range candidates {
		task, archiveErr := s.Archive(ctx, candidate.ID, ArchiveInput{
			ExpectedVersion: candidate.Version,
			DeleteAssets:    input.DeleteAssets,
			Reason:          input.Reason,
		})
		if archiveErr == nil {
			result.Archived = append(result.Archived, task)
			continue
		}
		failure := ArchiveFailure{
			TaskID:  candidate.ID,
			Code:    "task_archive_failed",
			Message: "任务未移入回收站",
		}
		var conflict *ConflictError
		if errors.As(archiveErr, &conflict) {
			failure.Code = conflict.Code
			failure.Message = conflict.Message
		} else if errors.Is(archiveErr, ErrNotFound) {
			failure.Code = "task_not_found"
			failure.Message = "任务已经不存在"
		}
		result.Failed = append(result.Failed, failure)
	}
	return result, nil
}

func (s *Service) Restore(
	ctx context.Context,
	id string,
	input RestoreInput,
) (Task, error) {
	if !identity.IsUUID(id) {
		return Task{}, ErrInvalidID
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if err := validateArchiveInput(input.ExpectedVersion, input.Reason); err != nil {
		return Task{}, err
	}
	if err := s.store.Restore(ctx, id, input, s.now().UTC()); err != nil {
		return Task{}, err
	}
	return s.store.Get(ctx, id)
}

func (s *Service) Purge(
	ctx context.Context,
	id string,
	input PurgeInput,
) (PurgeResult, error) {
	if !identity.IsUUID(id) {
		return PurgeResult{}, ErrInvalidID
	}
	input.Reason = strings.TrimSpace(input.Reason)
	fields := map[string]string{}
	if input.ExpectedVersion < 1 {
		fields["expected_version"] = "缺少有效版本，请刷新后重试"
	}
	if strings.TrimSpace(input.Confirmation) != "purge:"+id {
		fields["confirmation"] = "请重新确认永久删除的任务"
	}
	if len([]rune(input.Reason)) < 4 || len([]rune(input.Reason)) > 500 {
		fields["reason"] = "操作原因需为 4 到 500 个字符"
	}
	if len(fields) > 0 {
		return PurgeResult{}, &ValidationError{Fields: fields}
	}
	now := s.now().UTC()
	if err := s.store.Purge(ctx, id, input, now); err != nil {
		return PurgeResult{}, err
	}
	return PurgeResult{TaskID: id, PurgedAt: now}, nil
}

func validateArchiveInput(expectedVersion int64, reason string) error {
	fields := map[string]string{}
	if expectedVersion < 1 {
		fields["expected_version"] = "缺少有效版本，请刷新后重试"
	}
	length := len([]rune(strings.TrimSpace(reason)))
	if length < 4 || length > 500 {
		fields["reason"] = "操作原因需为 4 到 500 个字符"
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func (s *Service) RetryMany(ctx context.Context, input BulkRetryInput) (BulkRetryResult, error) {
	if len(input.TaskIDs) == 0 {
		return BulkRetryResult{}, &ValidationError{
			Fields: map[string]string{"task_ids": "至少选择一条失败任务"},
		}
	}
	if len(input.TaskIDs) > 50 {
		return BulkRetryResult{}, &ValidationError{
			Fields: map[string]string{"task_ids": "单次最多重试 50 条任务"},
		}
	}

	seen := make(map[string]struct{}, len(input.TaskIDs))
	result := BulkRetryResult{
		Succeeded: make([]Task, 0, len(input.TaskIDs)),
		Failed:    make([]BulkRetryFailure, 0),
	}
	for _, rawID := range input.TaskIDs {
		taskID := strings.TrimSpace(rawID)
		if _, exists := seen[taskID]; exists {
			continue
		}
		seen[taskID] = struct{}{}

		task, err := s.Retry(ctx, taskID)
		if err == nil {
			result.Succeeded = append(result.Succeeded, task)
			continue
		}

		failure := BulkRetryFailure{
			TaskID:  taskID,
			Code:    "task_retry_failed",
			Message: "任务重试失败",
		}
		switch {
		case errors.Is(err, ErrInvalidID):
			failure.Code = "invalid_task_id"
			failure.Message = "任务 ID 格式无效"
		case errors.Is(err, ErrNotFound):
			failure.Code = "task_not_found"
			failure.Message = "任务不存在"
		default:
			var conflict *ConflictError
			if errors.As(err, &conflict) {
				failure.Code = conflict.Code
				failure.Message = conflict.Message
			}
		}
		result.Failed = append(result.Failed, failure)
	}
	return result, nil
}

func validateAndNormalize(input CreateInput, _ time.Time) (CreateInput, error) {
	issues := map[string]string{}
	input.SourceURL = strings.TrimSpace(input.SourceURL)
	input.RepostStatementVersion = strings.ToLower(strings.TrimSpace(input.RepostStatementVersion))
	input.TargetPlatforms = normalizePlatforms(input.TargetPlatforms)
	if input.CookieProfileID != nil {
		normalized := strings.TrimSpace(*input.CookieProfileID)
		input.CookieProfileID = &normalized
		if !identity.IsUUID(normalized) {
			issues["cookie_profile_id"] = "Cookie 配置 ID 格式无效"
		}
	}
	if input.PostingStrategyID != nil {
		normalized := strings.TrimSpace(*input.PostingStrategyID)
		input.PostingStrategyID = &normalized
		if !identity.IsUUID(normalized) {
			issues["posting_strategy_id"] = "投稿策略 ID 格式无效"
		}
	}
	if input.AutoPublish && input.PostingStrategyID == nil {
		issues["auto_publish"] = "开启全自动投稿前必须选择投稿策略"
	}

	parsedURL, err := url.ParseRequestURI(input.SourceURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		issues["source_url"] = "请输入有效的 http/https 来源 URL"
	}

	if len(input.TargetPlatforms) == 0 {
		issues["target_platforms"] = "至少选择一个目标平台"
	}
	for _, platform := range input.TargetPlatforms {
		if platform != "acfun" && platform != "bilibili" {
			issues["platforms"] = "当前仅支持 acfun 和 bilibili"
			break
		}
	}
	if input.RepostStatementVersion != StatementBriefV1 && input.RepostStatementVersion != StatementFullV1 {
		issues["repost_statement_version"] = "请选择简版或完整版转载声明"
	}

	if len(issues) > 0 {
		return CreateInput{}, &ValidationError{Fields: issues}
	}

	return input, nil
}

func normalizePlatforms(platforms []string) []string {
	result := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		normalized := strings.ToLower(strings.TrimSpace(platform))
		if normalized == "" || slices.Contains(result, normalized) {
			continue
		}
		result = append(result, normalized)
	}
	slices.Sort(result)
	return result
}

func BuildRepostStatements(sourceURL string) (string, string) {
	brief := fmt.Sprintf("转载来源：%s", sourceURL)
	full := fmt.Sprintf(
		"【转载说明】本内容转载自：%s。转载声明仅说明来源，不代表取得版权许可。",
		sourceURL,
	)
	return brief, full
}
