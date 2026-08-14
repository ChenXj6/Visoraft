package publishing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAcFunAdapterPublishesThroughFragmentWorkflow(t *testing.T) {
	t.Parallel()

	var (
		mu                 sync.Mutex
		mediaFragments     []int
		coverFragments     []int
		mediaCompleteCount int
		coverCompleteCount int
		createDougaForm    url.Values
	)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/channels":
			writeTestJSON(t, writer, map[string]any{
				"result":   0,
				"userId":   12345,
				"userName": "测试账号",
				"channels": []any{
					map[string]any{
						"channelId":   1,
						"channelName": "生活",
						"childChannels": []any{
							map[string]any{
								"channelId":   2,
								"channelName": "日常",
							},
						},
					},
				},
			})
		case "/cloud-token":
			requireTestForm(t, request)
			if request.Form.Get("fileName") != "media.mp4" ||
				request.Form.Get("template") != "1" {
				t.Errorf("unexpected media token form: %v", request.Form)
			}
			writeTestJSON(t, writer, map[string]any{
				"result": 0,
				"taskId": "task-1",
				"token":  "media-token",
				"uploadConfig": map[string]any{
					"partSize": 3,
				},
			})
		case "/fragment":
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read fragment: %v", err)
			}
			index, _ := strconv.Atoi(request.URL.Query().Get("fragment_id"))
			token := request.URL.Query().Get("upload_token")
			mu.Lock()
			if token == "media-token" {
				mediaFragments = append(mediaFragments, index)
				if len(body) > 3 {
					t.Errorf("media fragment is too large: %d", len(body))
				}
			} else if token == "cover-token" {
				coverFragments = append(coverFragments, index)
			} else {
				t.Errorf("unexpected upload token: %s", token)
			}
			mu.Unlock()
			writeTestJSON(t, writer, map[string]any{"result": 1})
		case "/complete":
			token := request.URL.Query().Get("upload_token")
			mu.Lock()
			if token == "media-token" {
				mediaCompleteCount++
				if request.URL.Query().Get("fragment_count") != "4" {
					t.Errorf(
						"unexpected media fragment count: %s",
						request.URL.Query().Get("fragment_count"),
					)
				}
			} else if token == "cover-token" {
				coverCompleteCount++
			}
			mu.Unlock()
			writeTestJSON(t, writer, map[string]any{"result": 1})
		case "/upload-finish":
			requireTestForm(t, request)
			if request.Form.Get("taskId") != "task-1" {
				t.Errorf("unexpected task id: %v", request.Form)
			}
			writeTestJSON(t, writer, map[string]any{"result": 0})
		case "/create-video":
			requireTestForm(t, request)
			if request.Form.Get("videoKey") != "task-1" ||
				request.Form.Get("vodType") != "ksCloud" {
				t.Errorf("unexpected create video form: %v", request.Form)
			}
			writeTestJSON(t, writer, map[string]any{
				"result":  0,
				"videoId": 42,
			})
		case "/qiniu-token":
			requireTestForm(t, request)
			writeTestJSON(t, writer, map[string]any{
				"result": 0,
				"info": map[string]any{
					"token": "cover-token",
				},
			})
		case "/uploaded-url":
			requireTestForm(t, request)
			if request.Form.Get("bizFlag") != "web-douga-cover" {
				t.Errorf("unexpected cover URL form: %v", request.Form)
			}
			writeTestJSON(t, writer, map[string]any{
				"result": 0,
				"url":    "https://cdn.example.invalid/cover.jpg",
			})
		case "/create-douga":
			requireTestForm(t, request)
			mu.Lock()
			createDougaForm = request.Form
			mu.Unlock()
			writeTestJSON(t, writer, map[string]any{
				"result":  0,
				"dougaId": 98765,
			})
		default:
			http.NotFound(writer, request)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	endpoints := acfunEndpoints{
		channels:     server.URL + "/channels",
		cloudToken:   server.URL + "/cloud-token",
		fragment:     server.URL + "/fragment",
		complete:     server.URL + "/complete",
		uploadFinish: server.URL + "/upload-finish",
		createVideo:  server.URL + "/create-video",
		qiniuToken:   server.URL + "/qiniu-token",
		uploadedURL:  server.URL + "/uploaded-url",
		createDouga:  server.URL + "/create-douga",
		referer:      server.URL + "/creator",
	}
	adapter := newAcFunWebAdapter(server.Client(), endpoints)
	adapter.now = func() time.Time {
		return time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	}

	host := strings.TrimPrefix(server.URL, "http://")
	jar := []byte(
		fmt.Sprintf(
			"%s\tFALSE\t/\tFALSE\t0\tacPasstoken\tsecret\n",
			host,
		),
	)
	// Netscape domains cannot contain ports.
	jar = []byte(
		fmt.Sprintf(
			"%s\tFALSE\t/\tFALSE\t0\tacPasstoken\tsecret\n",
			strings.Split(host, ":")[0],
		),
	)
	identity, err := adapter.CheckAccount(context.Background(), jar)
	if err != nil {
		t.Fatalf("check account: %v", err)
	}
	if identity.RemoteUserID != "12345" ||
		identity.RemoteDisplayName != "测试账号" {
		t.Fatalf("unexpected identity: %+v", identity)
	}
	categories, err := adapter.Categories(context.Background(), jar)
	if err != nil {
		t.Fatalf("read categories: %v", err)
	}
	if len(categories) != 2 ||
		categories[1].CategoryID != "2" ||
		categories[1].ParentID != "1" ||
		categories[1].Path != "生活 / 日常" {
		t.Fatalf("unexpected categories: %+v", categories)
	}

	tempDir := t.TempDir()
	mediaPath := filepath.Join(tempDir, "media.mp4")
	coverPath := filepath.Join(tempDir, "cover.jpg")
	if err := os.WriteFile(mediaPath, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(coverPath, []byte("cover"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage := ""
	result, err := adapter.Publish(context.Background(), UploadRequest{
		Publication: PlatformPublication{
			CategoryID:  "2",
			Title:       "测试标题",
			Description: "测试简介\n转载声明",
			Tags:        []string{"测试", "视频"},
		},
		SourceURL: "https://www.youtube.com/watch?v=example",
		MediaPath: mediaPath,
		CoverPath: coverPath,
		CookieJar: jar,
		OnStage: func(_ context.Context, value string) error {
			stage = value
			return nil
		},
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if result.RemoteSubmissionID != "98765" ||
		result.RemoteURL != "https://www.acfun.cn/v/ac98765" {
		t.Fatalf("unexpected publish result: %+v", result)
	}
	if stage != "submitting" {
		t.Fatalf("expected submitting stage, got %q", stage)
	}

	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(mediaFragments) != "[0 1 2 3]" ||
		mediaCompleteCount != 1 ||
		fmt.Sprint(coverFragments) != "[0]" ||
		coverCompleteCount != 1 {
		t.Fatalf(
			"unexpected upload calls: media=%v/%d cover=%v/%d",
			mediaFragments,
			mediaCompleteCount,
			coverFragments,
			coverCompleteCount,
		)
	}
	if createDougaForm.Get("creationType") != "1" ||
		createDougaForm.Get("channelId") != "2" ||
		createDougaForm.Get("originalLinkUrl") !=
			"https://www.youtube.com/watch?v=example" ||
		createDougaForm.Get("originalDeclare") != "0" {
		t.Fatalf("unexpected create douga form: %v", createDougaForm)
	}
	var tags []string
	if err := json.Unmarshal(
		[]byte(createDougaForm.Get("tagNames")),
		&tags,
	); err != nil || len(tags) != 2 {
		t.Fatalf("unexpected tagNames: %q", createDougaForm.Get("tagNames"))
	}
}

func TestAcFunSubmissionWithoutIDIsUncertain(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"result":0}`))
		},
	))
	defer server.Close()
	adapter := newAcFunWebAdapter(server.Client(), acfunEndpoints{
		createDouga: server.URL,
		referer:     server.URL,
	})
	_, err := adapter.createDouga(
		context.Background(),
		"token=value",
		"video-1",
		"https://cdn.example.invalid/cover.jpg",
		UploadRequest{
			Publication: PlatformPublication{
				CategoryID: "2",
				Title:      "测试",
			},
		},
	)
	var adapterError *AdapterError
	if err == nil ||
		!errorsAs(err, &adapterError) ||
		!adapterError.Uncertain {
		t.Fatalf("expected uncertain adapter error, got %v", err)
	}
}

func TestDecodeAcFunCategoriesDoesNotDuplicateNodes(t *testing.T) {
	value := map[string]any{
		"result": json.Number("0"),
		"data": []any{
			map[string]any{
				"channelId":   json.Number("1"),
				"channelName": "生活",
				"children": []any{
					map[string]any{
						"channelId":   json.Number("2"),
						"channelName": "日常",
					},
					map[string]any{
						"channelId":   json.Number("2"),
						"channelName": "日常",
					},
				},
			},
		},
	}
	actual := decodeAcFunCategories(value)
	if len(actual) != 2 || actual[1].ParentID != "1" {
		t.Fatalf("unexpected categories: %+v", actual)
	}
}

func requireTestForm(t *testing.T, request *http.Request) {
	t.Helper()
	if err := request.ParseForm(); err != nil {
		t.Fatalf("parse form: %v", err)
	}
}

func writeTestJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Errorf("encode test response: %v", err)
	}
}

func errorsAs(err error, target any) bool {
	switch typed := target.(type) {
	case **AdapterError:
		value, ok := err.(*AdapterError)
		if ok {
			*typed = value
		}
		return ok
	default:
		return false
	}
}
