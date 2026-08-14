package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	green "github.com/alibabacloud-go/green-20220302/v3/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/visoraft/visoraft/internal/settings"
)

const (
	aliyunSuccessCode    int32 = 200
	aliyunProcessingCode int32 = 280
	maxTextRunes               = 600
	maxFindings                = 200
)

type aliyunSDK interface {
	TextModerationWithOptions(
		*green.TextModerationRequest,
		*dara.RuntimeOptions,
	) (*green.TextModerationResponse, error)
	ImageModerationWithOptions(
		*green.ImageModerationRequest,
		*dara.RuntimeOptions,
	) (*green.ImageModerationResponse, error)
	VideoModerationWithOptions(
		*green.VideoModerationRequest,
		*dara.RuntimeOptions,
	) (*green.VideoModerationResponse, error)
	VideoModerationResultWithOptions(
		*green.VideoModerationResultRequest,
		*dara.RuntimeOptions,
	) (*green.VideoModerationResultResponse, error)
}

type AliyunProvider struct {
	client  aliyunSDK
	runtime *dara.RuntimeOptions
	wait    func(context.Context, time.Duration) error
}

func NewAliyunProvider(
	accessKeyID string,
	accessKeySecret string,
	config settings.ModerationConfig,
) (*AliyunProvider, error) {
	accessKeyID = strings.TrimSpace(accessKeyID)
	accessKeySecret = strings.TrimSpace(accessKeySecret)
	region := strings.TrimSpace(config.Region)
	if accessKeyID == "" || accessKeySecret == "" {
		return nil, errors.New("Aliyun access key ID and secret are required")
	}
	if region == "" {
		return nil, errors.New("Aliyun region is required")
	}
	timeoutSeconds := config.RequestTimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	timeoutMS := timeoutSeconds * 1000
	endpoint := "green-cip." + region + ".aliyuncs.com"
	sdk, err := green.NewClient(&openapiutil.Config{
		AccessKeyId:     stringPointer(accessKeyID),
		AccessKeySecret: stringPointer(accessKeySecret),
		RegionId:        stringPointer(region),
		Endpoint:        stringPointer(endpoint),
		ConnectTimeout:  intPointer(timeoutMS),
		ReadTimeout:     intPointer(timeoutMS),
	})
	if err != nil {
		return nil, fmt.Errorf("create Aliyun Green client: %w", err)
	}
	return newAliyunProviderWithClient(sdk), nil
}

