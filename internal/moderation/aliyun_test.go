package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	green "github.com/alibabacloud-go/green-20220302/v3/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/visoraft/visoraft/internal/settings"
)

type fakeAliyunClient struct {
	textRequests    []*green.TextModerationRequest
	imageRequests   []*green.ImageModerationRequest
	videoRequests   []*green.VideoModerationRequest
	resultRequests  []*green.VideoModerationResultRequest
	textResponses   []*green.TextModerationResponse
	imageResponses  []*green.ImageModerationResponse
	videoResponses  []*green.VideoModerationResponse
	resultResponses []*green.VideoModerationResultResponse
}

func (f *fakeAliyunClient) TextModerationWithOptions(
	request *green.TextModerationRequest,
	_ *dara.RuntimeOptions,
) (*green.TextModerationResponse, error) {
	f.textRequests = append(f.textRequests, request)
	if len(f.textResponses) == 0 {
		return nil, errors.New("unexpected text request")
	}
	response := f.textResponses[0]
	f.textResponses = f.textResponses[1:]
	return response, nil
}

func (f *fakeAliyunClient) ImageModerationWithOptions(
	request *green.ImageModerationRequest,
	_ *dara.RuntimeOptions,
) (*green.ImageModerationResponse, error) {
	f.imageRequests = append(f.imageRequests, request)
	if len(f.imageResponses) == 0 {
		return nil, errors.New("unexpected image request")
	}
	response := f.imageResponses[0]
	f.imageResponses = f.imageResponses[1:]
	return response, nil
}

func (f *fakeAliyunClient) VideoModerationWithOptions(
	request *green.VideoModerationRequest,
	_ *dara.RuntimeOptions,
) (*green.VideoModerationResponse, error) {
	f.videoRequests = append(f.videoRequests, request)
	if len(f.videoResponses) == 0 {
		return nil, errors.New("unexpected video request")
	}
	response := f.videoResponses[0]
	f.videoResponses = f.videoResponses[1:]
	return response, nil
}

func (f *fakeAliyunClient) VideoModerationResultWithOptions(
	request *green.VideoModerationResultRequest,
	_ *dara.RuntimeOptions,
) (*green.VideoModerationResultResponse, error) {
	f.resultRequests = append(f.resultRequests, request)
	if len(f.resultResponses) == 0 {
		return nil, errors.New("unexpected video result request")
	}
	response := f.resultResponses[0]
	f.resultResponses = f.resultResponses[1:]
	return response, nil
}

