package monitors

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/visoraft/visoraft/internal/identity"
	"github.com/visoraft/visoraft/internal/settings"
	"github.com/visoraft/visoraft/internal/tasks"
)

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

type Service struct {
	store    *PostgresStore
	settings *settings.Service
	tasks    *tasks.Service
	now      func() time.Time
}

func NewService(
	store *PostgresStore,
	settingsService *settings.Service,
	taskService *tasks.Service,
) *Service {
	return &Service{
		store:    store,
		settings: settingsService,
		tasks:    taskService,
		now:      time.Now,
	}
}

func (s *Service) List(ctx context.Context) ([]Monitor, error) {
	return s.store.List(ctx)
}

func (s *Service) Get(ctx context.Context, id string) (Monitor, error) {
	if !identity.IsUUID(id) {
		return Monitor{}, tasks.ErrInvalidID
	}
	return s.store.Get(ctx, id)
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Monitor, error) {
	normalizeInput(&input)
	if err := validateInput(input); err != nil {
		return Monitor{}, err
	}
	if err := s.validateTaskTemplateReferences(ctx, input.TaskTemplate); err != nil {
		return Monitor{}, err
	}
	id, err := identity.NewUUID()
	if err != nil {
		return Monitor{}, err
	}
	return s.store.Create(ctx, id, input, s.now().UTC())
}

func (s *Service) Update(
	ctx context.Context,
	id string,
	input UpdateInput,
) (Monitor, error) {
	if !identity.IsUUID(id) {
		return Monitor{}, tasks.ErrInvalidID
	}
	normalizeInput(&input.CreateInput)
	if input.ExpectedVersion < 1 {
		return Monitor{}, &ValidationError{
			Fields: map[string]string{"expected_version": "缺少有效的配置版本"},
		}
	}
	if err := validateInput(input.CreateInput); err != nil {
		return Monitor{}, err
	}
	if err := s.validateTaskTemplateReferences(ctx, input.TaskTemplate); err != nil {
		return Monitor{}, err
	}
	return s.store.Update(ctx, id, input, s.now().UTC())
}

func (s *Service) Pause(ctx context.Context, id string) (Monitor, error) {
	if !identity.IsUUID(id) {
		return Monitor{}, tasks.ErrInvalidID
	}
	return s.store.SetEnabled(ctx, id, false, s.now().UTC())
}

func (s *Service) Resume(ctx context.Context, id string) (Monitor, error) {
	if !identity.IsUUID(id) {
		return Monitor{}, tasks.ErrInvalidID
	}
	return s.store.SetEnabled(ctx, id, true, s.now().UTC())
}

