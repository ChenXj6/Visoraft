package monitors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/visoraft/visoraft/internal/settings"
)

type Discoverer struct {
	settings *settings.Service
}

func NewDiscoverer(settingsService *settings.Service) *Discoverer {
	return &Discoverer{settings: settingsService}
}

func (d *Discoverer) Discover(
	ctx context.Context,
	monitor Monitor,
) ([]Candidate, int, error) {
	current, err := d.settings.Get(ctx)
	if err != nil {
		return nil, 0, err
	}
	switch current.YouTube.Provider {
	case "fixture":
		return fixtureCandidates(monitor, current.YouTube)
	case "google":
		apiKey, err := d.settings.ResolveSecret(ctx, settings.SecretYouTubeAPI)
		if err != nil {
			return nil, 0, err
		}
		if apiKey == "" {
			return nil, 0, fmt.Errorf("YouTube Data API Key is not configured")
		}
		proxyPassword, err := d.settings.ResolveSecret(
			ctx,
			settings.SecretYouTubeProxyPassword,
		)
		if err != nil {
			return nil, 0, err
		}
		client, err := youtubeHTTPClient(current.YouTube, proxyPassword)
		if err != nil {
			return nil, 0, err
		}
		return discoverGoogle(
			ctx,
			client,
			current.YouTube.APIBaseURL,
			apiKey,
			monitor,
		)
	default:
		return nil, 0, fmt.Errorf(
			"unsupported YouTube provider %q",
			current.YouTube.Provider,
		)
	}
}

func fixtureCandidates(
	monitor Monitor,
	config settings.YouTubeConfig,
) ([]Candidate, int, error) {
	if !validateHTTPURL(config.FixtureMediaURL) {
		return nil, 0, fmt.Errorf("fixture media URL is invalid")
	}
	now := time.Now().UTC()
	base := Candidate{
		ExternalVideoID: "fixture-local-media-v1",
		SourceURL:       config.FixtureMediaURL,
		Title:           "Visoraft 本地监控验收媒体",
		Description:     "由明确标记的本地验收提供商生成，不代表 YouTube 真实结果。",
		ChannelID:       "visoraft-fixture-channel",
		ChannelTitle:    "Visoraft Fixture",
		PublishedAt:     &now,
		DurationSeconds: 4,
		ViewCount:       100000,
		LikeCount:       5000,
		CommentCount:    500,
		VideoType:       "video",
	}
	if monitor.MonitorType != "series" {
		return []Candidate{base}, 0, nil
	}
	scopes := effectiveSeriesScopes(monitor)
	items := make([]Candidate, 0, seriesEpisodeCount(scopes))
	for _, scope := range scopes {
		for episode := scope.EpisodeStart; episode <= scope.EpisodeEnd; episode++ {
			item := base
			item.ExternalVideoID = fmt.Sprintf(
				"fixture-series-%s-episode-%03d", scope.Key, episode,
			)
			item.EpisodeNumber = episode
			item.SeriesScopeKey = scope.Key
			item.SeriesScopeName = scope.Name
			item.Title = strings.TrimSpace(fmt.Sprintf(
				"%s %s 第%02d集（本地验收）", monitor.SeriesTitle, scope.Name, episode,
			))
			items = append(items, item)
		}
	}
	return items, 0, nil
}

func youtubeHTTPClient(
	config settings.YouTubeConfig,
	proxyPassword string,
) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if config.ProxyEnabled {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("parse youtube proxy url: %w", err)
		}
		if config.ProxyUsername != "" {
			proxyURL.User = url.UserPassword(config.ProxyUsername, proxyPassword)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{
		Transport: transport,
		Timeout:   time.Duration(config.RequestTimeoutSeconds) * time.Second,
	}, nil
}

func discoverGoogle(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	apiKey string,
	monitor Monitor,
) ([]Candidate, int, error) {
	if monitor.MonitorType == "series" {
		return discoverGoogleSeries(ctx, client, baseURL, apiKey, monitor)
	}
	return discoverGoogleStandard(ctx, client, baseURL, apiKey, monitor)
}

