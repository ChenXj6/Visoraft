package cookieprofiles

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) Create(ctx context.Context, value record) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO cookie_profiles (
			id, name, kind, status, encrypted_cookie_jar, cloud_server_url,
			encrypted_cloud_credentials, source_filename, cookie_count,
			domain_count, last_synced_at, last_error, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,COALESCE($5, '\x'::bytea),$6,
			COALESCE($7, '\x'::bytea),$8,$9,$10,$11,$12,$13,$14
		)
	`,
		value.ID,
		value.Name,
		value.Kind,
		value.Status,
		value.EncryptedCookieJar,
		value.ServerURL,
		value.EncryptedCloudCredentials,
		value.SourceFilename,
		value.CookieCount,
		value.DomainCount,
		value.LastSyncedAt,
		value.LastError,
		value.CreatedAt,
		value.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert cookie profile: %w", err)
	}
	return nil
}

func (s *PostgresStore) List(ctx context.Context) ([]Profile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, name, kind, status, cloud_server_url, source_filename,
		       cookie_count, domain_count, octet_length(encrypted_cookie_jar) > 0,
		       last_synced_at, last_error, created_at, updated_at
		FROM cookie_profiles
		ORDER BY updated_at DESC, name
	`)
	if err != nil {
		return nil, fmt.Errorf("list cookie profiles: %w", err)
	}
	defer rows.Close()
	result := make([]Profile, 0)
	for rows.Next() {
		var profile Profile
		if err := rows.Scan(
			&profile.ID,
			&profile.Name,
			&profile.Kind,
			&profile.Status,
			&profile.ServerURL,
			&profile.SourceFilename,
			&profile.CookieCount,
			&profile.DomainCount,
			&profile.HasUsableCookies,
			&profile.LastSyncedAt,
			&profile.LastError,
			&profile.CreatedAt,
			&profile.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan cookie profile: %w", err)
		}
		result = append(result, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cookie profiles: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) Get(ctx context.Context, id string) (record, error) {
	var value record
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, name, kind, status, cloud_server_url, source_filename,
		       cookie_count, domain_count, octet_length(encrypted_cookie_jar) > 0,
		       last_synced_at, last_error, created_at, updated_at,
		       encrypted_cookie_jar, encrypted_cloud_credentials
		FROM cookie_profiles
		WHERE id=$1
	`, id).Scan(
		&value.ID,
		&value.Name,
		&value.Kind,
		&value.Status,
		&value.ServerURL,
		&value.SourceFilename,
		&value.CookieCount,
		&value.DomainCount,
		&value.HasUsableCookies,
		&value.LastSyncedAt,
		&value.LastError,
		&value.CreatedAt,
		&value.UpdatedAt,
		&value.EncryptedCookieJar,
		&value.EncryptedCloudCredentials,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return record{}, ErrNotFound
	}
	if err != nil {
		return record{}, fmt.Errorf("get cookie profile: %w", err)
	}
	return value, nil
}

func (s *PostgresStore) MarkSyncing(ctx context.Context, id string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE cookie_profiles
		SET status='syncing', last_error='', updated_at=$2
		WHERE id=$1 AND kind='cookiecloud'
	`, id, now)
	if err != nil {
		return fmt.Errorf("mark CookieCloud profile syncing: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) CompleteSync(
	ctx context.Context,
	id string,
	encryptedJar []byte,
	cookieCount int,
	domainCount int,
	now time.Time,
) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE cookie_profiles
		SET status='ready',
		    encrypted_cookie_jar=$2,
		    cookie_count=$3,
		    domain_count=$4,
		    last_synced_at=$5,
		    last_error='',
		    updated_at=$5
		WHERE id=$1 AND kind='cookiecloud'
	`, id, encryptedJar, cookieCount, domainCount, now)
	if err != nil {
		return fmt.Errorf("complete CookieCloud sync: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) FailSync(ctx context.Context, id string, message string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE cookie_profiles
		SET status='error', last_error=$2, updated_at=$3
		WHERE id=$1 AND kind='cookiecloud'
	`, id, message, now)
	if err != nil {
		return fmt.Errorf("record CookieCloud sync failure: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM cookie_profiles WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete cookie profile: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
