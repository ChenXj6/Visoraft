package publishing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	acfunChannelsURL       = "https://member.acfun.cn/video/api/getMyChannels"
	acfunCloudTokenURL     = "https://member.acfun.cn/video/api/getKSCloudToken"
	acfunFragmentURL       = "https://upload.kuaishouzt.com/api/upload/fragment"
	acfunCompleteURL       = "https://upload.kuaishouzt.com/api/upload/complete"
	acfunUploadFinishURL   = "https://member.acfun.cn/video/api/uploadFinish"
	acfunCreateVideoURL    = "https://member.acfun.cn/video/api/createVideo"
	acfunQiniuTokenURL     = "https://member.acfun.cn/common/api/getQiniuToken"
	acfunUploadedURL       = "https://member.acfun.cn/common/api/getUrlAfterUpload"
	acfunCreateDougaURL    = "https://member.acfun.cn/video/api/createDouga"
	acfunCreatorRefererURL = "https://member.acfun.cn/platform/upload-video"
)

type acfunEndpoints struct {
	channels     string
	cloudToken   string
	fragment     string
	complete     string
	uploadFinish string
	createVideo  string
	qiniuToken   string
	uploadedURL  string
	createDouga  string
	referer      string
}

func productionAcFunEndpoints() acfunEndpoints {
	return acfunEndpoints{
		channels:     acfunChannelsURL,
		cloudToken:   acfunCloudTokenURL,
		fragment:     acfunFragmentURL,
		complete:     acfunCompleteURL,
		uploadFinish: acfunUploadFinishURL,
		createVideo:  acfunCreateVideoURL,
		qiniuToken:   acfunQiniuTokenURL,
		uploadedURL:  acfunUploadedURL,
		createDouga:  acfunCreateDougaURL,
		referer:      acfunCreatorRefererURL,
	}
}

// AcFunWebAdapter is an independently implemented adapter for AcFun's
// creator-center web workflow. It never attempts to bypass captcha or
// anti-abuse challenges. A platform response that cannot prove whether the
// final createDouga request succeeded is deliberately marked uncertain so the
// worker will reconcile instead of blindly submitting a duplicate.
type AcFunWebAdapter struct {
	client    *http.Client
	endpoints acfunEndpoints
	now       func() time.Time
}

func NewAcFunWebAdapter() *AcFunWebAdapter {
	return newAcFunWebAdapter(
		&http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          20,
				MaxIdleConnsPerHost:   10,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
			},
		},
		productionAcFunEndpoints(),
	)
}

func newAcFunWebAdapter(
	client *http.Client,
	endpoints acfunEndpoints,
) *AcFunWebAdapter {
	return &AcFunWebAdapter{
		client:    client,
		endpoints: endpoints,
		now:       time.Now,
	}
}

func (a *AcFunWebAdapter) Platform() string {
	return PlatformAcFun
}

func (a *AcFunWebAdapter) AuthMode() string {
	return "cookie"
}

func (a *AcFunWebAdapter) Version() string {
	return "acfun-web-v1"
}

func (a *AcFunWebAdapter) CheckAccount(
	ctx context.Context,
	jar []byte,
) (AccountIdentity, error) {
	cookies, err := cookiesForURL(jar, a.endpoints.channels, a.now())
	if err != nil {
		return AccountIdentity{}, err
	}
	if cookies.Header == "" {
		return AccountIdentity{}, &AdapterError{
			Code:      "acfun_cookie_missing",
			Message:   "AcFun Cookie 中没有适用于创作中心的登录信息",
			Retryable: false,
		}
	}
	response, err := a.memberJSON(
		ctx,
		http.MethodGet,
		a.endpoints.channels,
		cookies.Header,
		nil,
	)
	if err != nil {
		return AccountIdentity{}, err
	}
	if code, ok := acfunResultCode(response); !ok || code != 0 {
		return AccountIdentity{}, &AdapterError{
			Code:      "acfun_login_invalid",
			Message:   "AcFun 登录态无效：" + acfunResponseMessage(response),
			Retryable: false,
		}
	}
	remoteID := firstRecursiveScalar(
		response,
		"userId",
		"user_id",
		"uid",
		"userid",
	)
	displayName := firstRecursiveScalar(
		response,
		"userName",
		"username",
		"nickname",
	)
	if remoteID == "" {
		remoteID = acfunCookieIdentity(cookies.Values)
	}
	if displayName == "" {
		displayName = "AcFun 已登录账号"
	}
	return AccountIdentity{
		RemoteUserID:      remoteID,
		RemoteDisplayName: displayName,
	}, nil
}