func discoverGoogleStandard(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	apiKey string,
	monitor Monitor,
) ([]Candidate, int, error) {
	channelIDs := []string{""}
	quotaUnits := 0
	if monitor.MonitorType == "channel" {
		var err error
		channelIDs, quotaUnits, err = resolveYouTubeChannelReferences(
			ctx,
			client,
			baseURL,
			apiKey,
			monitor.ChannelIDs,
			monitor.RateLimitRequests,
		)
		if err != nil {
			return nil, quotaUnits, err
		}
	}
	videoIDs := make([]string, 0, monitor.MaxResults)
	for _, channelID := range channelIDs {
		if len(videoIDs) >= monitor.MaxResults ||
			quotaUnits >= monitor.RateLimitRequests {
			break
		}
		searchURL, err := url.Parse(strings.TrimRight(baseURL, "/") + "/search")
		if err != nil {
			return nil, quotaUnits, fmt.Errorf("parse youtube search url: %w", err)
		}
		query := searchURL.Query()
		query.Set("part", "snippet")
		query.Set("type", "video")
		query.Set("maxResults", strconv.Itoa(min(50, monitor.MaxResults-len(videoIDs))))
		query.Set("key", apiKey)
		query.Set("order", monitor.OrderBy)
		if monitor.RegionCode != "" {
			query.Set("regionCode", monitor.RegionCode)
		}
		if monitor.CategoryID != "" {
			query.Set("videoCategoryId", monitor.CategoryID)
		}
		// Keep retrieval and acceptance semantics separate. IncludeKeywords are
		// evaluated as an "any keyword" filter by the scheduler; appending all of
		// them to q makes Google treat the request far more narrowly and can hide
		// otherwise valid candidates. Only use them to seed discovery when the
		// monitor has no explicit search query.
		if monitor.Query != "" {
			query.Set("q", monitor.Query)
		} else if len(monitor.IncludeKeywords) > 0 {
			query.Set("q", strings.Join(monitor.IncludeKeywords, "|"))
		}
		if channelID != "" {
			query.Set("channelId", channelID)
		}
		if monitor.ChannelMode != "historical" {
			publishedAfter := time.Now().UTC().Add(
				-time.Duration(monitor.LookbackDays) * 24 * time.Hour,
			)
			query.Set("publishedAfter", publishedAfter.Format(time.RFC3339))
		}
		if monitor.PublishedAfter != nil && *monitor.PublishedAfter != "" {
			if parsed, err := time.Parse("2006-01-02", *monitor.PublishedAfter); err == nil {
				query.Set("publishedAfter", parsed.UTC().Format(time.RFC3339))
			}
		}
		if monitor.PublishedBefore != nil && *monitor.PublishedBefore != "" {
			if parsed, err := time.Parse("2006-01-02", *monitor.PublishedBefore); err == nil {
				query.Set(
					"publishedBefore",
					parsed.Add(24*time.Hour-time.Second).UTC().Format(time.RFC3339),
				)
			}
		}
		searchURL.RawQuery = query.Encode()
		var response struct {
			Items []struct {
				ID struct {
					VideoID string `json:"videoId"`
				} `json:"id"`
			} `json:"items"`
		}
		if err := getYouTubeJSON(ctx, client, searchURL.String(), &response); err != nil {
			return nil, quotaUnits, err
		}
		quotaUnits++
		for _, item := range response.Items {
			if item.ID.VideoID != "" && !contains(videoIDs, item.ID.VideoID) {
				videoIDs = append(videoIDs, item.ID.VideoID)
			}
		}
	}
	if len(videoIDs) == 0 && quotaUnits >= monitor.RateLimitRequests {
		return nil, quotaUnits, fmt.Errorf(
			"单次请求上限不足：频道解析后没有剩余请求可用于检索视频",
		)
	}
	if len(videoIDs) == 0 {
		return []Candidate{}, quotaUnits, nil
	}
	if quotaUnits >= monitor.RateLimitRequests {
		return []Candidate{}, quotaUnits, fmt.Errorf(
			"单次请求上限不足：至少需要 2 次请求才能读取视频详情",
		)
	}

	candidates, err := loadGoogleVideoCandidates(
		ctx, client, baseURL, apiKey, videoIDs,
	)
	if err != nil {
		return nil, quotaUnits, err
	}
	quotaUnits++
	return candidates, quotaUnits, nil
}

