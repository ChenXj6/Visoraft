package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/visoraft/visoraft/internal/cookieprofiles"
)

type Service struct {
	store      *PostgresStore
	secretBox  *cookieprofiles.SecretBox
	httpClient *http.Client
	now        func() time.Time
}

type ProcessingConfig struct {
	ConfigSnapshot
	Secrets map[string]string `json:"secrets"`
	Runtime TaskRuntime       `json:"runtime"`
}

func NewService(store *PostgresStore, secretBox *cookieprofiles.SecretBox) *Service {
	return &Service{
		store:     store,
		secretBox: secretBox,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		now: time.Now,
	}
}

func (s *Service) Get(ctx context.Context) (Settings, error) {
	return s.store.Get(ctx)
}

func (s *Service) ResolveSecret(ctx context.Context, key string) (string, error) {
	if _, allowed := AllowedSecretKeys[key]; !allowed {
		return "", fmt.Errorf("unsupported setting secret %s", key)
	}
	return s.openCurrentSecret(ctx, key)
}

func (s *Service) Update(ctx context.Context, input UpdateInput) (Settings, error) {
	normalize(&input.ConfigSnapshot)
	if err := validate(input); err != nil {
		return Settings{}, err
	}

	sealed := make(map[string][]byte, len(input.Secrets))
	for key, value := range input.Secrets {
		if _, allowed := AllowedSecretKeys[key]; !allowed {
			return Settings{}, &ValidationError{
				Fields: map[string]string{"secrets." + key: "不支持的密钥字段"},
			}
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		packet, err := s.secretBox.Seal("setting:"+key, []byte(value))
		if err != nil {
			return Settings{}, fmt.Errorf("encrypt setting secret %s: %w", key, err)
		}
		sealed[key] = packet
	}
	input.ClearSecrets = uniqueAllowedKeys(input.ClearSecrets)
	for key := range sealed {
		if slices.Contains(input.ClearSecrets, key) {
			return Settings{}, &ValidationError{
				Fields: map[string]string{"secrets." + key: "不能同时更新并清除此密钥"},
			}
		}
	}
	return s.store.Update(ctx, input, sealed, s.now().UTC())
}

func (s *Service) ProcessingConfig(
	ctx context.Context,
	taskID string,
) (ProcessingConfig, error) {
	snapshot, encrypted, runtime, err := s.store.TaskProcessingConfig(ctx, taskID)
	if err != nil {
		return ProcessingConfig{}, err
	}
	result := ProcessingConfig{
		ConfigSnapshot: snapshot,
		Secrets:        make(map[string]string, len(encrypted)),
		Runtime:        runtime,
	}
	for key, ciphertext := range encrypted {
		plaintext, err := s.secretBox.Open("setting:"+key, ciphertext)
		if err != nil {
			return ProcessingConfig{}, fmt.Errorf("decrypt task secret %s: %w", key, err)
		}
		result.Secrets[key] = string(plaintext)
	}
	return result, nil
}

func (s *Service) TestConnection(
	ctx context.Context,
	input ConnectionTestInput,
) (ConnectionTestResult, error) {
	current, err := s.store.Get(ctx)
	if err != nil {
		return ConnectionTestResult{}, err
	}
	target := strings.TrimSpace(input.Target)
	started := s.now()
	var result ConnectionTestResult

	switch target {
	case "global", "subtitle_translation", "subtitle_qc", "smart_segmentation":
		endpoint, secretKey, err := effectiveModel(current.Models, target)
		if err != nil {
			return ConnectionTestResult{}, err
		}
		result, err = s.testModelEndpoint(ctx, target, endpoint, secretKey)
		if err != nil {
			return ConnectionTestResult{}, err
		}
	case "asr":
		result, err = s.testASR(ctx, current.Subtitle.ASR)
		if err != nil {
			return ConnectionTestResult{}, err
		}
	case "youtube":
		result, err = s.testYouTube(ctx, current.YouTube)
		if err != nil {
			return ConnectionTestResult{}, err
		}
	default:
		return ConnectionTestResult{}, &ValidationError{
			Fields: map[string]string{"target": "未知的连接测试目标"},
		}
	}

	result.Latency = s.now().Sub(started)
	result.LatencyMS = result.Latency.Milliseconds()
	result.CheckedAt = s.now().UTC()
	return result, nil
}

func (s *Service) testModelEndpoint(
	ctx context.Context,
	target string,
	endpoint ModelEndpoint,
	secretKey string,
) (ConnectionTestResult, error) {
	if endpoint.Provider == "fixture" {
		return s.testModelsURL(ctx, target, endpoint, secretKey, true)
	}
	if endpoint.Provider != "openai_compatible" {
		return ConnectionTestResult{}, &ValidationError{
			Fields: map[string]string{"target": "当前仅支持 OpenAI 兼容或本地验收提供商"},
		}
	}
	if !endpoint.Enabled && target == "global" {
		return ConnectionTestResult{}, &ValidationError{
			Fields: map[string]string{"target": "全局模型尚未启用"},
		}
	}
	return s.testModelsURL(ctx, target, endpoint, secretKey, false)
}

func (s *Service) testModelsURL(
	ctx context.Context,
	target string,
	endpoint ModelEndpoint,
	secretKey string,
	secretOptional bool,
) (ConnectionTestResult, error) {
	apiKey, err := s.openCurrentSecret(ctx, secretKey)
	if err != nil {
		return ConnectionTestResult{}, err
	}
	if apiKey == "" && !secretOptional {
		return ConnectionTestResult{}, &ValidationError{
			Fields: map[string]string{"target": "尚未配置 API Key"},
		}
	}
	modelsURL := strings.TrimRight(endpoint.BaseURL, "/") + "/models"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return ConnectionTestResult{}, fmt.Errorf("create model connection request: %w", err)
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return ConnectionTestResult{}, fmt.Errorf("model connection failed: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ConnectionTestResult{}, fmt.Errorf(
			"model service returned HTTP %d",
			response.StatusCode,
		)
	}
	return ConnectionTestResult{
		Target:   target,
		OK:       true,
		Message:  "模型服务可访问，凭据已通过服务端校验",
		Provider: endpoint.Provider,
		Model:    endpoint.Model,
	}, nil
}

