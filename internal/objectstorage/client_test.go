package objectstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPutSignsPayloadAndPersistsBody(t *testing.T) {
	t.Parallel()
	body := []byte("cover-image")
	expectedHash := sha256.Sum256(body)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Method != http.MethodPut {
			t.Errorf("unexpected method: %s", request.Method)
		}
		if request.URL.EscapedPath() != "/media/tasks/task/cover.jpg" {
			t.Errorf("unexpected path: %s", request.URL.EscapedPath())
		}
		if request.Header.Get("X-Amz-Content-Sha256") != hex.EncodeToString(expectedHash[:]) {
			t.Errorf("unexpected payload hash")
		}
		if request.Header.Get("Content-Type") != "image/jpeg" {
			t.Errorf("unexpected content type")
		}
		actual, err := io.ReadAll(request.Body)
		if err != nil || string(actual) != string(body) {
			t.Errorf("unexpected body: %q err=%v", actual, err)
		}
		writer.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	client, err := New(Config{
		Endpoint: server.URL, AccessKey: "access", SecretKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Put(
		context.Background(), "media", "tasks/task/cover.jpg", "image/jpeg", body,
	); err != nil {
		t.Fatal(err)
	}
}

func TestGetSignsAndForwardsRange(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.EscapedPath() != "/media-bucket/tasks/a%20file.mp4" {
			t.Errorf("unexpected path: %s", request.URL.EscapedPath())
		}
		if request.Header.Get("Range") != "bytes=10-19" {
			t.Errorf("unexpected range: %s", request.Header.Get("Range"))
		}
		if !strings.HasPrefix(
			request.Header.Get("Authorization"),
			"AWS4-HMAC-SHA256 Credential=access/20260724/us-east-1/s3/aws4_request",
		) {
			t.Errorf("request was not signed: %s", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Range", "bytes 10-19/100")
		writer.WriteHeader(http.StatusPartialContent)
		_, _ = writer.Write([]byte("0123456789"))
	}))
	defer server.Close()

	client, err := New(Config{
		Endpoint:  server.URL,
		AccessKey: "access",
		SecretKey: "secret",
		Region:    "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time {
		return time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	}
	response, err := client.Get(
		context.Background(),
		"media-bucket",
		"tasks/a file.mp4",
		"bytes=10-19",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusPartialContent || string(body) != "0123456789" {
		t.Fatalf("unexpected response: %d %q", response.StatusCode, body)
	}
}

func TestGetRejectsInvalidRange(t *testing.T) {
	t.Parallel()
	client, err := New(Config{
		Endpoint:  "http://127.0.0.1:8333",
		AccessKey: "access",
		SecretKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Get(context.Background(), "bucket", "key", "items=0-4")
	if err == nil {
		t.Fatal("expected an invalid range error")
	}
}

func TestPresignGetUsesPublicEndpointAndBoundedExpiry(t *testing.T) {
	t.Parallel()
	client, err := New(Config{
		Endpoint:       "http://seaweedfs:8333",
		PublicEndpoint: "https://media.example.test/storage",
		AccessKey:      "access",
		SecretKey:      "secret",
		Region:         "cn-shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time {
		return time.Date(2026, 7, 27, 9, 30, 0, 0, time.UTC)
	}
	value, err := client.PresignGet(
		"media-bucket",
		"tasks/a file.mp4",
		15*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "media.example.test" ||
		parsed.EscapedPath() != "/storage/media-bucket/tasks/a%20file.mp4" {
		t.Fatalf("unexpected public URL: %s", value)
	}
	query := parsed.Query()
	if query.Get("X-Amz-Expires") != "900" ||
		query.Get("X-Amz-Credential") !=
			"access/20260727/cn-shanghai/s3/aws4_request" ||
		query.Get("X-Amz-Signature") == "" {
		t.Fatalf("presign query is incomplete: %s", value)
	}
}

func TestPresignGetRequiresExplicitPublicEndpoint(t *testing.T) {
	t.Parallel()
	client, err := New(Config{
		Endpoint:  "http://seaweedfs:8333",
		AccessKey: "access",
		SecretKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.PresignGet("bucket", "key", 15*time.Minute); err == nil {
		t.Fatal("expected missing public endpoint error")
	}
}