func loadGoogleVideoCandidates(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	apiKey string,
	videoIDs []string,
) ([]Candidate, error) {
	if len(videoIDs) == 0 {
		return []Candidate{}, nil
	}
	detailsURL, err := url.Parse(strings.TrimRight(baseURL, "/") + "/videos")
	if err != nil {
		return nil, fmt.Errorf("parse youtube videos url: %w", err)
	}
	query := detailsURL.Query()
	query.Set("part", "snippet,contentDetails,statistics,liveStreamingDetails")
	query.Set("id", strings.Join(videoIDs, ","))
	query.Set("key", apiKey)
	detailsURL.RawQuery = query.Encode()
	var response struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title                string    `json:"title"`
				Description          string    `json:"description"`
				ChannelID            string    `json:"channelId"`
				ChannelTitle         string    `json:"channelTitle"`
				PublishedAt          time.Time `json:"publishedAt"`
				LiveBroadcastContent string    `json:"liveBroadcastContent"`
			} `json:"snippet"`
			ContentDetails struct {
				Duration string `json:"duration"`
			} `json:"contentDetails"`
			Statistics struct {
				ViewCount    string `json:"viewCount"`
				LikeCount    string `json:"likeCount"`
				CommentCount string `json:"commentCount"`
			} `json:"statistics"`
		} `json:"items"`
	}
	if err := getYouTubeJSON(ctx, client, detailsURL.String(), &response); err != nil {
		return nil, err
	}
	candidates := make([]Candidate, 0, len(response.Items))
	for _, item := range response.Items {
		duration := parseISODuration(item.ContentDetails.Duration)
		videoType := "video"
		if item.Snippet.LiveBroadcastContent != "" &&
			item.Snippet.LiveBroadcastContent != "none" {
			videoType = "live"
		} else if duration <= 180 &&
			strings.Contains(strings.ToLower(item.Snippet.Title), "#shorts") {
			videoType = "short"
		}
		publishedAt := item.Snippet.PublishedAt
		candidates = append(candidates, Candidate{
			ExternalVideoID: item.ID,
			SourceURL:       "https://www.youtube.com/watch?v=" + item.ID,
			Title:           item.Snippet.Title,
			Description:     item.Snippet.Description,
			ChannelID:       item.Snippet.ChannelID,
			ChannelTitle:    item.Snippet.ChannelTitle,
			PublishedAt:     &publishedAt,
			DurationSeconds: duration,
			ViewCount:       parseMetric(item.Statistics.ViewCount),
			LikeCount:       parseMetric(item.Statistics.LikeCount),
			CommentCount:    parseMetric(item.Statistics.CommentCount),
			VideoType:       videoType,
		})
	}
	return candidates, nil
}

func resolveYouTubeChannelReferences(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	apiKey string,
	references []string,
	requestLimit int,
) ([]string, int, error) {
	channelIDs := make([]string, 0, len(references))
	quotaUnits := 0
	for _, reference := range references {
		lookupKind, lookupValue, err := parseYouTubeChannelReference(reference)
		if err != nil {
			return nil, quotaUnits, err
		}
		if lookupKind == "id" {
			if !contains(channelIDs, lookupValue) {
				channelIDs = append(channelIDs, lookupValue)
			}
			continue
		}
		if quotaUnits >= requestLimit {
			return nil, quotaUnits, fmt.Errorf(
				"单次请求上限不足：解析频道“%s”后还需要检索视频",
				reference,
			)
		}

		channelsURL, err := url.Parse(strings.TrimRight(baseURL, "/") + "/channels")
		if err != nil {
			return nil, quotaUnits, fmt.Errorf("parse youtube channels url: %w", err)
		}
		query := channelsURL.Query()
		query.Set("part", "id")
		query.Set("key", apiKey)
		query.Set(lookupKind, lookupValue)
		channelsURL.RawQuery = query.Encode()
		var response struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if err := getYouTubeJSON(ctx, client, channelsURL.String(), &response); err != nil {
			return nil, quotaUnits, fmt.Errorf("解析频道“%s”失败：%w", reference, err)
		}
		quotaUnits++
		if len(response.Items) == 0 || strings.TrimSpace(response.Items[0].ID) == "" {
			return nil, quotaUnits, fmt.Errorf(
				"找不到频道“%s”，请粘贴频道主页链接、@账号或 UC 开头的频道 ID",
				reference,
			)
		}
		channelID := strings.TrimSpace(response.Items[0].ID)
		if !contains(channelIDs, channelID) {
			channelIDs = append(channelIDs, channelID)
		}
	}
	return channelIDs, quotaUnits, nil
}

