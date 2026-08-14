package publishing

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateAccountInputKeepsFixtureAndRealAuthenticationSeparate(t *testing.T) {
	cookieID := "9f790564-fce1-4f72-8d3d-699bff5cbbed"
	err := validateAccountInput(CreateAccountInput{
		Platform:        PlatformBilibili,
		Name:            "fixture",
		AuthMode:        "fixture",
		CookieProfileID: &cookieID,
	})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if validation.Fields["cookie_profile_id"] == "" {
		t.Fatalf("expected fixture/cookie separation error, got %+v", validation.Fields)
	}
}

func TestFixtureAccountCannotPublishPublicSource(t *testing.T) {
	if !fixtureAccountBlocked("fixture", "https://www.youtube.com/watch?v=real") {
		t.Fatal("expected public YouTube source to be blocked for fixture account")
	}
	if fixtureAccountBlocked(
		"fixture",
		"http://fixture-provider:8090/media/sample.wav",
	) {
		t.Fatal("expected the exact local fixture provider to remain available")
	}
	if fixtureAccountBlocked("cookie", "https://www.youtube.com/watch?v=real") {
		t.Fatal("expected a real Cookie account to accept a public source")
	}
}

func TestRealAccountRejectsFixtureSuccessResult(t *testing.T) {
	failure := validatePublishResult(ClaimedPublication{
		PlatformPublication: PlatformPublication{Platform: PlatformBilibili},
		Account:             Account{AuthMode: "cookie"},
		SourceURL:           "https://www.youtube.com/watch?v=real",
	}, PublishResult{
		RemoteSubmissionID: "fixture-id",
		RemoteURL:          "https://fixture.invalid/bilibili/video/fixture-id",
		RemoteStatus:       "published_fixture",
		ResponseSummary:    map[string]any{"fixture": true},
	})
	if failure == nil || failure.Code != "fixture_result_from_real_account" {
		t.Fatalf("expected fixture result rejection, got %+v", failure)
	}
}

func TestValidateTranscodePresetRejectsInputOverride(t *testing.T) {
	err := validateTranscodePreset(TranscodePresetInput{
		Name:                    "safe",
		EncoderMode:             "auto",
		VideoCodec:              "h264",
		AudioCodec:              "aac",
		Container:               "mp4",
		CPUPreset:               "medium",
		HighResolutionCPUPreset: "veryfast",
		AudioBitrateKbps:        192,
		CustomArguments:         []string{"-i", "https://example.invalid/media.mp4"},
	})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if validation.Fields["custom_arguments"] == "" {
		t.Fatalf("expected unsafe argument error, got %+v", validation.Fields)
	}
}

func TestRenderTemplateAndStatementAppendExactlyOnce(t *testing.T) {
	rendered := renderTemplate(
		"{{title}}\n{{description}}\n{{repost_statement}}",
		"标题",
		"简介",
		"https://example.invalid/source",
		"转载声明",
	)
	actual := appendStatement(rendered, "转载声明")
	expected := "标题\n简介\n转载声明"
	if actual != expected {
		t.Fatalf("unexpected rendered description: %q", actual)
	}
}

func TestNextDailyScheduleMovesPastTimeToTomorrow(t *testing.T) {
	value := "09:30"
	strategy := &PostingStrategy{
		ScheduleMode: "daily_time",
		ScheduleTime: &value,
	}
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	actual := nextSchedule(strategy, now)
	expected := time.Date(2026, 7, 28, 9, 30, 0, 0, time.UTC)
	if actual == nil || !actual.Equal(expected) {
		t.Fatalf("unexpected next schedule: %v", actual)
	}
}

func TestFingerprintIsStableAndScopeSensitive(t *testing.T) {
	first := fingerprint("job", "task", "1")
	second := fingerprint("job", "task", "1")
	different := fingerprint("publication", "task", "1")
	if first != second {
		t.Fatal("expected stable fingerprint")
	}
	if first == different {
		t.Fatal("expected scope to affect fingerprint")
	}
}

