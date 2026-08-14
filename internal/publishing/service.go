package publishing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/visoraft/visoraft/internal/identity"
)

type CookieJarProvider interface {
	CookieJar(context.Context, string) ([]byte, error)
}

type AccountIdentity struct {
	RemoteUserID      string
	RemoteDisplayName string
}

type PlatformGateway interface {
	Platform() string
	AuthMode() string
	Version() string
	CheckAccount(context.Context, []byte) (AccountIdentity, error)
	Categories(context.Context, []byte) ([]Category, error)
}

type Service struct {
	store        *PostgresStore
	cookies      CookieJarProvider
	gateways     map[string]PlatformGateway
	now          func() time.Time
	coverStorage CoverObjectStorage
	coverBucket  string
	coverHTTP    *http.Client
}

func NewService(
	store *PostgresStore,
	cookies CookieJarProvider,
	gateways ...PlatformGateway,
) *Service {
	registry := make(map[string]PlatformGateway, len(gateways))
	for _, gateway := range gateways {
		if gateway == nil {
			continue
		}
		registry[gateway.Platform()+":"+gateway.AuthMode()] = gateway
	}
	return &Service{
		store:    store,
		cookies:  cookies,
		gateways: registry,
		now:      time.Now,
	}
}

func (s *Service) ListAccounts(ctx context.Context, platform string) ([]Account, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform != "" && !validPlatform(platform) {
		return nil, &ValidationError{
			Fields: map[string]string{"platform": "平台必须是 AcFun 或 bilibili"},
		}
	}
	return s.store.ListAccounts(ctx, platform)
}

func (s *Service) GetAccount(ctx context.Context, id string) (Account, error) {
	if !identity.IsUUID(id) {
		return Account{}, ErrNotFound
	}
	return s.store.GetAccount(ctx, id)
}

func (s *Service) CreateAccount(
	ctx context.Context,
	input CreateAccountInput,
) (Account, error) {
	normalizeAccountInput(&input)
	if err := validateAccountInput(input); err != nil {
		return Account{}, err
	}
	if input.AuthMode == "cookie" {
		exists, err := s.store.CookieProfileExists(ctx, *input.CookieProfileID)
		if err != nil {
			return Account{}, err
		}
		if !exists {
			return Account{}, &ValidationError{
				Fields: map[string]string{
					"cookie_profile_id": "Cookie 配置不存在、尚未同步或不可用",
				},
			}
		}
	}
	id, err := identity.NewUUID()
	if err != nil {
		return Account{}, err
	}
	return s.store.CreateAccount(ctx, id, input, s.now().UTC())
}

func (s *Service) UpdateAccount(
	ctx context.Context,
	id string,
	input UpdateAccountInput,
) (Account, error) {
	if !identity.IsUUID(id) {
		return Account{}, ErrNotFound
	}
	input.Name = strings.TrimSpace(input.Name)
	normalizeOptionalUUID(&input.CookieProfileID)
	fields := validateName("name", input.Name, 80)
	if input.ExpectedVersion < 1 {
		fields["expected_version"] = "缺少有效版本"
	}
	if input.CookieProfileID == nil || !identity.IsUUID(*input.CookieProfileID) {
		fields["cookie_profile_id"] = "请选择有效的 Cookie 配置"
	}
	if len(fields) > 0 {
		return Account{}, &ValidationError{Fields: fields}
	}
	exists, err := s.store.CookieProfileExists(ctx, *input.CookieProfileID)
	if err != nil {
		return Account{}, err
	}
	if !exists {
		return Account{}, &ValidationError{
			Fields: map[string]string{
				"cookie_profile_id": "Cookie 配置不存在、尚未同步或不可用",
			},
		}
	}
	return s.store.UpdateAccount(ctx, id, input, s.now().UTC())
}

func (s *Service) ArchiveAccount(
	ctx context.Context,
	id string,
	expectedVersion int64,
) error {
	if !identity.IsUUID(id) {
		return ErrNotFound
	}
	if expectedVersion < 1 {
		return &ValidationError{
			Fields: map[string]string{"expected_version": "缺少有效版本"},
		}
	}
	return s.store.ArchiveAccount(ctx, id, expectedVersion, s.now().UTC())
}