func (a *AcFunWebAdapter) Categories(
	ctx context.Context,
	jar []byte,
) ([]Category, error) {
	cookies, err := cookiesForURL(jar, a.endpoints.channels, a.now())
	if err != nil {
		return nil, err
	}
	response, err := a.memberJSON(
		ctx,
		http.MethodGet,
		a.endpoints.channels,
		cookies.Header,
		nil,
	)
	if err != nil {
		return nil, err
	}
	if code, ok := acfunResultCode(response); !ok || code != 0 {
		return nil, &AdapterError{
			Code:      "acfun_categories_rejected",
			Message:   "AcFun 分区读取失败：" + acfunResponseMessage(response),
			Retryable: code != -401 && code != -403,
		}
	}
	items := decodeAcFunCategories(response)
	if len(items) == 0 {
		return nil, &AdapterError{
			Code:      "acfun_categories_empty",
			Message:   "AcFun 创作中心没有返回可用投稿分区",
			Retryable: true,
		}
	}
	return items, nil
}

func (a *AcFunWebAdapter) Publish(
	ctx context.Context,
	request UploadRequest,
) (PublishResult, error) {
	cookies, err := cookiesForURL(
		request.CookieJar,
		a.endpoints.createDouga,
		a.now(),
	)
	if err != nil {
		return PublishResult{}, err
	}
	if cookies.Header == "" {
		return PublishResult{}, &AdapterError{
			Code:      "acfun_cookie_missing",
			Message:   "AcFun Cookie 中没有适用于创作中心的登录信息",
			Retryable: false,
		}
	}
	if strings.TrimSpace(request.Publication.CategoryID) == "" {
		return PublishResult{}, &AdapterError{
			Code:      "acfun_category_missing",
			Message:   "AcFun 投稿必须选择有效分区",
			Retryable: false,
		}
	}
	if strings.TrimSpace(request.CoverPath) == "" {
		return PublishResult{}, &AdapterError{
			Code:      "acfun_cover_required",
			Message:   "AcFun 投稿需要封面，请先完成封面处理",
			Retryable: false,
		}
	}
	mediaInfo, err := validUploadFile(request.MediaPath, "AcFun 媒体")
	if err != nil {
		return PublishResult{}, err
	}
	coverInfo, err := validUploadFile(request.CoverPath, "AcFun 封面")
	if err != nil {
		return PublishResult{}, err
	}

	mediaToken, err := a.createMediaUpload(
		ctx,
		cookies.Header,
		filepath.Base(request.MediaPath),
		mediaInfo.Size(),
	)
	if err != nil {
		return PublishResult{}, err
	}
	if err := a.uploadFragmented(
		ctx,
		mediaToken.Token,
		request.MediaPath,
		mediaInfo.Size(),
		mediaToken.PartSize,
	); err != nil {
		return PublishResult{}, err
	}
	if err := a.finishMediaUpload(
		ctx,
		cookies.Header,
		mediaToken.TaskID,
	); err != nil {
		return PublishResult{}, err
	}
	videoID, err := a.createVideo(
		ctx,
		cookies.Header,
		mediaToken.TaskID,
		filepath.Base(request.MediaPath),
	)
	if err != nil {
		return PublishResult{}, err
	}
	coverURL, err := a.uploadCover(
		ctx,
		cookies.Header,
		request.CoverPath,
		coverInfo.Size(),
	)
	if err != nil {
		return PublishResult{}, err
	}
	if request.OnStage != nil {
		if err := request.OnStage(ctx, "submitting"); err != nil {
			return PublishResult{}, err
		}
	}
	return a.createDouga(
		ctx,
		cookies.Header,
		videoID,
		coverURL,
		request,
	)
}

