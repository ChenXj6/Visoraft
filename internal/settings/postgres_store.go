package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrVersionConflict = errors.New("settings version conflict")

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) Get(ctx context.Context) (Settings, error) {
	var result Settings
	var raw encodedConfig
	err := s.pool.QueryRow(ctx, `
		SELECT
			version,
			review_config,
			model_config,
			subtitle_config,
			prompt_config,
			youtube_config,
			automation_config,
			transcode_config,
			moderation_config,
			publishing_config,
			updated_at
		FROM application_settings
		WHERE singleton=true
	`).Scan(
		&result.Version,
		&raw.review,
		&raw.models,
		&raw.subtitle,
		&raw.prompts,
		&raw.youtube,
		&raw.automation,
		&raw.transcode,
		&raw.moderation,
		&raw.publishing,
		&result.UpdatedAt,
	)
	if err != nil {
		return Settings{}, fmt.Errorf("load application settings: %w", err)
	}
	if err := decodeConfig(raw, &result.ConfigSnapshot); err != nil {
		return Settings{}, err
	}

	result.SecretConfigured = make(map[string]bool, len(AllowedSecretKeys))
	rows, err := s.pool.Query(ctx, `SELECT key FROM setting_secrets`)
	if err != nil {
		return Settings{}, fmt.Errorf("list setting secrets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return Settings{}, fmt.Errorf("scan setting secret: %w", err)
		}
		result.SecretConfigured[key] = true
	}
	if err := rows.Err(); err != nil {
		return Settings{}, fmt.Errorf("iterate setting secrets: %w", err)
	}
	for key := range AllowedSecretKeys {
		if _, exists := result.SecretConfigured[key]; !exists {
			result.SecretConfigured[key] = false
		}
	}
	return result, nil
}

