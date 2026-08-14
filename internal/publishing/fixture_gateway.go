package publishing

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// FixtureGateway is only for deterministic local and CI acceptance. Accounts
// using it are stored with auth_mode=fixture and can never be mistaken for a
// real platform login.
type FixtureGateway struct {
	platform string
}

func NewFixtureGateway(platform string) *FixtureGateway {
	return &FixtureGateway{platform: platform}
}

func (g *FixtureGateway) Platform() string {
	return g.platform
}

func (g *FixtureGateway) AuthMode() string {
	return "fixture"
}

func (g *FixtureGateway) Version() string {
	return "fixture-v1"
}

func (g *FixtureGateway) CheckAccount(
	_ context.Context,
	_ []byte,
) (AccountIdentity, error) {
	if !validPlatform(g.platform) {
		return AccountIdentity{}, fmt.Errorf("unsupported fixture platform %s", g.platform)
	}
	return AccountIdentity{
		RemoteUserID:      "fixture-" + g.platform,
		RemoteDisplayName: "本地验收账号（" + g.platform + "）",
	}, nil
}

func (g *FixtureGateway) Categories(
	_ context.Context,
	_ []byte,
) ([]Category, error) {
	switch g.platform {
	case PlatformAcFun:
		return []Category{
			{
				CategoryID: "63",
				Name:       "生活娱乐",
				Path:       "生活娱乐",
				SortOrder:  10,
				Metadata:   map[string]any{"fixture": true},
			},
			{
				CategoryID: "136",
				ParentID:   "63",
				Name:       "生活日常",
				Path:       "生活娱乐 / 生活日常",
				SortOrder:  20,
				Metadata:   map[string]any{"fixture": true},
			},
		}, nil
	case PlatformBilibili:
		return []Category{
			{
				CategoryID: "160",
				Name:       "生活",
				Path:       "生活",
				SortOrder:  10,
				Metadata:   map[string]any{"fixture": true},
			},
			{
				CategoryID: "21",
				ParentID:   "160",
				Name:       "日常",
				Path:       "生活 / 日常",
				SortOrder:  20,
				Metadata:   map[string]any{"fixture": true},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported fixture platform %s", g.platform)
	}
}

func (g *FixtureGateway) Publish(
	ctx context.Context,
	request UploadRequest,
) (PublishResult, error) {
	if !isFixtureSourceURL(request.SourceURL) {
		return PublishResult{}, &AdapterError{
			Code:      "fixture_account_real_source_forbidden",
			Message:   "本地验收适配器只接受 fixture-provider 测试媒体，不会模拟真实来源的平台投稿",
			Retryable: false,
		}
	}
	if _, err := os.Stat(request.MediaPath); err != nil {
		return PublishResult{}, &AdapterError{
			Code:      "fixture_media_missing",
			Message:   "本地验收投稿找不到媒体文件",
			Retryable: false,
		}
	}
	if strings.Contains(request.Publication.Title, "[fixture:fail]") {
		return PublishResult{}, &AdapterError{
			Code:      "fixture_requested_failure",
			Message:   "标题触发了本地验收失败场景",
			Retryable: false,
		}
	}
	if request.OnStage != nil {
		if err := request.OnStage(ctx, "submitting"); err != nil {
			return PublishResult{}, err
		}
	}
	if strings.Contains(request.Publication.Title, "[fixture:uncertain]") &&
		request.Publication.Attempt <= 1 {
		return PublishResult{}, &AdapterError{
			Code:      "fixture_requested_uncertain",
			Message:   "标题触发了本地验收结果不确定场景",
			Retryable: true,
			Uncertain: true,
		}
	}
	remoteID := request.Publication.Fingerprint[:16]
	return PublishResult{
		RemoteSubmissionID: remoteID,
		RemoteURL:          "https://fixture.invalid/" + g.platform + "/video/" + remoteID,
		RemoteStatus:       "published_fixture",
		ResponseSummary: map[string]any{
			"fixture":            true,
			"platform":           g.platform,
			"media_materialized": true,
		},
	}, nil
}

func (g *FixtureGateway) Reconcile(
	_ context.Context,
	request ReconcileRequest,
) (PublishResult, bool, error) {
	if request.Publication.RemoteSubmissionID == "" {
		return PublishResult{}, false, nil
	}
	return PublishResult{
		RemoteSubmissionID: request.Publication.RemoteSubmissionID,
		RemoteURL:          request.Publication.RemoteURL,
		RemoteStatus:       "published_fixture",
		ResponseSummary: map[string]any{
			"fixture":    true,
			"reconciled": true,
		},
	}, true, nil
}
