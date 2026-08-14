package publishing

import (
	"net/url"
	"strings"
)

const fixtureSourceHost = "fixture-provider"

// isFixtureSourceURL deliberately accepts only the private fixture service used
// by local/CI acceptance. A public URL must never be routed to a fixture
// account because that would make a local simulation look like a real submit.
func isFixtureSourceURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), fixtureSourceHost)
}

func fixtureAccountBlocked(authMode string, sourceURL string) bool {
	return strings.EqualFold(strings.TrimSpace(authMode), "fixture") &&
		!isFixtureSourceURL(sourceURL)
}

func validatePublishResult(
	publication ClaimedPublication,
	result PublishResult,
) *AdapterError {
	if fixtureAccountBlocked(publication.Account.AuthMode, publication.SourceURL) {
		return &AdapterError{
			Code:      "fixture_account_real_source_forbidden",
			Message:   "本地验收账号不会连接真实平台；请改用已校验的 Cookie 认证账号",
			Retryable: false,
		}
	}

	fixtureResult := strings.EqualFold(result.RemoteStatus, "published_fixture") ||
		strings.Contains(strings.ToLower(result.RemoteURL), "fixture.invalid")
	if value, ok := result.ResponseSummary["fixture"].(bool); ok && value {
		fixtureResult = true
	}
	if publication.Account.AuthMode != "fixture" && fixtureResult {
		return &AdapterError{
			Code:      "fixture_result_from_real_account",
			Message:   "真实投稿账号返回了本地模拟结果，系统已拒绝将其标记为平台投稿成功",
			Retryable: false,
		}
	}
	if publication.Account.AuthMode == "fixture" && !fixtureResult {
		return &AdapterError{
			Code:      "fixture_result_marker_missing",
			Message:   "本地验收结果缺少模拟标识，系统已拒绝将其记录为成功",
			Retryable: false,
		}
	}
	if strings.TrimSpace(result.RemoteSubmissionID) == "" {
		return &AdapterError{
			Code:      "platform_submission_id_missing",
			Message:   "平台没有返回可核验的稿件编号，不能标记为投稿完成",
			Retryable: true,
			Uncertain: true,
		}
	}
	if strings.TrimSpace(result.RemoteURL) == "" {
		return &AdapterError{
			Code:      "platform_submission_url_missing",
			Message:   "平台没有返回可核验的稿件地址，不能标记为投稿完成",
			Retryable: true,
			Uncertain: true,
		}
	}
	return nil
}