func parseYouTubeChannelReference(reference string) (string, string, error) {
	value := strings.TrimSpace(reference)
	if value == "" {
		return "", "", fmt.Errorf("频道链接或账号不能为空")
	}
	if strings.HasPrefix(value, "UC") {
		return "id", value, nil
	}
	if strings.HasPrefix(value, "@") {
		return "forHandle", value, nil
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
		if host != "youtube.com" && host != "m.youtube.com" {
			return "", "", fmt.Errorf("“%s”不是 YouTube 频道链接", reference)
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] == "channel" && strings.HasPrefix(parts[1], "UC") {
			return "id", parts[1], nil
		}
		if len(parts) >= 1 && strings.HasPrefix(parts[0], "@") {
			return "forHandle", parts[0], nil
		}
		if len(parts) >= 2 && parts[0] == "user" {
			return "forUsername", parts[1], nil
		}
		return "", "", fmt.Errorf(
			"无法识别频道链接“%s”，请使用频道主页链接、@账号或 UC 开头的频道 ID",
			reference,
		)
	}
	// channels.list 的 forHandle 参数接受带或不带 @ 的账号名称。
	return "forHandle", value, nil
}

const seriesSearchResultsPerEpisode = 3

func seriesRequiredRequests(scopeCount int) int {
	if scopeCount <= 0 {
		return 0
	}
	return scopeCount * 2
}

