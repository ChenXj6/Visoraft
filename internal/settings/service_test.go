package settings

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestAliyunUploadPolicyConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/uploads" ||
			request.URL.Query().Get("action") != "getPolicy" ||
			request.URL.Query().Get("model") != "paraformer-v2" {
			http.Error(writer, "unexpected request", http.StatusBadRequest)
			return
		}
		if request.Header.Get("Authorization") != "Bearer secret" {
			http.Error(writer, "missing bearer", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"data":{"upload_host":"https://oss.example","upload_dir":"temp","policy":"p","signature":"s"}}`)
	}))
	defer server.Close()

	service := &Service{httpClient: server.Client()}
	result, err := service.testAliyunUploadPolicy(context.Background(), ASRConfig{
		Provider: "aliyun_paraformer",
		BaseURL:  server.URL + "/api/v1",
		Model:    "paraformer-v2",
	}, "secret")
	if err != nil {
		t.Fatalf("test aliyun upload policy: %v", err)
	}
	if !result.OK || result.Provider != "aliyun_paraformer" || result.Model != "paraformer-v2" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestAliyunUploadPolicyRejectsIncompleteResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"data":{"upload_host":"https://oss.example"}}`)
	}))
	defer server.Close()

	service := &Service{httpClient: server.Client()}
	_, err := service.testAliyunUploadPolicy(context.Background(), ASRConfig{
		Provider: "aliyun_paraformer",
		BaseURL:  server.URL + "/api/v1",
		Model:    "paraformer-v2",
	}, "secret")
	if err == nil {
		t.Fatal("expected incomplete policy to fail")
	}
}

func TestEffectiveModelRouting(t *testing.T) {
	models := ModelConfig{
		Global: ModelEndpoint{
			Enabled:  true,
			Provider: "fixture",
			BaseURL:  "http://fixture/v1",
			Model:    "global-model",
		},
		SubtitleTranslation: ModelEndpoint{
			Mode:     "override",
			Provider: "fixture",
			BaseURL:  "http://fixture/v1",
			Model:    "translation-model",
		},
		SubtitleQC: ModelEndpoint{Mode: "inherit"},
		SmartSegmentation: ModelEndpoint{
			Mode: "disabled",
		},
	}

	endpoint, secret, err := effectiveModel(models, "subtitle_translation")
	if err != nil || endpoint.Model != "translation-model" ||
		secret != SecretModelSubtitleTranslation {
		t.Fatalf("unexpected override route: endpoint=%+v secret=%q err=%v", endpoint, secret, err)
	}
	endpoint, secret, err = effectiveModel(models, "subtitle_qc")
	if err != nil || endpoint.Model != "global-model" || secret != SecretModelGlobal {
		t.Fatalf("unexpected inherited route: endpoint=%+v secret=%q err=%v", endpoint, secret, err)
	}
	endpoint, secret, err = effectiveModel(models, "smart_segmentation")
	if err == nil || endpoint.Model != "" || secret != "" {
		t.Fatalf("unexpected disabled route: endpoint=%+v secret=%q err=%v", endpoint, secret, err)
	}
}

func TestNormalizeConfigSnapshot(t *testing.T) {
	snapshot := ConfigSnapshot{
		Review: ReviewConfig{
			Mode:              " AUTOMATIC ",
			AutomaticFallback: " MANUAL ",
		},
		Models: ModelConfig{
			Global: ModelEndpoint{
				Provider: " FIXTURE ",
				BaseURL:  " http://fixture/v1/ ",
				Model:    " model-a ",
			},
			SubtitleTranslation: ModelEndpoint{Mode: " INHERIT "},
		},
		Subtitle: SubtitleConfig{
			SourceStrategy: " ASR_ONLY ",
			ExistingChinese: ExistingChineseSubtitleConfig{
				HardcodedAction: " SKIP_TRANSLATION ",
				UncertainAction: " CONTINUE_PIPELINE ",
			},
			ASR: ASRConfig{
				Provider: " FIXTURE ",
				BaseURL:  " http://fixture/v1/ ",
				Model:    " asr-a ",
			},
		},
		Prompts: PromptConfig{
			SubtitleTranslation: PromptEntry{Mode: " APPEND ", Text: " requirement "},
		},
		YouTube: YouTubeConfig{
			Provider:   " FIXTURE ",
			APIBaseURL: " https://www.googleapis.com/youtube/v3/ ",
		},
		Transcode: TranscodeConfig{
			EncoderMode:             " NVIDIA ",
			VideoCodec:              " H264 ",
			AudioCodec:              " AAC ",
			Container:               " MP4 ",
			CPUPreset:               " MEDIUM ",
			HighResolutionCPUPreset: " VERYFAST ",
			CustomArguments:         []string{" -vf ", " scale=-2:1080 "},
		},
		Moderation: ModerationConfig{
			Provider:      " ALIYUN ",
			Region:        " CN-SHANGHAI ",
			TextService:   " comment_detection_pro ",
			ImageService:  " baselineCheck ",
			VideoService:  " videoDetection ",
			FailureAction: " MANUAL_REVIEW ",
		},
	}

	normalize(&snapshot)
	if snapshot.Review.Mode != "automatic" ||
		snapshot.Models.Global.BaseURL != "http://fixture/v1" ||
		snapshot.Subtitle.ASR.Model != "asr-a" ||
		snapshot.Subtitle.ExistingChinese.HardcodedAction != "skip_translation" ||
		snapshot.Subtitle.ExistingChinese.UncertainAction != "continue_pipeline" ||
		snapshot.Prompts.SubtitleTranslation.Text != "requirement" ||
		snapshot.YouTube.Provider != "fixture" ||
		snapshot.Transcode.EncoderMode != "nvidia" ||
		snapshot.Transcode.CustomArguments[1] != "scale=-2:1080" ||
		snapshot.Moderation.Provider != "aliyun" ||
		snapshot.Moderation.Region != "cn-shanghai" {
		t.Fatalf("unexpected normalized snapshot: %+v", snapshot)
	}
}