func (a *AcFunWebAdapter) Reconcile(
	_ context.Context,
	request ReconcileRequest,
) (PublishResult, bool, error) {
	// When createDouga returned a concrete ID, the publication is already
	// proven. AcFun's creator web workflow does not expose a stable idempotency
	// key or a documented exact-match lookup, so an unknown final response is
	// intentionally left for operator reconciliation instead of risking a
	// duplicate submission.
	if request.Publication.RemoteSubmissionID == "" {
		return PublishResult{}, false, nil
	}
	remoteID := request.Publication.RemoteSubmissionID
	remoteURL := request.Publication.RemoteURL
	if remoteURL == "" {
		remoteURL = "https://www.acfun.cn/v/ac" + remoteID
	}
	return PublishResult{
		RemoteSubmissionID: remoteID,
		RemoteURL:          remoteURL,
		RemoteStatus:       "submitted",
		ResponseSummary: map[string]any{
			"reconciled": true,
		},
	}, true, nil
}

type acfunUploadToken struct {
	TaskID   string
	Token    string
	PartSize int64
}

func (a *AcFunWebAdapter) createMediaUpload(
	ctx context.Context,
	cookieHeader string,
	filename string,
	size int64,
) (acfunUploadToken, error) {
	form := url.Values{}
	form.Set("fileName", filename)
	form.Set("size", strconv.FormatInt(size, 10))
	form.Set("template", "1")
	response, err := a.memberJSON(
		ctx,
		http.MethodPost,
		a.endpoints.cloudToken,
		cookieHeader,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return acfunUploadToken{}, err
	}
	if code, ok := acfunResultCode(response); !ok || code != 0 {
		return acfunUploadToken{}, &AdapterError{
			Code:      "acfun_upload_token_rejected",
			Message:   "AcFun 未签发媒体上传凭证：" + acfunResponseMessage(response),
			Retryable: true,
		}
	}
	taskID := firstRecursiveScalar(response, "taskId", "task_id")
	token := firstRecursiveScalar(response, "token", "uploadToken", "upload_token")
	partSize := firstRecursiveInt64(response, "partSize", "part_size")
	if taskID == "" || token == "" {
		return acfunUploadToken{}, &AdapterError{
			Code:      "acfun_upload_token_invalid",
			Message:   "AcFun 返回的媒体上传凭证不完整",
			Retryable: true,
		}
	}
	if partSize <= 0 || partSize > 64<<20 {
		partSize = 4 << 20
	}
	return acfunUploadToken{
		TaskID:   taskID,
		Token:    token,
		PartSize: partSize,
	}, nil
}

