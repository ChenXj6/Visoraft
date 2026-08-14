package objectstorage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const emptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

type Client struct {
	endpoint       *url.URL
	publicEndpoint *url.URL
	accessKey      string
	secretKey      string
	region         string
	httpClient     *http.Client
	now            func() time.Time
}

type Config struct {
	Endpoint       string
	PublicEndpoint string
	AccessKey      string
	SecretKey      string
	Region         string
}

type UpstreamError struct {
	StatusCode int
	Status     string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("object storage returned %s", e.Status)
}

func New(config Config) (*Client, error) {
	endpoint, err := url.Parse(strings.TrimSpace(config.Endpoint))
	if err != nil {
		return nil, fmt.Errorf("parse object storage endpoint: %w", err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, errors.New("object storage endpoint must use http or https")
	}
	if endpoint.Host == "" {
		return nil, errors.New("object storage endpoint host is required")
	}
	var publicEndpoint *url.URL
	if value := strings.TrimSpace(config.PublicEndpoint); value != "" {
		publicEndpoint, err = url.Parse(value)
		if err != nil {
			return nil, fmt.Errorf("parse public object storage endpoint: %w", err)
		}
		if publicEndpoint.Scheme != "https" && publicEndpoint.Scheme != "http" {
			return nil, errors.New(
				"public object storage endpoint must use http or https",
			)
		}
		if publicEndpoint.Host == "" {
			return nil, errors.New("public object storage endpoint host is required")
		}
	}
	if strings.TrimSpace(config.AccessKey) == "" ||
		strings.TrimSpace(config.SecretKey) == "" {
		return nil, errors.New("object storage credentials are required")
	}
	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = "us-east-1"
	}
	return &Client{
		endpoint:       endpoint,
		publicEndpoint: publicEndpoint,
		accessKey:      strings.TrimSpace(config.AccessKey),
		secretKey:      config.SecretKey,
		region:         region,
		httpClient: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          20,
				MaxIdleConnsPerHost:   10,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 15 * time.Second,
			},
		},
		now: time.Now,
	}, nil
}