func (s *Service) CheckAccount(
	ctx context.Context,
	id string,
) (AccountCheckResult, error) {
	account, err := s.GetAccount(ctx, id)
	if err != nil {
		return AccountCheckResult{}, err
	}
	gateway, exists := s.gateways[account.Platform+":"+account.AuthMode]
	if !exists {
		return AccountCheckResult{}, &ConflictError{
			Code:    "platform_adapter_unavailable",
			Message: "当前服务没有装载该平台的投稿适配器",
		}
	}
	var cookieJar []byte
	if account.AuthMode == "cookie" {
		if account.CookieProfileID == nil {
			return AccountCheckResult{}, &ConflictError{
				Code:    "platform_cookie_missing",
				Message: "投稿账号没有绑定 Cookie 配置",
			}
		}
		cookieJar, err = s.cookies.CookieJar(ctx, *account.CookieProfileID)
		if err != nil {
			updated, saveErr := s.store.SaveAccountCheck(
				ctx,
				account.ID,
				AccountStatusError,
				"",
				"",
				gateway.Version(),
				"cookie_unavailable",
				err.Error(),
				s.now().UTC(),
			)
			if saveErr != nil {
				return AccountCheckResult{}, errors.Join(err, saveErr)
			}
			return AccountCheckResult{
				Account: updated,
				OK:      false,
				Message: "Cookie 不可用，请同步或重新上传",
			}, nil
		}
	}
	identityResult, err := gateway.CheckAccount(ctx, cookieJar)
	if err != nil {
		updated, saveErr := s.store.SaveAccountCheck(
			ctx,
			account.ID,
			AccountStatusError,
			"",
			"",
			gateway.Version(),
			"account_check_failed",
			err.Error(),
			s.now().UTC(),
		)
		if saveErr != nil {
			return AccountCheckResult{}, errors.Join(err, saveErr)
		}
		return AccountCheckResult{
			Account: updated,
			OK:      false,
			Message: "账号校验失败，请更新 Cookie 后重试",
		}, nil
	}
	updated, err := s.store.SaveAccountCheck(
		ctx,
		account.ID,
		AccountStatusReady,
		identityResult.RemoteUserID,
		identityResult.RemoteDisplayName,
		gateway.Version(),
		"",
		"",
		s.now().UTC(),
	)
	if err != nil {
		return AccountCheckResult{}, err
	}
	return AccountCheckResult{
		Account: updated,
		OK:      true,
		Message: "账号可用，已完成服务端身份校验",
	}, nil
}

func (s *Service) ListCategories(
	ctx context.Context,
	platform string,
) ([]Category, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if !validPlatform(platform) {
		return nil, &ValidationError{
			Fields: map[string]string{"platform": "平台必须是 AcFun 或 bilibili"},
		}
	}
	return s.store.ListCategories(ctx, platform)
}

func (s *Service) RefreshCategories(
	ctx context.Context,
	accountID string,
) ([]Category, error) {
	account, err := s.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	gateway, exists := s.gateways[account.Platform+":"+account.AuthMode]
	if !exists {
		return nil, &ConflictError{
			Code:    "platform_adapter_unavailable",
			Message: "当前服务没有装载该平台的投稿适配器",
		}
	}
	if account.Status != AccountStatusReady {
		return nil, &ConflictError{
			Code:    "platform_account_not_ready",
			Message: "请先完成投稿账号校验",
		}
	}
	var jar []byte
	if account.AuthMode == "cookie" && account.CookieProfileID != nil {
		jar, err = s.cookies.CookieJar(ctx, *account.CookieProfileID)
		if err != nil {
			return nil, fmt.Errorf("load platform account cookies: %w", err)
		}
	}
	items, err := gateway.Categories(ctx, jar)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	for index := range items {
		items[index].Platform = account.Platform
		items[index].RefreshedAt = now
		if items[index].Metadata == nil {
			items[index].Metadata = map[string]any{}
		}
	}
	if err := s.store.ReplaceCategories(ctx, account.Platform, items, now); err != nil {
		return nil, err
	}
	return s.store.ListCategories(ctx, account.Platform)
}

func (s *Service) ListTranscodePresets(
	ctx context.Context,
) ([]TranscodePreset, error) {
	return s.store.ListTranscodePresets(ctx)
}

