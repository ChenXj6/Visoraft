package medialibrary

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type postgresStore struct{ pool *pgxpool.Pool }

func newPostgresStore(pool *pgxpool.Pool) *postgresStore { return &postgresStore{pool: pool} }

func (s *postgresStore) settings(ctx context.Context) (Settings, error) {
	var value Settings
	err := s.pool.QueryRow(ctx, `
		SELECT requested_host_path, auto_sync, version, updated_at
		FROM local_library_settings WHERE singleton=true
	`).Scan(&value.RequestedHostPath, &value.AutoSync, &value.Version, &value.UpdatedAt)
	return value, err
}

func (s *postgresStore) updateSettings(ctx context.Context, input UpdateSettingsInput, now time.Time) (Settings, error) {
	var value Settings
	err := s.pool.QueryRow(ctx, `
		UPDATE local_library_settings
		SET requested_host_path=$2, auto_sync=$3, version=version+1, updated_at=$4
		WHERE singleton=true AND version=$1
		RETURNING requested_host_path, auto_sync, version, updated_at
	`, input.ExpectedVersion, input.HostPath, input.AutoSync, now).Scan(
		&value.RequestedHostPath, &value.AutoSync, &value.Version, &value.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, &ConflictError{Code: "library_settings_conflict", Message: "本地媒体库设置已被更新，请刷新后重试"}
	}
	return value, err
}

func (s *postgresStore) records(ctx context.Context) ([]record, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT asset.id::text, asset.task_id::text, asset.kind, asset.bucket,
		       asset.object_key, asset.original_name, asset.content_type,
		       asset.size_bytes, asset.checksum_sha256, asset.status, asset.deleted_at,
		       asset.created_at,
		       task.title, task.original_title, task.status, task.archived_at, task.updated_at,
		       task.origin_kind, COALESCE(task.origin_monitor_id::text, ''),
		       task.origin_monitor_name, task.origin_series_title,
		       task.origin_series_scope_key, task.origin_series_scope_name,
		       task.origin_episode_number,
		       COALESCE(entry.relative_path, ''), COALESCE(entry.status, ''),
		       COALESCE(entry.local_size_bytes, 0), entry.materialized_at,
		       entry.last_verified_at, entry.missing_at, COALESCE(entry.last_error, '')
		FROM media_assets AS asset
		JOIN tasks AS task ON task.id=asset.task_id
		LEFT JOIN local_library_entries AS entry ON entry.asset_id=asset.id
		ORDER BY task.created_at DESC, asset.created_at ASC, asset.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list local library records: %w", err)
	}
	defer rows.Close()
	items := make([]record, 0)
	for rows.Next() {
		var item record
		if err := rows.Scan(
			&item.ID, &item.TaskID, &item.Kind, &item.Bucket, &item.ObjectKey,
			&item.OriginalName, &item.ContentType, &item.SizeBytes, &item.ChecksumSHA256,
			&item.AssetStatus, &item.AssetDeletedAt, &item.CreatedAt,
			&item.TaskTitle, &item.OriginalTitle, &item.TaskStatus, &item.TaskArchivedAt,
			&item.TaskUpdatedAt, &item.OriginKind, &item.MonitorID, &item.MonitorName,
			&item.SeriesTitle, &item.SeriesScopeKey, &item.SeriesScopeName, &item.EpisodeNumber,
			&item.RelativePath, &item.LocalStatus, &item.LocalSizeBytes, &item.MaterializedAt,
			&item.LastVerifiedAt, &item.MissingAt, &item.LastError,
		); err != nil {
			return nil, fmt.Errorf("scan local library record: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *postgresStore) ensureEntry(ctx context.Context, item record, relative string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO local_library_entries (
			asset_id, task_id, relative_path, status, created_at, updated_at
		) VALUES ($1,$2,$3,'pending',$4,$4)
		ON CONFLICT (asset_id) DO NOTHING
	`, item.ID, item.TaskID, relative, now)
	return err
}

func (s *postgresStore) setStatus(
	ctx context.Context,
	assetID string,
	status string,
	size int64,
	errorMessage string,
	now time.Time,
) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE local_library_entries
		SET status=$2,
		    local_size_bytes=$3,
		    last_error=$4,
		    materialized_at=CASE WHEN $2='available' THEN COALESCE(materialized_at,$5) ELSE materialized_at END,
		    last_verified_at=CASE WHEN $2 IN ('available','missing','removed') THEN $5 ELSE last_verified_at END,
		    missing_at=CASE WHEN $2 IN ('missing','removed') THEN COALESCE(missing_at,$5) ELSE NULL END,
		    updated_at=$5
		WHERE asset_id=$1
	`, assetID, status, size, errorMessage, now)
	return err
}

func (s *postgresStore) claim(ctx context.Context, assetID string, allowed []string, now time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE local_library_entries
		SET status='syncing', last_error='', updated_at=$3
		WHERE asset_id=$1 AND status=ANY($2::text[])
	`, assetID, allowed, now)
	return tag.RowsAffected() == 1, err
}