func (s *Service) RunNow(ctx context.Context, id string) (Run, error) {
	monitor, err := s.Get(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if err := s.ensureRunnable(ctx); err != nil {
		return Run{}, err
	}
	return s.store.CreateRun(ctx, monitor, "manual", s.now().UTC())
}

func (s *Service) Delete(
	ctx context.Context,
	id string,
	input DeleteInput,
) error {
	if !identity.IsUUID(id) {
		return tasks.ErrInvalidID
	}
	input.HistoryMode = strings.ToLower(strings.TrimSpace(input.HistoryMode))
	if input.HistoryMode != "archive" && input.HistoryMode != "purge" {
		return &ValidationError{
			Fields: map[string]string{
				"history_mode": "请选择保留历史或永久删除",
			},
		}
	}
	return s.store.Delete(ctx, id, input.HistoryMode, s.now().UTC())
}

func (s *Service) History(ctx context.Context, id string) (History, error) {
	if !identity.IsUUID(id) {
		return History{}, tasks.ErrInvalidID
	}
	return s.store.History(ctx, id, 50)
}

func (s *Service) EnqueueItems(
	ctx context.Context,
	monitorID string,
	input EnqueueItemsInput,
) (EnqueueItemsResult, error) {
	if !identity.IsUUID(monitorID) {
		return EnqueueItemsResult{}, tasks.ErrInvalidID
	}
	itemIDs := normalizeList(input.ItemIDs)
	fields := map[string]string{}
	if len(itemIDs) == 0 {
		fields["item_ids"] = "请至少选择一条尚未建单的发现结果"
	} else if len(itemIDs) > 100 {
		fields["item_ids"] = "单次最多可加入 100 条任务"
	}
	for _, itemID := range itemIDs {
		if !identity.IsUUID(itemID) {
			fields["item_ids"] = "发现结果 ID 格式无效"
			break
		}
	}
	if len(fields) > 0 {
		return EnqueueItemsResult{}, &ValidationError{Fields: fields}
	}
	monitor, err := s.Get(ctx, monitorID)
	if err != nil {
		return EnqueueItemsResult{}, err
	}
	if s.tasks == nil {
		return EnqueueItemsResult{}, fmt.Errorf("monitor task service is unavailable")
	}
	if err := s.validateTaskTemplateReferences(ctx, monitor.TaskTemplate); err != nil {
		return EnqueueItemsResult{}, err
	}
	items, err := s.store.ItemsByIDs(ctx, monitorID, itemIDs)
	if err != nil {
		return EnqueueItemsResult{}, err
	}
	byID := make(map[string]Item, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	result := EnqueueItemsResult{
		RequestedCount: len(itemIDs),
		Items:          make([]EnqueueItemResult, 0, len(itemIDs)),
	}
	for _, itemID := range itemIDs {
		item, exists := byID[itemID]
		if !exists {
			result.FailedCount++
			result.Items = append(result.Items, EnqueueItemResult{
				ItemID: itemID, Status: "failed", Message: "发现结果不存在或不属于当前监控",
			})
			continue
		}
		itemResult := s.enqueueItem(ctx, monitor, item)
		result.Items = append(result.Items, itemResult)
		switch itemResult.Status {
		case "created":
			result.CreatedCount++
		case "duplicate":
			result.DuplicateCount++
		default:
			result.FailedCount++
		}
	}
	return result, nil
}

func (s *Service) enqueueItem(
	ctx context.Context,
	monitor Monitor,
	item Item,
) EnqueueItemResult {
	result := EnqueueItemResult{ItemID: item.ID}
	if item.TaskID != nil {
		result.Status = "duplicate"
		result.TaskID = item.TaskID
		result.Message = "该发现结果已经关联任务"
		return result
	}
	if !itemCanEnqueue(item) {
		result.Status = "failed"
		result.Message = "只有尚未关联任务的已接收、重复发现、原任务已删除或建单失败结果可以加入任务"
		return result
	}
	now := s.now().UTC()
	reserved, err := s.store.ReserveIngestion(
		ctx, item.ExternalVideoID, monitor.ID, now,
	)
	if err != nil {
		result.Status = "failed"
		result.Message = "无法预留任务：" + err.Error()
		return result
	}
	if !reserved {
		existingTaskID, state, lookupErr := s.store.Ingestion(ctx, item.ExternalVideoID)
		if lookupErr != nil {
			result.Status = "failed"
			result.Message = "无法核对全局去重状态：" + lookupErr.Error()
			return result
		}
		if existingTaskID != nil {
			_ = s.store.LinkItemTask(
				ctx, item.ID, "duplicate", "该视频已由其他监控创建任务", existingTaskID,
			)
			result.Status = "duplicate"
			result.TaskID = existingTaskID
			result.Message = "该视频已存在任务，已关联现有任务"
			return result
		}
		result.Status = "failed"
		result.Message = "该视频正在由其他操作建单，请稍后重试（" + state + "）"
		return result
	}
	task, err := s.tasks.Create(ctx, tasks.CreateInput{
		SourceURL:              item.SourceURL,
		TargetPlatforms:        monitor.TaskTemplate.TargetPlatforms,
		CookieProfileID:        monitor.TaskTemplate.CookieProfileID,
		RepostStatementVersion: monitor.TaskTemplate.RepostStatementVersion,
		PostingStrategyID:      monitor.TaskTemplate.PostingStrategyID,
		AutoPublish:            monitor.TaskTemplate.AutoPublish,
		Origin: &tasks.TaskOrigin{
			Kind:            "monitor",
			MonitorID:       monitor.ID,
			MonitorName:     monitor.Name,
			SeriesTitle:     monitor.SeriesTitle,
			SeriesScopeKey:  item.SeriesScopeKey,
			SeriesScopeName: item.SeriesScopeName,
			EpisodeNumber:   item.EpisodeNumber,
		},
	})
	if err != nil {
		_ = s.store.ReleaseIngestion(ctx, item.ExternalVideoID)
		_ = s.store.LinkItemTask(ctx, item.ID, "task_failed", err.Error(), nil)
		result.Status = "failed"
		result.Message = "创建任务失败：" + err.Error()
		return result
	}
	if err := s.store.FinalizeIngestion(
		ctx, item.ExternalVideoID, task.ID, monitor.ID, item.RunID, now,
	); err != nil {
		result.Status = "failed"
		result.TaskID = &task.ID
		result.Message = "任务已创建，但监控关联失败：" + err.Error()
		return result
	}
	if err := s.store.LinkItemTask(
		ctx, item.ID, "task_created", "手动加入统一任务流水线", &task.ID,
	); err != nil {
		result.Status = "failed"
		result.TaskID = &task.ID
		result.Message = "任务已创建，但结果回写失败：" + err.Error()
		return result
	}
	result.Status = "created"
	result.TaskID = &task.ID
	result.Message = "已加入统一任务流水线"
	return result
}

func itemCanEnqueue(item Item) bool {
	if item.TaskID != nil {
		return false
	}
	return item.Decision == "accepted" ||
		item.Decision == "duplicate" ||
		item.Decision == "task_created" ||
		item.Decision == "task_failed"
}

func (s *Service) YouTubeCategories(
	ctx context.Context,
	region string,
) ([]YouTubeCategory, error) {
	region = strings.ToUpper(strings.TrimSpace(region))
	if len(region) != 2 {
		return nil, &ValidationError{
			Fields: map[string]string{"region": "地区代码必须是两位代码"},
		}
	}
	current, err := s.settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	if current.YouTube.Provider == "fixture" {
		return []YouTubeCategory{
			{ID: "1", Title: "电影与动画（本地测试）", Provider: "fixture"},
			{ID: "10", Title: "音乐（本地测试）", Provider: "fixture"},
			{ID: "20", Title: "游戏（本地测试）", Provider: "fixture"},
			{ID: "22", Title: "人物与博客（本地测试）", Provider: "fixture"},
			{ID: "27", Title: "教育（本地测试）", Provider: "fixture"},
			{ID: "28", Title: "科学与技术（本地测试）", Provider: "fixture"},
		}, nil
	}
	if current.YouTube.Provider != "google" {
		return nil, &ConflictError{
			Code:    "youtube_provider_invalid",
			Message: "YouTube 数据提供商配置无效",
		}
	}
	apiKey, err := s.settings.ResolveSecret(ctx, settings.SecretYouTubeAPI)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, &ConflictError{
			Code:    "youtube_api_key_missing",
			Message: "缺少 YouTube Data API Key，请先前往设置中心配置",
		}
	}
	proxyPassword, err := s.settings.ResolveSecret(
		ctx,
		settings.SecretYouTubeProxyPassword,
	)
	if err != nil {
		return nil, err
	}
	client, err := youtubeHTTPClient(current.YouTube, proxyPassword)
	if err != nil {
		return nil, err
	}
	return loadGoogleCategories(
		ctx,
		client,
		current.YouTube.APIBaseURL,
		apiKey,
		region,
	)
}

func (s *Service) ensureRunnable(ctx context.Context) error {
	current, err := s.settings.Get(ctx)
	if err != nil {
		return err
	}
	if current.YouTube.Provider == "fixture" {
		return nil
	}
	if current.YouTube.Provider != "google" {
		return &ConflictError{
			Code:    "youtube_provider_invalid",
			Message: "YouTube 数据提供商配置无效",
		}
	}
	if !current.SecretConfigured[settings.SecretYouTubeAPI] {
		return &ConflictError{
			Code:    "youtube_api_key_missing",
			Message: "缺少 YouTube Data API Key，请先前往设置中心配置",
		}
	}
	return nil
}

func normalizeInput(input *CreateInput) {
	input.Name = strings.TrimSpace(input.Name)
	input.MonitorType = strings.ToLower(strings.TrimSpace(input.MonitorType))
	input.ChannelMode = strings.ToLower(strings.TrimSpace(input.ChannelMode))
	input.Query = strings.TrimSpace(input.Query)
	input.SeriesTitle = strings.TrimSpace(input.SeriesTitle)
	input.SeriesScopes = normalizeSeriesScopes(input.SeriesScopes)
	if input.MonitorType == "series" {
		if len(input.SeriesScopes) == 0 && input.EpisodeStart > 0 && input.EpisodeEnd >= input.EpisodeStart {
			input.SeriesScopes = []SeriesScope{{
				Key: "default", Query: input.Query,
				EpisodeStart: input.EpisodeStart, EpisodeEnd: input.EpisodeEnd,
			}}
		}
		if len(input.SeriesScopes) > 0 {
			input.EpisodeStart = input.SeriesScopes[0].EpisodeStart
			input.EpisodeEnd = input.SeriesScopes[0].EpisodeEnd
			for _, scope := range input.SeriesScopes[1:] {
				input.EpisodeStart = min(input.EpisodeStart, scope.EpisodeStart)
				input.EpisodeEnd = max(input.EpisodeEnd, scope.EpisodeEnd)
			}
		}
	} else {
		input.SeriesScopes = []SeriesScope{}
		input.SeriesTitle = ""
		input.EpisodeStart = 0
		input.EpisodeEnd = 0
	}
	input.ChannelIDs = normalizeList(input.ChannelIDs)
	input.IncludeKeywords = normalizeList(input.IncludeKeywords)
	input.ExcludeKeywords = normalizeList(input.ExcludeKeywords)
	input.ExcludeChannelIDs = normalizeList(input.ExcludeChannelIDs)
	input.RegionCode = strings.ToUpper(strings.TrimSpace(input.RegionCode))
	input.CategoryID = strings.TrimSpace(input.CategoryID)
	input.OrderBy = strings.TrimSpace(input.OrderBy)
	input.VideoTypes = normalizeListLower(input.VideoTypes)
	input.ScheduleType = strings.ToLower(strings.TrimSpace(input.ScheduleType))
	input.TaskTemplate.TargetPlatforms = normalizeListLower(
		input.TaskTemplate.TargetPlatforms,
	)
	input.TaskTemplate.RepostStatementVersion = strings.ToLower(
		strings.TrimSpace(input.TaskTemplate.RepostStatementVersion),
	)
	if input.TaskTemplate.CookieProfileID != nil {
		normalized := strings.TrimSpace(*input.TaskTemplate.CookieProfileID)
		if normalized == "" {
			input.TaskTemplate.CookieProfileID = nil
		} else {
			input.TaskTemplate.CookieProfileID = &normalized
		}
	}
	if input.TaskTemplate.PostingStrategyID != nil {
		normalized := strings.TrimSpace(*input.TaskTemplate.PostingStrategyID)
		if normalized == "" {
			input.TaskTemplate.PostingStrategyID = nil
		} else {
			input.TaskTemplate.PostingStrategyID = &normalized
		}
	}
	for _, date := range []*string{input.PublishedAfter, input.PublishedBefore} {
		if date != nil {
			normalized := strings.TrimSpace(*date)
			if normalized == "" {
				*date = ""
			} else if len(normalized) >= 10 {
				*date = normalized[:10]
			}
		}
	}
}

func normalizeSeriesScopes(values []SeriesScope) []SeriesScope {
	result := make([]SeriesScope, 0, len(values))
	usedKeys := map[string]bool{}
	for index, value := range values {
		value.Key = strings.ToLower(strings.TrimSpace(value.Key))
		value.Name = strings.TrimSpace(value.Name)
		value.Query = strings.TrimSpace(value.Query)
		if value.Key == "" || usedKeys[value.Key] {
			value.Key = fmt.Sprintf("part-%d", index+1)
		}
		usedKeys[value.Key] = true
		result = append(result, value)
	}
	return result
}

func validateInput(input CreateInput) error {
	fields := map[string]string{}
	if input.Name == "" {
		fields["name"] = "配置名称不能为空"
	} else if len([]rune(input.Name)) > 120 {
		fields["name"] = "配置名称不能超过 120 个字符"
	}
	if input.MonitorType != "search" && input.MonitorType != "channel" && input.MonitorType != "series" {
		fields["monitor_type"] = "监控类型必须为全网搜索、频道或剧集"
	}
	if !slices.Contains([]string{"search", "historical", "latest"}, input.ChannelMode) {
		fields["channel_mode"] = "频道模式无效"
	}
	if input.MonitorType == "search" &&
		input.Query == "" &&
		len(input.IncludeKeywords) == 0 {
		fields["query"] = "全网搜索至少需要一个关键词"
	}
	if input.MonitorType == "channel" && len(input.ChannelIDs) == 0 {
		fields["channel_ids"] = "频道监控至少需要一个频道主页链接、@账号或频道 ID"
	}
	if input.MonitorType == "series" {
		if input.SeriesTitle == "" {
			fields["series_title"] = "剧集监控必须填写剧名"
		}
		if len(input.SeriesScopes) == 0 {
			fields["series_scopes"] = "至少添加一个篇章或季度"
		} else if len(input.SeriesScopes) > 10 {
			fields["series_scopes"] = "单条监控最多包含 10 个篇章"
		} else {
			totalEpisodes := 0
			for index, scope := range input.SeriesScopes {
				if scope.EpisodeStart < 1 || scope.EpisodeEnd < scope.EpisodeStart {
					fields[fmt.Sprintf("series_scopes.%d", index)] = "集数范围无效"
					continue
				}
				totalEpisodes += scope.EpisodeEnd - scope.EpisodeStart + 1
			}
			if len(input.SeriesScopes) > 1 {
				for index, scope := range input.SeriesScopes {
					if scope.Name == "" {
						fields[fmt.Sprintf("series_scopes.%d", index)] = "多个篇章时必须填写篇章名称"
					}
				}
			}
			if totalEpisodes > 100 {
				fields["series_scopes"] = "单条监控最多覆盖 100 集"
			}
			requiredRequests := seriesRequiredRequests(len(input.SeriesScopes))
			if input.RateLimitRequests < requiredRequests {
				fields["rate_limit_requests"] = fmt.Sprintf(
					"当前集数范围至少需要 %d 次请求",
					requiredRequests,
				)
			}
		}
	} else if input.EpisodeStart != 0 || input.EpisodeEnd != 0 || input.SeriesTitle != "" || len(input.SeriesScopes) != 0 {
		fields["episode_range"] = "只有剧集监控可以配置剧名和集数范围"
	}
	if len(input.RegionCode) != 2 {
		fields["region_code"] = "地区代码必须是两位代码"
	}
	if input.LookbackDays < 1 || input.LookbackDays > 30 {
		fields["lookback_days"] = "回溯天数必须为 1–30"
	}
	if input.MaxResults < 1 || input.MaxResults > 50 {
		fields["max_results"] = "最大结果数必须为 1–50"
	}
	if !slices.Contains([]string{"viewCount", "date", "rating", "relevance"}, input.OrderBy) {
		fields["order_by"] = "排序方式无效"
	}
	if len(input.VideoTypes) == 0 {
		fields["video_types"] = "至少选择一种视频类型"
	}
	for _, kind := range input.VideoTypes {
		if !slices.Contains([]string{"video", "short", "live"}, kind) {
			fields["video_types"] = "视频类型包含未知值"
			break
		}
	}
	if input.MinViewCount < 0 || input.MinLikeCount < 0 || input.MinCommentCount < 0 {
		fields["thresholds"] = "互动指标不能为负数"
	}
	if input.MinDurationSeconds < 0 ||
		input.MaxDurationSeconds < 0 ||
		(input.MaxDurationSeconds > 0 &&
			input.MaxDurationSeconds < input.MinDurationSeconds) {
		fields["duration"] = "时长范围无效"
	}
	for key, value := range map[string]*string{
		"published_after":  input.PublishedAfter,
		"published_before": input.PublishedBefore,
	} {
		if value != nil && *value != "" && !datePattern.MatchString(*value) {
			fields[key] = "日期格式必须为 YYYY-MM-DD"
		}
	}
	if input.PublishedAfter != nil &&
		input.PublishedBefore != nil &&
		*input.PublishedAfter != "" &&
		*input.PublishedBefore != "" &&
		*input.PublishedAfter > *input.PublishedBefore {
		fields["published_before"] = "结束日期不能早于开始日期"
	}
	if input.ScheduleType != "manual" && input.ScheduleType != "automatic" {
		fields["schedule_type"] = "调度类型必须为手动或自动"
	}
	if input.ScheduleIntervalMinutes < 1 || input.ScheduleIntervalMinutes > 43200 {
		fields["schedule_interval_minutes"] = "调度间隔必须为 1–43200 分钟"
	}
	if input.RateLimitRequests < 1 || input.RateLimitRequests > 250 {
		fields["rate_limit_requests"] = "单次请求上限必须为 1–250"
	}
	if len(input.TaskTemplate.TargetPlatforms) == 0 {
		fields["task_template.target_platforms"] = "至少选择一个目标平台"
	}
	for _, platform := range input.TaskTemplate.TargetPlatforms {
		if platform != "acfun" && platform != "bilibili" {
			fields["task_template.target_platforms"] = "目标平台只支持 AcFun 和 bilibili"
		}
	}
	if input.TaskTemplate.RepostStatementVersion != tasks.StatementBriefV1 &&
		input.TaskTemplate.RepostStatementVersion != tasks.StatementFullV1 {
		fields["task_template.repost_statement_version"] = "请选择简版或完整版转载声明"
	}
	if input.TaskTemplate.CookieProfileID != nil &&
		!identity.IsUUID(*input.TaskTemplate.CookieProfileID) {
		fields["task_template.cookie_profile_id"] = "Cookie 配置 ID 格式无效"
	}
	if input.TaskTemplate.PostingStrategyID != nil &&
		!identity.IsUUID(*input.TaskTemplate.PostingStrategyID) {
		fields["task_template.posting_strategy_id"] = "投稿策略 ID 格式无效"
	}
	if input.TaskTemplate.AutoPublish && input.TaskTemplate.PostingStrategyID == nil {
		fields["task_template.auto_publish"] = "全自动投稿必须选择投稿策略"
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func (s *Service) validateTaskTemplateReferences(
	ctx context.Context,
	template TaskTemplate,
) error {
	fields := map[string]string{}
	if template.CookieProfileID == nil {
		currentSettings, err := s.settings.Get(ctx)
		if err != nil {
			return err
		}
		if currentSettings.YouTube.Provider != "fixture" {
			fields["task_template.cookie_profile_id"] = "请选择用于 YouTube 下载的 Cookie 配置"
		}
	} else {
		exists, err := s.store.CookieProfileExists(ctx, *template.CookieProfileID)
		if err != nil {
			return err
		}
		if !exists {
			fields["task_template.cookie_profile_id"] = "Cookie 配置不存在或尚无可用 Cookie"
		}
	}
	if template.PostingStrategyID != nil {
		strategy, err := s.store.PostingStrategy(ctx, *template.PostingStrategyID)
		if err != nil {
			return err
		}
		if strategy.ID == "" || !strategy.Enabled {
			fields["task_template.posting_strategy_id"] = "投稿策略不存在或已停用"
		} else {
			for _, platform := range template.TargetPlatforms {
				if !slices.Contains(strategy.TargetPlatforms, platform) {
					fields["task_template.target_platforms"] = "目标平台必须包含在投稿策略中"
					break
				}
			}
			if template.AutoPublish &&
				strategy.AutomationMode != "automatic_after_review" {
				fields["task_template.auto_publish"] = "全自动监控必须选择“审核后自动投稿”策略"
			}
		}
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func normalizeList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, raw := range values {
		for _, value := range strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == '，' || r == '、' ||
				r == ';' || r == '；' || r == '\n'
		}) {
			normalized := strings.TrimSpace(value)
			if normalized == "" || slices.Contains(result, normalized) {
				continue
			}
			result = append(result, normalized)
		}
	}
	return result
}

func normalizeListLower(values []string) []string {
	values = normalizeList(values)
	for index := range values {
		values[index] = strings.ToLower(values[index])
	}
	return values
}

func validateHTTPURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil &&
		parsed.Host != "" &&
		(parsed.Scheme == "http" || parsed.Scheme == "https")
}

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

func formatPreconditionError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("monitor precondition failed: %w", err)
}