func (a *AcFunWebAdapter) uploadFragmented(
	ctx context.Context,
	token string,
	path string,
	size int64,
	partSize int64,
) error {
	if partSize <= 0 || partSize > 64<<20 {
		return &AdapterError{
			Code:      "acfun_fragment_size_invalid",
			Message:   "AcFun 返回了无效的分片大小",
			Retryable: true,
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return &AdapterError{
			Code:      "acfun_upload_file_unavailable",
			Message:   "无法打开 AcFun 待上传文件",
			Retryable: true,
		}
	}
	defer file.Close()
	partCount := int(math.Ceil(float64(size) / float64(partSize)))
	for index := 0; index < partCount; index++ {
		offset := int64(index) * partSize
		length := partSize
		if remaining := size - offset; remaining < length {
			length = remaining
		}
		endpoint, err := url.Parse(a.endpoints.fragment)
		if err != nil {
			return fmt.Errorf("parse AcFun fragment endpoint: %w", err)
		}
		query := endpoint.Query()
		query.Set("fragment_id", strconv.Itoa(index))
		query.Set("upload_token", token)
		endpoint.RawQuery = query.Encode()
		httpRequest, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			endpoint.String(),
			io.NewSectionReader(file, offset, length),
		)
		if err != nil {
			return fmt.Errorf("create AcFun fragment request: %w", err)
		}
		a.applyUploadHeaders(httpRequest)
		httpRequest.Header.Set("Content-Type", "application/octet-stream")
		httpRequest.ContentLength = length
		response, err := a.client.Do(httpRequest)
		if err != nil {
			return &AdapterError{
				Code:      "acfun_fragment_upload_failed",
				Message:   "AcFun 分片上传网络失败",
				Retryable: true,
			}
		}
		payload, readErr := readJSONResponse(response, 1<<20)
		if readErr != nil {
			return acfunHTTPAdapterError(
				"acfun_fragment_upload",
				"AcFun 分片上传",
				response.StatusCode,
				readErr,
			)
		}
		if code, ok := acfunResultCode(payload); !ok || code != 1 {
			return &AdapterError{
				Code:      "acfun_fragment_upload_rejected",
				Message:   "AcFun 拒绝媒体分片：" + acfunResponseMessage(payload),
				Retryable: true,
			}
		}
	}
	endpoint, err := url.Parse(a.endpoints.complete)
	if err != nil {
		return fmt.Errorf("parse AcFun completion endpoint: %w", err)
	}
	query := endpoint.Query()
	query.Set("fragment_count", strconv.Itoa(partCount))
	query.Set("upload_token", token)
	endpoint.RawQuery = query.Encode()
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint.String(),
		http.NoBody,
	)
	if err != nil {
		return fmt.Errorf("create AcFun completion request: %w", err)
	}
	a.applyUploadHeaders(httpRequest)
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return &AdapterError{
			Code:      "acfun_upload_completion_failed",
			Message:   "AcFun 合并分片网络失败",
			Retryable: true,
		}
	}
	payload, readErr := readJSONResponse(response, 1<<20)
	if readErr != nil {
		return acfunHTTPAdapterError(
			"acfun_upload_completion",
			"AcFun 合并分片",
			response.StatusCode,
			readErr,
		)
	}
	if code, ok := acfunResultCode(payload); !ok || code != 1 {
		return &AdapterError{
			Code:      "acfun_upload_completion_rejected",
			Message:   "AcFun 未确认分片合并：" + acfunResponseMessage(payload),
			Retryable: true,
		}
	}
	return nil
}

func (a *AcFunWebAdapter) finishMediaUpload(
	ctx context.Context,
	cookieHeader string,
	taskID string,
) error {
	form := url.Values{}
	form.Set("taskId", taskID)
	response, err := a.memberJSON(
		ctx,
		http.MethodPost,
		a.endpoints.uploadFinish,
		cookieHeader,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return err
	}
	if code, ok := acfunResultCode(response); !ok || code != 0 {
		return &AdapterError{
			Code:      "acfun_upload_finish_rejected",
			Message:   "AcFun 未确认媒体上传完成：" + acfunResponseMessage(response),
			Retryable: true,
		}
	}
	return nil
}

func (a *AcFunWebAdapter) createVideo(
	ctx context.Context,
	cookieHeader string,
	taskID string,
	filename string,
) (string, error) {
	form := url.Values{}
	form.Set("videoKey", taskID)
	form.Set("fileName", filename)
	form.Set("vodType", "ksCloud")
	response, err := a.memberJSON(
		ctx,
		http.MethodPost,
		a.endpoints.createVideo,
		cookieHeader,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", err
	}
	if code, ok := acfunResultCode(response); !ok || code != 0 {
		return "", &AdapterError{
			Code:      "acfun_video_create_rejected",
			Message:   "AcFun 媒体登记失败：" + acfunResponseMessage(response),
			Retryable: true,
		}
	}
	videoID := firstRecursiveScalar(response, "videoId", "video_id")
	if videoID == "" {
		return "", &AdapterError{
			Code:      "acfun_video_id_missing",
			Message:   "AcFun 未返回媒体 ID",
			Retryable: true,
		}
	}
	return videoID, nil
}

