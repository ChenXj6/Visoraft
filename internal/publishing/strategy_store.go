package publishing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const presetSelect = `
	SELECT
		id::text,
		name,
		enabled,
		encoder_mode,
		video_codec,
		audio_codec,
		container,
		cpu_preset,
		high_resolution_cpu_preset,
		maximum_height,
		video_bitrate_kbps,
		audio_bitrate_kbps,
		burn_subtitles,
		custom_arguments,
		version,
		created_at,
		updated_at
	FROM transcode_presets
`

func scanPreset(row rowScanner) (TranscodePreset, error) {
	var item TranscodePreset
	if err := row.Scan(
		&item.ID,
		&item.Name,
		&item.Enabled,
		&item.EncoderMode,
		&item.VideoCodec,
		&item.AudioCodec,
		&item.Container,
		&item.CPUPreset,
		&item.HighResolutionCPUPreset,
		&item.MaximumHeight,
		&item.VideoBitrateKbps,
		&item.AudioBitrateKbps,
		&item.BurnSubtitles,
		&item.CustomArguments,
		&item.Version,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TranscodePreset{}, ErrNotFound
		}
		return TranscodePreset{}, fmt.Errorf("scan transcode preset: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) ListTranscodePresets(
	ctx context.Context,
) ([]TranscodePreset, error) {
	rows, err := s.pool.Query(
		ctx,
		presetSelect+" WHERE archived_at IS NULL ORDER BY enabled DESC, lower(name)",
	)
	if err != nil {
		return nil, fmt.Errorf("list transcode presets: %w", err)
	}
	defer rows.Close()
	result := make([]TranscodePreset, 0)
	for rows.Next() {
		item, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transcode presets: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) GetTranscodePreset(
	ctx context.Context,
	id string,
) (TranscodePreset, error) {
	return scanPreset(s.pool.QueryRow(
		ctx,
		presetSelect+" WHERE id=$1 AND archived_at IS NULL",
		id,
	))
}

func (s *PostgresStore) CreateTranscodePreset(
	ctx context.Context,
	id string,
	input TranscodePresetInput,
	now time.Time,
) (TranscodePreset, error) {
	return scanPreset(s.pool.QueryRow(ctx, `
		INSERT INTO transcode_presets (
			id, name, enabled, encoder_mode, video_codec, audio_codec,
			container, cpu_preset, high_resolution_cpu_preset, maximum_height,
			video_bitrate_kbps, audio_bitrate_kbps, burn_subtitles,
			custom_arguments, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15
		)
		RETURNING
			id::text, name, enabled, encoder_mode, video_codec, audio_codec,
			container, cpu_preset, high_resolution_cpu_preset, maximum_height,
			video_bitrate_kbps, audio_bitrate_kbps, burn_subtitles,
			custom_arguments, version, created_at, updated_at
	`,
		id,
		input.Name,
		input.Enabled,
		input.EncoderMode,
		input.VideoCodec,
		input.AudioCodec,
		input.Container,
		input.CPUPreset,
		input.HighResolutionCPUPreset,
		input.MaximumHeight,
		input.VideoBitrateKbps,
		input.AudioBitrateKbps,
		input.BurnSubtitles,
		input.CustomArguments,
		now,
	))
}

func (s *PostgresStore) UpdateTranscodePreset(
	ctx context.Context,
	id string,
	input UpdateTranscodePresetInput,
	now time.Time,
) (TranscodePreset, error) {
	item, err := scanPreset(s.pool.QueryRow(ctx, `
		UPDATE transcode_presets
		SET
			name=$3,
			enabled=$4,
			encoder_mode=$5,
			video_codec=$6,
			audio_codec=$7,
			container=$8,
			cpu_preset=$9,
			high_resolution_cpu_preset=$10,
			maximum_height=$11,
			video_bitrate_kbps=$12,
			audio_bitrate_kbps=$13,
			burn_subtitles=$14,
			custom_arguments=$15,
			version=version+1,
			updated_at=$16
		WHERE id=$1 AND version=$2 AND archived_at IS NULL
		RETURNING
			id::text, name, enabled, encoder_mode, video_codec, audio_codec,
			container, cpu_preset, high_resolution_cpu_preset, maximum_height,
			video_bitrate_kbps, audio_bitrate_kbps, burn_subtitles,
			custom_arguments, version, created_at, updated_at
	`,
		id,
		input.ExpectedVersion,
		input.Name,
		input.Enabled,
		input.EncoderMode,
		input.VideoCodec,
		input.AudioCodec,
		input.Container,
		input.CPUPreset,
		input.HighResolutionCPUPreset,
		input.MaximumHeight,
		input.VideoBitrateKbps,
		input.AudioBitrateKbps,
		input.BurnSubtitles,
		input.CustomArguments,
		now,
	))
	if errors.Is(err, ErrNotFound) {
		return TranscodePreset{}, s.notFoundOrConflict(ctx, "transcode_presets", id)
	}
	return item, err
}

func (s *PostgresStore) ArchiveTranscodePreset(
	ctx context.Context,
	id string,
	expectedVersion int64,
	now time.Time,
) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE transcode_presets
		SET archived_at=$3, enabled=false, version=version+1, updated_at=$3
		WHERE id=$1 AND version=$2 AND archived_at IS NULL
	`, id, expectedVersion, now)
	if err != nil {
		return fmt.Errorf("archive transcode preset: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return s.notFoundOrConflict(ctx, "transcode_presets", id)
	}
	return nil
}

const strategySelect = `
	SELECT
		id::text,
		name,
		enabled,
		automation_mode,
		target_platforms,
		account_bindings,
		category_bindings,
		title_templates,
		description_templates,
		default_tags,
		repost_statement_version,
		transcode_preset_id::text,
		require_content_moderation,
		schedule_mode,
		to_char(schedule_time, 'HH24:MI'),
		version,
		created_at,
		updated_at
	FROM posting_strategies
`

func scanStrategy(row rowScanner) (PostingStrategy, error) {
	var item PostingStrategy
	var accountRaw, categoryRaw, titleRaw, descriptionRaw []byte
	if err := row.Scan(
		&item.ID,
		&item.Name,
		&item.Enabled,
		&item.AutomationMode,
		&item.TargetPlatforms,
		&accountRaw,
		&categoryRaw,
		&titleRaw,
		&descriptionRaw,
		&item.DefaultTags,
		&item.RepostStatementVersion,
		&item.TranscodePresetID,
		&item.RequireContentModeration,
		&item.ScheduleMode,
		&item.ScheduleTime,
		&item.Version,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PostingStrategy{}, ErrNotFound
		}
		return PostingStrategy{}, fmt.Errorf("scan posting strategy: %w", err)
	}
	for name, target := range map[string]*map[string]string{
		"account bindings":      &item.AccountBindings,
		"category bindings":     &item.CategoryBindings,
		"title templates":       &item.TitleTemplates,
		"description templates": &item.DescriptionTemplates,
	} {
		var raw []byte
		switch name {
		case "account bindings":
			raw = accountRaw
		case "category bindings":
			raw = categoryRaw
		case "title templates":
			raw = titleRaw
		default:
			raw = descriptionRaw
		}
		if err := json.Unmarshal(raw, target); err != nil {
			return PostingStrategy{}, fmt.Errorf("decode %s: %w", name, err)
		}
	}
	return item, nil
}

func (s *PostgresStore) ListPostingStrategies(
	ctx context.Context,
) ([]PostingStrategy, error) {
	rows, err := s.pool.Query(
		ctx,
		strategySelect+" WHERE archived_at IS NULL ORDER BY enabled DESC, lower(name)",
	)
	if err != nil {
		return nil, fmt.Errorf("list posting strategies: %w", err)
	}
	defer rows.Close()
	result := make([]PostingStrategy, 0)
	for rows.Next() {
		item, err := scanStrategy(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate posting strategies: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) GetPostingStrategy(
	ctx context.Context,
	id string,
) (PostingStrategy, error) {
	return scanStrategy(s.pool.QueryRow(
		ctx,
		strategySelect+" WHERE id=$1 AND archived_at IS NULL",
		id,
	))
}

func encodeStrategyMaps(input PostingStrategyInput) ([][]byte, error) {
	values := []map[string]string{
		input.AccountBindings,
		input.CategoryBindings,
		input.TitleTemplates,
		input.DescriptionTemplates,
	}
	result := make([][]byte, len(values))
	for index, value := range values {
		if value == nil {
			value = map[string]string{}
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode posting strategy map: %w", err)
		}
		result[index] = raw
	}
	return result, nil
}

func scheduleTimeValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *PostgresStore) CreatePostingStrategy(
	ctx context.Context,
	id string,
	input PostingStrategyInput,
	now time.Time,
) (PostingStrategy, error) {
	maps, err := encodeStrategyMaps(input)
	if err != nil {
		return PostingStrategy{}, err
	}
	return scanStrategy(s.pool.QueryRow(ctx, `
		INSERT INTO posting_strategies (
			id, name, enabled, automation_mode, target_platforms,
			account_bindings, category_bindings, title_templates,
			description_templates, default_tags, repost_statement_version,
			transcode_preset_id, require_content_moderation, schedule_mode,
			schedule_time, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,
			NULLIF($15,'')::time,$16,$16
		)
		RETURNING
			id::text, name, enabled, automation_mode, target_platforms,
			account_bindings, category_bindings, title_templates,
			description_templates, default_tags, repost_statement_version,
			transcode_preset_id::text, require_content_moderation,
			schedule_mode, to_char(schedule_time, 'HH24:MI'),
			version, created_at, updated_at
	`,
		id,
		input.Name,
		input.Enabled,
		input.AutomationMode,
		input.TargetPlatforms,
		maps[0],
		maps[1],
		maps[2],
		maps[3],
		input.DefaultTags,
		input.RepostStatementVersion,
		input.TranscodePresetID,
		input.RequireContentModeration,
		input.ScheduleMode,
		scheduleTimeValue(input.ScheduleTime),
		now,
	))
}

func (s *PostgresStore) UpdatePostingStrategy(
	ctx context.Context,
	id string,
	input UpdatePostingStrategyInput,
	now time.Time,
) (PostingStrategy, error) {
	maps, err := encodeStrategyMaps(input.PostingStrategyInput)
	if err != nil {
		return PostingStrategy{}, err
	}
	item, err := scanStrategy(s.pool.QueryRow(ctx, `
		UPDATE posting_strategies
		SET
			name=$3,
			enabled=$4,
			automation_mode=$5,
			target_platforms=$6,
			account_bindings=$7,
			category_bindings=$8,
			title_templates=$9,
			description_templates=$10,
			default_tags=$11,
			repost_statement_version=$12,
			transcode_preset_id=$13,
			require_content_moderation=$14,
			schedule_mode=$15,
			schedule_time=NULLIF($16,'')::time,
			version=version+1,
			updated_at=$17
		WHERE id=$1 AND version=$2 AND archived_at IS NULL
		RETURNING
			id::text, name, enabled, automation_mode, target_platforms,
			account_bindings, category_bindings, title_templates,
			description_templates, default_tags, repost_statement_version,
			transcode_preset_id::text, require_content_moderation,
			schedule_mode, to_char(schedule_time, 'HH24:MI'),
			version, created_at, updated_at
	`,
		id,
		input.ExpectedVersion,
		input.Name,
		input.Enabled,
		input.AutomationMode,
		input.TargetPlatforms,
		maps[0],
		maps[1],
		maps[2],
		maps[3],
		input.DefaultTags,
		input.RepostStatementVersion,
		input.TranscodePresetID,
		input.RequireContentModeration,
		input.ScheduleMode,
		scheduleTimeValue(input.ScheduleTime),
		now,
	))
	if errors.Is(err, ErrNotFound) {
		return PostingStrategy{}, s.notFoundOrConflict(ctx, "posting_strategies", id)
	}
	return item, err
}

func (s *PostgresStore) ArchivePostingStrategy(
	ctx context.Context,
	id string,
	expectedVersion int64,
	now time.Time,
) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE posting_strategies
		SET archived_at=$3, enabled=false, version=version+1, updated_at=$3
		WHERE id=$1 AND version=$2 AND archived_at IS NULL
	`, id, expectedVersion, now)
	if err != nil {
		return fmt.Errorf("archive posting strategy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return s.notFoundOrConflict(ctx, "posting_strategies", id)
	}
	return nil
}
