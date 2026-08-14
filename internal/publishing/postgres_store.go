package publishing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("publishing resource not found")
var ErrVersionConflict = errors.New("publishing resource version conflict")

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

type rowScanner interface {
	Scan(dest ...any) error
}

const accountSelect = `
	SELECT
		id::text,
		platform,
		name,
		auth_mode,
		cookie_profile_id::text,
		status,
		remote_user_id,
		remote_display_name,
		adapter_version,
		last_checked_at,
		last_error_code,
		last_error_message,
		version,
		created_at,
		updated_at
	FROM platform_accounts
`

func scanAccount(row rowScanner) (Account, error) {
	var item Account
	if err := row.Scan(
		&item.ID,
		&item.Platform,
		&item.Name,
		&item.AuthMode,
		&item.CookieProfileID,
		&item.Status,
		&item.RemoteUserID,
		&item.RemoteDisplayName,
		&item.AdapterVersion,
		&item.LastCheckedAt,
		&item.LastErrorCode,
		&item.LastErrorMessage,
		&item.Version,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Account{}, ErrNotFound
		}
		return Account{}, fmt.Errorf("scan platform account: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) ListAccounts(
	ctx context.Context,
	platform string,
) ([]Account, error) {
	query := accountSelect + `
		WHERE archived_at IS NULL
		  AND ($1='' OR platform=$1)
		ORDER BY platform, lower(name), created_at
	`
	rows, err := s.pool.Query(ctx, query, platform)
	if err != nil {
		return nil, fmt.Errorf("list platform accounts: %w", err)
	}
	defer rows.Close()
	result := make([]Account, 0)
	for rows.Next() {
		item, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate platform accounts: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) GetAccount(ctx context.Context, id string) (Account, error) {
	return scanAccount(s.pool.QueryRow(
		ctx,
		accountSelect+" WHERE id=$1 AND archived_at IS NULL",
		id,
	))
}

func (s *PostgresStore) CreateAccount(
	ctx context.Context,
	id string,
	input CreateAccountInput,
	now time.Time,
) (Account, error) {
	query := `
		INSERT INTO platform_accounts (
			id, platform, name, auth_mode, cookie_profile_id, status,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,'unchecked',$6,$6)
		RETURNING
			id::text, platform, name, auth_mode, cookie_profile_id::text,
			status, remote_user_id, remote_display_name, adapter_version,
			last_checked_at, last_error_code, last_error_message, version,
			created_at, updated_at
	`
	return scanAccount(s.pool.QueryRow(
		ctx,
		query,
		id,
		input.Platform,
		input.Name,
		input.AuthMode,
		input.CookieProfileID,
		now,
	))
}

func (s *PostgresStore) UpdateAccount(
	ctx context.Context,
	id string,
	input UpdateAccountInput,
	now time.Time,
) (Account, error) {
	query := `
		UPDATE platform_accounts
		SET
			name=$3,
			cookie_profile_id=$4,
			status='unchecked',
			remote_user_id='',
			remote_display_name='',
			last_error_code='',
			last_error_message='',
			last_checked_at=NULL,
			version=version+1,
			updated_at=$5
		WHERE id=$1 AND version=$2 AND archived_at IS NULL
		RETURNING
			id::text, platform, name, auth_mode, cookie_profile_id::text,
			status, remote_user_id, remote_display_name, adapter_version,
			last_checked_at, last_error_code, last_error_message, version,
			created_at, updated_at
	`
	item, err := scanAccount(s.pool.QueryRow(
		ctx,
		query,
		id,
		input.ExpectedVersion,
		input.Name,
		input.CookieProfileID,
		now,
	))
	if errors.Is(err, ErrNotFound) {
		return Account{}, s.notFoundOrConflict(ctx, "platform_accounts", id)
	}
	return item, err
}

func (s *PostgresStore) SaveAccountCheck(
	ctx context.Context,
	id string,
	status string,
	remoteUserID string,
	remoteDisplayName string,
	adapterVersion string,
	errorCode string,
	errorMessage string,
	now time.Time,
) (Account, error) {
	query := `
		UPDATE platform_accounts
		SET
			status=$2,
			remote_user_id=$3,
			remote_display_name=$4,
			adapter_version=$5,
			last_checked_at=$6,
			last_error_code=$7,
			last_error_message=$8,
			version=version+1,
			updated_at=$6
		WHERE id=$1 AND archived_at IS NULL
		RETURNING
			id::text, platform, name, auth_mode, cookie_profile_id::text,
			status, remote_user_id, remote_display_name, adapter_version,
			last_checked_at, last_error_code, last_error_message, version,
			created_at, updated_at
	`
	return scanAccount(s.pool.QueryRow(
		ctx,
		query,
		id,
		status,
		remoteUserID,
		remoteDisplayName,
		adapterVersion,
		now,
		errorCode,
		errorMessage,
	))
}

func (s *PostgresStore) ArchiveAccount(
	ctx context.Context,
	id string,
	expectedVersion int64,
	now time.Time,
) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE platform_accounts
		SET
			status='archived',
			archived_at=$3,
			updated_at=$3,
			version=version+1
		WHERE id=$1 AND version=$2 AND archived_at IS NULL
	`, id, expectedVersion, now)
	if err != nil {
		return fmt.Errorf("archive platform account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return s.notFoundOrConflict(ctx, "platform_accounts", id)
	}
	return nil
}

func (s *PostgresStore) CookieProfileExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM cookie_profiles
			WHERE id=$1 AND status='ready'
		)
	`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check cookie profile for platform account: %w", err)
	}
	return exists, nil
}

