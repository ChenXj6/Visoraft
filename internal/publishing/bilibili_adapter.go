package publishing

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	bilibiliNavURL       = "https://api.bilibili.com/x/web-interface/nav"
	bilibiliCategoryURL  = "https://member.bilibili.com/x/vupre/web/archive/pre?lang=zh-CN"
	bilibiliPreuploadURL = "https://member.bilibili.com/preupload"
	bilibiliCoverURL     = "https://member.bilibili.com/x/vu/web/cover/up"
	bilibiliSubmitURL    = "https://member.bilibili.com/x/vu/web/add/v3"
	bilibiliArchivesURL  = "https://member.bilibili.com/x/web/archives"
)

type BilibiliWebAdapter struct {
	client *http.Client
	now    func() time.Time
}

func NewBilibiliWebAdapter() *BilibiliWebAdapter {
	return &BilibiliWebAdapter{
		client: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          20,
				MaxIdleConnsPerHost:   10,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
			},
		},
		now: time.Now,
	}
}

func (a *BilibiliWebAdapter) Platform() string {
	return PlatformBilibili
}

func (a *BilibiliWebAdapter) AuthMode() string {
	return "cookie"
}

func (a *BilibiliWebAdapter) Version() string {
	return "bilibili-web-v1"
}

func (a *BilibiliWebAdapter) CheckAccount(
	ctx context.Context,
	jar []byte,
) (AccountIdentity, error) {
	cookies, err := cookiesForURL(jar, bilibiliNavURL, a.now())
	if err != nil {
		return AccountIdentity{}, err
	}
	if cookies.Header == "" {
		return AccountIdentity{}, errors.New("Cookie 中没有 bilibili 登录信息")
	}
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			IsLogin bool        `json:"isLogin"`
			Mid     json.Number `json:"mid"`
			Uname   string      `json:"uname"`
		} `json:"data"`
	}
	if err := a.doJSON(
		ctx,
		http.MethodGet,
		bilibiliNavURL,
		cookies.Header,
		"",
		nil,
		&response,
	); err != nil {
		return AccountIdentity{}, err
	}
	if response.Code != 0 || !response.Data.IsLogin {
		return AccountIdentity{}, fmt.Errorf(
			"bilibili 登录态无效：%s",
			safePlatformMessage(response.Message),
		)
	}
	return AccountIdentity{
		RemoteUserID:      response.Data.Mid.String(),
		RemoteDisplayName: response.Data.Uname,
	}, nil
}

func (a *BilibiliWebAdapter) Categories(
	ctx context.Context,
	jar []byte,
) ([]Category, error) {
	cookies, err := cookiesForURL(jar, bilibiliCategoryURL, a.now())
	if err != nil {
		return nil, err
	}
	var response struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := a.doJSON(
		ctx,
		http.MethodGet,
		bilibiliCategoryURL,
		cookies.Header,
		"https://member.bilibili.com/platform/upload/video/frame",
		nil,
		&response,
	); err != nil {
		return nil, err
	}
	if response.Code != 0 {
		return nil, &AdapterError{
			Code:      "bilibili_categories_rejected",
			Message:   "bilibili 分区读取失败：" + safePlatformMessage(response.Message),
			Retryable: response.Code != -101 && response.Code != -111,
		}
	}
	items, err := decodeBilibiliCategories(response.Data)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, &AdapterError{
			Code:      "bilibili_categories_empty",
			Message:   "bilibili 未返回可用投稿分区",
			Retryable: true,
		}
	}
	return items, nil
}