func discoverGoogleSeries(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	apiKey string,
	monitor Monitor,
) ([]Candidate, int, error) {
	scopes := effectiveSeriesScopes(monitor)
	result := make([]Candidate, 0, seriesEpisodeCount(scopes))
	quotaUnits := 0
	for _, scope := range scopes {
		if quotaUnits+2 > monitor.RateLimitRequests {
			return nil, quotaUnits, fmt.Errorf(
				"单次请求上限不足：每个分部至少需要 2 次请求，当前共需 %d 次",
				seriesRequiredRequests(len(scopes)),
			)
		}
		selectedByEpisode := make(map[int]Candidate)
		playlistCandidates, playlistUsed, playlistErr := discoverGoogleSeriesPlaylists(
			ctx,
			client,
			baseURL,
			apiKey,
			monitor,
			scope,
			monitor.RateLimitRequests-quotaUnits,
		)
		quotaUnits += playlistUsed
		if playlistErr != nil && len(playlistCandidates) == 0 {
			return nil, quotaUnits, fmt.Errorf("查找%s播放列表失败：%w", scope.Name, playlistErr)
		}
		collectSeriesCandidates(monitor, scope, selectedByEpisode, playlistCandidates)
		if len(selectedByEpisode) >= scope.EpisodeEnd-scope.EpisodeStart+1 {
			for episode := scope.EpisodeStart; episode <= scope.EpisodeEnd; episode++ {
				result = append(result, selectedByEpisode[episode])
			}
			continue
		}
		if quotaUnits+2 > monitor.RateLimitRequests {
			for episode := scope.EpisodeStart; episode <= scope.EpisodeEnd; episode++ {
				if selected, exists := selectedByEpisode[episode]; exists {
					result = append(result, selected)
				}
			}
			continue
		}

		// When no complete playlist is available, use one broad video search
		// before falling back to per-episode recovery.
		scopeMonitor := monitor
		scopeMonitor.MonitorType = "search"
		scopeMonitor.ChannelMode = "historical"
		scopeMonitor.Query = strings.TrimSpace(fmt.Sprintf(
			"%s %s %s %s",
			monitor.SeriesTitle,
			scope.Name,
			scope.Query,
			monitor.Query,
		))
		scopeMonitor.MaxResults = min(
			50,
			max(monitor.MaxResults, scope.EpisodeEnd-scope.EpisodeStart+1),
		)
		scopeMonitor.RateLimitRequests = 2
		candidates, used, err := discoverGoogleStandard(
			ctx,
			client,
			baseURL,
			apiKey,
			scopeMonitor,
		)
		quotaUnits += used
		if err != nil {
			if len(selectedByEpisode) == 0 {
				return nil, quotaUnits, fmt.Errorf("检索%s失败：%w", scope.Name, err)
			}
			for episode := scope.EpisodeStart; episode <= scope.EpisodeEnd; episode++ {
				if selected, exists := selectedByEpisode[episode]; exists {
					result = append(result, selected)
				}
			}
			continue
		}

		collectSeriesCandidates(monitor, scope, selectedByEpisode, candidates)

		// Use the remaining request budget only for episodes missing from the
		// broad result. This is a bounded recovery path, not an up-front cost.
		for episode := scope.EpisodeStart; episode <= scope.EpisodeEnd; episode++ {
			if _, exists := selectedByEpisode[episode]; exists {
				continue
			}
			if quotaUnits+2 > monitor.RateLimitRequests {
				break
			}
			episodeMonitor := monitor
			episodeMonitor.MonitorType = "search"
			episodeMonitor.ChannelMode = "historical"
			episodeMonitor.Query = strings.TrimSpace(fmt.Sprintf(
				"%s %s 第%02d集 %s %s",
				monitor.SeriesTitle,
				scope.Name,
				episode,
				scope.Query,
				monitor.Query,
			))
			episodeMonitor.MaxResults = seriesSearchResultsPerEpisode
			episodeMonitor.RateLimitRequests = 2
			candidates, used, err := discoverGoogleStandard(
				ctx,
				client,
				baseURL,
				apiKey,
				episodeMonitor,
			)
			quotaUnits += used
			if err != nil {
				if len(selectedByEpisode) == 0 {
					return nil, quotaUnits, fmt.Errorf("补查第 %d 集失败：%w", episode, err)
				}
				break
			}
			collectSeriesCandidates(monitor, scope, selectedByEpisode, candidates)
		}
		for episode := scope.EpisodeStart; episode <= scope.EpisodeEnd; episode++ {
			if selected, exists := selectedByEpisode[episode]; exists {
				result = append(result, selected)
			}
		}
	}
	return result, quotaUnits, nil
}

