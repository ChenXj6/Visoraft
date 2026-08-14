package monitors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestDiscoverGoogleExplainsInsufficientRequestLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/search" {
				t.Fatalf("unexpected path %s", request.URL.Path)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{
				"items": [{"id":{"videoId":"real-video-id"}}]
			}`))
		},
	))
	defer server.Close()

	items, quotaUnits, err := discoverGoogle(
		context.Background(),
		server.Client(),
		server.URL,
		"test-key",
		Monitor{
			MonitorType:       "search",
			Query:             "OpenAI",
			LookbackDays:      3,
			MaxResults:        5,
			OrderBy:           "date",
			RateLimitRequests: 1,
		},
	)
	if err == nil {
		t.Fatal("expected request limit error")
	}
	if len(items) != 0 {
		t.Fatalf("expected no candidates, got %d", len(items))
	}
	if quotaUnits != 1 {
		t.Fatalf("expected one consumed request, got %d", quotaUnits)
	}
	if !strings.Contains(err.Error(), "至少需要 2 次请求") {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestLoadGoogleCategoriesReturnsAssignableItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/videoCategories" {
				t.Fatalf("unexpected path %s", request.URL.Path)
			}
			if got := request.URL.Query().Get("part"); got != "snippet" {
				t.Fatalf("unexpected part %q", got)
			}
			if got := request.URL.Query().Get("regionCode"); got != "CN" {
				t.Fatalf("unexpected region %q", got)
			}
			if got := request.URL.Query().Get("hl"); got != "zh-CN" {
				t.Fatalf("unexpected language %q", got)
			}
			if got := request.URL.Query().Get("key"); got != "test-key" {
				t.Fatalf("unexpected key %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{
				"items": [
					{"id":"20","snippet":{"title":"游戏","assignable":true}},
					{"id":"99","snippet":{"title":"不可选","assignable":false}}
				]
			}`))
		},
	))
	defer server.Close()

	items, err := loadGoogleCategories(
		context.Background(),
		server.Client(),
		server.URL,
		"test-key",
		"CN",
	)
	if err != nil {
		t.Fatalf("load categories: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one assignable category, got %d", len(items))
	}
	if items[0].ID != "20" ||
		items[0].Title != "游戏" ||
		items[0].Provider != "google" {
		t.Fatalf("unexpected category: %+v", items[0])
	}
}

func TestLoadGoogleCategoriesSurfacesAPIProblem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
		},
	))
	defer server.Close()

	_, err := loadGoogleCategories(
		context.Background(),
		server.Client(),
		server.URL,
		"test-key",
		"US",
	)
	if err == nil {
		t.Fatal("expected API error")
	}
	if got := err.Error(); got != "youtube api rejected request: quota exceeded" {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestDiscoverGoogleSeriesFindsEveryRequestedEpisode(t *testing.T) {
	var mu sync.Mutex
	searchQueries := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/search":
				if request.URL.Query().Get("type") == "playlist" {
					_, _ = writer.Write([]byte(`{"items":[]}`))
					return
				}
				query := request.URL.Query().Get("q")
				mu.Lock()
				searchQueries = append(searchQueries, query)
				mu.Unlock()
				episode := "01"
				if strings.Contains(query, "第02集") {
					episode = "02"
				}
				_, _ = writer.Write([]byte(`{"items":[{"id":{"videoId":"episode-` + episode + `"}}]}`))
			case "/videos":
				ids := strings.Split(request.URL.Query().Get("id"), ",")
				items := make([]map[string]any, 0, len(ids))
				for _, id := range ids {
					episode := strings.TrimPrefix(id, "episode-")
					items = append(items, map[string]any{
						"id": id,
						"snippet": map[string]any{
							"title":                "《還珠格格1》 第" + episode + "集",
							"channelId":            "official-channel",
							"channelTitle":         "正版剧场",
							"publishedAt":          "2019-08-26T00:00:00Z",
							"liveBroadcastContent": "none",
						},
						"contentDetails": map[string]any{"duration": "PT45M"},
						"statistics":     map[string]any{"viewCount": "1000"},
					})
				}
				_ = json.NewEncoder(writer).Encode(map[string]any{"items": items})
			default:
				http.NotFound(writer, request)
			}
		},
	))
	defer server.Close()

	items, quotaUnits, err := discoverGoogle(
		context.Background(),
		server.Client(),
		server.URL,
		"test-key",
		Monitor{
			MonitorType:       "series",
			ChannelMode:       "historical",
			SeriesTitle:       "还珠格格1",
			EpisodeStart:      1,
			EpisodeEnd:        2,
			OrderBy:           "relevance",
			RateLimitRequests: 5,
		},
	)
	if err != nil {
		t.Fatalf("discover series: %v", err)
	}
	if quotaUnits != 5 || len(items) != 2 {
		t.Fatalf("unexpected series result: quota=%d items=%+v", quotaUnits, items)
	}
	if items[0].EpisodeNumber != 1 || items[1].EpisodeNumber != 2 {
		t.Fatalf("episode numbers were not persisted: %+v", items)
	}
	if len(searchQueries) != 2 ||
		!strings.Contains(searchQueries[0], "还珠格格1") ||
		strings.Contains(searchQueries[0], "第02集") ||
		!strings.Contains(searchQueries[1], "第02集") {
		t.Fatalf("expected one broad query and one missing-episode query, got %v", searchQueries)
	}
}