func (s *Service) testASR(
	ctx context.Context,
	config ASRConfig,
) (ConnectionTestResult, error) {
	if !config.Enabled {
		return ConnectionTestResult{}, &ValidationError{
			Fields: map[string]string{"target": "ASR 尚未启用"},
		}
	}
	if config.Provider == "aliyun_paraformer" {
		return s.testAliyunParaformer(ctx, config)
	}
	endpoint := ModelEndpoint{
		Enabled:        true,
		Provider:       config.Provider,
		BaseURL:        config.BaseURL,
		Model:          config.Model,
		TimeoutSeconds: config.TimeoutSeconds,
	}
	result, err := s.testModelsURL(
		ctx,
		"asr",
		endpoint,
		SecretSubtitleASR,
		config.Provider == "fixture",
	)
	if err == nil {
		result.Message = "ASR 服务可访问；实际转写将在任务音频上执行"
	}
	return result, err
}

func (s *Service) testAliyunParaformer(
	ctx context.Context,
	config ASRConfig,
) (ConnectionTestResult, error) {
	apiKey, err := s.openCurrentSecret(ctx, SecretSubtitleASR)
	if err != nil {
		return ConnectionTestResult{}, err
	}
	if apiKey == "" {
		return ConnectionTestResult{}, &ValidationError{
			Fields: map[string]string{"target": "尚未配置阿里云百炼 API Key"},
		}
	}
	return s.testAliyunUploadPolicy(ctx, config, apiKey)
}