func discoverGoogleSeriesPlaylists(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	apiKey string,
	monitor Monitor,
	scope SeriesScope,
	requestLimit int,
) ([]Candidate, int, error) {
	if requestLimit < 3 {
		return []Candidate{}, 0, nil
	}
	searchURL, err := url.Parse(strings.TrimRight(baseURL, "/") + "/search")
	if err != nil {
		return nil, 0, fmt.Errorf("parse youtube playlist search url: %w", err)
	}
	query := searchURL.Query()
	query.Set("part", "snippet")
	query.Set("type", "playlist")
	query.Set("maxResults", "5")
	query.Set("key", apiKey)
	query.Set("order", "relevance")
	query.Set("q", strings.TrimSpace(fmt.Sprintf(
		"%s %s %s %s 全集",
		monitor.SeriesTitle,
		scope.Name,
		scope.Query,
		monitor.Query,
	)))
	searchURL.RawQuery = query.Encode()
	var searchResponse struct {
		Items []struct {
			ID struct {
				PlaylistID string `json:"playlistId"`
			} `json:"id"`
		} `json:"items"`
	}
	if err := getYouTubeJSON(ctx, client, searchURL.String(), &searchResponse); err != nil {
		return nil, 0, err
	}
	used := 1
	best := []Candidate{}
	bestCoverage := 0
	targetCoverage := scope.EpisodeEnd - scope.EpisodeStart + 1
	for _, item := range searchResponse.Items {
		if item.ID.PlaylistID == "" || used+2 > requestLimit {
			continue
		}
		videoIDs, playlistUsed, err := loadGooglePlaylistVideoIDs(
			ctx,
			client,
			baseURL,
			apiKey,
			item.ID.PlaylistID,
			targetCoverage,
			requestLimit-used-1,
		)
		used += playlistUsed
		if err != nil {
			if len(best) > 0 {
				break
			}
			return nil, used, err
		}
		candidates := make([]Candidate, 0, len(videoIDs))
		for start := 0; start < len(videoIDs) && used < requestLimit; start += 50 {
			end := min(len(videoIDs), start+50)
			batch, err := loadGoogleVideoCandidates(
				ctx, client, baseURL, apiKey, videoIDs[start:end],
			)
			used++
			if err != nil {
				if len(best) > 0 {
					return best, used, nil
				}
				return nil, used, err
			}
			candidates = append(candidates, batch...)
		}
		coverage := map[int]bool{}
		for _, candidate := range candidates {
			episode := parseEpisodeNumber(candidate.Title)
			if episode >= scope.EpisodeStart && episode <= scope.EpisodeEnd &&
				seriesTitleMatches(monitor.SeriesTitle, candidate.Title) {
				coverage[episode] = true
			}
		}
		if len(coverage) > bestCoverage {
			best = candidates
			bestCoverage = len(coverage)
		}
		if bestCoverage >= targetCoverage {
			break
		}
	}
	return best, used, nil
}

func loadGooglePlaylistVideoIDs(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	apiKey string,
	playlistID string,
	wanted int,
	requestLimit int,
) ([]string, int, error) {
	videoIDs := make([]string, 0, wanted)
	nextPageToken := ""
	used := 0
	for used < requestLimit && len(videoIDs) < wanted {
		playlistURL, err := url.Parse(strings.TrimRight(baseURL, "/") + "/playlistItems")
		if err != nil {
			return nil, used, fmt.Errorf("parse youtube playlist items url: %w", err)
		}
		query := playlistURL.Query()
		query.Set("part", "contentDetails")
		query.Set("playlistId", playlistID)
		query.Set("maxResults", "50")
		query.Set("key", apiKey)
		if nextPageToken != "" {
			query.Set("pageToken", nextPageToken)
		}
		playlistURL.RawQuery = query.Encode()
		var response struct {
			NextPageToken string `json:"nextPageToken"`
			Items         []struct {
				ContentDetails struct {
					VideoID string `json:"videoId"`
				} `json:"contentDetails"`
			} `json:"items"`
		}
		if err := getYouTubeJSON(ctx, client, playlistURL.String(), &response); err != nil {
			return nil, used, err
		}
		used++
		for _, item := range response.Items {
			if item.ContentDetails.VideoID != "" && !contains(videoIDs, item.ContentDetails.VideoID) {
				videoIDs = append(videoIDs, item.ContentDetails.VideoID)
			}
		}
		if response.NextPageToken == "" {
			break
		}
		nextPageToken = response.NextPageToken
	}
	return videoIDs, used, nil
}

func collectSeriesCandidates(
	monitor Monitor,
	scope SeriesScope,
	selectedByEpisode map[int]Candidate,
	candidates []Candidate,
) {
	for _, candidate := range candidates {
		episode := parseEpisodeNumber(candidate.Title)
		if episode < scope.EpisodeStart || episode > scope.EpisodeEnd ||
			!seriesTitleMatches(monitor.SeriesTitle, candidate.Title) {
			continue
		}
		current, exists := selectedByEpisode[episode]
		if !exists || preferSeriesCandidate(monitor, current, candidate) {
			candidate.EpisodeNumber = episode
			candidate.SeriesScopeKey = scope.Key
			candidate.SeriesScopeName = scope.Name
			selectedByEpisode[episode] = candidate
		}
	}
}

