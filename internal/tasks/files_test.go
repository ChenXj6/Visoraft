package tasks

import (
	"testing"
	"time"
)

func TestBuildFileLibraryAggregatesPersistedAssets(t *testing.T) {
	now := time.Date(2026, 8, 14, 3, 0, 0, 0, time.UTC)
	deletedAt := now.Add(-time.Minute)
	archivedAt := now.Add(-time.Hour)

	library := BuildFileLibrary([]Task{
		{
			ID:        "task-active",
			Title:     "示例视频",
			Status:    StatusAwaitingReview,
			UpdatedAt: now,
			Assets: []MediaAsset{
				{ID: "source", Status: "available", SizeBytes: 100},
				{ID: "cover", Status: "available", SizeBytes: 20},
				{ID: "old", Status: "deleted", SizeBytes: 999, DeletedAt: &deletedAt},
			},
		},
		{
			ID:            "task-archived",
			OriginalTitle: "已归档视频",
			Status:        StatusCancelled,
			ArchivedAt:    &archivedAt,
			UpdatedAt:     archivedAt,
			Assets:        []MediaAsset{{ID: "subtitle", Status: "available", SizeBytes: 5}},
		},
		{ID: "task-empty", Title: "无文件任务", UpdatedAt: now},
	})

	if library.FolderCount != 2 || library.FileCount != 4 {
		t.Fatalf("unexpected library counts: %+v", library)
	}
	if library.AvailableCount != 3 || library.DeletedCount != 1 || library.TotalBytes != 125 {
		t.Fatalf("unexpected library totals: %+v", library)
	}
	if library.Folders[0].Title != "示例视频" || library.Folders[0].TotalBytes != 120 {
		t.Fatalf("unexpected active folder: %+v", library.Folders[0])
	}
	if !library.Folders[1].Archived || library.Folders[1].Title != "已归档视频" {
		t.Fatalf("unexpected archived folder: %+v", library.Folders[1])
	}
}