func (s *Service) testAliyunUploadPolicy(
	ctx context.Context,
	config ASRConfig,
	apiKey string,
) (ConnectionTestResult, error) {
	policyURL, err := url.Parse(strings.TrimRight(config.BaseURL, "/") + "/uploads")
	if err != nil {
		return ConnectionTestResult{}, fmt.Errorf("parse aliyun asr url: %w", err)
	}
	query := policyURL.Query()
	query.Set("action", "getPolicy")
	query.Set("model", config.Model)
	policyURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, policyURL.String(), nil)
	if err != nil {
		return ConnectionTestResult{}, fmt.Errorf("create aliyun asr request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return ConnectionTestResult{}, fmt.Errorf("aliyun asr connection failed: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 256<<10))
	if err != nil {
		return ConnectionTestResult{}, fmt.Errorf("read aliyun asr response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ConnectionTestResult{}, fmt.Errorf(
			"aliyun asr service returned HTTP %d",
			response.StatusCode,
		)
	}
	var value struct {
		Data struct {
			UploadHost string `json:"upload_host"`
			UploadDir  string `json:"upload_dir"`
			Policy     string `json:"policy"`
			Signature  string `json:"signature"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return ConnectionTestResult{}, fmt.Errorf("decode aliyun asr response: %w", err)
	}
	if value.Data.UploadHost == "" || value.Data.UploadDir == "" ||
		value.Data.Policy == "" || value.Data.Signature == "" {
		return ConnectionTestResult{}, errors.New("aliyun asr upload policy is incomplete")
	}
	return ConnectionTestResult{
		Target:   "asr",
		OK:       true,
		Message:  "阿里云 Paraformer 凭证与临时上传通道可用；未提交计费转写任务",
		Provider: config.Provider,
		Model:    config.Model,
	}, nil
}

func (s *Service) testYouTube(
	ctx context.Context,
	config YouTubeConfig,
) (ConnectionTestResult, error) {
	if config.Provider == "fixture" {
		if _, err := url.ParseRequestURI(config.FixtureMediaURL); err != nil {
			return ConnectionTestResult{}, &ValidationError{
				Fields: map[string]string{"target": "本地验收媒体 URL 无效"},
			}
		}
		return ConnectionTestResult{
			Target:   "youtube",
			OK:       true,
			Message:  "本地验收发现器已就绪；其数据会明确标记为测试数据",
			Provider: "fixture",
		}, nil
	}
	if config.Provider != "google" {
		return ConnectionTestResult{}, &ValidationError{
			Fields: map[string]string{"target": "未知的 YouTube 数据提供商"},
		}
	}
	apiKey, err := s.openCurrentSecret(ctx, SecretYouTubeAPI)
	if err != nil {
		return ConnectionTestResult{}, err
	}
	if apiKey == "" {
		return ConnectionTestResult{}, &ValidationError{
			Fields: map[string]string{"target": "尚未配置 YouTube Data API Key"},
		}
	}
	endpoint, err := url.Parse(strings.TrimRight(config.APIBaseURL, "/") + "/videos")
	if err != nil {
		return ConnectionTestResult{}, fmt.Errorf("parse youtube api url: %w", err)
	}
	query := endpoint.Query()
	query.Set("part", "id")
	query.Set("id", "dQw4w9WgXcQ")
	query.Set("key", apiKey)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return ConnectionTestResult{}, fmt.Errorf("create youtube connection request: %w", err)
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return ConnectionTestResult{}, fmt.Errorf("youtube api connection failed: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var message struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &message)
		if message.Error.Message == "" {
			message.Error.Message = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		return ConnectionTestResult{}, fmt.Errorf("youtube api rejected credentials: %s", message.Error.Message)
	}
	return ConnectionTestResult{
		Target:   "youtube",
		OK:       true,
		Message:  "YouTube Data API 可访问，密钥校验通过",
		Provider: "google",
	}, nil
}

func (s *Service) openCurrentSecret(ctx context.Context, key string) (string, error) {
	ciphertext, _, err := s.store.Secret(ctx, key)
	if err != nil || len(ciphertext) == 0 {
		return "", err
	}
	plaintext, err := s.secretBox.Open("setting:"+key, ciphertext)
	if err != nil {
		return "", fmt.Errorf("decrypt setting secret %s: %w", key, err)
	}
	return string(plaintext), nil
}

func normalize(snapshot *ConfigSnapshot) {
	snapshot.Review.Mode = strings.ToLower(strings.TrimSpace(snapshot.Review.Mode))
	snapshot.Review.AutomaticFallback = strings.ToLower(strings.TrimSpace(snapshot.Review.AutomaticFallback))
	for _, endpoint := range []*ModelEndpoint{
		&snapshot.Models.Global,
		&snapshot.Models.SubtitleTranslation,
		&snapshot.Models.SubtitleQC,
		&snapshot.Models.SmartSegmentation,
	} {
		endpoint.Mode = strings.ToLower(strings.TrimSpace(endpoint.Mode))
		endpoint.Provider = strings.ToLower(strings.TrimSpace(endpoint.Provider))
		endpoint.BaseURL = strings.TrimRight(strings.TrimSpace(endpoint.BaseURL), "/")
		endpoint.Model = strings.TrimSpace(endpoint.Model)
	}
	snapshot.Subtitle.SourceStrategy = strings.ToLower(strings.TrimSpace(snapshot.Subtitle.SourceStrategy))
	snapshot.Subtitle.SourceLanguage = strings.TrimSpace(snapshot.Subtitle.SourceLanguage)
	snapshot.Subtitle.TargetLanguage = strings.TrimSpace(snapshot.Subtitle.TargetLanguage)
	snapshot.Subtitle.ExistingChinese.HardcodedAction = strings.ToLower(
		strings.TrimSpace(snapshot.Subtitle.ExistingChinese.HardcodedAction),
	)
	snapshot.Subtitle.ExistingChinese.UncertainAction = strings.ToLower(
		strings.TrimSpace(snapshot.Subtitle.ExistingChinese.UncertainAction),
	)
	snapshot.Subtitle.ASR.Provider = strings.ToLower(strings.TrimSpace(snapshot.Subtitle.ASR.Provider))
	snapshot.Subtitle.ASR.BaseURL = strings.TrimRight(strings.TrimSpace(snapshot.Subtitle.ASR.BaseURL), "/")
	snapshot.Subtitle.ASR.Model = strings.TrimSpace(snapshot.Subtitle.ASR.Model)
	snapshot.Subtitle.ASR.Language = strings.TrimSpace(snapshot.Subtitle.ASR.Language)
	snapshot.Subtitle.Style.FontName = strings.TrimSpace(snapshot.Subtitle.Style.FontName)
	snapshot.YouTube.Provider = strings.ToLower(strings.TrimSpace(snapshot.YouTube.Provider))
	snapshot.YouTube.APIBaseURL = strings.TrimRight(strings.TrimSpace(snapshot.YouTube.APIBaseURL), "/")
	snapshot.YouTube.ProxyURL = strings.TrimSpace(snapshot.YouTube.ProxyURL)
	snapshot.YouTube.ProxyUsername = strings.TrimSpace(snapshot.YouTube.ProxyUsername)
	snapshot.YouTube.FixtureMediaURL = strings.TrimSpace(snapshot.YouTube.FixtureMediaURL)
	snapshot.Transcode.EncoderMode = strings.ToLower(strings.TrimSpace(snapshot.Transcode.EncoderMode))
	snapshot.Transcode.VideoCodec = strings.ToLower(strings.TrimSpace(snapshot.Transcode.VideoCodec))
	snapshot.Transcode.AudioCodec = strings.ToLower(strings.TrimSpace(snapshot.Transcode.AudioCodec))
	snapshot.Transcode.Container = strings.ToLower(strings.TrimSpace(snapshot.Transcode.Container))
	snapshot.Transcode.CPUPreset = strings.ToLower(strings.TrimSpace(snapshot.Transcode.CPUPreset))
	snapshot.Transcode.HighResolutionCPUPreset = strings.ToLower(
		strings.TrimSpace(snapshot.Transcode.HighResolutionCPUPreset),
	)
	for index := range snapshot.Transcode.CustomArguments {
		snapshot.Transcode.CustomArguments[index] = strings.TrimSpace(
			snapshot.Transcode.CustomArguments[index],
		)
	}
	snapshot.Moderation.Provider = strings.ToLower(strings.TrimSpace(snapshot.Moderation.Provider))
	snapshot.Moderation.Region = strings.ToLower(strings.TrimSpace(snapshot.Moderation.Region))
	snapshot.Moderation.TextService = strings.TrimSpace(snapshot.Moderation.TextService)
	snapshot.Moderation.ImageService = strings.TrimSpace(snapshot.Moderation.ImageService)
	snapshot.Moderation.VideoService = strings.TrimSpace(snapshot.Moderation.VideoService)
	snapshot.Moderation.HighRiskAction = strings.ToLower(
		strings.TrimSpace(snapshot.Moderation.HighRiskAction),
	)
	snapshot.Moderation.MediumRiskAction = strings.ToLower(
		strings.TrimSpace(snapshot.Moderation.MediumRiskAction),
	)
	snapshot.Moderation.FailureAction = strings.ToLower(
		strings.TrimSpace(snapshot.Moderation.FailureAction),
	)
	for _, prompt := range []*PromptEntry{
		&snapshot.Prompts.SubtitleTranslation,
		&snapshot.Prompts.SubtitleTranslationStrict,
		&snapshot.Prompts.SubtitleQC,
		&snapshot.Prompts.MetadataTranslation,
		&snapshot.Prompts.MetadataDescriptionRetry,
		&snapshot.Prompts.SmartSegmentation,
	} {
		prompt.Mode = strings.ToLower(strings.TrimSpace(prompt.Mode))
		prompt.Text = strings.TrimSpace(prompt.Text)
	}
}

func validate(input UpdateInput) error {
	fields := map[string]string{}
	if input.ExpectedVersion < 1 {
		fields["expected_version"] = "缺少有效的配置版本"
	}
	if input.Review.Mode != "manual" && input.Review.Mode != "automatic" {
		fields["review.mode"] = "审核模式必须是手动或自动"
	}
	if input.Review.AutomaticFallback != "manual" && input.Review.AutomaticFallback != "reject" {
		fields["review.automatic_fallback"] = "自动审核失败后必须转人工或拒绝"
	}
	if input.Review.Rules.MinimumDescription < 0 || input.Review.Rules.MinimumDescription > 10000 {
		fields["review.rules.minimum_description_length"] = "描述最小长度必须为 0–10000"
	}
	if input.Review.Rules.MaximumDurationSeconds < 0 || input.Review.Rules.MaximumDurationSeconds > 86400 {
		fields["review.rules.maximum_duration_seconds"] = "最大时长必须为 0–86400 秒"
	}
	if input.Review.Rules.MinimumSubtitleQCScore < 0 || input.Review.Rules.MinimumSubtitleQCScore > 100 {
		fields["review.rules.minimum_subtitle_qc_score"] = "字幕质检阈值必须为 0–100"
	}

	validateEndpoint(fields, "models.global", input.Models.Global, true)
	validateEndpoint(fields, "models.subtitle_translation", input.Models.SubtitleTranslation, false)
	validateEndpoint(fields, "models.subtitle_qc", input.Models.SubtitleQC, false)
	validateEndpoint(fields, "models.smart_segmentation", input.Models.SmartSegmentation, false)

	if !slices.Contains(
		[]string{"youtube_then_asr", "youtube_only", "asr_only", "youtube_manual_then_asr"},
		input.Subtitle.SourceStrategy,
	) {
		fields["subtitle.source_strategy"] = "未知的字幕来源策略"
	}
	if input.Subtitle.ExistingChinese.Enabled {
		validateExistingChinese(fields, input.Subtitle.ExistingChinese)
	}
	if input.Subtitle.ASR.Enabled {
		if input.Subtitle.ASR.Provider != "openai_compatible" &&
			input.Subtitle.ASR.Provider != "voxtral" &&
			input.Subtitle.ASR.Provider != "aliyun_paraformer" &&
			input.Subtitle.ASR.Provider != "fixture" {
			fields["subtitle.asr.provider"] = "未知的 ASR 提供商"
		}
		validateURL(fields, "subtitle.asr.base_url", input.Subtitle.ASR.BaseURL)
		if input.Subtitle.ASR.Model == "" {
			fields["subtitle.asr.model"] = "ASR 模型不能为空"
		}
		validateASRLanguageHint(fields, input.Subtitle)
	}
	if input.Subtitle.ASR.TimeoutSeconds < 10 || input.Subtitle.ASR.TimeoutSeconds > 3600 {
		fields["subtitle.asr.timeout_seconds"] = "ASR 超时必须为 10–3600 秒"
	}
	if input.Subtitle.ASR.MaxRetries < 0 || input.Subtitle.ASR.MaxRetries > 10 {
		fields["subtitle.asr.max_retries"] = "ASR 重试次数必须为 0–10"
	}
	if input.Subtitle.ASR.ChunkSeconds < 30 || input.Subtitle.ASR.ChunkSeconds > 3600 {
		fields["subtitle.asr.chunk_seconds"] = "ASR 分片必须为 30–3600 秒"
	}
	if input.Subtitle.ASR.ChunkOverlapSeconds < 0 ||
		input.Subtitle.ASR.ChunkOverlapSeconds >= input.Subtitle.ASR.ChunkSeconds {
		fields["subtitle.asr.chunk_overlap_seconds"] = "分片重叠必须小于分片长度"
	}
	if input.Subtitle.Postprocess.MinimumCueSeconds < 0.1 ||
		input.Subtitle.Postprocess.MinimumCueSeconds > 30 {
		fields["subtitle.postprocess.minimum_cue_seconds"] = "最短字幕时长必须为 0.1–30 秒"
	}
	if input.Subtitle.Postprocess.MaximumCharactersPerLine < 5 ||
		input.Subtitle.Postprocess.MaximumCharactersPerLine > 100 {
		fields["subtitle.postprocess.maximum_characters_per_line"] = "每行字符数必须为 5–100"
	}
	if input.Subtitle.Postprocess.MaximumLines < 1 || input.Subtitle.Postprocess.MaximumLines > 4 {
		fields["subtitle.postprocess.maximum_lines"] = "字幕行数必须为 1–4"
	}
	if input.Subtitle.Segmentation.MinimumCueSeconds < 0.1 ||
		input.Subtitle.Segmentation.MaximumCueSeconds < input.Subtitle.Segmentation.MinimumCueSeconds {
		fields["subtitle.segmentation.maximum_cue_seconds"] = "智能分段时长范围无效"
	}
	if input.Subtitle.Translation.BatchSize < 1 || input.Subtitle.Translation.BatchSize > 100 {
		fields["subtitle.translation.batch_size"] = "翻译批次必须为 1–100"
	}
	if input.Subtitle.QC.Threshold < 0 || input.Subtitle.QC.Threshold > 100 {
		fields["subtitle.qc.threshold"] = "质检阈值必须为 0–100"
	}
	if input.Subtitle.Style.FontSize < 12 || input.Subtitle.Style.FontSize > 120 {
		fields["subtitle.style.font_size"] = "字幕字号必须为 12–120"
	}

	for name, prompt := range map[string]PromptEntry{
		"subtitle_translation":        input.Prompts.SubtitleTranslation,
		"subtitle_translation_strict": input.Prompts.SubtitleTranslationStrict,
		"subtitle_qc":                 input.Prompts.SubtitleQC,
		"metadata_translation":        input.Prompts.MetadataTranslation,
		"metadata_description_retry":  input.Prompts.MetadataDescriptionRetry,
		"smart_segmentation":          input.Prompts.SmartSegmentation,
	} {
		if !slices.Contains([]string{"builtin", "append", "replace"}, prompt.Mode) {
			fields["prompts."+name+".mode"] = "提示词模式必须为内置、追加或替换"
		}
		if prompt.Mode != "builtin" && prompt.Text == "" {
			fields["prompts."+name+".text"] = "追加或替换模式必须填写提示词"
		}
		if len(prompt.Text) > 20000 {
			fields["prompts."+name+".text"] = "提示词不能超过 20000 字符"
		}
	}

	if input.YouTube.Provider != "google" && input.YouTube.Provider != "fixture" {
		fields["youtube.provider"] = "YouTube 提供商必须为 Google 或本地验收"
	}
	validateURL(fields, "youtube.api_base_url", input.YouTube.APIBaseURL)
	if input.YouTube.ProxyEnabled {
		validateURL(fields, "youtube.proxy_url", input.YouTube.ProxyURL)
	}
	if input.YouTube.RequestTimeoutSeconds < 5 || input.YouTube.RequestTimeoutSeconds > 120 {
		fields["youtube.request_timeout_seconds"] = "YouTube 请求超时必须为 5–120 秒"
	}
	if input.YouTube.Provider == "fixture" {
		validateURL(fields, "youtube.fixture_media_url", input.YouTube.FixtureMediaURL)
	}

	if !slices.Contains(
		[]string{"auto", "cpu", "nvidia", "intel", "amd"},
		input.Transcode.EncoderMode,
	) {
		fields["transcode.encoder_mode"] = "编码模式必须是自动、CPU、NVIDIA、Intel 或 AMD"
	}
	if !slices.Contains([]string{"h264", "hevc", "copy"}, input.Transcode.VideoCodec) {
		fields["transcode.video_codec"] = "视频编码必须是 H.264、HEVC 或直接复制"
	}
	if !slices.Contains([]string{"aac", "copy"}, input.Transcode.AudioCodec) {
		fields["transcode.audio_codec"] = "音频编码必须是 AAC 或直接复制"
	}
	if !slices.Contains([]string{"mp4", "mkv"}, input.Transcode.Container) {
		fields["transcode.container"] = "封装格式必须是 MP4 或 MKV"
	}
	validPresets := []string{
		"ultrafast",
		"superfast",
		"veryfast",
		"faster",
		"fast",
		"medium",
		"slow",
		"slower",
		"veryslow",
	}
	if !slices.Contains(validPresets, input.Transcode.CPUPreset) {
		fields["transcode.cpu_preset"] = "CPU 预设值无效"
	}
	if !slices.Contains(validPresets, input.Transcode.HighResolutionCPUPreset) {
		fields["transcode.high_resolution_cpu_preset"] = "高分辨率 CPU 预设值无效"
	}
	if input.Transcode.MaximumHeight != 0 &&
		(input.Transcode.MaximumHeight < 240 || input.Transcode.MaximumHeight > 4320) {
		fields["transcode.maximum_height"] = "最大高度必须为 0，或介于 240–4320"
	}
	if input.Transcode.VideoBitrateKbps < 0 || input.Transcode.VideoBitrateKbps > 200000 {
		fields["transcode.video_bitrate_kbps"] = "视频码率必须介于 0–200000 Kbps"
	}
	if input.Transcode.AudioBitrateKbps < 32 || input.Transcode.AudioBitrateKbps > 1024 {
		fields["transcode.audio_bitrate_kbps"] = "音频码率必须介于 32–1024 Kbps"
	}
	validateCustomTranscodeArguments(fields, input.Transcode)

	if !slices.Contains([]string{"aliyun", "fixture", "disabled"}, input.Moderation.Provider) {
		fields["moderation.provider"] = "内容审核服务必须是阿里云、本地验收或停用"
	}
	if input.Moderation.Enabled && input.Moderation.Provider == "disabled" {
		fields["moderation.provider"] = "启用内容审核时不能选择停用服务"
	}
	if input.Moderation.Enabled {
		if input.Moderation.Region == "" {
			fields["moderation.region"] = "内容审核区域不能为空"
		}
		if !input.Moderation.CheckText &&
			!input.Moderation.CheckImage &&
			!input.Moderation.CheckVideo {
			fields["moderation.check_text"] = "至少启用文本、封面或视频中的一项审核"
		}
		if input.Moderation.CheckText && input.Moderation.TextService == "" {
			fields["moderation.text_service"] = "文本审核服务不能为空"
		}
		if input.Moderation.CheckImage && input.Moderation.ImageService == "" {
			fields["moderation.image_service"] = "图片审核服务不能为空"
		}
		if input.Moderation.CheckVideo && input.Moderation.VideoService == "" {
			fields["moderation.video_service"] = "视频审核服务不能为空"
		}
	}
	if !slices.Contains(
		[]string{"manual_review", "block"},
		input.Moderation.HighRiskAction,
	) {
		fields["moderation.high_risk_action"] = "高风险内容必须转人工或阻断"
	}
	if !slices.Contains(
		[]string{"manual_review", "block"},
		input.Moderation.MediumRiskAction,
	) {
		fields["moderation.medium_risk_action"] = "中风险内容必须转人工或阻断"
	}
	if !slices.Contains([]string{"manual_review", "block"}, input.Moderation.FailureAction) {
		fields["moderation.failure_action"] = "审核异常后必须转人工或阻断"
	}
	if input.Moderation.RequestTimeoutSeconds < 5 ||
		input.Moderation.RequestTimeoutSeconds > 120 {
		fields["moderation.request_timeout_seconds"] = "内容审核超时必须介于 5–120 秒"
	}
	if input.Moderation.VideoPollSeconds < 2 ||
		input.Moderation.VideoPollSeconds > 60 {
		fields["moderation.video_poll_seconds"] = "视频审核轮询间隔必须介于 2–60 秒"
	}
	if input.Moderation.VideoMaximumWaitSeconds < 30 ||
		input.Moderation.VideoMaximumWaitSeconds > 7200 {
		fields["moderation.video_maximum_wait_seconds"] =
			"视频审核最长等待必须介于 30–7200 秒"
	}

	if input.Publishing.MaximumConcurrentUploads < 1 ||
		input.Publishing.MaximumConcurrentUploads > 32 {
		fields["publishing.maximum_concurrent_uploads"] = "并发投稿数必须介于 1–32"
	}
	if input.Publishing.MaximumAttempts < 1 || input.Publishing.MaximumAttempts > 10 {
		fields["publishing.maximum_attempts"] = "投稿最大尝试次数必须介于 1–10"
	}
	if input.Publishing.RetryDelaySeconds < 1 ||
		input.Publishing.RetryDelaySeconds > 3600 {
		fields["publishing.retry_delay_seconds"] = "投稿重试间隔必须介于 1–3600 秒"
	}

	for _, key := range input.ClearSecrets {
		if _, allowed := AllowedSecretKeys[key]; !allowed {
			fields["clear_secrets"] = "包含未知的密钥字段"
			break
		}
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func validateASRLanguageHint(fields map[string]string, subtitle SubtitleConfig) {
	source := languageBase(subtitle.SourceLanguage)
	hints := strings.TrimSpace(subtitle.ASR.Language)
	if source == "" || source == "auto" || hints == "" || strings.EqualFold(hints, "auto") {
		return
	}
	for _, hint := range strings.Split(hints, ",") {
		if languageBase(hint) == source {
			return
		}
	}
	fields["subtitle.asr.language"] = "ASR 语言提示必须包含源语言，或改为 auto"
}

func validateExistingChinese(
	fields map[string]string,
	detection ExistingChineseSubtitleConfig,
) {
	if detection.Version != 1 {
		fields["subtitle.existing_chinese.version"] = "当前仅支持第 1 版中文字幕识别策略"
	}
	if !detection.InspectPlatformSubtitles &&
		!detection.InspectEmbeddedSubtitles &&
		!detection.InspectHardcodedSubtitles {
		fields["subtitle.existing_chinese.inspect_platform_subtitles"] =
			"至少启用平台字幕、内嵌字幕或画面字幕中的一种检测"
	}
	if detection.HardcodedAction != "skip_translation" {
		fields["subtitle.existing_chinese.hardcoded_action"] =
			"检测到画面中文字幕时必须跳过翻译和重复烧录"
	}
	if detection.UncertainAction != "continue_pipeline" {
		fields["subtitle.existing_chinese.uncertain_action"] =
			"识别不确定时必须继续原字幕流水线，避免误跳过处理"
	}
	if detection.SampleCount < 8 || detection.SampleCount > 120 {
		fields["subtitle.existing_chinese.sample_count"] = "抽帧数必须为 8–120"
	}
	if detection.ConfidenceThresholdPercent < 50 ||
		detection.ConfidenceThresholdPercent > 99 {
		fields["subtitle.existing_chinese.confidence_threshold_percent"] =
			"文字置信度阈值必须为 50–99"
	}
	if detection.CoverageThresholdPercent < 20 ||
		detection.CoverageThresholdPercent > 100 {
		fields["subtitle.existing_chinese.coverage_threshold_percent"] =
			"画面覆盖率阈值必须为 20–100"
	}
	if detection.MinimumDistinctTexts < 2 || detection.MinimumDistinctTexts > 30 {
		fields["subtitle.existing_chinese.minimum_distinct_texts"] =
			"不同字幕文本数必须为 2–30"
	}
}

func languageBase(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	if index := strings.IndexByte(value, '-'); index >= 0 {
		return value[:index]
	}
	return value
}

func validateCustomTranscodeArguments(fields map[string]string, config TranscodeConfig) {
	if !config.CustomArgumentsEnabled {
		return
	}
	if len(config.CustomArguments) > 32 {
		fields["transcode.custom_arguments"] = "自定义参数最多 32 项"
		return
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
	for index, argument := range config.CustomArguments {
		if argument == "" {
			fields["transcode.custom_arguments"] = "自定义参数不能包含空项"
			return
		}
		if len(argument) > 256 || strings.ContainsAny(argument, "\r\n\x00") {
			fields["transcode.custom_arguments"] = "自定义参数包含过长或非法内容"
			return
		}
		if expectValue {
			if strings.Contains(argument, "://") {
				fields["transcode.custom_arguments"] = "自定义参数不能引用网络地址"
				return
			}
			expectValue = false
			continue
		}
		if _, allowed := allowedOptions[argument]; !allowed {
			fields["transcode.custom_arguments"] = fmt.Sprintf(
				"第 %d 项不是允许的 FFmpeg 参数",
				index+1,
			)
			return
		}
		expectValue = true
	}
	if expectValue {
		fields["transcode.custom_arguments"] = "最后一个自定义参数缺少取值"
	}
}

func validateEndpoint(
	fields map[string]string,
	path string,
	endpoint ModelEndpoint,
	global bool,
) {
	if !global && !slices.Contains([]string{"inherit", "override", "disabled"}, endpoint.Mode) {
		fields[path+".mode"] = "覆盖模式必须为继承、专用或禁用"
	}
	active := (global && endpoint.Enabled) || (!global && endpoint.Mode == "override")
	if active {
		if endpoint.Provider != "openai_compatible" && endpoint.Provider != "fixture" {
			fields[path+".provider"] = "模型提供商必须为 OpenAI 兼容或本地验收"
		}
		validateURL(fields, path+".base_url", endpoint.BaseURL)
		if endpoint.Model == "" {
			fields[path+".model"] = "模型名称不能为空"
		}
	}
	if endpoint.Temperature < 0 || endpoint.Temperature > 2 {
		fields[path+".temperature"] = "温度必须为 0–2"
	}
	if endpoint.TimeoutSeconds < 5 || endpoint.TimeoutSeconds > 600 {
		fields[path+".timeout_seconds"] = "超时必须为 5–600 秒"
	}
}

func validateURL(fields map[string]string, path, value string) {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		fields[path] = "请输入有效的 http/https 地址"
	}
}

func uniqueAllowedKeys(values []string) []string {
	result := make([]string, 0, len(values))
	for _, raw := range values {
		key := strings.TrimSpace(raw)
		if _, allowed := AllowedSecretKeys[key]; !allowed || slices.Contains(result, key) {
			continue
		}
		result = append(result, key)
	}
	slices.Sort(result)
	return result
}

func effectiveModel(models ModelConfig, target string) (ModelEndpoint, string, error) {
	if target == "global" {
		return models.Global, SecretModelGlobal, nil
	}
	var endpoint ModelEndpoint
	var secret string
	switch target {
	case "subtitle_translation":
		endpoint = models.SubtitleTranslation
		secret = SecretModelSubtitleTranslation
	case "subtitle_qc":
		endpoint = models.SubtitleQC
		secret = SecretModelSubtitleQC
	case "smart_segmentation":
		endpoint = models.SmartSegmentation
		secret = SecretModelSmartSegmentation
	default:
		return ModelEndpoint{}, "", errors.New("unknown model target")
	}
	if endpoint.Mode == "disabled" {
		return ModelEndpoint{}, "", &ValidationError{
			Fields: map[string]string{"target": "该专用模型已禁用"},
		}
	}
	if endpoint.Mode == "inherit" {
		return models.Global, SecretModelGlobal, nil
	}
	endpoint.Enabled = true
	return endpoint, secret, nil
}