func preferSeriesCandidate(monitor Monitor, current Candidate, candidate Candidate) bool {
	currentPasses, _ := candidatePasses(monitor, current)
	candidatePassesRules, _ := candidatePasses(monitor, candidate)
	if currentPasses != candidatePassesRules {
		return candidatePassesRules
	}
	return candidate.ViewCount > current.ViewCount
}

func effectiveSeriesScopes(monitor Monitor) []SeriesScope {
	if len(monitor.SeriesScopes) > 0 {
		return monitor.SeriesScopes
	}
	return []SeriesScope{{
		Key: "default", Query: monitor.Query,
		EpisodeStart: monitor.EpisodeStart, EpisodeEnd: monitor.EpisodeEnd,
	}}
}

func seriesEpisodeCount(scopes []SeriesScope) int {
	total := 0
	for _, scope := range scopes {
		if scope.EpisodeEnd >= scope.EpisodeStart {
			total += scope.EpisodeEnd - scope.EpisodeStart + 1
		}
	}
	return total
}

var episodeNumberPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)第\s*0*([0-9]{1,3})\s*集`),
	regexp.MustCompile(`(?i)\b(?:EP|EPISODE)\s*0*([0-9]{1,3})\b`),
}

func parseEpisodeNumber(title string) int {
	for _, pattern := range episodeNumberPatterns {
		match := pattern.FindStringSubmatch(title)
		if len(match) == 2 {
			return parseInt(match[1])
		}
	}
	return 0
}

func seriesTitleMatches(seriesTitle string, candidateTitle string) bool {
	fold := func(value string) string {
		replacer := strings.NewReplacer(
			"還", "还",
			"Ⅰ", "1",
			"Ｉ", "I",
			"《", "",
			"》", "",
			" ", "",
		)
		return strings.ToLower(replacer.Replace(value))
	}
	return strings.Contains(fold(candidateTitle), fold(seriesTitle))
}

func loadGoogleCategories(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	apiKey string,
	region string,
) ([]YouTubeCategory, error) {
	endpoint, err := url.Parse(
		strings.TrimRight(baseURL, "/") + "/videoCategories",
	)
	if err != nil {
		return nil, fmt.Errorf("parse youtube categories url: %w", err)
	}
	query := endpoint.Query()
	query.Set("part", "snippet")
	query.Set("regionCode", region)
	query.Set("hl", "zh-CN")
	query.Set("key", apiKey)
	endpoint.RawQuery = query.Encode()
	var response struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title      string `json:"title"`
				Assignable bool   `json:"assignable"`
			} `json:"snippet"`
		} `json:"items"`
	}
	if err := getYouTubeJSON(ctx, client, endpoint.String(), &response); err != nil {
		return nil, err
	}
	result := make([]YouTubeCategory, 0, len(response.Items))
	for _, item := range response.Items {
		if item.ID == "" || !item.Snippet.Assignable {
			continue
		}
		result = append(result, YouTubeCategory{
			ID:       item.ID,
			Title:    item.Snippet.Title,
			Provider: "google",
		})
	}
	return result, nil
}

func getYouTubeJSON(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	target any,
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create youtube api request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("youtube api request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read youtube api response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &problem)
		if problem.Error.Message == "" {
			problem.Error.Message = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		return fmt.Errorf("youtube api rejected request: %s", problem.Error.Message)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode youtube api response: %w", err)
	}
	return nil
}

var isoDurationPattern = regexp.MustCompile(
	`^P(?:(\d+)D)?T?(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?$`,
)

func parseISODuration(value string) int {
	match := isoDurationPattern.FindStringSubmatch(value)
	if len(match) == 0 {
		return 0
	}
	days := parseInt(match[1])
	hours := parseInt(match[2])
	minutes := parseInt(match[3])
	seconds := parseInt(match[4])
	return days*86400 + hours*3600 + minutes*60 + seconds
}

func parseInt(value string) int {
	result, _ := strconv.Atoi(value)
	return result
}

func parseMetric(value string) int64 {
	result, _ := strconv.ParseInt(value, 10, 64)
	return result
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
