package medialibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type objectReader interface {
	Get(context.Context, string, string, string) (*http.Response, error)
}

type Service struct {
	store    *postgresStore
	objects  objectReader
	root     string
	hostPath string
	logger   *slog.Logger
	now      func() time.Time
}

func NewService(pool *pgxpool.Pool, objects objectReader, root, hostPath string, logger *slog.Logger) *Service {
	return &Service{
		store:    newPostgresStore(pool),
		objects:  objects,
		root:     filepath.Clean(strings.TrimSpace(root)),
		hostPath: strings.TrimSpace(hostPath),
		logger:   logger,
		now:      time.Now,
	}
}

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	value, err := s.store.settings(ctx)
	if err != nil {
		return Settings{}, fmt.Errorf("load local library settings: %w", err)
	}
	return s.decorateSettings(value), nil
}

func (s *Service) UpdateSettings(ctx context.Context, input UpdateSettingsInput) (Settings, error) {
	input.HostPath = strings.TrimSpace(input.HostPath)
	if input.ExpectedVersion < 1 || !validHostPath(input.HostPath) {
		return Settings{}, &ValidationError{Fields: map[string]string{
			"host_path": "请输入有效的本机绝对路径，例如 D:\\Visoraft媒体库",
		}}
	}
	value, err := s.store.updateSettings(ctx, input, s.now().UTC())
	if err != nil {
		return Settings{}, err
	}
	return s.decorateSettings(value), nil
}

func (s *Service) decorateSettings(value Settings) Settings {
	value.HostPath = s.hostPath
	value.Writable = s.rootWritable()
	value.RestartRequired = !sameHostPath(value.RequestedHostPath, value.HostPath)
	return value
}

