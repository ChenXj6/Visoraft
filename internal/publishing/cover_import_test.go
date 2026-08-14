package publishing

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"testing"
)

type coverRoundTripper func(*http.Request) (*http.Response, error)

func (function coverRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

func TestFetchImportedCoverValidatesImage(t *testing.T) {
	t.Parallel()
	canvas := image.NewNRGBA(image.Rect(0, 0, 1280, 720))
	canvas.Set(0, 0, color.NRGBA{R: 42, G: 90, B: 210, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: coverRoundTripper(func(
		request *http.Request,
	) (*http.Response, error) {
		if request.URL.Hostname() != "i.ytimg.com" {
			t.Errorf("unexpected host: %s", request.URL.Hostname())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(encoded.Bytes())),
			Request:    request,
		}, nil
	})}
	cover, err := fetchImportedCover(
		context.Background(), client, "https://i.ytimg.com/vi/example/maxresdefault.jpg",
	)
	if err != nil {
		t.Fatal(err)
	}
	if cover.ContentType != "image/png" || cover.Width != 1280 || cover.Height != 720 {
		t.Fatalf("unexpected cover: %#v", cover)
	}
}

func TestFetchImportedCoverRejectsUntrustedHost(t *testing.T) {
	t.Parallel()
	_, err := fetchImportedCover(
		context.Background(),
		&http.Client{},
		"https://example.com/cover.jpg",
	)
	if err == nil {
		t.Fatal("expected untrusted host error")
	}
}