func TestDiscoverGoogleSeriesUsesOneBroadRequestPerCompleteScope(t *testing.T) {
	searchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/search":
				if request.URL.Query().Get("type") == "playlist" {
					_, _ = writer.Write([]byte(`{"items":[]}`))
					return
				}
				searchCalls++
				_, _ = writer.Write([]byte(`{"items":[{"id":{"videoId":"episode-01"}},{"id":{"videoId":"episode-02"}}]}`))
			case "/videos":
				items := []map[string]any{}
				for _, id := range strings.Split(request.URL.Query().Get("id"), ",") {
					episode := strings.TrimPrefix(id, "episode-")
					items = append(items, map[string]any{
						"id": id,
						"snippet": map[string]any{
							"title":     "《還珠格格1》 第" + episode + "集",
							"channelId": "official-channel", "channelTitle": "正版剧场",
							"publishedAt": "2019-08-26T00:00:00Z", "liveBroadcastContent": "none",
						},
						"contentDetails": map[string]any{"duration": "PT45M"},
						"statistics":     map[string]any{"viewCount": "1000"},
					})
				}
				_ = json.NewEncoder(writer).Encode(map[string]any{"items": items})
			default:
				http.NotFound(writer, request)
			}
		},
	))
	defer server.Close()

	items, quotaUnits, err := discoverGoogle(
		context.Background(), server.Client(), server.URL, "test-key",
		Monitor{
			MonitorType: "series", ChannelMode: "historical", SeriesTitle: "还珠格格1",
			EpisodeStart: 1, EpisodeEnd: 2, MaxResults: 50, OrderBy: "relevance",
			RateLimitRequests: 4,
		},
	)
	if err != nil {
		t.Fatalf("discover complete series scope: %v", err)
	}
	if searchCalls != 1 || quotaUnits != 3 || len(items) != 2 {
		t.Fatalf("expected one search plus one details request, searches=%d quota=%d items=%d", searchCalls, quotaUnits, len(items))
	}
}

func TestDiscoverGoogleSeriesPrefersCompletePlaylist(t *testing.T) {
	videoSearchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/search":
				if request.URL.Query().Get("type") != "playlist" {
					videoSearchCalls++
				}
				_, _ = writer.Write([]byte(`{"items":[{"id":{"playlistId":"playlist-full"}}]}`))
			case "/playlistItems":
				_, _ = writer.Write([]byte(`{"items":[{"contentDetails":{"videoId":"episode-01"}},{"contentDetails":{"videoId":"episode-02"}}]}`))
			case "/videos":
				items := []map[string]any{}
				for _, id := range strings.Split(request.URL.Query().Get("id"), ",") {
					episode := strings.TrimPrefix(id, "episode-")
					items = append(items, map[string]any{
						"id": id,
						"snippet": map[string]any{
							"title": "山河令 第" + episode + "集", "description": "张哲瀚 龚俊",
							"channelId": "official", "channelTitle": "官方频道",
							"publishedAt": "2021-01-01T00:00:00Z", "liveBroadcastContent": "none",
						},
						"contentDetails": map[string]any{"duration": "PT45M"},
						"statistics":     map[string]any{"viewCount": "1000"},
					})
				}
				_ = json.NewEncoder(writer).Encode(map[string]any{"items": items})
			default:
				http.NotFound(writer, request)
			}
		},
	))
	defer server.Close()

	items, quotaUnits, err := discoverGoogle(
		context.Background(), server.Client(), server.URL, "test-key",
		Monitor{
			MonitorType: "series", ChannelMode: "historical", SeriesTitle: "山河令",
			EpisodeStart: 1, EpisodeEnd: 2, SeriesScopes: []SeriesScope{{Key: "part-1", EpisodeStart: 1, EpisodeEnd: 2}},
			MaxResults: 50, OrderBy: "relevance", RateLimitRequests: 10,
			VideoTypes: []string{"video"}, MinDurationSeconds: 1200,
			IncludeKeywords: []string{"张哲瀚", "龚俊"},
		},
	)
	if err != nil || quotaUnits != 3 || len(items) != 2 {
		t.Fatalf("unexpected playlist result: quota=%d items=%+v err=%v", quotaUnits, items, err)
	}
	if videoSearchCalls != 0 {
		t.Fatalf("complete playlist should avoid video search, got %d calls", videoSearchCalls)
	}
}

