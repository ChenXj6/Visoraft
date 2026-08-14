package cookieprofiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/visoraft/visoraft/internal/identity"
)

const (
	jarPurpose         = "visoraft-cookie-jar-v1"
	credentialsPurpose = "visoraft-cookiecloud-credentials-v1"
)

type Service struct {
	store Store
	box   *SecretBox
	cloud CookieCloudClient
	now   func() time.Time
}

func NewService(store Store, box *SecretBox, cloud CookieCloudClient) *Service {
	return &Service{
		store: store,
		box:   box,
		cloud: cloud,
		now:   time.Now,
	}
}

func (s *Service) List(ctx context.Context) ([]Profile, error) {
	return s.store.List(ctx)
}

func (s *Service) Upload(
	ctx context.Context,
	name string,
	filename string,
	content []byte,
) (Profile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
	}
	if fields := validateName(name); len(fields) > 0 {
		return Profile{}, &ValidationError{Fields: fields}
	}
	summary, err := validateNetscapeJar(content)
	if err != nil {
		return Profile{}, &ValidationError{
			Fields: map[string]string{"file": err.Error()},
		}
	}
	sealed, err := s.box.Seal(jarPurpose, summary.Content)
	if err != nil {
		return Profile{}, err
	}
	id, err := identity.NewUUID()
	if err != nil {
		return Profile{}, err
	}
	now := s.now().UTC()
	profile := Profile{
		ID:               id,
		Name:             name,
		Kind:             KindUpload,
		Status:           StatusReady,
		SourceFilename:   cleanFilename(filename),
		CookieCount:      summary.CookieCount,
		DomainCount:      summary.DomainCount,
		HasUsableCookies: true,
		LastSyncedAt:     &now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.store.Create(ctx, record{
		Profile:            profile,
		EncryptedCookieJar: sealed,
	}); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (s *Service) CreateCookieCloud(
	ctx context.Context,
	input CookieCloudInput,
) (Profile, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.UUID = strings.TrimSpace(input.UUID)
	fields := validateName(input.Name)
	serverURL, err := normalizeServerURL(input.ServerURL)
	if err != nil {
		fields["server_url"] = err.Error()
	}
	if input.UUID == "" || len(input.UUID) > 200 {
		fields["uuid"] = "请填写 1 到 200 个字符的 CookieCloud UUID"
	}
	if input.Password == "" || len(input.Password) > 500 {
		fields["password"] = "请填写 1 到 500 个字符的 CookieCloud 密码"
	}
	if len(fields) > 0 {
		return Profile{}, &ValidationError{Fields: fields}
	}

	credentials, err := json.Marshal(cloudCredentials{
		UUID:     input.UUID,
		Password: input.Password,
	})
	if err != nil {
		return Profile{}, fmt.Errorf("marshal CookieCloud credentials: %w", err)
	}
	sealedCredentials, err := s.box.Seal(credentialsPurpose, credentials)
	if err != nil {
		return Profile{}, err
	}
	id, err := identity.NewUUID()
	if err != nil {
		return Profile{}, err
	}
	now := s.now().UTC()
	profile := Profile{
		ID:        id,
		Name:      input.Name,
		Kind:      KindCookieCloud,
		Status:    StatusSyncing,
		ServerURL: serverURL,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.Create(ctx, record{
		Profile:                   profile,
		EncryptedCloudCredentials: sealedCredentials,
	}); err != nil {
		return Profile{}, err
	}
	return s.Sync(ctx, id)
}

func (s *Service) Sync(ctx context.Context, id string) (Profile, error) {
	if !identity.IsUUID(id) {
		return Profile{}, ErrNotFound
	}
	value, err := s.store.Get(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	if value.Kind != KindCookieCloud {
		return Profile{}, &ValidationError{
			Fields: map[string]string{"profile": "上传文件类型的 Cookie 配置不能同步"},
		}
	}
	now := s.now().UTC()
	if err := s.store.MarkSyncing(ctx, id, now); err != nil {
		return Profile{}, err
	}

	credentialsJSON, err := s.box.Open(credentialsPurpose, value.EncryptedCloudCredentials)
	if err == nil {
		var credentials cloudCredentials
		err = json.Unmarshal(credentialsJSON, &credentials)
		if err == nil {
			var data map[string][]cloudCookie
			data, err = s.cloud.Fetch(ctx, value.ServerURL, credentials.UUID, credentials.Password)
			if err == nil {
				var summary jarSummary
				summary, err = buildNetscapeJar(data)
				if err == nil {
					var sealed []byte
					sealed, err = s.box.Seal(jarPurpose, summary.Content)
					if err == nil {
						err = s.store.CompleteSync(
							ctx,
							id,
							sealed,
							summary.CookieCount,
							summary.DomainCount,
							now,
						)
					}
				}
			}
		}
	}
	if err != nil {
		message := cleanSyncError(err)
		if recordErr := s.store.FailSync(ctx, id, message, now); recordErr != nil {
			return Profile{}, errors.Join(err, recordErr)
		}
	}
	current, getErr := s.store.Get(ctx, id)
	if getErr != nil {
		return Profile{}, getErr
	}
	return current.Profile, nil
}

func (s *Service) CookieJar(ctx context.Context, id string) ([]byte, error) {
	if !identity.IsUUID(id) {
		return nil, ErrNotFound
	}
	value, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(value.EncryptedCookieJar) == 0 {
		return nil, errors.New("Cookie 配置尚无可用 Cookie，请先同步或重新上传")
	}
	plaintext, err := s.box.Open(jarPurpose, value.EncryptedCookieJar)
	if err != nil {
		return nil, fmt.Errorf("decrypt cookie jar: %w", err)
	}
	return plaintext, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if !identity.IsUUID(id) {
		return ErrNotFound
	}
	return s.store.Delete(ctx, id)
}

func validateName(name string) map[string]string {
	fields := map[string]string{}
	count := len([]rune(strings.TrimSpace(name)))
	if count < 1 || count > 80 {
		fields["name"] = "名称需为 1 到 80 个字符"
	}
	return fields
}

func cleanFilename(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "\x00", "")
	if len([]rune(value)) > 200 {
		return string([]rune(value)[:200])
	}
	return value
}

func cleanSyncError(err error) string {
	message := strings.TrimSpace(strings.ReplaceAll(err.Error(), "\x00", ""))
	if len([]rune(message)) > 500 {
		message = string([]rune(message)[:500])
	}
	if message == "" {
		return "CookieCloud 同步失败"
	}
	return message
}