func (a *BilibiliWebAdapter) Publish(
	ctx context.Context,
	request UploadRequest,
) (PublishResult, error) {
	cookies, err := cookiesForURL(request.CookieJar, bilibiliSubmitURL, a.now())
	if err != nil {
		return PublishResult{}, err
	}
	csrf := strings.TrimSpace(cookies.Values["bili_jct"])
	if cookies.Header == "" || csrf == "" {
		return PublishResult{}, &AdapterError{
			Code:      "bilibili_csrf_missing",
			Message:   "bilibili Cookie 缺少登录态或 bili_jct",
			Retryable: false,
		}
	}
	if request.CoverPath == "" {
		return PublishResult{}, &AdapterError{
			Code:      "bilibili_cover_required",
			Message:   "bilibili 投稿需要封面，请先完成封面处理",
			Retryable: false,
		}
	}
	fileInfo, err := os.Stat(request.MediaPath)
	if err != nil {
		return PublishResult{}, &AdapterError{
			Code:      "bilibili_media_unavailable",
			Message:   "无法读取待投稿媒体文件",
			Retryable: true,
		}
	}
	preupload, err := a.preupload(
		ctx,
		cookies.Header,
		filepath.Base(request.MediaPath),
		fileInfo.Size(),
	)
	if err != nil {
		return PublishResult{}, err
	}
	remoteFilename, err := a.uploadUPOS(
		ctx,
		cookies.Header,
		preupload,
		request.MediaPath,
		fileInfo.Size(),
	)
	if err != nil {
		return PublishResult{}, err
	}
	coverURL, err := a.uploadCover(
		ctx,
		cookies.Header,
		csrf,
		request.CoverPath,
	)
	if err != nil {
		return PublishResult{}, err
	}
	if request.OnStage != nil {
		if err := request.OnStage(ctx, "submitting"); err != nil {
			return PublishResult{}, err
		}
	}
	return a.submit(
		ctx,
		cookies.Header,
		csrf,
		remoteFilename,
		coverURL,
		request,
	)
}

func (a *BilibiliWebAdapter) Reconcile(
	ctx context.Context,
	request ReconcileRequest,
) (PublishResult, bool, error) {
	cookies, err := cookiesForURL(request.CookieJar, bilibiliArchivesURL, a.now())
	if err != nil {
		return PublishResult{}, false, err
	}
	endpoint, _ := url.Parse(bilibiliArchivesURL)
	query := endpoint.Query()
	query.Set("status", "pubed,pubing,not_pubed")
	query.Set("pn", "1")
	query.Set("ps", "20")
	query.Set("coop", "1")
	query.Set("interactive", "1")
	query.Set("keyword", request.Publication.Title)
	endpoint.RawQuery = query.Encode()
	var response struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := a.doJSON(
		ctx,
		http.MethodGet,
		endpoint.String(),
		cookies.Header,
		"https://member.bilibili.com/platform/upload-manager/article",
		nil,
		&response,
	); err != nil {
		return PublishResult{}, false, err
	}
	if response.Code != 0 {
		return PublishResult{}, false, &AdapterError{
			Code:      "bilibili_reconcile_rejected",
			Message:   "bilibili 对账查询失败：" + safePlatformMessage(response.Message),
			Retryable: true,
		}
	}
	candidates, err := decodeBilibiliArchives(response.Data)
	if err != nil {
		return PublishResult{}, false, err
	}
	matches := make([]bilibiliArchive, 0)
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Title) != strings.TrimSpace(request.Publication.Title) {
			continue
		}
		if request.SourceURL != "" &&
			candidate.Description != "" &&
			!strings.Contains(candidate.Description, request.SourceURL) {
			continue
		}
		matches = append(matches, candidate)
	}
	if len(matches) != 1 {
		return PublishResult{}, false, nil
	}
	item := matches[0]
	remoteID := item.BVID
	remoteURL := ""
	if item.BVID != "" {
		remoteURL = "https://www.bilibili.com/video/" + item.BVID
	} else if item.AID != "" {
		remoteID = item.AID
		remoteURL = "https://www.bilibili.com/video/av" + item.AID
	}
	if remoteID == "" {
		return PublishResult{}, false, nil
	}
	return PublishResult{
		RemoteSubmissionID: remoteID,
		RemoteURL:          remoteURL,
		RemoteStatus:       item.Status,
		ResponseSummary: map[string]any{
			"reconciled": true,
			"state":      item.Status,
		},
	}, true, nil
}

