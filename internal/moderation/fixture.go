package moderation

import (
	"context"
	"encoding/json"
	"strings"
)

type FixtureProvider struct{}

func NewFixtureProvider() *FixtureProvider {
	return &FixtureProvider{}
}

func (p *FixtureProvider) Moderate(
	ctx context.Context,
	request Request,
) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	risk := "none"
	for _, item := range request.Texts {
		value := strings.ToLower(item.Content)
		switch {
		case strings.Contains(value, "[fixture:block]"),
			strings.Contains(value, "【本地审核：高风险】"):
			risk = HighestRisk(risk, "high")
		case strings.Contains(value, "[fixture:review]"),
			strings.Contains(value, "【本地审核：中风险】"):
			risk = HighestRisk(risk, "medium")
		}
	}
	raw, _ := json.Marshal(map[string]any{
		"provider": "fixture",
		"note":     "仅用于本地不计费联调，不代表第三方内容安全结果",
	})
	text := skipped(request.Config.TextService)
	if request.Config.CheckText {
		text = ChannelResult{
			Status:     "completed",
			Service:    request.Config.TextService,
			RiskLevel:  risk,
			RequestIDs: []string{"fixture-text"},
			Findings:   []Finding{},
			Raw:        raw,
		}
		if risk != "none" {
			text.Findings = append(text.Findings, Finding{
				Label:       "fixture_marker",
				Description: "命中显式本地审核测试标记",
				RiskLevel:   risk,
			})
		}
	}
	image := skipped(request.Config.ImageService)
	if request.Config.CheckImage {
		image.Status = "completed"
		image.RequestIDs = []string{"fixture-image"}
		image.Raw = raw
	}
	video := skipped(request.Config.VideoService)
	if request.Config.CheckVideo {
		video.Status = "completed"
		video.RequestIDs = []string{"fixture-video"}
		video.Raw = raw
	}
	return Result{
		TaskID:    request.TaskID,
		Provider:  "fixture",
		Decision:  DecisionForRisk(risk, request.Config),
		RiskLevel: risk,
		Text:      text,
		Image:     image,
		Video:     video,
	}, nil
}