func TestValidateResolvePublicationRequiresRemoteEvidence(t *testing.T) {
	err := validateResolvePublicationInput(ResolvePublicationInput{
		ExpectedVersion: 2,
		Resolution:      "remote_published",
		RemoteURL:       "javascript:alert(1)",
		Note:            "已登录平台检查",
	})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if validation.Fields["remote_submission_id"] == "" {
		t.Fatalf("expected remote submission id error, got %+v", validation.Fields)
	}
	if validation.Fields["remote_url"] == "" {
		t.Fatalf("expected remote URL error, got %+v", validation.Fields)
	}
}

func TestValidateResolvePublicationAllowsAuditedRequeue(t *testing.T) {
	err := validateResolvePublicationInput(ResolvePublicationInput{
		ExpectedVersion: 3,
		Resolution:      "remote_not_created",
		Note:            "已在平台创作中心确认没有对应稿件",
	})
	if err != nil {
		t.Fatalf("expected audited requeue input to pass, got %v", err)
	}
}

func TestValidateResolvePublicationRejectsRemoteDataForMissingResult(t *testing.T) {
	err := validateResolvePublicationInput(ResolvePublicationInput{
		ExpectedVersion:    3,
		Resolution:         "remote_not_created",
		RemoteSubmissionID: "already-created",
		Note:               "已完成核验",
	})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if validation.Fields["remote_submission_id"] == "" {
		t.Fatalf("expected conflicting remote evidence error, got %+v", validation.Fields)
	}
}

func TestCookiesForURLFiltersDomainPathAndExpiry(t *testing.T) {
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	raw := strings.Join([]string{
		"# Netscape HTTP Cookie File",
		".bilibili.com\tTRUE\t/\tTRUE\t0\tSESSDATA\tactive",
		"#HttpOnly_.bilibili.com\tTRUE\t/\tTRUE\t0\tbili_jct\tcsrf",
		".example.com\tTRUE\t/\tTRUE\t0\tignored\tvalue",
		".bilibili.com\tTRUE\t/\tTRUE\t1\texpired\tvalue",
	}, "\n")
	actual, err := cookiesForURL(
		[]byte(raw),
		"https://member.bilibili.com/platform/upload",
		now,
	)
	if err != nil {
		t.Fatalf("parse cookies: %v", err)
	}
	if actual.Values["SESSDATA"] != "active" || actual.Values["bili_jct"] != "csrf" {
		t.Fatalf("expected bilibili cookies, got %+v", actual.Values)
	}
	if _, exists := actual.Values["ignored"]; exists {
		t.Fatal("expected foreign-domain cookie to be filtered")
	}
	if _, exists := actual.Values["expired"]; exists {
		t.Fatal("expected expired cookie to be filtered")
	}
}

func TestBilibiliUploadURLRejectsUntrustedEndpoint(t *testing.T) {
	_, err := bilibiliUploadURL(bilibiliPreupload{
		Endpoint: "https://example.invalid",
		UposURI:  "upos://bucket/file.mp4",
	})
	if err == nil {
		t.Fatal("expected untrusted UPOS endpoint rejection")
	}

	actual, err := bilibiliUploadURL(bilibiliPreupload{
		Endpoint: "//upos-sz-upcdn.bilivideo.com",
		UposURI:  "upos://ugc/file.mp4",
	})
	if err != nil {
		t.Fatalf("expected bilivideo endpoint to pass: %v", err)
	}
	if actual != "https://upos-sz-upcdn.bilivideo.com/ugc/file.mp4" {
		t.Fatalf("unexpected upload url: %s", actual)
	}
}

func TestDecodeBilibiliCategoriesBuildsPaths(t *testing.T) {
	raw := []byte(`{
		"type": [
			{"id": 160, "name": "生活", "children": [
				{"id": 21, "name": "日常"}
			]}
		]
	}`)
	items, err := decodeBilibiliCategories(raw)
	if err != nil {
		t.Fatalf("decode categories: %v", err)
	}
	if len(items) != 2 ||
		items[1].CategoryID != "21" ||
		items[1].ParentID != "160" ||
		items[1].Path != "生活 / 日常" {
		t.Fatalf("unexpected categories: %+v", items)
	}
}