func TestValidateExistingChineseSubtitlePolicy(t *testing.T) {
	fields := map[string]string{}
	validateExistingChinese(fields, ExistingChineseSubtitleConfig{
		Version:                    1,
		Enabled:                    true,
		InspectPlatformSubtitles:   true,
		InspectEmbeddedSubtitles:   true,
		InspectHardcodedSubtitles:  true,
		HardcodedAction:            "skip_translation",
		UncertainAction:            "continue_pipeline",
		SampleCount:                32,
		ConfidenceThresholdPercent: 85,
		CoverageThresholdPercent:   60,
		MinimumDistinctTexts:       3,
	})
	if len(fields) != 0 {
		t.Fatalf("expected valid detection policy, got %+v", fields)
	}

	validateExistingChinese(fields, ExistingChineseSubtitleConfig{})
	if len(fields) < 6 {
		t.Fatalf("expected invalid policy fields, got %+v", fields)
	}
}

func TestConfigEncodingRoundTripsProcessingAndPublishingPolicy(t *testing.T) {
	expected := ConfigSnapshot{
		Automation: AutomationConfig{
			Enabled:              true,
			TranslateTitle:       true,
			TranslateDescription: true,
			GenerateTags:         true,
			RecommendCategories:  true,
			ProcessCover:         true,
		},
		Transcode: TranscodeConfig{
			Enabled:                 true,
			EncoderMode:             "auto",
			VideoCodec:              "h264",
			AudioCodec:              "aac",
			Container:               "mp4",
			CPUPreset:               "medium",
			HighResolutionCPUPreset: "veryfast",
			AudioBitrateKbps:        192,
			CustomArguments:         []string{},
		},
		Moderation: ModerationConfig{
			Enabled:               true,
			Provider:              "fixture",
			Region:                "local",
			TextService:           "fixture-text",
			ImageService:          "fixture-image",
			VideoService:          "fixture-video",
			FailureAction:         "manual_review",
			RequestTimeoutSeconds: 30,
		},
		Publishing: PublishingConfig{
			AutoPublishAfterReview:    true,
			MaximumConcurrentUploads:  2,
			MaximumAttempts:           3,
			RetryDelaySeconds:         30,
			ReconcileUncertainResults: true,
		},
	}

	raw, err := encodeConfig(expected)
	if err != nil {
		t.Fatalf("encode config: %v", err)
	}
	var actual ConfigSnapshot
	if err := decodeConfig(raw, &actual); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("round trip mismatch:\nactual:  %+v\nexpected: %+v", actual, expected)
	}
}

func TestValidateCustomTranscodeArguments(t *testing.T) {
	validFields := map[string]string{}
	validateCustomTranscodeArguments(validFields, TranscodeConfig{
		CustomArgumentsEnabled: true,
		CustomArguments:        []string{"-vf", "scale=-2:1080", "-movflags", "+faststart"},
	})
	if len(validFields) != 0 {
		t.Fatalf("expected safe arguments to pass, got %+v", validFields)
	}

	invalidFields := map[string]string{}
	validateCustomTranscodeArguments(invalidFields, TranscodeConfig{
		CustomArgumentsEnabled: true,
		CustomArguments:        []string{"-i", "https://example.invalid/video.mp4"},
	})
	if invalidFields["transcode.custom_arguments"] == "" {
		t.Fatal("expected input override to be rejected")
	}
}

func TestValidateASRLanguageHintMatchesConfiguredSource(t *testing.T) {
	fields := map[string]string{}
	validateASRLanguageHint(fields, SubtitleConfig{
		SourceLanguage: "en-US",
		ASR:            ASRConfig{Language: "zh, en"},
	})
	if len(fields) != 0 {
		t.Fatalf("expected matching language hint, got %+v", fields)
	}

	validateASRLanguageHint(fields, SubtitleConfig{
		SourceLanguage: "en",
		ASR:            ASRConfig{Language: "zh"},
	})
	if fields["subtitle.asr.language"] == "" {
		t.Fatal("expected mismatched ASR language hint to be rejected")
	}
}
