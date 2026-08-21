package medialibrary

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskRelativeDirectoryGroupsMonitorEpisodes(t *testing.T) {
	value := taskRelativeDirectory(record{
		Asset:      Asset{TaskID: "12345678-0000-0000-0000-000000000000"},
		TaskTitle:  "第二集: 重逢?",
		OriginKind: "monitor", MonitorID: "87654321-0000-0000-0000-000000000000",
		MonitorName: "还珠格格全集", SeriesTitle: "还珠格格", SeriesScopeName: "第二部",
		EpisodeNumber: 2,
	})
	want := filepath.Join("监控任务", "还珠格格_87654321", "第二部", "第02集_第二集 重逢_12345678")
	if value != want {
		t.Fatalf("unexpected path: %q want %q", value, want)
	}
}

func TestBuildLibraryGroupsMonitorAndManualTasks(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	items := []record{
		{
			Asset:     Asset{ID: "asset-monitor", TaskID: "task-monitor", OriginalName: "source.mp4", SizeBytes: 100, LocalStatus: "available", RelativePath: filepath.Join("监控任务", "节目", "source.mp4"), LocalSizeBytes: 100},
			TaskTitle: "第二集", TaskUpdatedAt: now, OriginKind: "monitor", MonitorID: "monitor-id", MonitorName: "节目监控", SeriesTitle: "节目", EpisodeNumber: 2,
		},
		{
			Asset:     Asset{ID: "asset-manual", TaskID: "task-manual", OriginalName: "cover.jpg", SizeBytes: 20, LocalStatus: "missing", RelativePath: filepath.Join("独立任务", "单条", "cover.jpg")},
			TaskTitle: "单条视频", TaskUpdatedAt: now.Add(-time.Minute), OriginKind: "manual",
		},
	}
	library := buildLibrary(items, Settings{HostPath: `D:\媒体库`})
	if library.CollectionCount != 2 || library.FolderCount != 2 || library.FileCount != 2 {
		t.Fatalf("unexpected totals: %#v", library)
	}
	if library.Collections[0].Kind != "monitor" || library.Collections[0].Title != "节目" {
		t.Fatalf("monitor collection was not grouped first: %#v", library.Collections[0])
	}
	if library.Collections[0].Folders[0].EpisodeNumber != 2 {
		t.Fatalf("episode number was not preserved: %#v", library.Collections[0].Folders[0])
	}
	if library.Collections[1].Kind != "manual" || library.Collections[1].Title != "独立任务" {
		t.Fatalf("manual task collection missing: %#v", library.Collections[1])
	}
}

func TestBuildLibraryReplacesUnreadableLegacyMonitorName(t *testing.T) {
	library := buildLibrary([]record{{
		Asset:     Asset{ID: "asset", TaskID: "task", OriginalName: "source.mp4", RelativePath: filepath.Join("监控任务", "未知", "source.mp4")},
		TaskTitle: "视频", TaskUpdatedAt: time.Now(), OriginKind: "monitor", MonitorID: "monitor-id", MonitorName: "????", SeriesTitle: "???",
	}}, Settings{})
	if got := library.Collections[0].Title; got != "未命名监控" {
		t.Fatalf("unexpected legacy monitor fallback: %q", got)
	}
}

func TestBuildLibraryInfersUnreadableMonitorNameFromTaskTitle(t *testing.T) {
	library := buildLibrary([]record{{
		Asset: Asset{
			ID: "asset", TaskID: "task", OriginalName: "source.mp4",
			RelativePath: filepath.Join("监控任务", "未知", "source.mp4"),
		},
		TaskTitle:     "MULTISUB【山河令 Word Of Honor】EP02 | 古装剧情",
		TaskUpdatedAt: time.Now(), OriginKind: "monitor", MonitorID: "monitor-id",
		MonitorName: "?????", SeriesTitle: "???",
	}}, Settings{})
	if got := library.Collections[0].Title; got != "山河令 Word Of Honor" {
		t.Fatalf("unexpected inferred monitor title: %q", got)
	}
}

func TestResolveWithinRootRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if _, err := resolveWithinRoot(root, filepath.Join("..", "outside.mp4")); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
	inside, err := resolveWithinRoot(root, filepath.Join("独立任务", "视频", "source.mp4"))
	if err != nil || !strings.HasPrefix(inside, root) {
		t.Fatalf("expected safe path, got %q %v", inside, err)
	}
}

func TestVisibleAbsolutePathUsesWindowsSeparators(t *testing.T) {
	got := visibleAbsolutePath(`D:\媒体库`, filepath.Join("监控任务", "节目", "source.mp4"))
	if got != `D:\媒体库\监控任务\节目\source.mp4` {
		t.Fatalf("unexpected visible path: %q", got)
	}
}

func TestScopeLabelUsesReadableGenericFallback(t *testing.T) {
	if got := scopeLabel("part-2", ""); got != "第 2 部" {
		t.Fatalf("unexpected part label: %q", got)
	}
	if got := scopeLabel("season_3", ""); got != "第 3 季" {
		t.Fatalf("unexpected season label: %q", got)
	}
	if got := scopeLabel("part-2", "续集"); got != "续集" {
		t.Fatalf("explicit scope label should win: %q", got)
	}
}