func newAliyunProviderWithClient(client aliyunSDK) *AliyunProvider {
	return &AliyunProvider{
		client:  client,
		runtime: &dara.RuntimeOptions{},
		wait: func(ctx context.Context, duration time.Duration) error {
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

func (p *AliyunProvider) Moderate(
	ctx context.Context,
	request Request,
) (Result, error) {
	result := Result{
		TaskID:   request.TaskID,
		Provider: "aliyun",
		Text:     skipped(request.Config.TextService),
		Image:    skipped(request.Config.ImageService),
		Video:    skipped(request.Config.VideoService),
	}
	if request.Config.CheckText {
		channel, err := p.moderateText(ctx, request)
		if err != nil {
			return result, withPartial(err, result)
		}
		result.Text = channel
	}
	if request.Config.CheckImage {
		channel, err := p.moderateImage(ctx, request)
		if err != nil {
			result.Image = channel
			return result, withPartial(err, result)
		}
		result.Image = channel
	}
	if request.Config.CheckVideo {
		channel, err := p.moderateVideo(ctx, request)
		if err != nil {
			result.Video = channel
			return result, withPartial(err, result)
		}
		result.Video = channel
	}
	result.RiskLevel = HighestRisk(
		result.Text.RiskLevel,
		result.Image.RiskLevel,
		result.Video.RiskLevel,
	)
	result.Decision = DecisionForRisk(result.RiskLevel, request.Config)
	return result, nil
}

func (p *AliyunProvider) moderateText(
	ctx context.Context,
	request Request,
) (ChannelResult, error) {
	channel := completedChannel(request.Config.TextService)
	summaries := make([]map[string]any, 0)
	chunkNumber := 0
	for _, input := range request.Texts {
		for _, content := range splitText(input.Content, maxTextRunes) {
			if err := ctx.Err(); err != nil {
				return channel, providerFailure(
					"moderation_cancelled",
					"content moderation was cancelled",
					true,
					err,
				)
			}
			chunkNumber++
			parameters, err := json.Marshal(map[string]any{
				"content": content,
				"dataId":  fmt.Sprintf("%s:%s:%d", request.TaskID, input.ID, chunkNumber),
			})
			if err != nil {
				return channel, providerFailure(
					"request_encoding_failed",
					"encode Aliyun text moderation request",
					false,
					err,
				)
			}
			response, err := p.client.TextModerationWithOptions(
				&green.TextModerationRequest{
					Service:           stringPointer(request.Config.TextService),
					ServiceParameters: stringPointer(string(parameters)),
				},
				p.runtime,
			)
			if err != nil {
				return channel, providerFailure(
					"aliyun_text_request_failed",
					"Aliyun text moderation request failed",
					true,
					err,
				)
			}
			body, err := validateTextResponse(response)
			if err != nil {
				return channel, err
			}
			requestID := pointerString(body.RequestId)
			if requestID != "" {
				channel.RequestIDs = append(channel.RequestIDs, requestID)
			}
			labels := ""
			description := ""
			reason := ""
			if body.Data != nil {
				labels = pointerString(body.Data.Labels)
				description = pointerString(body.Data.Descriptions)
				reason = pointerString(body.Data.Reason)
			}
			risk := "none"
			if hasRiskLabel(labels) {
				// Text Moderation 2.0 returns matched labels but no separate
				// severity field. A label hit is therefore treated as high risk.
				risk = "high"
				for _, label := range splitLabels(labels) {
					appendFinding(&channel.Findings, Finding{
						Label:       label,
						Description: firstNonEmpty(description, reason),
						RiskLevel:   risk,
						Location:    input.ID,
					})
				}
			}
			channel.RiskLevel = HighestRisk(channel.RiskLevel, risk)
			summaries = append(summaries, map[string]any{
				"request_id": requestID,
				"input_id":   input.ID,
				"labels":     splitLabels(labels),
				"risk_level": risk,
			})
		}
	}
	channel.Raw = safeRaw(map[string]any{
		"provider":  "aliyun",
		"responses": summaries,
	})
	return channel, nil
}

func (p *AliyunProvider) moderateImage(
	ctx context.Context,
	request Request,
) (ChannelResult, error) {
	channel := completedChannel(request.Config.ImageService)
	if err := ctx.Err(); err != nil {
		return channel, providerFailure(
			"moderation_cancelled",
			"content moderation was cancelled",
			true,
			err,
		)
	}
	if strings.TrimSpace(request.ImageURL) == "" {
		return channel, providerFailure(
			"image_url_missing",
			"image moderation is enabled but no public image URL is available",
			false,
			nil,
		)
	}
	parameters, err := json.Marshal(map[string]any{
		"imageUrl": request.ImageURL,
		"dataId":   request.TaskID + ":cover",
	})
	if err != nil {
		return channel, providerFailure(
			"request_encoding_failed",
			"encode Aliyun image moderation request",
			false,
			err,
		)
	}
	response, err := p.client.ImageModerationWithOptions(
		&green.ImageModerationRequest{
			Service:           stringPointer(request.Config.ImageService),
			ServiceParameters: stringPointer(string(parameters)),
		},
		p.runtime,
	)
	if err != nil {
		return channel, providerFailure(
			"aliyun_image_request_failed",
			"Aliyun image moderation request failed",
			true,
			err,
		)
	}
	body, err := validateImageResponse(response)
	if err != nil {
		return channel, err
	}
	requestID := pointerString(body.RequestId)
	if requestID != "" {
		channel.RequestIDs = append(channel.RequestIDs, requestID)
	}
	if body.Data != nil {
		channel.RiskLevel = normalizeRisk(pointerString(body.Data.RiskLevel))
		for _, item := range body.Data.Result {
			if item == nil {
				continue
			}
			appendFinding(&channel.Findings, Finding{
				Label:       pointerString(item.Label),
				Description: pointerString(item.Description),
				RiskLevel:   pointerString(item.RiskLevel),
				Confidence:  float64(pointerFloat32(item.Confidence)),
				Location:    "cover",
			})
		}
	}
	channel.Raw = safeRaw(map[string]any{
		"provider":   "aliyun",
		"request_id": requestID,
		"risk_level": channel.RiskLevel,
		"findings":   channel.Findings,
	})
	return channel, nil
}

func (p *AliyunProvider) moderateVideo(
	ctx context.Context,
	request Request,
) (ChannelResult, error) {
	channel := completedChannel(request.Config.VideoService)
	if err := ctx.Err(); err != nil {
		return channel, providerFailure(
			"moderation_cancelled",
			"content moderation was cancelled",
			true,
			err,
		)
	}
	if strings.TrimSpace(request.VideoURL) == "" {
		return channel, providerFailure(
			"video_url_missing",
			"video moderation is enabled but no public video URL is available",
			false,
			nil,
		)
	}
	parameters, err := json.Marshal(map[string]any{
		"url":    request.VideoURL,
		"dataId": request.TaskID,
	})
	if err != nil {
		return channel, providerFailure(
			"request_encoding_failed",
			"encode Aliyun video moderation request",
			false,
			err,
		)
	}
	submission, err := p.client.VideoModerationWithOptions(
		&green.VideoModerationRequest{
			Service:           stringPointer(request.Config.VideoService),
			ServiceParameters: stringPointer(string(parameters)),
		},
		p.runtime,
	)
	if err != nil {
		return channel, providerFailure(
			"aliyun_video_submit_failed",
			"Aliyun video moderation submission failed",
			true,
			err,
		)
	}
	submissionBody, err := validateVideoSubmission(submission)
	if err != nil {
		return channel, err
	}
	requestID := pointerString(submissionBody.RequestId)
	if requestID != "" {
		channel.RequestIDs = append(channel.RequestIDs, requestID)
	}
	taskID := ""
	if submissionBody.Data != nil {
		taskID = pointerString(submissionBody.Data.TaskId)
	}
	if taskID == "" {
		return channel, providerFailure(
			"aliyun_video_task_missing",
			"Aliyun video moderation did not return a task ID",
			true,
			nil,
		)
	}

	pollSeconds := request.Config.VideoPollSeconds
	if pollSeconds <= 0 {
		pollSeconds = 5
	}
	maximumWaitSeconds := request.Config.VideoMaximumWaitSeconds
	if maximumWaitSeconds <= 0 {
		maximumWaitSeconds = 900
	}
	pollInterval := time.Duration(pollSeconds) * time.Second
	maximumPolls := maximumWaitSeconds / pollSeconds
	if maximumPolls < 1 {
		maximumPolls = 1
	}
	queryParameters, err := json.Marshal(map[string]string{"taskId": taskID})
	if err != nil {
		return channel, providerFailure(
			"request_encoding_failed",
			"encode Aliyun video result request",
			false,
			err,
		)
	}
	for poll := 0; poll < maximumPolls; poll++ {
		if err := p.wait(ctx, pollInterval); err != nil {
			return channel, providerFailure(
				"moderation_cancelled",
				"content moderation was cancelled",
				true,
				err,
			)
		}
		response, err := p.client.VideoModerationResultWithOptions(
			&green.VideoModerationResultRequest{
				Service:           stringPointer(request.Config.VideoService),
				ServiceParameters: stringPointer(string(queryParameters)),
			},
			p.runtime,
		)
		if err != nil {
			return channel, providerFailure(
				"aliyun_video_result_failed",
				"Aliyun video moderation result request failed",
				true,
				err,
			)
		}
		if response == nil || response.Body == nil {
			return channel, providerFailure(
				"aliyun_invalid_response",
				"Aliyun video moderation returned an empty response",
				true,
				nil,
			)
		}
		body := response.Body
		resultRequestID := pointerString(body.RequestId)
		if resultRequestID != "" {
			channel.RequestIDs = append(channel.RequestIDs, resultRequestID)
		}
		switch pointerInt32(body.Code) {
		case aliyunProcessingCode:
			continue
		case aliyunSuccessCode:
			fillVideoResult(&channel, body.Data)
			channel.Raw = safeRaw(map[string]any{
				"provider":    "aliyun",
				"task_id":     taskID,
				"request_ids": channel.RequestIDs,
				"risk_level":  channel.RiskLevel,
				"findings":    channel.Findings,
			})
			return channel, nil
		default:
			return channel, providerFailure(
				fmt.Sprintf("aliyun_video_code_%d", pointerInt32(body.Code)),
				firstNonEmpty(
					pointerString(body.Message),
					"Aliyun video moderation result request failed",
				),
				isRetryableCode(pointerInt32(body.Code)),
				nil,
			)
		}
	}
	return channel, providerFailure(
		"aliyun_video_timeout",
		"Aliyun video moderation did not finish before the configured timeout",
		true,
		nil,
	)
}

func validateTextResponse(
	response *green.TextModerationResponse,
) (*green.TextModerationResponseBody, error) {
	if response == nil || response.Body == nil {
		return nil, providerFailure(
			"aliyun_invalid_response",
			"Aliyun text moderation returned an empty response",
			true,
			nil,
		)
	}
	if pointerInt32(response.Body.Code) != aliyunSuccessCode {
		return nil, providerFailure(
			fmt.Sprintf("aliyun_text_code_%d", pointerInt32(response.Body.Code)),
			firstNonEmpty(
				pointerString(response.Body.Message),
				"Aliyun text moderation request failed",
			),
			isRetryableCode(pointerInt32(response.Body.Code)),
			nil,
		)
	}
	return response.Body, nil
}

func validateImageResponse(
	response *green.ImageModerationResponse,
) (*green.ImageModerationResponseBody, error) {
	if response == nil || response.Body == nil {
		return nil, providerFailure(
			"aliyun_invalid_response",
			"Aliyun image moderation returned an empty response",
			true,
			nil,
		)
	}
	if pointerInt32(response.Body.Code) != aliyunSuccessCode {
		return nil, providerFailure(
			fmt.Sprintf("aliyun_image_code_%d", pointerInt32(response.Body.Code)),
			firstNonEmpty(
				pointerString(response.Body.Msg),
				"Aliyun image moderation request failed",
			),
			isRetryableCode(pointerInt32(response.Body.Code)),
			nil,
		)
	}
	return response.Body, nil
}

func validateVideoSubmission(
	response *green.VideoModerationResponse,
) (*green.VideoModerationResponseBody, error) {
	if response == nil || response.Body == nil {
		return nil, providerFailure(
			"aliyun_invalid_response",
			"Aliyun video moderation returned an empty response",
			true,
			nil,
		)
	}
	if pointerInt32(response.Body.Code) != aliyunSuccessCode {
		return nil, providerFailure(
			fmt.Sprintf("aliyun_video_code_%d", pointerInt32(response.Body.Code)),
			firstNonEmpty(
				pointerString(response.Body.Message),
				"Aliyun video moderation submission failed",
			),
			isRetryableCode(pointerInt32(response.Body.Code)),
			nil,
		)
	}
	return response.Body, nil
}

func fillVideoResult(
	channel *ChannelResult,
	data *green.VideoModerationResultResponseBodyData,
) {
	if data == nil {
		return
	}
	channel.RiskLevel = normalizeRisk(pointerString(data.RiskLevel))
	if data.FrameResult != nil {
		channel.RiskLevel = HighestRisk(
			channel.RiskLevel,
			pointerString(data.FrameResult.RiskLevel),
		)
		for _, summary := range data.FrameResult.FrameSummarys {
			if summary == nil {
				continue
			}
			appendFinding(&channel.Findings, Finding{
				Label:       pointerString(summary.Label),
				Description: pointerString(summary.Description),
				RiskLevel:   pointerString(data.FrameResult.RiskLevel),
				Location:    "video frames",
			})
		}
		for _, frame := range data.FrameResult.Frames {
			if frame == nil {
				continue
			}
			frameRisk := pointerString(frame.RiskLevel)
			channel.RiskLevel = HighestRisk(channel.RiskLevel, frameRisk)
			for _, resultGroup := range frame.Results {
				if resultGroup == nil {
					continue
				}
				for _, item := range resultGroup.Result {
					if item == nil {
						continue
					}
					appendFinding(&channel.Findings, Finding{
						Label:       pointerString(item.Label),
						Description: pointerString(item.Description),
						RiskLevel:   frameRisk,
						Confidence:  float64(pointerFloat32(item.Confidence)),
						Location:    fmt.Sprintf("video %.3fs", pointerFloat32(frame.Offset)),
					})
				}
			}
		}
	}
	if data.AudioResult != nil {
		channel.RiskLevel = HighestRisk(
			channel.RiskLevel,
			pointerString(data.AudioResult.RiskLevel),
		)
		for _, summary := range data.AudioResult.AudioSummarys {
			if summary == nil {
				continue
			}
			appendFinding(&channel.Findings, Finding{
				Label:       pointerString(summary.Label),
				Description: pointerString(summary.Description),
				RiskLevel:   pointerString(data.AudioResult.RiskLevel),
				Location:    "video audio",
			})
		}
		for _, slice := range data.AudioResult.SliceDetails {
			if slice == nil {
				continue
			}
			sliceRisk := pointerString(slice.RiskLevel)
			channel.RiskLevel = HighestRisk(channel.RiskLevel, sliceRisk)
			for _, label := range splitLabels(pointerString(slice.Labels)) {
				appendFinding(&channel.Findings, Finding{
					Label:       label,
					Description: pointerString(slice.Descriptions),
					RiskLevel:   sliceRisk,
					Confidence:  float64(pointerFloat32(slice.Score)),
					Location: fmt.Sprintf(
						"audio %ds-%ds",
						pointerInt64(slice.StartTime),
						pointerInt64(slice.EndTime),
					),
				})
			}
		}
	}
}

func completedChannel(service string) ChannelResult {
	return ChannelResult{
		Status:     "completed",
		Service:    service,
		RiskLevel:  "none",
		RequestIDs: []string{},
		Findings:   []Finding{},
	}
}

func splitText(value string, maximum int) []string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return nil
	}
	result := make([]string, 0, (len(runes)+maximum-1)/maximum)
	for len(runes) > 0 {
		length := maximum
		if len(runes) < length {
			length = len(runes)
		}
		result = append(result, string(runes[:length]))
		runes = runes[length:]
	}
	return result
}

func splitLabels(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '，' || r == '；'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" && !strings.EqualFold(part, "none") {
			result = append(result, part)
		}
	}
	return result
}