func sameHostPath(left, right string) bool {
	left = strings.TrimRight(strings.TrimSpace(left), `/\`)
	right = strings.TrimRight(strings.TrimSpace(right), `/\`)
	if left == "" {
		return true
	}
	return strings.EqualFold(left, right)
}

func (s *Service) rootWritable() bool {
	if s.root == "" || s.root == "." {
		return false
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return false
	}
	probe, err := os.CreateTemp(s.root, ".write-check-")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}

func (s *Service) Library(ctx context.Context) (Library, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return Library{}, err
	}
	records, err := s.catalog(ctx, true)
	if err != nil {
		return Library{}, err
	}
	return buildLibrary(records, settings), nil
}

func (s *Service) catalog(ctx context.Context, verify bool) ([]record, error) {
	items, err := s.store.records(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	paths := desiredPaths(items)
	created := false
	for index := range items {
		item := &items[index]
		if item.RelativePath == "" {
			if err := s.store.ensureEntry(ctx, *item, paths[item.ID], now); err != nil {
				return nil, fmt.Errorf("register local library entry: %w", err)
			}
			created = true
		}
	}
	if created {
		items, err = s.store.records(ctx)
		if err != nil {
			return nil, err
		}
	}
	if verify {
		for index := range items {
			if err := s.verifyRecord(ctx, &items[index], now); err != nil {
				s.logger.Warn("local library verification failed", "asset_id", items[index].ID, "error", err)
			}
		}
	}
	return items, nil
}

func desiredPaths(items []record) map[string]string {
	type candidate struct{ dir, name string }
	candidates := make(map[string]candidate, len(items))
	counts := map[string]int{}
	for _, item := range items {
		dir := taskRelativeDirectory(item)
		name := safeFileName(item.OriginalName, item.Kind)
		key := strings.ToLower(filepath.Join(dir, name))
		candidates[item.ID] = candidate{dir: dir, name: name}
		counts[key]++
	}
	result := make(map[string]string, len(items))
	for _, item := range items {
		value := candidates[item.ID]
		name := value.name
		if counts[strings.ToLower(filepath.Join(value.dir, value.name))] > 1 {
			name = withAssetSuffix(name, item.ID)
		}
		result[item.ID] = filepath.Join(value.dir, name)
	}
	return result
}

func (s *Service) verifyRecord(ctx context.Context, item *record, now time.Time) error {
	if item.RelativePath == "" || item.LocalStatus == "syncing" {
		return nil
	}
	target, err := resolveWithinRoot(s.root, item.RelativePath)
	if err != nil {
		return err
	}
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		if item.LocalStatus == "available" {
			item.LocalStatus = "missing"
			item.LocalSizeBytes = 0
			item.MissingAt = &now
			return s.store.setStatus(ctx, item.ID, "missing", 0, "本地文件已被移除", now)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		item.LocalStatus = "error"
		item.LastError = "本地路径不是普通文件"
		return s.store.setStatus(ctx, item.ID, "error", 0, item.LastError, now)
	}
	if item.LocalStatus != "available" || item.LocalSizeBytes != info.Size() {
		item.LocalStatus = "available"
		item.LocalSizeBytes = info.Size()
		item.LastVerifiedAt = &now
		return s.store.setStatus(ctx, item.ID, "available", info.Size(), "", now)
	}
	return nil
}

func (s *Service) Sync(ctx context.Context, assetID string) (Asset, error) {
	items, err := s.catalog(ctx, false)
	if err != nil {
		return Asset{}, err
	}
	for _, item := range items {
		if item.ID == assetID {
			if item.AssetDeletedAt != nil || item.AssetStatus != "available" {
				return Asset{}, &ConflictError{Code: "asset_unavailable", Message: "系统原文件已清理，无法恢复本地副本"}
			}
			claimed, err := s.store.claim(
				ctx,
				item.ID,
				[]string{"pending", "missing", "removed", "error", "available"},
				s.now().UTC(),
			)
			if err != nil {
				return Asset{}, err
			}
			if !claimed {
				return Asset{}, &ConflictError{Code: "local_sync_in_progress", Message: "该文件正在同步，请稍后刷新"}
			}
			item.LocalStatus = "syncing"
			go func(value record) {
				background, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
				defer cancel()
				if syncErr := s.materializeClaimed(background, &value); syncErr != nil {
					s.logger.Warn("manual local media sync failed", "asset_id", value.ID, "error", syncErr)
				}
			}(item)
			item.AbsolutePath = visibleAbsolutePath(s.hostPath, item.RelativePath)
			return item.Asset, nil
		}
	}
	return Asset{}, &ConflictError{Code: "local_asset_not_found", Message: "文件记录不存在"}
}

func (s *Service) Remove(ctx context.Context, assetID string) error {
	items, err := s.catalog(ctx, false)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID != assetID {
			continue
		}
		target, err := resolveWithinRoot(s.root, item.RelativePath)
		if err != nil {
			return err
		}
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete local media file: %w", err)
		}
		s.removeEmptyParents(filepath.Dir(target))
		return s.store.setStatus(ctx, item.ID, "removed", 0, "", s.now().UTC())
	}
	return &ConflictError{Code: "local_asset_not_found", Message: "文件记录不存在"}
}

func (s *Service) removeEmptyParents(directory string) {
	root := filepath.Clean(s.root)
	for directory != root {
		rel, err := filepath.Rel(root, directory)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return
		}
		if err := os.Remove(directory); err != nil {
			return
		}
		directory = filepath.Dir(directory)
	}
}

func (s *Service) materialize(ctx context.Context, item *record, allowed []string) error {
	now := s.now().UTC()
	claimed, err := s.store.claim(ctx, item.ID, allowed, now)
	if err != nil {
		return err
	}
	if !claimed {
		return &ConflictError{Code: "local_sync_in_progress", Message: "该文件正在同步，请稍后刷新"}
	}
	return s.materializeClaimed(ctx, item)
}

func (s *Service) materializeClaimed(ctx context.Context, item *record) error {
	target, err := resolveWithinRoot(s.root, item.RelativePath)
	if err != nil {
		_ = s.store.setStatus(ctx, item.ID, "error", 0, err.Error(), s.now().UTC())
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		_ = s.store.setStatus(ctx, item.ID, "error", 0, err.Error(), s.now().UTC())
		return fmt.Errorf("create local library folder: %w", err)
	}
	partial := target + ".partial-" + shortID(item.ID)
	_ = os.Remove(partial)
	response, err := s.objects.Get(ctx, item.Bucket, item.ObjectKey, "")
	if err != nil {
		_ = s.store.setStatus(ctx, item.ID, "error", 0, err.Error(), s.now().UTC())
		return fmt.Errorf("read system media object: %w", err)
	}
	defer response.Body.Close()
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		_ = s.store.setStatus(ctx, item.ID, "error", 0, err.Error(), s.now().UTC())
		return fmt.Errorf("create local media file: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), response.Body)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(partial)
		if copyErr == nil {
			copyErr = closeErr
		}
		_ = s.store.setStatus(ctx, item.ID, "error", 0, copyErr.Error(), s.now().UTC())
		return fmt.Errorf("write local media file: %w", copyErr)
	}
	if item.SizeBytes > 0 && written != item.SizeBytes {
		_ = os.Remove(partial)
		err = fmt.Errorf("文件大小校验失败：预期 %d 字节，实际 %d 字节", item.SizeBytes, written)
		_ = s.store.setStatus(ctx, item.ID, "error", 0, err.Error(), s.now().UTC())
		return err
	}
	checksum := hex.EncodeToString(hash.Sum(nil))
	if item.ChecksumSHA256 != "" && !strings.EqualFold(checksum, item.ChecksumSHA256) {
		_ = os.Remove(partial)
		err = fmt.Errorf("文件完整性校验失败")
		_ = s.store.setStatus(ctx, item.ID, "error", 0, err.Error(), s.now().UTC())
		return err
	}
	if err := os.Rename(partial, target); err != nil {
		_ = os.Remove(partial)
		_ = s.store.setStatus(ctx, item.ID, "error", 0, err.Error(), s.now().UTC())
		return fmt.Errorf("publish local media file: %w", err)
	}
	item.LocalStatus = "available"
	item.LocalSizeBytes = written
	item.AbsolutePath = visibleAbsolutePath(s.hostPath, item.RelativePath)
	return s.store.setStatus(ctx, item.ID, "available", written, "", s.now().UTC())
}

func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if interval < time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := s.syncOnePending(ctx); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Warn("automatic local media sync failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) syncOnePending(ctx context.Context) error {
	settings, err := s.Settings(ctx)
	if err != nil || !settings.AutoSync || !settings.Writable || settings.RestartRequired {
		return err
	}
	items, err := s.catalog(ctx, false)
	if err != nil {
		return err
	}
	for index := range items {
		item := &items[index]
		if item.LocalStatus == "pending" && item.AssetStatus == "available" && item.AssetDeletedAt == nil {
			return s.materialize(ctx, item, []string{"pending"})
		}
	}
	return nil
}

func buildLibrary(items []record, settings Settings) Library {
	library := Library{Settings: settings, Collections: []Collection{}}
	collectionIndex := map[string]int{}
	folderIndex := map[string]map[string]int{}
	for _, item := range items {
		collectionKey := "manual"
		collectionKind := "manual"
		collectionTitle := "独立任务"
		if item.OriginKind == "monitor" && item.MonitorID != "" {
			collectionKey = "monitor:" + item.MonitorID
			collectionKind = "monitor"
			collectionTitle = monitorCollectionTitle(item)
		}
		ci, exists := collectionIndex[collectionKey]
		if !exists {
			ci = len(library.Collections)
			collectionIndex[collectionKey] = ci
			folderIndex[collectionKey] = map[string]int{}
			library.Collections = append(library.Collections, Collection{
				Key: collectionKey, Kind: collectionKind, Title: collectionTitle,
				MonitorID: item.MonitorID, SeriesTitle: item.SeriesTitle, Folders: []TaskFolder{},
			})
		}
		collection := &library.Collections[ci]
		if collection.Title == "未命名监控" && collectionTitle != "未命名监控" {
			collection.Title = collectionTitle
		}
		fi, exists := folderIndex[collectionKey][item.TaskID]
		if !exists {
			fi = len(collection.Folders)
			folderIndex[collectionKey][item.TaskID] = fi
			title := strings.TrimSpace(item.TaskTitle)
			if title == "" {
				title = strings.TrimSpace(item.OriginalTitle)
			}
			if title == "" {
				title = "未命名任务"
			}
			dir := filepath.Dir(item.RelativePath)
			collection.Folders = append(collection.Folders, TaskFolder{
				TaskID: item.TaskID, Title: title, Status: item.TaskStatus,
				Archived: item.TaskArchivedAt != nil, EpisodeNumber: item.EpisodeNumber,
				SeriesScopeKey: item.SeriesScopeKey, SeriesScope: scopeLabel(item.SeriesScopeKey, item.SeriesScopeName),
				RelativePath: dir, AbsolutePath: visibleAbsolutePath(settings.HostPath, dir),
				UpdatedAt: item.TaskUpdatedAt, Files: []Asset{},
			})
			collection.FolderCount++
			library.FolderCount++
		}
		folder := &collection.Folders[fi]
		asset := item.Asset
		asset.AbsolutePath = visibleAbsolutePath(settings.HostPath, asset.RelativePath)
		folder.Files = append(folder.Files, asset)
		folder.FileCount++
		folder.TotalBytes += item.SizeBytes
		collection.FileCount++
		collection.TotalBytes += item.SizeBytes
		library.FileCount++
		library.TotalBytes += item.SizeBytes
		switch item.LocalStatus {
		case "available":
			folder.AvailableCount++
			folder.LocalBytes += item.LocalSizeBytes
			collection.AvailableCount++
			collection.LocalBytes += item.LocalSizeBytes
			library.AvailableCount++
			library.LocalBytes += item.LocalSizeBytes
		case "missing", "removed":
			folder.MissingCount++
			collection.MissingCount++
			library.MissingCount++
		default:
			folder.PendingCount++
			collection.PendingCount++
			library.PendingCount++
		}
	}
	for index := range library.Collections {
		collection := &library.Collections[index]
		sort.SliceStable(collection.Folders, func(left, right int) bool {
			l, r := collection.Folders[left], collection.Folders[right]
			lScope := strings.TrimSpace(l.SeriesScopeKey)
			if lScope == "" {
				lScope = strings.TrimSpace(l.SeriesScope)
			}
			rScope := strings.TrimSpace(r.SeriesScopeKey)
			if rScope == "" {
				rScope = strings.TrimSpace(r.SeriesScope)
			}
			if lScope != rScope {
				return lScope < rScope
			}
			if l.EpisodeNumber > 0 && r.EpisodeNumber > 0 && l.EpisodeNumber != r.EpisodeNumber {
				return l.EpisodeNumber < r.EpisodeNumber
			}
			return l.UpdatedAt.After(r.UpdatedAt)
		})
	}
	sort.SliceStable(library.Collections, func(left, right int) bool {
		if library.Collections[left].Kind != library.Collections[right].Kind {
			return library.Collections[left].Kind == "monitor"
		}
		return library.Collections[left].Title < library.Collections[right].Title
	})
	library.CollectionCount = len(library.Collections)
	return library
}