type bilibiliPreupload struct {
	OK        int    `json:"OK"`
	Auth      string `json:"auth"`
	BizID     int64  `json:"biz_id"`
	ChunkSize int64  `json:"chunk_size"`
	Endpoint  string `json:"endpoint"`
	UposURI   string `json:"upos_uri"`
}

func (a *BilibiliWebAdapter) preupload(
	ctx context.Context,
	cookieHeader string,
	filename string,
	size int64,
) (bilibiliPreupload, error) {
	endpoint, _ := url.Parse(bilibiliPreuploadURL)
	query := endpoint.Query()
	query.Set("name", filename)
	query.Set("size", strconv.FormatInt(size, 10))
	query.Set("r", "upos")
	query.Set("profile", "ugcupos/bup")
	query.Set("ssl", "0")
	query.Set("version", "2.10.4.0")
	query.Set("build", "2100400")
	query.Set("probe_version", "20221109")
	endpoint.RawQuery = query.Encode()
	var result bilibiliPreupload
	if err := a.doJSON(
		ctx,
		http.MethodGet,
		endpoint.String(),
		cookieHeader,
		"https://member.bilibili.com/platform/upload/video/frame",
		nil,
		&result,
	); err != nil {
		return bilibiliPreupload{}, err
	}
	if result.OK != 1 ||
		result.Auth == "" ||
		result.Endpoint == "" ||
		result.UposURI == "" {
		return bilibiliPreupload{}, &AdapterError{
			Code:      "bilibili_preupload_rejected",
			Message:   "bilibili 未返回有效上传凭据",
			Retryable: true,
		}
	}
	if result.ChunkSize <= 0 || result.ChunkSize > 64<<20 {
		result.ChunkSize = 4 << 20
	}
	return result, nil
}

type bilibiliUploadSession struct {
	UploadID string `json:"upload_id"`
}