func (a *AcFunWebAdapter) uploadCover(
	ctx context.Context,
	cookieHeader string,
	path string,
	size int64,
) (string, error) {
	form := url.Values{}
	form.Set("fileName", filepath.Base(path))
	response, err := a.memberJSON(
		ctx,
		http.MethodPost,
		a.endpoints.qiniuToken,
		cookieHeader,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", err
	}
	if code, ok := acfunResultCode(response); !ok || code != 0 {
		return "", &AdapterError{
			Code:      "acfun_cover_token_rejected",
			Message:   "AcFun 未签发封面上传凭证：" + acfunResponseMessage(response),
			Retryable: true,
		}
	}
	token := firstRecursiveScalar(response, "token", "uploadToken", "upload_token")
	if token == "" {
		return "", &AdapterError{
			Code:      "acfun_cover_token_invalid",
			Message:   "AcFun 返回的封面上传凭证不完整",
			Retryable: true,
		}
	}
	partSize := size
	if partSize <= 0 || partSize > 8<<20 {
		partSize = 4 << 20
	}
	if err := a.uploadFragmented(ctx, token, path, size, partSize); err != nil {
		return "", err
	}
	form = url.Values{}
	form.Set("bizFlag", "web-douga-cover")
	form.Set("token", token)
	response, err = a.memberJSON(
		ctx,
		http.MethodPost,
		a.endpoints.uploadedURL,
		cookieHeader,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", err
	}
	if code, ok := acfunResultCode(response); !ok || code != 0 {
		return "", &AdapterError{
			Code:      "acfun_cover_url_rejected",
			Message:   "AcFun 未返回封面地址：" + acfunResponseMessage(response),
			Retryable: true,
		}
	}
	coverURL := firstRecursiveScalar(response, "url", "coverUrl", "cover_url")
	if coverURL == "" {
		return "", &AdapterError{
			Code:      "acfun_cover_url_missing",
			Message:   "AcFun 返回的封面地址为空",
			Retryable: true,
		}
	}
	return coverURL, nil
}

func (a *AcFunWebAdapter) createDouga(
	ctx context.Context,
	cookieHeader string,
	videoID string,
	coverURL string,
	request UploadRequest,
) (PublishResult, error) {
	tagsJSON, err := json.Marshal(request.Publication.Tags)
	if err != nil {
		return PublishResult{}, fmt.Errorf("encode AcFun tags: %w", err)
	}
	videoInfosJSON, err := json.Marshal([]map[string]string{{
		"videoId": videoID,
		"title":   request.Publication.Title,
	}})
	if err != nil {
		return PublishResult{}, fmt.Errorf("encode AcFun video info: %w", err)
	}
	form := url.Values{}
	form.Set("title", request.Publication.Title)
	form.Set("description", request.Publication.Description)
	form.Set("tagNames", string(tagsJSON))
	form.Set("channelId", request.Publication.CategoryID)
	form.Set("coverUrl", coverURL)
	form.Set("videoInfos", string(videoInfosJSON))
	form.Set("isJoinUpCollege", "0")
	form.Set("isSyncKs", "0")
	if strings.TrimSpace(request.SourceURL) != "" {
		form.Set("creationType", "1")
		form.Set("originalLinkUrl", request.SourceURL)
		form.Set("originalDeclare", "0")
	} else {
		form.Set("creationType", "2")
	}

	response, err := a.memberJSON(
		ctx,
		http.MethodPost,
		a.endpoints.createDouga,
		cookieHeader,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return PublishResult{}, &AdapterError{
			Code:      "acfun_submission_uncertain",
			Message:   "AcFun 投稿请求未获得确定结果，将进入人工对账",
			Retryable: true,
			Uncertain: true,
		}
	}
	if code, ok := acfunResultCode(response); !ok || code != 0 {
		return PublishResult{}, &AdapterError{
			Code:      "acfun_submission_rejected",
			Message:   "AcFun 拒绝投稿：" + acfunResponseMessage(response),
			Retryable: acfunCodeRetryable(code),
		}
	}
	dougaID := firstRecursiveScalar(response, "dougaId", "douga_id")
	if dougaID == "" {
		return PublishResult{}, &AdapterError{
			Code:      "acfun_submission_id_missing",
			Message:   "AcFun 已接收请求但未返回稿件 ID，将进入人工对账",
			Retryable: true,
			Uncertain: true,
		}
	}
	return PublishResult{
		RemoteSubmissionID: dougaID,
		RemoteURL:          "https://www.acfun.cn/v/ac" + dougaID,
		RemoteStatus:       "submitted",
		ResponseSummary: map[string]any{
			"result":   0,
			"douga_id": dougaID,
		},
	}, nil
}