func (s *Service) GetTranscodePreset(
	ctx context.Context,
	id string,
) (TranscodePreset, error) {
	if !identity.IsUUID(id) {
		return TranscodePreset{}, ErrNotFound
	}
	return s.store.GetTranscodePreset(ctx, id)
}

func (s *Service) CreateTranscodePreset(
	ctx context.Context,
	input TranscodePresetInput,
) (TranscodePreset, error) {
	normalizeTranscodePreset(&input)
	if err := validateTranscodePreset(input); err != nil {
		return TranscodePreset{}, err
	}
	id, err := identity.NewUUID()
	if err != nil {
		return TranscodePreset{}, err
	}
	return s.store.CreateTranscodePreset(ctx, id, input, s.now().UTC())
}

func (s *Service) UpdateTranscodePreset(
	ctx context.Context,
	id string,
	input UpdateTranscodePresetInput,
) (TranscodePreset, error) {
	if !identity.IsUUID(id) {
		return TranscodePreset{}, ErrNotFound
	}
	normalizeTranscodePreset(&input.TranscodePresetInput)
	if input.ExpectedVersion < 1 {
		return TranscodePreset{}, &ValidationError{
			Fields: map[string]string{"expected_version": "缺少有效版本"},
		}
	}
	if err := validateTranscodePreset(input.TranscodePresetInput); err != nil {
		return TranscodePreset{}, err
	}
	return s.store.UpdateTranscodePreset(ctx, id, input, s.now().UTC())
}

func (s *Service) ArchiveTranscodePreset(
	ctx context.Context,
	id string,
	expectedVersion int64,
) error {
	if !identity.IsUUID(id) {
		return ErrNotFound
	}
	if expectedVersion < 1 {
		return &ValidationError{
			Fields: map[string]string{"expected_version": "缺少有效版本"},
		}
	}
	return s.store.ArchiveTranscodePreset(ctx, id, expectedVersion, s.now().UTC())
}

func (s *Service) ListPostingStrategies(
	ctx context.Context,
) ([]PostingStrategy, error) {
	return s.store.ListPostingStrategies(ctx)
}

func (s *Service) GetPostingStrategy(
	ctx context.Context,
	id string,
) (PostingStrategy, error) {
	if !identity.IsUUID(id) {
		return PostingStrategy{}, ErrNotFound
	}
	return s.store.GetPostingStrategy(ctx, id)
}

func (s *Service) CreatePostingStrategy(
	ctx context.Context,
	input PostingStrategyInput,
) (PostingStrategy, error) {
	normalizePostingStrategy(&input)
	if err := s.validatePostingStrategy(ctx, input); err != nil {
		return PostingStrategy{}, err
	}
	id, err := identity.NewUUID()
	if err != nil {
		return PostingStrategy{}, err
	}
	return s.store.CreatePostingStrategy(ctx, id, input, s.now().UTC())
}

func (s *Service) UpdatePostingStrategy(
	ctx context.Context,
	id string,
	input UpdatePostingStrategyInput,
) (PostingStrategy, error) {
	if !identity.IsUUID(id) {
		return PostingStrategy{}, ErrNotFound
	}
	normalizePostingStrategy(&input.PostingStrategyInput)
	if input.ExpectedVersion < 1 {
		return PostingStrategy{}, &ValidationError{
			Fields: map[string]string{"expected_version": "缺少有效版本"},
		}
	}
	if err := s.validatePostingStrategy(ctx, input.PostingStrategyInput); err != nil {
		return PostingStrategy{}, err
	}
	return s.store.UpdatePostingStrategy(ctx, id, input, s.now().UTC())
}

func (s *Service) ArchivePostingStrategy(
	ctx context.Context,
	id string,
	expectedVersion int64,
) error {
	if !identity.IsUUID(id) {
		return ErrNotFound
	}
	if expectedVersion < 1 {
		return &ValidationError{
			Fields: map[string]string{"expected_version": "缺少有效版本"},
		}
	}
	return s.store.ArchivePostingStrategy(ctx, id, expectedVersion, s.now().UTC())
}