func (a *BilibiliWebAdapter) uploadUPOS(
	ctx context.Context,
	cookieHeader string,
	preupload bilibiliPreupload,
	path string,
	size int64,
) (string, error) {
	uploadURL, err := bilibiliUploadURL(preupload)
	if err != nil {
		return "", err
	}
	initializeURL := addRawQuery(uploadURL, "uploads&output=json")
	var session bilibiliUploadSession
	if err := a.doJSON(
		ctx,
		http.MethodPost,
		initializeURL,
		cookieHeader,
		"https://member.bilibili.com/platform/upload/video/frame",
		nil,
		&session,
		map[string]string{"X-Upos-Auth": preupload.Auth},
	); err != nil {
		return "", err
	}
	if session.UploadID == "" {
		return "", &AdapterError{
			Code:      "bilibili_upload_session_missing",
			Message:   "bilibili 未返回分片上传会话",
			Retryable: true,
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open bilibili upload file: %w", err)
	}
	defer file.Close()
	chunkSize := preupload.ChunkSize
	chunks := int(math.Ceil(float64(size) / float64(chunkSize)))
	parts := make([]map[string]any, 0, chunks)
	for index := 0; index < chunks; index++ {
		start := int64(index) * chunkSize
		length := chunkSize
		if remaining := size - start; remaining < length {
			length = remaining
		}
		partNumber := index + 1
		chunkURL, _ := url.Parse(uploadURL)
		query := chunkURL.Query()
		query.Set("partNumber", strconv.Itoa(partNumber))
		query.Set("uploadId", session.UploadID)
		query.Set("chunk", strconv.Itoa(index))
		query.Set("chunks", strconv.Itoa(chunks))
		query.Set("size", strconv.FormatInt(length, 10))
		query.Set("start", strconv.FormatInt(start, 10))
		query.Set("end", strconv.FormatInt(start+length, 10))
		query.Set("total", strconv.FormatInt(size, 10))
		chunkURL.RawQuery = query.Encode()
		httpRequest, err := http.NewRequestWithContext(
			ctx,
			http.MethodPut,
			chunkURL.String(),
			io.NewSectionReader(file, start, length),
		)
		if err != nil {
			return "", fmt.Errorf("create bilibili chunk request: %w", err)
		}
		a.applyHeaders(
			httpRequest,
			cookieHeader,
			"https://member.bilibili.com/platform/upload/video/frame",
		)
		httpRequest.Header.Set("X-Upos-Auth", preupload.Auth)
		httpRequest.Header.Set("Content-Type", "application/octet-stream")
		httpRequest.ContentLength = length
		response, err := a.client.Do(httpRequest)
		if err != nil {
			return "", &AdapterError{
				Code:      "bilibili_chunk_upload_failed",
				Message:   "bilibili 分片上传网络失败",
				Retryable: true,
			}
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return "", &AdapterError{
				Code:      "bilibili_chunk_upload_rejected",
				Message:   fmt.Sprintf("bilibili 分片上传返回 HTTP %d", response.StatusCode),
				Retryable: response.StatusCode >= 500 || response.StatusCode == 429,
			}
		}
		parts = append(parts, map[string]any{
			"partNumber": partNumber,
			"eTag":       strings.Trim(response.Header.Get("ETag"), "\""),
		})
	}
	completeURL, _ := url.Parse(uploadURL)
	query := completeURL.Query()
	query.Set("output", "json")
	query.Set("name", filepath.Base(path))
	query.Set("profile", "ugcupos/bup")
	query.Set("uploadId", session.UploadID)
	query.Set("biz_id", strconv.FormatInt(preupload.BizID, 10))
	completeURL.RawQuery = query.Encode()
	body, _ := json.Marshal(map[string]any{"parts": parts})
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		completeURL.String(),
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("create bilibili upload completion request: %w", err)
	}
	a.applyHeaders(
		httpRequest,
		cookieHeader,
		"https://member.bilibili.com/platform/upload/video/frame",
	)
	httpRequest.Header.Set("X-Upos-Auth", preupload.Auth)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return "", &AdapterError{
			Code:      "bilibili_upload_completion_failed",
			Message:   "bilibili 合并分片网络失败",
			Retryable: true,
		}
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	_ = response.Body.Close()
	if readErr != nil {
		return "", fmt.Errorf("read bilibili upload completion: %w", readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", &AdapterError{
			Code:      "bilibili_upload_completion_rejected",
			Message:   fmt.Sprintf("bilibili 合并分片返回 HTTP %d", response.StatusCode),
			Retryable: response.StatusCode >= 500 || response.StatusCode == 429,
		}
	}
	var completed struct {
		OK int `json:"OK"`
	}
	if err := json.Unmarshal(responseBody, &completed); err != nil || completed.OK != 1 {
		return "", &AdapterError{
			Code:      "bilibili_upload_completion_invalid",
			Message:   "bilibili 未确认媒体上传完成",
			Retryable: true,
		}
	}
	remoteName := filepath.Base(strings.TrimPrefix(preupload.UposURI, "upos://"))
	return strings.TrimSuffix(remoteName, filepath.Ext(remoteName)), nil
}

func (a *BilibiliWebAdapter) uploadCover(
	ctx context.Context,
	cookieHeader string,
	csrf string,
	path string,
) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read bilibili cover: %w", err)
	}
	if len(raw) == 0 || len(raw) > 10<<20 {
		return "", &AdapterError{
			Code:      "bilibili_cover_invalid",
			Message:   "bilibili 封面必须大于 0 且不超过 10 MiB",
			Retryable: false,
		}
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if contentType == "" {
		contentType = http.DetectContentType(raw)
	}
	form := url.Values{}
	form.Set("csrf", csrf)
	form.Set(
		"cover",
		"data:"+contentType+";base64,"+base64.StdEncoding.EncodeToString(raw),
	)
	endpoint := bilibiliCoverURL + "?csrf=" + url.QueryEscape(csrf)
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := a.doJSON(
		ctx,
		http.MethodPost,
		endpoint,
		cookieHeader,
		"https://member.bilibili.com/platform/upload/video/frame",
		strings.NewReader(form.Encode()),
		&response,
	); err != nil {
		return "", err
	}
	if response.Code != 0 || response.Data.URL == "" {
		return "", &AdapterError{
			Code:      "bilibili_cover_upload_rejected",
			Message:   "bilibili 封面上传失败：" + safePlatformMessage(response.Message),
			Retryable: response.Code != -101 && response.Code != -111,
		}
	}
	return response.Data.URL, nil
}