func TestAliyunProviderModeratesAllChannelsAndPollsVideo(t *testing.T) {
	client := &fakeAliyunClient{
		textResponses: []*green.TextModerationResponse{
			textResponse("text-1", ""),
			textResponse("text-2", "profanity"),
		},
		imageResponses: []*green.ImageModerationResponse{{
			Body: &green.ImageModerationResponseBody{
				Code:      int32Pointer(200),
				RequestId: stringPointer("image-1"),
				Data: &green.ImageModerationResponseBodyData{
					RiskLevel: stringPointer("medium"),
					Result: []*green.ImageModerationResponseBodyDataResult{{
						Label:       stringPointer("logo"),
						Description: stringPointer("review"),
						RiskLevel:   stringPointer("medium"),
						Confidence:  float32Pointer(81.2),
					}},
				},
			},
		}},
		videoResponses: []*green.VideoModerationResponse{{
			Body: &green.VideoModerationResponseBody{
				Code:      int32Pointer(200),
				RequestId: stringPointer("video-submit"),
				Data: &green.VideoModerationResponseBodyData{
					TaskId: stringPointer("aliyun-task"),
				},
			},
		}},
		resultResponses: []*green.VideoModerationResultResponse{
			{
				Body: &green.VideoModerationResultResponseBody{
					Code:      int32Pointer(280),
					RequestId: stringPointer("video-poll-1"),
				},
			},
			{
				Body: &green.VideoModerationResultResponseBody{
					Code:      int32Pointer(200),
					RequestId: stringPointer("video-poll-2"),
					Data: &green.VideoModerationResultResponseBodyData{
						RiskLevel: stringPointer("high"),
						FrameResult: &green.VideoModerationResultResponseBodyDataFrameResult{
							RiskLevel: stringPointer("high"),
							FrameSummarys: []*green.VideoModerationResultResponseBodyDataFrameResultFrameSummarys{{
								Label:       stringPointer("violence"),
								Description: stringPointer("matched"),
							}},
						},
					},
				},
			},
		},
	}
	provider := newAliyunProviderWithClient(client)
	provider.wait = func(context.Context, time.Duration) error { return nil }

	result, err := provider.Moderate(context.Background(), Request{
		TaskID: "task-1",
		Config: settings.ModerationConfig{
			CheckText:               true,
			CheckImage:              true,
			CheckVideo:              true,
			TextService:             "pgc_detection",
			ImageService:            "baselineCheck",
			VideoService:            "videoDetection",
			HighRiskAction:          "block",
			MediumRiskAction:        "manual_review",
			VideoPollSeconds:        2,
			VideoMaximumWaitSeconds: 30,
		},
		Texts: []TextInput{{
			ID:      "description",
			Content: strings.Repeat("字", 601),
		}},
		ImageURL: "https://media.example.test/cover.jpg?signature=secret",
		VideoURL: "https://media.example.test/video.mp4?signature=secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.textRequests) != 2 {
		t.Fatalf("expected two text chunks, got %d", len(client.textRequests))
	}
	if len(client.resultRequests) != 2 {
		t.Fatalf("expected two video polls, got %d", len(client.resultRequests))
	}
	if result.RiskLevel != "high" || result.Decision != DecisionBlock {
		t.Fatalf("unexpected moderation decision: %#v", result)
	}
	if strings.Contains(string(result.Image.Raw), "signature") ||
		strings.Contains(string(result.Video.Raw), "signature") {
		t.Fatal("signed media URL leaked into persisted raw result")
	}
	var imageParameters map[string]string
	if err := json.Unmarshal(
		[]byte(pointerString(client.imageRequests[0].ServiceParameters)),
		&imageParameters,
	); err != nil {
		t.Fatal(err)
	}
	if imageParameters["imageUrl"] == "" {
		t.Fatal("image URL was not sent to the provider")
	}
}

func TestAliyunProviderReturnsPartialResultOnLaterFailure(t *testing.T) {
	client := &fakeAliyunClient{
		textResponses: []*green.TextModerationResponse{
			textResponse("text-1", ""),
		},
		imageResponses: []*green.ImageModerationResponse{{
			Body: &green.ImageModerationResponseBody{
				Code: int32Pointer(500),
				Msg:  stringPointer("upstream unavailable"),
			},
		}},
	}
	provider := newAliyunProviderWithClient(client)
	_, err := provider.Moderate(context.Background(), Request{
		TaskID: "task-2",
		Config: settings.ModerationConfig{
			CheckText:    true,
			CheckImage:   true,
			TextService:  "pgc_detection",
			ImageService: "baselineCheck",
		},
		Texts:    []TextInput{{ID: "title", Content: "safe"}},
		ImageURL: "https://media.example.test/cover.jpg",
	})
	var providerError *ProviderError
	if !errors.As(err, &providerError) {
		t.Fatalf("expected ProviderError, got %v", err)
	}
	if !providerError.Retryable ||
		providerError.Partial.Text.Status != "completed" ||
		providerError.Partial.Text.RequestIDs[0] != "text-1" {
		t.Fatalf("unexpected partial failure: %#v", providerError)
	}
}

func TestAliyunVideoPollingHonorsCancellation(t *testing.T) {
	client := &fakeAliyunClient{
		videoResponses: []*green.VideoModerationResponse{{
			Body: &green.VideoModerationResponseBody{
				Code: int32Pointer(200),
				Data: &green.VideoModerationResponseBodyData{
					TaskId: stringPointer("aliyun-task"),
				},
			},
		}},
	}
	provider := newAliyunProviderWithClient(client)
	provider.wait = func(ctx context.Context, _ time.Duration) error {
		return context.Canceled
	}
	_, err := provider.Moderate(context.Background(), Request{
		TaskID: "task-3",
		Config: settings.ModerationConfig{
			CheckVideo:              true,
			VideoService:            "videoDetection",
			VideoPollSeconds:        2,
			VideoMaximumWaitSeconds: 30,
		},
		VideoURL: "https://media.example.test/video.mp4",
	})
	var providerError *ProviderError
	if !errors.As(err, &providerError) ||
		providerError.Code != "moderation_cancelled" {
		t.Fatalf("unexpected cancellation result: %#v", err)
	}
}

func textResponse(requestID string, labels string) *green.TextModerationResponse {
	return &green.TextModerationResponse{
		Body: &green.TextModerationResponseBody{
			Code:      int32Pointer(200),
			RequestId: stringPointer(requestID),
			Data: &green.TextModerationResponseBodyData{
				Labels: labelsPointer(labels),
			},
		},
	}
}

func labelsPointer(value string) *string {
	if value == "" {
		return nil
	}
	return stringPointer(value)
}

func int32Pointer(value int32) *int32 {
	return &value
}

func float32Pointer(value float32) *float32 {
	return &value
}