func (s *Service) Prepare(
	ctx context.Context,
	taskID string,
) (Detail, error) {
	if !identity.IsUUID(taskID) {
		return Detail{}, ErrNotFound
	}
	if err := s.ensureCover(ctx, taskID, ""); err != nil {
		return Detail{}, err
	}
	if _, err := s.store.PrepareApprovedTask(ctx, taskID, false, s.now().UTC()); err != nil {
		return Detail{}, err
	}
	return s.Detail(ctx, taskID)
}

func (s *Service) Detail(ctx context.Context, taskID string) (Detail, error) {
	if !identity.IsUUID(taskID) {
		return Detail{}, ErrNotFound
	}
	result, err := s.store.PublishingDetail(ctx, taskID)
	if err != nil {
		return Detail{}, err
	}
	if result.Job == nil {
		result.NextAction = "prepare_publish_job"
	}
	return result, nil
}

func (s *Service) UpdateDraft(
	ctx context.Context,
	taskID string,
	platform string,
	input DraftPlatformInput,
) (Detail, error) {
	if !identity.IsUUID(taskID) {
		return Detail{}, ErrNotFound
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.CategoryID = strings.TrimSpace(input.CategoryID)
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.Tags = normalizeTags(input.Tags)
	fields := map[string]string{}
	if !validPlatform(platform) {
		fields["platform"] = "平台必须是 AcFun 或 bilibili"
	}
	if input.ExpectedVersion < 1 {
		fields["expected_version"] = "缺少有效版本"
	}
	if !identity.IsUUID(input.AccountID) {
		fields["account_id"] = "请选择有效的投稿账号"
	}
	if input.CategoryID == "" {
		fields["category_id"] = "请选择视频分区"
	}
	if input.Title == "" {
		fields["title"] = "投稿标题不能为空"
	} else if len([]rune(input.Title)) > 80 {
		fields["title"] = "AcFun 与 bilibili 的投稿标题不能超过 80 个字符"
	}
	if len([]rune(input.Description)) > 2000 {
		fields["description"] = "投稿简介不能超过 2000 个字符"
	}
	if len(input.Tags) == 0 {
		fields["tags"] = "至少填写一个投稿标签"
	} else if len(input.Tags) > 10 {
		fields["tags"] = "投稿标签不能超过 10 个"
	}
	if len(fields) > 0 {
		return Detail{}, &ValidationError{Fields: fields}
	}
	if err := s.store.UpdatePublicationDraft(
		ctx,
		taskID,
		platform,
		input,
		s.now().UTC(),
	); err != nil {
		return Detail{}, err
	}
	return s.Detail(ctx, taskID)
}

func (s *Service) Enqueue(ctx context.Context, taskID string) (Detail, error) {
	if !identity.IsUUID(taskID) {
		return Detail{}, ErrNotFound
	}
	if err := s.store.EnqueuePublishJob(ctx, taskID, s.now().UTC()); err != nil {
		return Detail{}, err
	}
	return s.Detail(ctx, taskID)
}

func (s *Service) RetryPlatform(
	ctx context.Context,
	taskID string,
	platform string,
) (Detail, error) {
	if !identity.IsUUID(taskID) {
		return Detail{}, ErrNotFound
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	if !validPlatform(platform) {
		return Detail{}, &ValidationError{
			Fields: map[string]string{"platform": "平台必须是 AcFun 或 bilibili"},
		}
	}
	if platform == PlatformBilibili {
		if err := s.ensureCover(ctx, taskID, platform); err != nil {
			return Detail{}, err
		}
	}
	if err := s.store.RetryPlatformPublication(
		ctx,
		taskID,
		platform,
		s.now().UTC(),
	); err != nil {
		return Detail{}, err
	}
	return s.Detail(ctx, taskID)
}

func (s *Service) ResolvePlatform(
	ctx context.Context,
	taskID string,
	platform string,
	input ResolvePublicationInput,
) (Detail, error) {
	if !identity.IsUUID(taskID) {
		return Detail{}, ErrNotFound
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	input.Resolution = strings.ToLower(strings.TrimSpace(input.Resolution))
	input.RemoteSubmissionID = strings.TrimSpace(input.RemoteSubmissionID)
	input.RemoteURL = strings.TrimSpace(input.RemoteURL)
	input.Note = strings.TrimSpace(input.Note)
	if !validPlatform(platform) {
		return Detail{}, &ValidationError{
			Fields: map[string]string{"platform": "平台必须是 AcFun 或 bilibili"},
		}
	}
	if err := validateResolvePublicationInput(input); err != nil {
		return Detail{}, err
	}
	if err := s.store.ResolvePlatformPublication(
		ctx,
		taskID,
		platform,
		input,
		s.now().UTC(),
	); err != nil {
		return Detail{}, err
	}
	return s.Detail(ctx, taskID)
}

func validateResolvePublicationInput(input ResolvePublicationInput) error {
	fields := map[string]string{}
	if input.ExpectedVersion < 1 {
		fields["expected_version"] = "缺少有效版本，请刷新页面后重试"
	}
	if input.Resolution != "remote_published" &&
		input.Resolution != "remote_not_created" {
		fields["resolution"] = "请选择已经核验的远端结果"
	}
	if len([]rune(input.Note)) < 4 {
		fields["note"] = "请填写至少 4 个字符的核验说明"
	} else if len([]rune(input.Note)) > 500 {
		fields["note"] = "核验说明不能超过 500 个字符"
	}
	switch input.Resolution {
	case "remote_published":
		if input.RemoteSubmissionID == "" {
			fields["remote_submission_id"] = "请填写已找到的平台稿件编号"
		} else if len([]rune(input.RemoteSubmissionID)) > 200 {
			fields["remote_submission_id"] = "平台稿件编号不能超过 200 个字符"
		}
		if input.RemoteURL != "" {
			parsed, err := url.ParseRequestURI(input.RemoteURL)
			if err != nil ||
				(parsed.Scheme != "http" && parsed.Scheme != "https") ||
				parsed.Host == "" {
				fields["remote_url"] = "稿件地址必须是完整的 http 或 https 地址"
			}
		}
	case "remote_not_created":
		if input.RemoteSubmissionID != "" || input.RemoteURL != "" {
			fields["remote_submission_id"] = "确认未生成稿件时不能填写远端稿件信息"
		}
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func normalizeAccountInput(input *CreateAccountInput) {
	input.Platform = strings.ToLower(strings.TrimSpace(input.Platform))
	input.Name = strings.TrimSpace(input.Name)
	input.AuthMode = strings.ToLower(strings.TrimSpace(input.AuthMode))
	normalizeOptionalUUID(&input.CookieProfileID)
}

func validateAccountInput(input CreateAccountInput) error {
	fields := validateName("name", input.Name, 80)
	if !validPlatform(input.Platform) {
		fields["platform"] = "平台必须是 AcFun 或 bilibili"
	}
	if input.AuthMode != "cookie" && input.AuthMode != "fixture" {
		fields["auth_mode"] = "认证方式必须是 Cookie 或本地验收"
	}
	if input.AuthMode == "cookie" &&
		(input.CookieProfileID == nil || !identity.IsUUID(*input.CookieProfileID)) {
		fields["cookie_profile_id"] = "Cookie 认证必须选择有效的 Cookie 配置"
	}
	if input.AuthMode == "fixture" && input.CookieProfileID != nil {
		fields["cookie_profile_id"] = "本地验收账号不能绑定真实 Cookie"
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func normalizeTranscodePreset(input *TranscodePresetInput) {
	input.Name = strings.TrimSpace(input.Name)
	input.EncoderMode = strings.ToLower(strings.TrimSpace(input.EncoderMode))
	input.VideoCodec = strings.ToLower(strings.TrimSpace(input.VideoCodec))
	input.AudioCodec = strings.ToLower(strings.TrimSpace(input.AudioCodec))
	input.Container = strings.ToLower(strings.TrimSpace(input.Container))
	input.CPUPreset = strings.ToLower(strings.TrimSpace(input.CPUPreset))
	input.HighResolutionCPUPreset = strings.ToLower(
		strings.TrimSpace(input.HighResolutionCPUPreset),
	)
	for index := range input.CustomArguments {
		input.CustomArguments[index] = strings.TrimSpace(input.CustomArguments[index])
	}
}

func validateTranscodePreset(input TranscodePresetInput) error {
	fields := validateName("name", input.Name, 80)
	if !slices.Contains([]string{"auto", "cpu", "nvidia", "intel", "amd"}, input.EncoderMode) {
		fields["encoder_mode"] = "编码模式无效"
	}
	if !slices.Contains([]string{"h264", "hevc", "copy"}, input.VideoCodec) {
		fields["video_codec"] = "视频编码无效"
	}
	if !slices.Contains([]string{"aac", "copy"}, input.AudioCodec) {
		fields["audio_codec"] = "音频编码无效"
	}
	if !slices.Contains([]string{"mp4", "mkv"}, input.Container) {
		fields["container"] = "封装格式无效"
	}
	validPresets := []string{
		"ultrafast", "superfast", "veryfast", "faster", "fast",
		"medium", "slow", "slower", "veryslow",
	}
	if !slices.Contains(validPresets, input.CPUPreset) {
		fields["cpu_preset"] = "CPU 预设无效"
	}
	if !slices.Contains(validPresets, input.HighResolutionCPUPreset) {
		fields["high_resolution_cpu_preset"] = "高分辨率 CPU 预设无效"
	}
	if input.MaximumHeight != 0 && (input.MaximumHeight < 240 || input.MaximumHeight > 4320) {
		fields["maximum_height"] = "最大高度必须为 0，或介于 240–4320"
	}
	if input.VideoBitrateKbps < 0 || input.VideoBitrateKbps > 200000 {
		fields["video_bitrate_kbps"] = "视频码率必须介于 0–200000 Kbps"
	}
	if input.AudioBitrateKbps < 32 || input.AudioBitrateKbps > 1024 {
		fields["audio_bitrate_kbps"] = "音频码率必须介于 32–1024 Kbps"
	}
	if len(input.CustomArguments) > 32 {
		fields["custom_arguments"] = "自定义参数最多 32 项"
	}
	allowedOptions := map[string]struct{}{
		"-af":         {},
		"-bufsize":    {},
		"-g":          {},
		"-keyint_min": {},
		"-level":      {},
		"-maxrate":    {},
		"-movflags":   {},
		"-pix_fmt":    {},
		"-profile:v":  {},
		"-r":          {},
		"-vf":         {},
	}
	expectValue := false
	for index, value := range input.CustomArguments {
		if value == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") {
			fields["custom_arguments"] = "自定义参数包含空项、过长或非法内容"
			break
		}
		if expectValue {
			if strings.Contains(value, "://") {
				fields["custom_arguments"] = "自定义参数不能引用网络地址"
				break
			}
			expectValue = false
			continue
		}
		if _, allowed := allowedOptions[value]; !allowed {
			fields["custom_arguments"] = fmt.Sprintf(
				"第 %d 项不是允许的 FFmpeg 参数",
				index+1,
			)
			break
		}
		expectValue = true
	}
	if expectValue {
		fields["custom_arguments"] = "最后一个自定义参数缺少取值"
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func normalizePostingStrategy(input *PostingStrategyInput) {
	input.Name = strings.TrimSpace(input.Name)
	input.AutomationMode = strings.ToLower(strings.TrimSpace(input.AutomationMode))
	input.TargetPlatforms = normalizePlatforms(input.TargetPlatforms)
	input.RepostStatementVersion = strings.ToLower(
		strings.TrimSpace(input.RepostStatementVersion),
	)
	input.ScheduleMode = strings.ToLower(strings.TrimSpace(input.ScheduleMode))
	normalizeOptionalUUID(&input.TranscodePresetID)
	input.AccountBindings = normalizeStringMap(input.AccountBindings, true)
	input.CategoryBindings = normalizeStringMap(input.CategoryBindings, false)
	input.TitleTemplates = normalizeStringMap(input.TitleTemplates, false)
	input.DescriptionTemplates = normalizeStringMap(input.DescriptionTemplates, false)
	input.DefaultTags = normalizeTags(input.DefaultTags)
	if input.ScheduleTime != nil {
		value := strings.TrimSpace(*input.ScheduleTime)
		if value == "" {
			input.ScheduleTime = nil
		} else {
			input.ScheduleTime = &value
		}
	}
}

func (s *Service) validatePostingStrategy(
	ctx context.Context,
	input PostingStrategyInput,
) error {
	fields := validateName("name", input.Name, 120)
	if !slices.Contains(
		[]string{AutomationManual, AutomationAutomatic},
		input.AutomationMode,
	) {
		fields["automation_mode"] = "自动化模式必须是审核后手动投稿或审核后自动投稿"
	}
	if len(input.TargetPlatforms) == 0 {
		fields["target_platforms"] = "至少选择一个投稿平台"
	}
	for _, platform := range input.TargetPlatforms {
		if !validPlatform(platform) {
			fields["target_platforms"] = "投稿平台只支持 AcFun 和 bilibili"
			continue
		}
		accountID := input.AccountBindings[platform]
		if !identity.IsUUID(accountID) {
			fields["account_bindings."+platform] = "请选择该平台的投稿账号"
		} else {
			account, err := s.store.GetAccount(ctx, accountID)
			if errors.Is(err, ErrNotFound) {
				fields["account_bindings."+platform] = "选择的投稿账号不存在"
			} else if err != nil {
				return err
			} else if account.Platform != platform {
				fields["account_bindings."+platform] = "投稿账号与平台不匹配"
			}
		}
		if strings.TrimSpace(input.CategoryBindings[platform]) == "" {
			fields["category_bindings."+platform] = "请选择该平台的视频分区"
		}
		if title := input.TitleTemplates[platform]; len([]rune(title)) > 500 {
			fields["title_templates."+platform] = "标题模板不能超过 500 个字符"
		}
		if description := input.DescriptionTemplates[platform]; len([]rune(description)) > 20000 {
			fields["description_templates."+platform] = "简介模板不能超过 20000 个字符"
		}
	}
	if len(input.DefaultTags) > 20 {
		fields["default_tags"] = "默认标签不能超过 20 个"
	}
	if input.RepostStatementVersion != "brief_v1" &&
		input.RepostStatementVersion != "full_v1" {
		fields["repost_statement_version"] = "转载声明必须选择简版或完整版"
	}
	if input.TranscodePresetID != nil {
		preset, err := s.store.GetTranscodePreset(ctx, *input.TranscodePresetID)
		if errors.Is(err, ErrNotFound) {
			fields["transcode_preset_id"] = "选择的转码预设不存在"
		} else if err != nil {
			return err
		} else if !preset.Enabled {
			fields["transcode_preset_id"] = "选择的转码预设已停用"
		}
	}
	if input.ScheduleMode != "immediate" && input.ScheduleMode != "daily_time" {
		fields["schedule_mode"] = "投稿时间必须是立即或每日定时"
	}
	if input.ScheduleMode == "daily_time" {
		if input.ScheduleTime == nil || !validClock(*input.ScheduleTime) {
			fields["schedule_time"] = "每日定时必须填写 HH:MM"
		}
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func validPlatform(value string) bool {
	return value == PlatformAcFun || value == PlatformBilibili
}

func validClock(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return false
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	return hourErr == nil && minuteErr == nil &&
		hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59
}

func validateName(field string, value string, maximum int) map[string]string {
	fields := map[string]string{}
	length := len([]rune(value))
	if length < 1 || length > maximum {
		fields[field] = fmt.Sprintf("名称必须为 1–%d 个字符", maximum)
	}
	return fields
}

func normalizeOptionalUUID(value **string) {
	if *value == nil {
		return
	}
	normalized := strings.TrimSpace(**value)
	if normalized == "" {
		*value = nil
		return
	}
	*value = &normalized
}

func normalizePlatforms(values []string) []string {
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" || slices.Contains(result, value) {
			continue
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func normalizeStringMap(values map[string]string, lowerValue bool) map[string]string {
	result := make(map[string]string, len(values))
	for rawKey, rawValue := range values {
		key := strings.ToLower(strings.TrimSpace(rawKey))
		value := strings.TrimSpace(rawValue)
		if lowerValue {
			value = strings.ToLower(value)
		}
		if key == "" || value == "" {
			continue
		}
		result[key] = value
	}
	return result
}

func normalizeTags(values []string) []string {
	result := make([]string, 0, len(values))
	for _, raw := range values {
		for _, part := range strings.FieldsFunc(raw, func(value rune) bool {
			return value == ',' || value == '，' || value == '\n'
		}) {
			value := strings.TrimSpace(part)
			if value == "" || slices.Contains(result, value) {
				continue
			}
			result = append(result, value)
		}
	}
	return result
}