func (a *BilibiliWebAdapter) submit(
	ctx context.Context,
	cookieHeader string,
	csrf string,
	remoteFilename string,
	coverURL string,
	request UploadRequest,
) (PublishResult, error) {
	categoryID, err := strconv.Atoi(request.Publication.CategoryID)
	if err != nil || categoryID <= 0 {
		return PublishResult{}, &AdapterError{
			Code:      "bilibili_category_invalid",
			Message:   "bilibili 投稿分区 ID 无效",
			Retryable: false,
		}
	}
	payload := map[string]any{
		"copyright":      2,
		"source":         request.SourceURL,
		"tid":            categoryID,
		"cover":          coverURL,
		"title":          request.Publication.Title,
		"desc_format_id": 0,
		"desc":           request.Publication.Description,
		"dynamic":        "",
		"tag":            strings.Join(request.Publication.Tags, ","),
		"subtitle": map[string]any{
			"open": 0,
			"lan":  "",
		},
		"videos": []map[string]any{{
			"filename": remoteFilename,
			"title":    request.Publication.Title,
			"desc":     "",
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return PublishResult{}, fmt.Errorf("encode bilibili submission: %w", err)
	}
	endpoint := bilibiliSubmitURL + "?csrf=" + url.QueryEscape(csrf)
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			AID  json.Number `json:"aid"`
			BVID string      `json:"bvid"`
		} `json:"data"`
	}
	err = a.doJSON(
		ctx,
		http.MethodPost,
		endpoint,
		cookieHeader,
		"https://member.bilibili.com/platform/upload/video/frame",
		bytes.NewReader(body),
		&response,
	)
	if err != nil {
		return PublishResult{}, &AdapterError{
			Code:      "bilibili_submission_uncertain",
			Message:   "bilibili 提交请求未获得确定结果，将进入对账",
			Retryable: true,
			Uncertain: true,
		}
	}
	if response.Code != 0 {
		return PublishResult{}, &AdapterError{
			Code:      "bilibili_submission_rejected",
			Message:   "bilibili 拒绝投稿：" + safePlatformMessage(response.Message),
			Retryable: bilibiliCodeRetryable(response.Code),
		}
	}
	remoteID := response.Data.BVID
	remoteURL := ""
	if remoteID != "" {
		remoteURL = "https://www.bilibili.com/video/" + remoteID
	} else if response.Data.AID.String() != "" {
		remoteID = response.Data.AID.String()
		remoteURL = "https://www.bilibili.com/video/av" + remoteID
	}
	if remoteID == "" {
		return PublishResult{}, &AdapterError{
			Code:      "bilibili_submission_id_missing",
			Message:   "bilibili 已接收请求但未返回稿件 ID，将进入对账",
			Retryable: true,
			Uncertain: true,
		}
	}
	return PublishResult{
		RemoteSubmissionID: remoteID,
		RemoteURL:          remoteURL,
		RemoteStatus:       "submitted",
		ResponseSummary: map[string]any{
			"code": response.Code,
			"bvid": response.Data.BVID,
			"aid":  response.Data.AID.String(),
		},
	}, nil
}

