const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_WEB_URL || "http://localhost:4173";
let taskID = "";

async function requestJSON(request, path, options = {}) {
  const response = await request.fetch(`${baseURL}${path}`, options);
  const body = await response.json().catch(() => ({}));
  if (!response.ok()) {
    throw new Error(`${options.method || "GET"} ${path} 返回 HTTP ${response.status()}: ${JSON.stringify(body)}`);
  }
  return body;
}

async function waitForTask(request, predicate, timeoutMs = 30_000) {
  const deadline = Date.now() + timeoutMs;
  let latest;
  while (Date.now() < deadline) {
    latest = await requestJSON(request, `/api/v1/tasks/${taskID}`);
    if (predicate(latest)) return latest;
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error(`任务未在限定时间进入预期状态: ${JSON.stringify(latest)}`);
}

async function cleanup(request) {
  if (!taskID) return;

  let task;
  try {
    task = await requestJSON(request, `/api/v1/tasks/${taskID}`);
  } catch (error) {
    if (String(error).includes("HTTP 404")) return;
    throw error;
  }

  if (!["cancelled", "failed", "abandoned", "published", "reconciled"].includes(task.status)) {
    task = await requestJSON(request, `/api/v1/tasks/${taskID}/cancel`, { method: "POST" });
  }
  if (!task.archived_at) {
    task = await requestJSON(request, `/api/v1/tasks/${taskID}/archive`, {
      method: "POST",
      data: {
        expected_version: task.version,
        delete_assets: true,
        reason: "清理暂停继续回归任务"
      }
    });
  }
  await requestJSON(request, `/api/v1/tasks/${taskID}`, {
    method: "DELETE",
    data: {
      expected_version: task.version,
      confirmation: `purge:${taskID}`,
      reason: "永久清理暂停继续回归任务"
    }
  });
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  const browserErrors = [];
  page.on("console", (message) => {
    if (message.type() === "error") browserErrors.push(`console: ${message.text()}`);
  });
  page.on("pageerror", (error) => browserErrors.push(`page: ${error.message}`));

  try {
    const created = await requestJSON(page.request, "/api/v1/tasks", {
      method: "POST",
      data: {
        source_url: "http://fixture-provider:8090/media/slow-sample.wav",
        target_platforms: ["bilibili"],
        repost_statement_version: "brief_v1",
        auto_publish: false
      }
    });
    taskID = created.id;

    const paused = await requestJSON(page.request, `/api/v1/tasks/${taskID}/pause`, {
      method: "POST"
    });
    if (!paused.paused_at || paused.paused_step_kind !== "metadata") {
      throw new Error(`暂停状态未持久化: ${JSON.stringify(paused)}`);
    }

    await page.goto(`${baseURL}/tasks`, { waitUntil: "networkidle" });
    const row = page.locator("article.task-track", { hasText: `#${taskID.slice(0, 8)}` });
    await row.waitFor({ state: "visible" });
    await row.getByRole("button", { name: "继续处理", exact: true }).waitFor();

    await page.reload({ waitUntil: "networkidle" });
    const persistedRow = page.locator("article.task-track", { hasText: `#${taskID.slice(0, 8)}` });
    await persistedRow.getByRole("button", { name: "继续处理", exact: true }).waitFor();

    const resumeResponse = page.waitForResponse(
      (response) => response.url().endsWith(`/api/v1/tasks/${taskID}/resume`) && response.request().method() === "POST"
    );
    await persistedRow.getByRole("button", { name: "继续处理", exact: true }).click();
    if (!(await resumeResponse).ok()) throw new Error("页面继续处理请求失败");

    const runningDownload = await waitForTask(
      page.request,
      (task) => task.steps?.some(
        (step) =>
          step.kind === "download" &&
          step.status === "running" &&
          Number(step.detail?.downloaded_bytes || 0) >= 32 * 1024
      )
    );
    const firstDownload = runningDownload.steps.find((step) => step.kind === "download");
    await page.reload({ waitUntil: "networkidle" });
    const downloadingRow = page.locator("article.task-track", { hasText: `#${taskID.slice(0, 8)}` });
    await downloadingRow.getByRole("button", { name: "暂停处理", exact: true }).waitFor();

    const pauseResponse = page.waitForResponse(
      (response) => response.url().endsWith(`/api/v1/tasks/${taskID}/pause`) && response.request().method() === "POST"
    );
    await downloadingRow.getByRole("button", { name: "暂停处理", exact: true }).click();
    if (!(await pauseResponse).ok()) throw new Error("页面暂停处理请求失败");
    await downloadingRow.getByRole("button", { name: "继续处理", exact: true }).waitFor();

    const persisted = await requestJSON(page.request, `/api/v1/tasks/${taskID}`);
    if (!persisted.paused_at) throw new Error("页面暂停后刷新接口未返回暂停时间");
    if (persisted.paused_step_kind !== "download") {
      throw new Error(`下载阶段暂停点错误: ${persisted.paused_step_kind}`);
    }

    // 复现用户的真实路径：暂停后立即刷新、重新进入详情并继续。
    // 这里故意不等待 Worker 回报“已暂停”，用于覆盖旧进程尚在退出的竞态窗口。
    const pausedDownload = persisted.steps.find((step) => step.kind === "download");
    const pausedBytes = Number(pausedDownload.detail?.downloaded_bytes || 0);
    if (pausedBytes < 32 * 1024) throw new Error(`暂停断点字节数异常: ${pausedBytes}`);

    await page.reload({ waitUntil: "networkidle" });
    const refreshedRow = page.locator("article.task-track", { hasText: `#${taskID.slice(0, 8)}` });
    await refreshedRow.getByRole("link", { name: "查看详情", exact: true }).click();
    await page.waitForURL(`**/tasks/${taskID}`);
    const pausedActivity = page.locator(".download-activity");
    await pausedActivity.waitFor({ state: "visible" });
    await page.getByRole("progressbar", { name: "源文件下载进度" }).waitFor();
    await page.screenshot({
      path: "artifacts/ui/e2e-download-paused.png",
      fullPage: false
    });
    if (await page.getByText(/尝试\s*\d+/).count()) {
      throw new Error("任务详情仍显示内部尝试次数");
    }
    if (await page.locator(".work-panel-head").getByText(/^版本\s*\d+/).count()) {
      throw new Error("任务详情仍显示内部并发版本号");
    }

    const resumeDownloadResponse = page.waitForResponse(
      (response) => response.url().endsWith(`/api/v1/tasks/${taskID}/resume`) && response.request().method() === "POST"
    );
    await page.getByRole("button", { name: "继续处理", exact: true }).click();
    const resumedResponse = await resumeDownloadResponse;
    if (!resumedResponse.ok()) throw new Error("下载断点继续请求失败");

    // 恢复请求成功到 Worker 真正接单之间也必须保留同一条下载进度，不能退化成空白等待行。
    await page.locator(".download-activity").waitFor({ state: "visible" });
    await page.getByRole("progressbar", { name: "源文件下载进度" }).waitFor();
    await page.screenshot({
      path: "artifacts/ui/e2e-download-resume-transition.png",
      fullPage: false
    });

    const resumedTask = await waitForTask(
      page.request,
      (task) => {
        if (task.status === "cancelled") {
          throw new Error("暂停后立即继续被错误地改成了已取消");
        }
        return task.steps?.some(
          (step) =>
            step.kind === "download" &&
            step.status === "running" &&
            step.attempt >= firstDownload.attempt &&
            Number(step.detail?.downloaded_bytes || 0) > pausedBytes
        );
      },
      45_000
    );
    const resumedDownload = resumedTask.steps.find((step) => step.kind === "download");
    await requestJSON(page.request, `/api/v1/tasks/${taskID}/pause`, { method: "POST" });

    const minimumFontSize = await page.locator("main *:visible").evaluateAll((elements) =>
      Math.min(
        ...elements
          .filter((element) => (element.textContent || "").trim())
          .map((element) => Number.parseFloat(getComputedStyle(element).fontSize))
          .filter(Number.isFinite)
      )
    );
    if (minimumFontSize < 12) throw new Error(`暂停操作区域存在小于 12px 的文字: ${minimumFontSize}px`);
    if (browserErrors.length) throw new Error(browserErrors.join("\n"));

    console.log(
      JSON.stringify({
        taskID,
        apiPausePersisted: true,
        listPauseResumePassed: true,
        reloadPersistencePassed: true,
        activeDownloadPaused: true,
        refreshAndImmediateResumePassed: true,
        neverBecameCancelled: true,
        downloadAttempt: firstDownload.attempt,
        pausedBytes,
        resumedAttempt: resumedDownload.attempt,
        resumedBytes: resumedDownload.detail.downloaded_bytes,
        resumeTransitionProgressVisible: true,
        minimumFontSize
      })
    );
  } finally {
    await cleanup(page.request);
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