func (a *AcFunWebAdapter) memberJSON(
	ctx context.Context,
	method string,
	target string,
	cookieHeader string,
	body io.Reader,
) (map[string]any, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("create AcFun request: %w", err)
	}
	a.applyMemberHeaders(httpRequest, cookieHeader)
	if body != nil {
		httpRequest.Header.Set(
			"Content-Type",
			"application/x-www-form-urlencoded; charset=UTF-8",
		)
	}
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return nil, &AdapterError{
			Code:      "acfun_request_failed",
			Message:   "AcFun 创作中心请求失败",
			Retryable: true,
		}
	}
	payload, readErr := readJSONResponse(response, 2<<20)
	if readErr != nil {
		return nil, acfunHTTPAdapterError(
			"acfun_http",
			"AcFun 创作中心",
			response.StatusCode,
			readErr,
		)
	}
	return payload, nil
}

func (a *AcFunWebAdapter) applyMemberHeaders(
	request *http.Request,
	cookieHeader string,
) {
	request.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
			"AppleWebKit/537.36 (KHTML, like Gecko) "+
			"Chrome/138.0.0.0 Safari/537.36",
	)
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Origin", "https://member.acfun.cn")
	request.Header.Set("Referer", a.endpoints.referer)
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	if cookieHeader != "" {
		request.Header.Set("Cookie", cookieHeader)
	}
}

func (a *AcFunWebAdapter) applyUploadHeaders(request *http.Request) {
	request.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
			"AppleWebKit/537.36 (KHTML, like Gecko) "+
			"Chrome/138.0.0.0 Safari/537.36",
	)
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Origin", "https://member.acfun.cn")
	request.Header.Set("Referer", a.endpoints.referer)
}

func readJSONResponse(
	response *http.Response,
	limit int64,
) (map[string]any, error) {
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, limit))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func acfunHTTPAdapterError(
	codePrefix string,
	operation string,
	status int,
	cause error,
) error {
	retryable := status == 0 || status == http.StatusTooManyRequests || status >= 500
	message := operation + "返回无效响应"
	if status > 0 {
		message = fmt.Sprintf("%s返回 HTTP %d", operation, status)
	}
	return &AdapterError{
		Code:      codePrefix + "_error",
		Message:   message,
		Retryable: retryable,
	}
}

func validUploadFile(path string, label string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, &AdapterError{
			Code:      "acfun_file_unavailable",
			Message:   label + "文件不可用",
			Retryable: true,
		}
	}
	if info.Size() <= 0 {
		return nil, &AdapterError{
			Code:      "acfun_file_empty",
			Message:   label + "文件为空",
			Retryable: false,
		}
	}
	return info, nil
}