func TestHistoricalDiscoveryDoesNotApplyRollingLookback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/search" {
				if got := request.URL.Query().Get("publishedAfter"); got != "" {
					t.Fatalf("historical search unexpectedly applied publishedAfter=%q", got)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(`{"items":[]}`))
				return
			}
			http.NotFound(writer, request)
		},
	))
	defer server.Close()

	items, quotaUnits, err := discoverGoogle(
		context.Background(), server.Client(), server.URL, "test-key",
		Monitor{
			MonitorType: "search", ChannelMode: "historical", Query: "老剧",
			LookbackDays: 7, MaxResults: 5, OrderBy: "date", RateLimitRequests: 2,
		},
	)
	if err != nil || quotaUnits != 1 || len(items) != 0 {
		t.Fatalf("unexpected historical result: quota=%d items=%d err=%v", quotaUnits, len(items), err)
	}
}

func TestGoogleSearchDoesNotTurnIncludeKeywordsIntoRequiredQueryTerms(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/search" {
				if got := request.URL.Query().Get("q"); got != "还珠格格1 第01集" {
					t.Fatalf("search query was unexpectedly narrowed: %q", got)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(`{"items":[]}`))
				return
			}
			http.NotFound(writer, request)
		},
	))
	defer server.Close()

	items, quotaUnits, err := discoverGoogle(
		context.Background(), server.Client(), server.URL, "test-key",
		Monitor{
			MonitorType: "search", ChannelMode: "historical",
			Query: "还珠格格1 第01集", IncludeKeywords: []string{"赵薇", "苏有朋", "林心如"},
			MaxResults: 5, OrderBy: "relevance", RateLimitRequests: 2,
		},
	)
	if err != nil || quotaUnits != 1 || len(items) != 0 {
		t.Fatalf("unexpected search result: quota=%d items=%d err=%v", quotaUnits, len(items), err)
	}
}

func TestChannelMonitorAcceptsHandleAndResolvesItBeforeSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/channels":
				if got := request.URL.Query().Get("forHandle"); got != "@visoraft" {
					t.Fatalf("unexpected handle %q", got)
				}
				_, _ = writer.Write([]byte(`{"items":[{"id":"UC-visoraft"}]}`))
			case "/search":
				if got := request.URL.Query().Get("channelId"); got != "UC-visoraft" {
					t.Fatalf("unexpected channel id %q", got)
				}
				_, _ = writer.Write([]byte(`{"items":[]}`))
			default:
				http.NotFound(writer, request)
			}
		},
	))
	defer server.Close()

	items, quotaUnits, err := discoverGoogle(
		context.Background(), server.Client(), server.URL, "test-key",
		Monitor{
			MonitorType: "channel", ChannelMode: "historical",
			ChannelIDs: []string{"https://www.youtube.com/@visoraft"},
			MaxResults: 5, OrderBy: "date", RateLimitRequests: 3,
		},
	)
	if err != nil || quotaUnits != 2 || len(items) != 0 {
		t.Fatalf("unexpected channel result: quota=%d items=%d err=%v", quotaUnits, len(items), err)
	}
}

func TestChannelMonitorUsesDirectChannelURLWithoutLookup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/search" {
				t.Fatalf("unexpected path %s", request.URL.Path)
			}
			if got := request.URL.Query().Get("channelId"); got != "UC-direct-channel" {
				t.Fatalf("unexpected channel id %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"items":[]}`))
		},
	))
	defer server.Close()

	_, quotaUnits, err := discoverGoogle(
		context.Background(), server.Client(), server.URL, "test-key",
		Monitor{
			MonitorType: "channel", ChannelMode: "historical",
			ChannelIDs: []string{"https://youtube.com/channel/UC-direct-channel"},
			MaxResults: 5, OrderBy: "date", RateLimitRequests: 2,
		},
	)
	if err != nil || quotaUnits != 1 {
		t.Fatalf("unexpected direct channel result: quota=%d err=%v", quotaUnits, err)
	}
}

func TestSeriesCandidatePrefersRulePassingFullEpisode(t *testing.T) {
	monitor := Monitor{
		VideoTypes: []string{"video"}, MinDurationSeconds: 1200,
		IncludeKeywords: []string{"张哲瀚", "龚俊"},
	}
	shortClip := Candidate{
		Title: "山河令 第02集 花絮", Description: "张哲瀚",
		VideoType: "video", DurationSeconds: 479, ViewCount: 2000000,
	}
	fullEpisode := Candidate{
		Title: "山河令 第02集", Description: "张哲瀚 龚俊",
		VideoType: "video", DurationSeconds: 2700, ViewCount: 500000,
	}
	if !preferSeriesCandidate(monitor, shortClip, fullEpisode) {
		t.Fatal("expected the rule-passing full episode to be preferred")
	}
	if preferSeriesCandidate(monitor, fullEpisode, shortClip) {
		t.Fatal("short clip must not replace the rule-passing full episode")
	}
}