func (s *PostgresStore) ListCategories(
	ctx context.Context,
	platform string,
) ([]Category, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			platform, category_id, parent_id, name, path, active,
			sort_order, metadata, refreshed_at
		FROM platform_categories
		WHERE platform=$1
		ORDER BY sort_order, path, name
	`, platform)
	if err != nil {
		return nil, fmt.Errorf("list platform categories: %w", err)
	}
	defer rows.Close()
	result := make([]Category, 0)
	for rows.Next() {
		var item Category
		var metadataRaw []byte
		if err := rows.Scan(
			&item.Platform,
			&item.CategoryID,
			&item.ParentID,
			&item.Name,
			&item.Path,
			&item.Active,
			&item.SortOrder,
			&metadataRaw,
			&item.RefreshedAt,
		); err != nil {
			return nil, fmt.Errorf("scan platform category: %w", err)
		}
		if err := json.Unmarshal(metadataRaw, &item.Metadata); err != nil {
			return nil, fmt.Errorf("decode platform category metadata: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate platform categories: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) ReplaceCategories(
	ctx context.Context,
	platform string,
	items []Category,
	now time.Time,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin category replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(
		ctx,
		`UPDATE platform_categories SET active=false, refreshed_at=$2 WHERE platform=$1`,
		platform,
		now,
	); err != nil {
		return fmt.Errorf("deactivate old platform categories: %w", err)
	}
	for _, item := range items {
		metadataRaw, err := json.Marshal(item.Metadata)
		if err != nil {
			return fmt.Errorf("encode platform category metadata: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO platform_categories (
				platform, category_id, parent_id, name, path, active,
				sort_order, metadata, refreshed_at
			) VALUES ($1,$2,$3,$4,$5,true,$6,$7,$8)
			ON CONFLICT (platform, category_id) DO UPDATE SET
				parent_id=EXCLUDED.parent_id,
				name=EXCLUDED.name,
				path=EXCLUDED.path,
				active=true,
				sort_order=EXCLUDED.sort_order,
				metadata=EXCLUDED.metadata,
				refreshed_at=EXCLUDED.refreshed_at
		`,
			platform,
			item.CategoryID,
			item.ParentID,
			item.Name,
			item.Path,
			item.SortOrder,
			metadataRaw,
			now,
		); err != nil {
			return fmt.Errorf("upsert platform category: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit platform category replacement: %w", err)
	}
	return nil
}

func (s *PostgresStore) notFoundOrConflict(
	ctx context.Context,
	table string,
	id string,
) error {
	query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE id=$1)", table)
	var exists bool
	if err := s.pool.QueryRow(ctx, query, id).Scan(&exists); err != nil {
		return fmt.Errorf("check publishing resource after update: %w", err)
	}
	if exists {
		return ErrVersionConflict
	}
	return ErrNotFound
}
