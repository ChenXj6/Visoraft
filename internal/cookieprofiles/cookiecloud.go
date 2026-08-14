package cookieprofiles

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const maxCookieCloudResponseBytes = 20 * 1024 * 1024

type CookieCloudClient interface {
	Fetch(
		ctx context.Context,
		serverURL string,
		uuid string,
		password string,
	) (map[string][]cloudCookie, error)
}

type HTTPCookieCloudClient struct {
	client *http.Client
}

func NewHTTPCookieCloudClient() *HTTPCookieCloudClient {
	return &HTTPCookieCloudClient{
		client: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func normalizeServerURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("请输入有效的 http/https CookieCloud 地址")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("CookieCloud 地址不能包含账号、查询参数或片段")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func (c *HTTPCookieCloudClient) Fetch(
	ctx context.Context,
	serverURL string,
	uuid string,
	password string,
) (map[string][]cloudCookie, error) {
	base, err := url.Parse(serverURL)
	if err != nil {
		return nil, errors.New("CookieCloud 地址无效")
	}
	base.Path = path.Join(base.Path, "get", url.PathEscape(uuid))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, errors.New("无法创建 CookieCloud 同步请求")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "visoraft-control/0.1")

	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("无法连接 CookieCloud：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CookieCloud 返回 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCookieCloudResponseBytes+1))
	if err != nil {
		return nil, errors.New("读取 CookieCloud 响应失败")
	}
	if len(body) > maxCookieCloudResponseBytes {
		return nil, errors.New("CookieCloud 响应超过 20 MiB 限制")
	}

	var payload struct {
		Encrypted  string `json:"encrypted"`
		CryptoType string `json:"crypto_type"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Encrypted == "" {
		return nil, errors.New("CookieCloud 响应缺少加密 Cookie 数据")
	}
	plaintext, err := decryptCookieCloud(uuid, password, payload.CryptoType, payload.Encrypted)
	if err != nil {
		return nil, errors.New("CookieCloud 解密失败，请检查 UUID、密码和加密算法")
	}
	var decrypted struct {
		CookieData map[string][]cloudCookie `json:"cookie_data"`
	}
	if err := json.Unmarshal(plaintext, &decrypted); err != nil || len(decrypted.CookieData) == 0 {
		return nil, errors.New("CookieCloud 解密结果中没有 cookie_data")
	}
	return decrypted.CookieData, nil
}

func decryptCookieCloud(uuid, password, cryptoType, encrypted string) ([]byte, error) {
	hash := md5.Sum([]byte(uuid + "-" + password))
	keyPassword := []byte(hex.EncodeToString(hash[:])[:16])
	switch strings.TrimSpace(cryptoType) {
	case "", "legacy":
		return decryptLegacyCookieCloud(keyPassword, encrypted)
	case "aes-128-cbc-fixed":
		return decryptFixedCookieCloud(keyPassword, encrypted)
	default:
		return nil, fmt.Errorf("unsupported CookieCloud crypto type %q", cryptoType)
	}
}

func decryptFixedCookieCloud(key []byte, encrypted string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil || len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("invalid fixed CookieCloud ciphertext")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(plaintext, ciphertext)
	return unpadPKCS7(plaintext, aes.BlockSize)
}

func decryptLegacyCookieCloud(password []byte, encrypted string) ([]byte, error) {
	packet, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil || len(packet) < 16 || !bytes.Equal(packet[:8], []byte("Salted__")) {
		return nil, errors.New("invalid legacy CookieCloud ciphertext")
	}
	ciphertext := packet[16:]
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("legacy CookieCloud ciphertext is not block aligned")
	}
	keyAndIV := evpBytesToKey(password, packet[8:16], 32+aes.BlockSize)
	block, err := aes.NewCipher(keyAndIV[:32])
	if err != nil {
		return nil, err
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, keyAndIV[32:]).CryptBlocks(plaintext, ciphertext)
	return unpadPKCS7(plaintext, aes.BlockSize)
}

func evpBytesToKey(password, salt []byte, length int) []byte {
	result := make([]byte, 0, length)
	var previous []byte
	for len(result) < length {
		digest := md5.New()
		_, _ = digest.Write(previous)
		_, _ = digest.Write(password)
		_, _ = digest.Write(salt)
		previous = digest.Sum(nil)
		result = append(result, previous...)
	}
	return result[:length]
}

func unpadPKCS7(value []byte, blockSize int) ([]byte, error) {
	if len(value) == 0 || len(value)%blockSize != 0 {
		return nil, errors.New("invalid PKCS7 data")
	}
	padding := int(value[len(value)-1])
	if padding == 0 || padding > blockSize || padding > len(value) {
		return nil, errors.New("invalid PKCS7 padding")
	}
	for _, current := range value[len(value)-padding:] {
		if int(current) != padding {
			return nil, errors.New("invalid PKCS7 padding")
		}
	}
	return value[:len(value)-padding], nil
}