func hasRiskLabel(value string) bool {
	return len(splitLabels(value)) > 0
}

func appendFinding(target *[]Finding, value Finding) {
	if len(*target) >= maxFindings {
		return
	}
	if strings.TrimSpace(value.Label) == "" {
		return
	}
	value.RiskLevel = normalizeRisk(value.RiskLevel)
	*target = append(*target, value)
}

func safeRaw(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func withPartial(err error, partial Result) error {
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		copy := *providerError
		copy.Partial = partial
		return &copy
	}
	return &ProviderError{
		Code:      "moderation_failed",
		Message:   "content moderation failed",
		Retryable: true,
		Partial:   partial,
	}
}

func providerFailure(
	code string,
	message string,
	retryable bool,
	cause error,
) error {
	if cause != nil && !errors.Is(cause, context.Canceled) &&
		!errors.Is(cause, context.DeadlineExceeded) {
		message += ": " + cause.Error()
	}
	return &ProviderError{
		Code:      code,
		Message:   message,
		Retryable: retryable,
	}
}

func isRetryableCode(code int32) bool {
	return code == 0 || code == aliyunProcessingCode || code >= 500
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func pointerInt32(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}

func pointerInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func pointerFloat32(value *float32) float32 {
	if value == nil {
		return 0
	}
	return *value
}

func stringPointer(value string) *string {
	return &value
}

func intPointer(value int) *int {
	return &value
}
