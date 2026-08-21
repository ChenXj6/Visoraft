const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const baseURL = process.env.VISORAFT_API_URL || "http://127.0.0.1:8080";
const artifactDir = path.resolve(
  __dirname,
  "../../artifacts/v1/test-runs/local-library/automated"
);

async function get(pathname) {
  const response = await fetch(`${baseURL}${pathname}`);
  assert.ok(response.ok, `${pathname} 返回 HTTP ${response.status}`);
  return response.json();
}

function isAbsolutePath(value) {
  return /^([A-Za-z]:[\\/]|[/]|\\\\)/.test(value);
}

async function main() {
  const [settings, library, tasks] = await Promise.all([
    get("/api/v1/library/settings"),
    get("/api/v1/files"),
    get("/api/v1/tasks?limit=100")
  ]);

  assert.ok(isAbsolutePath(settings.host_path), `未返回电脑绝对路径: ${settings.host_path}`);
  assert.equal(typeof settings.auto_sync, "boolean");
  assert.equal(typeof settings.writable, "boolean");
  assert.equal(library.collection_count, library.collections.length);
  assert.ok(library.folder_count >= 1, "缺少可核验的任务目录");
  assert.ok(library.file_count >= 1, "缺少可核验的文件记录");

  let folderCount = 0;
  let fileCount = 0;
  for (const collection of library.collections) {
    assert.ok(["monitor", "manual"].includes(collection.kind));
    assert.ok(collection.title.trim(), "集合标题为空");
    assert.equal(collection.folder_count, collection.folders.length);
    const episodeNumbers = [];
    for (const folder of collection.folders) {
      folderCount += 1;
      assert.ok(isAbsolutePath(folder.absolute_path), `任务目录不是绝对路径: ${folder.absolute_path}`);
      assert.ok(!folder.relative_path.split(/[\\/]+/).includes(".."), "任务目录包含路径穿越");
      assert.equal(folder.file_count, folder.files.length);
      if (collection.kind === "monitor" && folder.episode_number > 0) {
        episodeNumbers.push(folder.episode_number);
      }
      for (const file of folder.files) {
        fileCount += 1;
        assert.ok(isAbsolutePath(file.absolute_path), `文件不是绝对路径: ${file.absolute_path}`);
        assert.ok(
          ["pending", "syncing", "available", "missing", "removed", "error"].includes(file.local_status),
          `本地状态无效: ${file.local_status}`
        );
      }
    }
    const sorted = [...episodeNumbers].sort((left, right) => left - right);
    assert.deepEqual(episodeNumbers, sorted, `监控剧集没有按集数排序: ${collection.title}`);
  }
  assert.equal(folderCount, library.folder_count);
  assert.equal(fileCount, library.file_count);

  const monitoredTasks = tasks.items.filter((task) => task.origin?.kind === "monitor");
  assert.ok(monitoredTasks.length >= 1, "没有监控来源任务可用于验收");
  assert.ok(
    monitoredTasks.every((task) => task.origin.monitor_id),
    "监控任务缺少持久化来源标识"
  );

  const report = {
    status: "passed",
    host_path: settings.host_path,
    collections: library.collection_count,
    folders: library.folder_count,
    files: library.file_count,
    available: library.available_count,
    missing: library.missing_count,
    pending: library.pending_count,
    monitored_tasks: monitoredTasks.length,
    verified_at: new Date().toISOString()
  };
  fs.mkdirSync(artifactDir, { recursive: true });
  fs.writeFileSync(
    path.join(artifactDir, "api-report.json"),
    JSON.stringify(report, null, 2)
  );
  console.log(JSON.stringify(report, null, 2));
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