func (s *PostgresStore) Update(
	ctx context.Context,
	input UpdateInput,
	sealedSecrets map[string][]byte,
	now time.Time,
) (Settings, error) {
	raw, err := encodeConfig(input.ConfigSnapshot)
	if err != nil {
		return Settings{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Settings{}, fmt.Errorf("begin settings update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentVersion int64
	if err := tx.QueryRow(ctx, `
		SELECT version
		FROM application_settings
		WHERE singleton=true
		FOR UPDATE
	`).Scan(&currentVersion); err != nil {
		return Settings{}, fmt.Errorf("lock application settings: %w", err)
	}
	if currentVersion != input.ExpectedVersion {
		return Settings{}, ErrVersionConflict
	}
	nextVersion := currentVersion + 1

	if _, err := tx.Exec(ctx, `
		UPDATE application_settings
		SET
			version=$1,
			review_config=$2,
			model_config=$3,
			subtitle_config=$4,
			prompt_config=$5,
			youtube_config=$6,
			automation_config=$7,
			transcode_config=$8,
			moderation_config=$9,
			publishing_config=$10,
			updated_at=$11
		WHERE singleton=true
	`,
		nextVersion,
		raw.review,
		raw.models,
		raw.subtitle,
		raw.prompts,
		raw.youtube,
		raw.automation,
		raw.transcode,
		raw.moderation,
		raw.publishing,
		now,
	); err != nil {
		return Settings{}, fmt.Errorf("update application settings: %w", err)
	}

	for key, ciphertext := range sealedSecrets {
		if _, err := tx.Exec(ctx, `
			INSERT INTO setting_secrets (
				key, ciphertext, version, created_at, updated_at
			) VALUES ($1, $2, 1, $3, $3)
			ON CONFLICT (key) DO UPDATE
			SET
				ciphertext=EXCLUDED.ciphertext,
				version=setting_secrets.version + 1,
				updated_at=EXCLUDED.updated_at
		`, key, ciphertext, now); err != nil {
			return Settings{}, fmt.Errorf("save setting secret %s: %w", key, err)
		}
	}
	for _, key := range input.ClearSecrets {
		if _, err := tx.Exec(ctx, `DELETE FROM setting_secrets WHERE key=$1`, key); err != nil {
			return Settings{}, fmt.Errorf("clear setting secret %s: %w", key, err)
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO settings_revisions (
			version, snapshot, actor_type, actor_id, created_at
		) VALUES ($1, $2, 'user', 'local-operator', $3)
	`, nextVersion, raw.snapshot, now); err != nil {
		return Settings{}, fmt.Errorf("record settings revision: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Settings{}, fmt.Errorf("commit settings update: %w", err)
	}
	return s.Get(ctx)
}

func (s *PostgresStore) Secret(ctx context.Context, key string) ([]byte, int64, error) {
	var ciphertext []byte
	var version int64
	err := s.pool.QueryRow(ctx, `
		SELECT ciphertext, version
		FROM setting_secrets
		WHERE key=$1
	`, key).Scan(&ciphertext, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("load setting secret %s: %w", key, err)
	}
	return ciphertext, version, nil
}

func (s *PostgresStore) TaskProcessingConfig(
	ctx context.Context,
	taskID string,
) (ConfigSnapshot, map[string][]byte, TaskRuntime, error) {
	var snapshotRaw []byte
	var subtitleDecisionRaw []byte
	var runtime TaskRuntime
	var (
		assetID, assetKind, assetBucket, assetKey, assetName  *string
		assetContentType, assetChecksum                       *string
		assetSize                                             *int64
		subtitleID, subtitleKind, subtitleBucket, subtitleKey *string
		subtitleName, subtitleContentType, subtitleChecksum   *string
		subtitleSize                                          *int64
		finalID, finalKind, finalBucket, finalKey             *string
		finalName, finalContentType, finalChecksum            *string
		finalSize                                             *int64
		coverID, coverKind, coverBucket, coverKey             *string
		coverName, coverContentType, coverChecksum            *string
		coverSize                                             *int64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT
			t.settings_snapshot,
			COALESCE((
				SELECT step.detail -> 'decision'
				FROM task_steps step
				WHERE step.task_id=t.id AND step.kind='subtitles'
				ORDER BY step.attempt DESC
				LIMIT 1
			), '{}'::jsonb),
			t.source_url,
			t.cookie_profile_id::text,
			t.title,
			t.description,
			t.thumbnail_url,
			t.tags,
			CASE t.repost_statement_version
				WHEN 'brief_v1' THEN t.repost_statement_brief
				ELSE t.repost_statement_full
			END,
			asset.id::text,
			asset.kind,
			asset.bucket,
			asset.object_key,
			asset.original_name,
			asset.content_type,
			asset.size_bytes,
			asset.checksum_sha256,
			subtitle.id::text,
			subtitle.kind,
			subtitle.bucket,
			subtitle.object_key,
			subtitle.original_name,
			subtitle.content_type,
			subtitle.size_bytes,
			subtitle.checksum_sha256,
			final_media.id::text,
			final_media.kind,
			final_media.bucket,
			final_media.object_key,
			final_media.original_name,
			final_media.content_type,
			final_media.size_bytes,
			final_media.checksum_sha256,
			cover.id::text,
			cover.kind,
			cover.bucket,
			cover.object_key,
			cover.original_name,
			cover.content_type,
			cover.size_bytes,
			cover.checksum_sha256
		FROM tasks t
		LEFT JOIN LATERAL (
			SELECT
				id, kind, bucket, object_key, original_name, content_type,
				size_bytes, checksum_sha256
			FROM media_assets
			WHERE task_id=t.id AND kind='source' AND status='available'
			ORDER BY created_at DESC
			LIMIT 1
		) asset ON true
		LEFT JOIN LATERAL (
			SELECT
				id, kind, bucket, object_key, original_name, content_type,
				size_bytes, checksum_sha256
			FROM media_assets
			WHERE task_id=t.id
			  AND kind IN ('subtitle_translated_vtt','subtitle_original_vtt')
			  AND status='available'
			ORDER BY
				CASE kind
					WHEN 'subtitle_translated_vtt' THEN 0
					ELSE 1
				END,
				created_at DESC
			LIMIT 1
		) subtitle ON true
		LEFT JOIN LATERAL (
			SELECT
				id, kind, bucket, object_key, original_name, content_type,
				size_bytes, checksum_sha256
			FROM media_assets
			WHERE task_id=t.id
			  AND kind IN ('transcoded','source')
			  AND status='available'
			ORDER BY
				CASE kind WHEN 'transcoded' THEN 0 ELSE 1 END,
				created_at DESC
			LIMIT 1
		) final_media ON true
		LEFT JOIN LATERAL (
			SELECT
				id, kind, bucket, object_key, original_name, content_type,
				size_bytes, checksum_sha256
			FROM media_assets
			WHERE task_id=t.id
			  AND kind IN ('cover_processed','thumbnail')
			  AND status='available'
			ORDER BY
				CASE kind WHEN 'cover_processed' THEN 0 ELSE 1 END,
				created_at DESC
			LIMIT 1
		) cover ON true
		WHERE t.id=$1
	`, taskID).Scan(
		&snapshotRaw,
		&subtitleDecisionRaw,
		&runtime.SourceURL,
		&runtime.CookieProfileID,
		&runtime.Title,
		&runtime.Description,
		&runtime.ThumbnailURL,
		&runtime.Tags,
		&runtime.RepostStatement,
		&assetID,
		&assetKind,
		&assetBucket,
		&assetKey,
		&assetName,
		&assetContentType,
		&assetSize,
		&assetChecksum,
		&subtitleID,
		&subtitleKind,
		&subtitleBucket,
		&subtitleKey,
		&subtitleName,
		&subtitleContentType,
		&subtitleSize,
		&subtitleChecksum,
		&finalID,
		&finalKind,
		&finalBucket,
		&finalKey,
		&finalName,
		&finalContentType,
		&finalSize,
		&finalChecksum,
		&coverID,
		&coverKind,
		&coverBucket,
		&coverKey,
		&coverName,
		&coverContentType,
		&coverSize,
		&coverChecksum,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConfigSnapshot{}, nil, TaskRuntime{}, pgx.ErrNoRows
	}
	if err != nil {
		return ConfigSnapshot{}, nil, TaskRuntime{}, fmt.Errorf("load task settings snapshot: %w", err)
	}
	if assetID != nil &&
		assetKind != nil &&
		assetBucket != nil &&
		assetKey != nil &&
		assetName != nil &&
		assetContentType != nil &&
		assetSize != nil &&
		assetChecksum != nil {
		runtime.SourceAsset = &RuntimeAsset{
			ID:           *assetID,
			Kind:         *assetKind,
			Bucket:       *assetBucket,
			ObjectKey:    *assetKey,
			OriginalName: *assetName,
			ContentType:  *assetContentType,
			SizeBytes:    *assetSize,
			Checksum:     *assetChecksum,
		}
	}
	if subtitleID != nil &&
		subtitleKind != nil &&
		subtitleBucket != nil &&
		subtitleKey != nil &&
		subtitleName != nil &&
		subtitleContentType != nil &&
		subtitleSize != nil &&
		subtitleChecksum != nil {
		runtime.SubtitleAsset = &RuntimeAsset{
			ID:           *subtitleID,
			Kind:         *subtitleKind,
			Bucket:       *subtitleBucket,
			ObjectKey:    *subtitleKey,
			OriginalName: *subtitleName,
			ContentType:  *subtitleContentType,
			SizeBytes:    *subtitleSize,
			Checksum:     *subtitleChecksum,
		}
	}
	if finalID != nil &&
		finalKind != nil &&
		finalBucket != nil &&
		finalKey != nil &&
		finalName != nil &&
		finalContentType != nil &&
		finalSize != nil &&
		finalChecksum != nil {
		runtime.FinalMediaAsset = &RuntimeAsset{
			ID:           *finalID,
			Kind:         *finalKind,
			Bucket:       *finalBucket,
			ObjectKey:    *finalKey,
			OriginalName: *finalName,
			ContentType:  *finalContentType,
			SizeBytes:    *finalSize,
			Checksum:     *finalChecksum,
		}
	}
	if coverID != nil &&
		coverKind != nil &&
		coverBucket != nil &&
		coverKey != nil &&
		coverName != nil &&
		coverContentType != nil &&
		coverSize != nil &&
		coverChecksum != nil {
		runtime.CoverAsset = &RuntimeAsset{
			ID:           *coverID,
			Kind:         *coverKind,
			Bucket:       *coverBucket,
			ObjectKey:    *coverKey,
			OriginalName: *coverName,
			ContentType:  *coverContentType,
			SizeBytes:    *coverSize,
			Checksum:     *coverChecksum,
		}
	}

	var snapshot ConfigSnapshot
	if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
		return ConfigSnapshot{}, nil, TaskRuntime{}, fmt.Errorf("decode task settings snapshot: %w", err)
	}
	var subtitleDecision struct {
		SchemaVersion int    `json:"schema_version"`
		Disposition   string `json:"disposition"`
		BurnSubtitles bool   `json:"burn_subtitles"`
	}
	if err := json.Unmarshal(subtitleDecisionRaw, &subtitleDecision); err != nil {
		return ConfigSnapshot{}, nil, TaskRuntime{}, fmt.Errorf(
			"decode subtitle processing decision: %w",
			err,
		)
	}
	if subtitleDecision.SchemaVersion == 1 &&
		(subtitleDecision.Disposition == "existing_soft_chinese" ||
			subtitleDecision.Disposition == "existing_hardcoded_chinese") {
		snapshot.Transcode.BurnSubtitles = false
		runtime.SubtitleAsset = nil
	}
	secrets := make(map[string][]byte)
	rows, err := s.pool.Query(ctx, `
		SELECT key, ciphertext
		FROM task_secret_snapshots
		WHERE task_id=$1
	`, taskID)
	if err != nil {
		return ConfigSnapshot{}, nil, TaskRuntime{}, fmt.Errorf("load task secret snapshots: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var ciphertext []byte
		if err := rows.Scan(&key, &ciphertext); err != nil {
			return ConfigSnapshot{}, nil, TaskRuntime{}, fmt.Errorf("scan task secret snapshot: %w", err)
		}
		secrets[key] = ciphertext
	}
	if err := rows.Err(); err != nil {
		return ConfigSnapshot{}, nil, TaskRuntime{}, fmt.Errorf("iterate task secret snapshots: %w", err)
	}
	return snapshot, secrets, runtime, nil
}

type encodedConfig struct {
	review     []byte
	models     []byte
	subtitle   []byte
	prompts    []byte
	youtube    []byte
	automation []byte
	transcode  []byte
	moderation []byte
	publishing []byte
	snapshot   []byte
}

func decodeConfig(raw encodedConfig, target *ConfigSnapshot) error {
	for _, item := range []struct {
		name   string
		raw    []byte
		target any
	}{
		{"review", raw.review, &target.Review},
		{"models", raw.models, &target.Models},
		{"subtitle", raw.subtitle, &target.Subtitle},
		{"prompts", raw.prompts, &target.Prompts},
		{"youtube", raw.youtube, &target.YouTube},
		{"automation", raw.automation, &target.Automation},
		{"transcode", raw.transcode, &target.Transcode},
		{"moderation", raw.moderation, &target.Moderation},
		{"publishing", raw.publishing, &target.Publishing},
	} {
		if err := json.Unmarshal(item.raw, item.target); err != nil {
			return fmt.Errorf("decode %s settings: %w", item.name, err)
		}
	}
	return nil
}

func encodeConfig(snapshot ConfigSnapshot) (encodedConfig, error) {
	result := encodedConfig{}
	values := []struct {
		name   string
		value  any
		target *[]byte
	}{
		{"review", snapshot.Review, &result.review},
		{"models", snapshot.Models, &result.models},
		{"subtitle", snapshot.Subtitle, &result.subtitle},
		{"prompts", snapshot.Prompts, &result.prompts},
		{"youtube", snapshot.YouTube, &result.youtube},
		{"automation", snapshot.Automation, &result.automation},
		{"transcode", snapshot.Transcode, &result.transcode},
		{"moderation", snapshot.Moderation, &result.moderation},
		{"publishing", snapshot.Publishing, &result.publishing},
		{"snapshot", snapshot, &result.snapshot},
	}
	for _, item := range values {
		raw, err := json.Marshal(item.value)
		if err != nil {
			return encodedConfig{}, fmt.Errorf("encode %s settings: %w", item.name, err)
		}
		*item.target = raw
	}
	return result, nil
}
