package publishing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/visoraft/visoraft/internal/identity"
)

const maximumImportedCoverBytes = 10 << 20

type CoverObjectStorage interface {
	Put(context.Context, string, string, string, []byte) error
}

type importedCover struct {
	Body        []byte
	ContentType string
	Extension   string
	Width       int
	Height      int
	Checksum    string
}

func (s *Service) ConfigureCoverImport(
	storage CoverObjectStorage,
	bucket string,
) {
	s.coverStorage = storage
	s.coverBucket = strings.TrimSpace(bucket)
	s.coverHTTP = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
			TLSHandshakeTimeout:   8 * time.Second,
			ResponseHeaderTimeout: 12 * time.Second,
			MaxIdleConns:          8,
			IdleConnTimeout:       30 * time.Second,
		},
	}
	s.coverHTTP.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return errors.New("cover download exceeded redirect limit")
		}
		return validateThumbnailURL(request.URL)
	}
}

func (s *Service) ensureCover(
	ctx context.Context,
	taskID string,
	platform string,
) error {
	thumbnailURL, requiresBilibili, hasCover, err := s.store.CoverImportSource(ctx, taskID)
	if err != nil {
		return err
	}
	if hasCover || (!requiresBilibili && platform != PlatformBilibili) {
		return nil
	}
	if s.coverStorage == nil || s.coverHTTP == nil || s.coverBucket == "" {
		return &ConflictError{
			Code:    "cover_import_unavailable",
			Message: "封面导入服务尚未配置，不能开始 Bilibili 投稿",
		}
	}
	cover, err := fetchImportedCover(ctx, s.coverHTTP, thumbnailURL)
	if err != nil {
		return &ConflictError{
			Code:    "cover_import_failed",
			Message: "无法获取并校验来源缩略图：" + err.Error(),
		}
	}
	assetID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	objectKey := fmt.Sprintf(
		"tasks/%s/cover/source-%s%s",
		taskID,
		cover.Checksum[:16],
		cover.Extension,
	)
	if err := s.coverStorage.Put(
		ctx,
		s.coverBucket,
		objectKey,
		cover.ContentType,
		cover.Body,
	); err != nil {
		return fmt.Errorf("store imported cover: %w", err)
	}
	_, err = s.store.SaveImportedCover(
		ctx,
		taskID,
		assetID,
		s.coverBucket,
		objectKey,
		"cover"+cover.Extension,
		cover.ContentType,
		int64(len(cover.Body)),
		cover.Checksum,
		cover.Width,
		cover.Height,
		s.now().UTC(),
	)
	return err
}

func fetchImportedCover(
	ctx context.Context,
	client *http.Client,
	rawURL string,
) (importedCover, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return importedCover{}, errors.New("缩略图地址格式无效")
	}
	if err := validateThumbnailURL(parsed); err != nil {
		return importedCover{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return importedCover{}, fmt.Errorf("创建封面请求失败: %w", err)
	}
	request.Header.Set("Accept", "image/jpeg,image/png;q=0.9")
	request.Header.Set("User-Agent", "Visoraft/0.1 cover-import")
	response, err := client.Do(request)
	if err != nil {
		return importedCover{}, fmt.Errorf("下载失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.CopyN(io.Discard, response.Body, 4096)
		return importedCover{}, fmt.Errorf("下载返回 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumImportedCoverBytes+1))
	if err != nil {
		return importedCover{}, fmt.Errorf("读取失败: %w", err)
	}
	if len(body) == 0 || len(body) > maximumImportedCoverBytes {
		return importedCover{}, errors.New("封面必须大于 0 且不超过 10 MiB")
	}
	configuration, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return importedCover{}, errors.New("文件不是有效的 JPEG 或 PNG 图片")
	}
	if configuration.Width < 480 || configuration.Height < 270 {
		return importedCover{}, fmt.Errorf(
			"图片尺寸过小（%dx%d），至少需要 480x270",
			configuration.Width,
			configuration.Height,
		)
	}
	contentType := ""
	extension := ""
	switch format {
	case "jpeg":
		contentType = "image/jpeg"
		extension = ".jpg"
	case "png":
		contentType = "image/png"
		extension = ".png"
	default:
		return importedCover{}, errors.New("仅支持 JPEG 或 PNG 封面")
	}
	sum := sha256.Sum256(body)
	return importedCover{
		Body:        body,
		ContentType: contentType,
		Extension:   extension,
		Width:       configuration.Width,
		Height:      configuration.Height,
		Checksum:    hex.EncodeToString(sum[:]),
	}, nil
}

func validateThumbnailURL(value *url.URL) error {
	if value == nil || value.Scheme != "https" || value.User != nil {
		return errors.New("缩略图必须使用可信 HTTPS 地址")
	}
	host := strings.ToLower(strings.TrimSuffix(value.Hostname(), "."))
	trusted := host == "i.ytimg.com" ||
		host == "img.youtube.com" ||
		strings.HasSuffix(host, ".ytimg.com")
	if !trusted {
		return fmt.Errorf("不允许从主机 %s 导入封面", host)
	}
	if value.Port() != "" && value.Port() != "443" {
		return errors.New("缩略图地址不允许使用自定义端口")
	}
	if strings.Contains(path.Clean(value.EscapedPath()), "..") {
		return errors.New("缩略图路径无效")
	}
	return nil
}