func (c *Client) PresignGet(
	bucket string,
	objectKey string,
	validFor time.Duration,
) (string, error) {
	if c.publicEndpoint == nil {
		return "", errors.New("public object storage endpoint is not configured")
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(objectKey) == "" {
		return "", errors.New("bucket and object key are required")
	}
	if validFor < time.Minute || validFor > 24*time.Hour {
		return "", errors.New("presigned URL lifetime must be between 1 minute and 24 hours")
	}
	now := c.now().UTC()
	shortDate := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	scope := shortDate + "/" + c.region + "/s3/aws4_request"

	requestURL := *c.publicEndpoint
	basePath := strings.TrimSuffix(requestURL.Path, "/")
	requestURL.Path = basePath + "/" + bucket + "/" + objectKey
	requestURL.RawPath = escapePath(basePath, bucket, objectKey)
	query := url.Values{}
	query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	query.Set("X-Amz-Credential", c.accessKey+"/"+scope)
	query.Set("X-Amz-Date", amzDate)
	query.Set("X-Amz-Expires", fmt.Sprint(int64(validFor/time.Second)))
	query.Set("X-Amz-SignedHeaders", "host")
	requestURL.RawQuery = query.Encode()
	requestURL.Fragment = ""

	canonicalRequest := strings.Join([]string{
		http.MethodGet,
		requestURL.EscapedPath(),
		requestURL.Query().Encode(),
		"host:" + requestURL.Host + "\n",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	dateKey := hmacSHA256([]byte("AWS4"+c.secretKey), shortDate)
	regionKey := hmacSHA256(dateKey, c.region)
	serviceKey := hmacSHA256(regionKey, "s3")
	signingKey := hmacSHA256(serviceKey, "aws4_request")
	query.Set(
		"X-Amz-Signature",
		hex.EncodeToString(hmacSHA256(signingKey, stringToSign)),
	)
	requestURL.RawQuery = query.Encode()
	return requestURL.String(), nil
}

func (c *Client) Get(
	ctx context.Context,
	bucket string,
	objectKey string,
	rangeHeader string,
) (*http.Response, error) {
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(objectKey) == "" {
		return nil, errors.New("bucket and object key are required")
	}
	requestURL := *c.endpoint
	basePath := strings.TrimSuffix(requestURL.Path, "/")
	requestURL.Path = basePath + "/" + bucket + "/" + objectKey
	requestURL.RawPath = escapePath(basePath, bucket, objectKey)
	requestURL.RawQuery = ""
	requestURL.Fragment = ""

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create object storage request: %w", err)
	}
	if value := strings.TrimSpace(rangeHeader); value != "" {
		if !strings.HasPrefix(strings.ToLower(value), "bytes=") || len(value) > 128 {
			return nil, errors.New("invalid range header")
		}
		request.Header.Set("Range", value)
	}
	c.sign(request, c.now().UTC())

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("read object storage: %w", err)
	}
	if response.StatusCode != http.StatusOK &&
		response.StatusCode != http.StatusPartialContent {
		_, _ = io.CopyN(io.Discard, response.Body, 4096)
		_ = response.Body.Close()
		return nil, &UpstreamError{
			StatusCode: response.StatusCode,
			Status:     response.Status,
		}
	}
	return response, nil
}

func (c *Client) Put(
	ctx context.Context,
	bucket string,
	objectKey string,
	contentType string,
	body []byte,
) error {
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(objectKey) == "" {
		return errors.New("bucket and object key are required")
	}
	if strings.TrimSpace(contentType) == "" {
		return errors.New("content type is required")
	}
	requestURL := *c.endpoint
	basePath := strings.TrimSuffix(requestURL.Path, "/")
	requestURL.Path = basePath + "/" + bucket + "/" + objectKey
	requestURL.RawPath = escapePath(basePath, bucket, objectKey)
	requestURL.RawQuery = ""
	requestURL.Fragment = ""

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPut,
		requestURL.String(),
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create object storage put request: %w", err)
	}
	request.Header.Set("Content-Type", contentType)
	payloadHash := sha256Hex(body)
	c.signPayload(request, c.now().UTC(), payloadHash)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("write object storage: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK &&
		response.StatusCode != http.StatusCreated &&
		response.StatusCode != http.StatusNoContent {
		_, _ = io.CopyN(io.Discard, response.Body, 4096)
		return &UpstreamError{StatusCode: response.StatusCode, Status: response.Status}
	}
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

func (c *Client) sign(request *http.Request, now time.Time) {
	c.signPayload(request, now, emptyPayloadSHA256)
}

func (c *Client) signPayload(
	request *http.Request,
	now time.Time,
	payloadHash string,
) {
	amzDate := now.Format("20060102T150405Z")
	shortDate := now.Format("20060102")
	request.Header.Set("X-Amz-Date", amzDate)
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)

	host := request.URL.Host
	canonicalHeaders := "host:" + host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := strings.Join([]string{
		request.Method,
		request.URL.EscapedPath(),
		request.URL.Query().Encode(),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := shortDate + "/" + c.region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	dateKey := hmacSHA256([]byte("AWS4"+c.secretKey), shortDate)
	regionKey := hmacSHA256(dateKey, c.region)
	serviceKey := hmacSHA256(regionKey, "s3")
	signingKey := hmacSHA256(serviceKey, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	request.Header.Set(
		"Authorization",
		"AWS4-HMAC-SHA256 Credential="+c.accessKey+"/"+scope+
			", SignedHeaders="+signedHeaders+
			", Signature="+signature,
	)
}

func escapePath(basePath string, bucket string, objectKey string) string {
	segments := make([]string, 0, 8)
	for _, value := range []string{strings.Trim(basePath, "/"), bucket, objectKey} {
		if value == "" {
			continue
		}
		for _, segment := range strings.Split(value, "/") {
			segments = append(segments, url.PathEscape(segment))
		}
	}
	return "/" + strings.Join(segments, "/")
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