func acfunResultCode(values map[string]any) (int, bool) {
	value, exists := values["result"]
	if !exists {
		return 0, false
	}
	text := scalarString(value)
	if text == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(text)
	return parsed, err == nil
}

func acfunResponseMessage(values map[string]any) string {
	message := firstRecursiveScalar(
		values,
		"error_msg",
		"errorMessage",
		"message",
		"msg",
	)
	return safePlatformMessage(message)
}

func acfunCodeRetryable(code int) bool {
	switch code {
	case -401, -403, 401, 403:
		return false
	default:
		return true
	}
}

func acfunCookieIdentity(values map[string]string) string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		_, _ = io.WriteString(hash, name)
		_, _ = io.WriteString(hash, "\x00")
		_, _ = io.WriteString(hash, values[name])
		_, _ = io.WriteString(hash, "\x00")
	}
	return "cookie-" + hex.EncodeToString(hash.Sum(nil))[:16]
}

func firstRecursiveScalar(value any, keys ...string) string {
	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keySet[strings.ToLower(key)] = struct{}{}
	}
	var walk func(any) string
	walk = func(node any) string {
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				if _, exists := keySet[strings.ToLower(key)]; exists {
					if result := scalarString(child); result != "" {
						return result
					}
				}
			}
			for _, child := range typed {
				if result := walk(child); result != "" {
					return result
				}
			}
		case []any:
			for _, child := range typed {
				if result := walk(child); result != "" {
					return result
				}
			}
		}
		return ""
	}
	return walk(value)
}

func firstRecursiveInt64(value any, keys ...string) int64 {
	text := firstRecursiveScalar(value, keys...)
	result, _ := strconv.ParseInt(text, 10, 64)
	return result
}

func decodeAcFunCategories(value any) []Category {
	result := make([]Category, 0)
	seen := map[string]struct{}{}
	var walk func(any, string, string)
	walk = func(node any, parentID string, parentPath string) {
		switch typed := node.(type) {
		case []any:
			for _, child := range typed {
				walk(child, parentID, parentPath)
			}
		case map[string]any:
			id := scalarString(
				firstValue(
					typed,
					"channelId",
					"channelID",
					"channel_id",
					"id",
				),
			)
			name := scalarString(
				firstValue(
					typed,
					"channelName",
					"channel_name",
					"name",
				),
			)
			currentParent := parentID
			currentPath := parentPath
			if id != "" && name != "" {
				if explicit := scalarString(
					firstValue(
						typed,
						"parentChannelId",
						"parentChannelID",
						"parent_id",
						"parentId",
					),
				); explicit != "" {
					currentParent = explicit
				}
				currentPath = name
				if parentPath != "" {
					currentPath = parentPath + " / " + name
				}
				if _, exists := seen[id]; !exists {
					seen[id] = struct{}{}
					result = append(result, Category{
						CategoryID: id,
						ParentID:   currentParent,
						Name:       name,
						Path:       currentPath,
						Active:     true,
						SortOrder:  len(result) * 10,
						Metadata: map[string]any{
							"source": "getMyChannels",
						},
					})
				}
				currentParent = id
			}
			knownChildren := false
			for _, key := range []string{
				"children",
				"childChannels",
				"childChannelList",
				"subChannels",
				"channels",
				"channelList",
				"list",
				"data",
				"info",
			} {
				if child, exists := typed[key]; exists {
					knownChildren = true
					walk(child, currentParent, currentPath)
				}
			}
			if !knownChildren {
				for key, child := range typed {
					switch key {
					case "result", "message", "msg", "error_msg":
						continue
					default:
						walk(child, currentParent, currentPath)
					}
				}
			}
		}
	}
	walk(value, "", "")
	return result
}

var _ PlatformGateway = (*AcFunWebAdapter)(nil)
var _ PlatformPublisher = (*AcFunWebAdapter)(nil)