func (a *BilibiliWebAdapter) doJSON(
	ctx context.Context,
	method string,
	target string,
	cookieHeader string,
	referer string,
	body io.Reader,
	result any,
	extraHeaders ...map[string]string,
) error {
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return fmt.Errorf("create bilibili request: %w", err)
	}
	a.applyHeaders(request, cookieHeader, referer)
	for _, headers := range extraHeaders {
		for name, value := range headers {
			request.Header.Set(name, value)
		}
	}
	if body != nil {
		if _, ok := body.(*strings.Reader); ok {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			request.Header.Set("Content-Type", "application/json")
		}
	}
	response, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("bilibili request failed: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("read bilibili response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &AdapterError{
			Code:      "bilibili_http_error",
			Message:   fmt.Sprintf("bilibili 返回 HTTP %d", response.StatusCode),
			Retryable: response.StatusCode >= 500 || response.StatusCode == 429,
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	if err := decoder.Decode(result); err != nil {
		return fmt.Errorf("decode bilibili response: %w", err)
	}
	return nil
}

func (a *BilibiliWebAdapter) applyHeaders(
	request *http.Request,
	cookieHeader string,
	referer string,
) {
	request.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
			"AppleWebKit/537.36 (KHTML, like Gecko) "+
			"Chrome/138.0.0.0 Safari/537.36",
	)
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Origin", "https://member.bilibili.com")
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	if cookieHeader != "" {
		request.Header.Set("Cookie", cookieHeader)
	}
}

func bilibiliUploadURL(value bilibiliPreupload) (string, error) {
	endpoint := strings.TrimSpace(value.Endpoint)
	if strings.HasPrefix(endpoint, "//") {
		endpoint = "https:" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return "", errors.New("bilibili returned an invalid UPOS endpoint")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "bilivideo.com" && !strings.HasSuffix(host, ".bilivideo.com") {
		return "", errors.New("bilibili returned an untrusted UPOS endpoint")
	}
	object := strings.TrimPrefix(strings.TrimSpace(value.UposURI), "upos://")
	if object == "" || strings.Contains(object, "..") {
		return "", errors.New("bilibili returned an invalid UPOS object")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/" + strings.TrimPrefix(object, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func addRawQuery(target string, raw string) string {
	separator := "?"
	if strings.Contains(target, "?") {
		separator = "&"
	}
	return target + separator + raw
}

func bilibiliCodeRetryable(code int) bool {
	switch code {
	case -101, -111, -412, 21012, 21138:
		return false
	default:
		return true
	}
}

func safePlatformMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "平台未提供原因"
	}
	runes := []rune(value)
	if len(runes) > 300 {
		return string(runes[:300])
	}
	return value
}

func decodeBilibiliCategories(raw []byte) ([]Category, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode bilibili category tree: %w", err)
	}
	result := make([]Category, 0)
	seen := map[string]struct{}{}
	var walk func(any, string, string)
	walk = func(node any, parentID string, parentPath string) {
		switch typed := node.(type) {
		case []any:
			for _, item := range typed {
				walk(item, parentID, parentPath)
			}
		case map[string]any:
			id := scalarString(firstValue(typed, "id", "tid", "type_id"))
			name := scalarString(firstValue(typed, "name", "typename", "type_name"))
			currentParent := parentID
			currentPath := parentPath
			if id != "" && name != "" {
				if value := scalarString(firstValue(typed, "parent", "parent_id", "pid")); value != "" {
					currentParent = value
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
						Metadata:   map[string]any{},
					})
				}
				currentParent = id
			}
			for _, key := range []string{
				"children",
				"child",
				"sub",
				"type",
				"types",
				"typelist",
				"type_list",
			} {
				if child, exists := typed[key]; exists {
					walk(child, currentParent, currentPath)
				}
			}
		}
	}
	walk(value, "", "")
	return result, nil
}

type bilibiliArchive struct {
	AID         string
	BVID        string
	Title       string
	Description string
	Status      string
}

func decodeBilibiliArchives(raw []byte) ([]bilibiliArchive, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode bilibili archives: %w", err)
	}
	result := make([]bilibiliArchive, 0)
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			title := scalarString(firstValue(typed, "title"))
			aid := scalarString(firstValue(typed, "aid"))
			bvid := scalarString(firstValue(typed, "bvid"))
			if title != "" && (aid != "" || bvid != "") {
				result = append(result, bilibiliArchive{
					AID:         aid,
					BVID:        bvid,
					Title:       title,
					Description: scalarString(firstValue(typed, "desc", "description")),
					Status:      scalarString(firstValue(typed, "state", "status")),
				})
			}
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return result, nil
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, exists := values[key]; exists {
			return value
		}
	}
	return nil
}

func scalarString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return ""
	}
}
