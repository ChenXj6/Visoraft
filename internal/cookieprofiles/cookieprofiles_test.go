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
	"net/http"
	"net/http/httptest"
	"testing"
)

const testEncryptionKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

func TestSecretBoxAuthenticatesPurposeAndCiphertext(t *testing.T) {
	box, err := NewSecretBox(testEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := box.Seal("jar", []byte("secret-cookie"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := box.Open("jar", packet)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "secret-cookie" {
		t.Fatalf("unexpected plaintext %q", plaintext)
	}
	if _, err := box.Open("credentials", packet); err == nil {
		t.Fatal("expected purpose mismatch to fail authentication")
	}
	packet[len(packet)-1] ^= 1
	if _, err := box.Open("jar", packet); err == nil {
		t.Fatal("expected tampered ciphertext to fail authentication")
	}
}

func TestValidateNetscapeJarNormalizesAndCounts(t *testing.T) {
	input := []byte(
		"# Netscape HTTP Cookie File\r\n" +
			".youtube.com\tTRUE\t/\tTRUE\t1893456000\tSID\tsecret\r\n" +
			"#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t1893456000\tHSID\tsecret2\r\n",
	)
	summary, err := validateNetscapeJar(input)
	if err != nil {
		t.Fatal(err)
	}
	if summary.CookieCount != 2 || summary.DomainCount != 1 {
		t.Fatalf("unexpected counts cookies=%d domains=%d", summary.CookieCount, summary.DomainCount)
	}
	if bytes.Contains(summary.Content, []byte("\r")) {
		t.Fatal("expected LF-normalized cookie content")
	}
}

func TestBuildNetscapeJarRejectsEmptyCloudData(t *testing.T) {
	if _, err := buildNetscapeJar(map[string][]cloudCookie{}); err == nil {
		t.Fatal("expected empty CookieCloud data to be rejected")
	}
}

func TestCookieCloudDecryptsFixedAndLegacyFormats(t *testing.T) {
	plaintext := []byte(`{"cookie_data":{"youtube.com":[{"domain":".youtube.com","name":"SID","path":"/","secure":true,"value":"value"}]}}`)
	uuid := "test-uuid"
	password := "test-password"

	fixed := encryptFixedForTest(t, uuid, password, plaintext)
	got, err := decryptCookieCloud(uuid, password, "aes-128-cbc-fixed", fixed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatal("fixed CookieCloud plaintext mismatch")
	}

	legacy := encryptLegacyForTest(t, uuid, password, plaintext)
	got, err = decryptCookieCloud(uuid, password, "legacy", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatal("legacy CookieCloud plaintext mismatch")
	}
}

func TestCookieCloudHTTPFetchUsesDocumentedGetRouteAndKeepsPasswordLocal(t *testing.T) {
	uuid := "test-uuid"
	password := "test-password"
	plaintext := []byte(
		`{"cookie_data":{"youtube.com":[{"domain":".youtube.com","name":"SID","path":"/","secure":true,"value":"value"}]}}`,
	)
	payload, err := json.Marshal(map[string]string{
		"encrypted":   encryptFixedForTest(t, uuid, password, plaintext),
		"crypto_type": "aes-128-cbc-fixed",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", request.Method)
		}
		if request.URL.Path != "/cookie/get/"+uuid {
			t.Fatalf("unexpected CookieCloud path %s", request.URL.Path)
		}
		if request.URL.RawQuery != "" || request.Header.Get("Authorization") != "" {
			t.Fatal("CookieCloud password or credentials must not be sent to the server")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	data, err := NewHTTPCookieCloudClient().Fetch(
		context.Background(),
		server.URL+"/cookie",
		uuid,
		password,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(data["youtube.com"]) != 1 || data["youtube.com"][0].Value != "value" {
		t.Fatalf("unexpected decrypted CookieCloud data: %#v", data)
	}
}

func encryptFixedForTest(t *testing.T, uuid, password string, plaintext []byte) string {
	t.Helper()
	key := cookieCloudPassword(uuid, password)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	padded := padForTest(plaintext)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(ciphertext, padded)
	return base64.StdEncoding.EncodeToString(ciphertext)
}

func encryptLegacyForTest(t *testing.T, uuid, password string, plaintext []byte) string {
	t.Helper()
	salt := []byte("12345678")
	keyAndIV := evpBytesToKey(cookieCloudPassword(uuid, password), salt, 48)
	block, err := aes.NewCipher(keyAndIV[:32])
	if err != nil {
		t.Fatal(err)
	}
	padded := padForTest(plaintext)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, keyAndIV[32:]).CryptBlocks(ciphertext, padded)
	packet := append([]byte("Salted__"), salt...)
	packet = append(packet, ciphertext...)
	return base64.StdEncoding.EncodeToString(packet)
}

func cookieCloudPassword(uuid, password string) []byte {
	hash := md5.Sum([]byte(uuid + "-" + password))
	return []byte(hex.EncodeToString(hash[:])[:16])
}

func padForTest(value []byte) []byte {
	padding := aes.BlockSize - len(value)%aes.BlockSize
	return append(append([]byte{}, value...), bytes.Repeat([]byte{byte(padding)}, padding)...)
}
