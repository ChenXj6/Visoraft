const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_WEB_URL || "http://localhost:4173";
let taskID = process.env.VISORAFT_TASK_ID || "";
const screenshotPath = process.env.VISORAFT_SCREENSHOT_PATH;
const timeoutMs = Number(process.env.VISORAFT_RECOVERY_TIMEOUT_MS || 900_000);
let originalSettings;
let selfCreatedTask = false;

const terminalStatuses = new Set([
  "awaiting_manual_review",
  "ready_to_publish",
  "published",
  "failed",
  "cancelled",
  "abandoned"
]);

async function apiJSON(route, options = {}) {
  const response = await fetch(`${baseURL}${route}`, {
    ...options,
    headers: {
      ...(options.body ? { "content-type": "application/json" } : {}),
      ...(options.headers || {})
    }
  });
  const text = await response.text();
  const body = text ? JSON.parse(text) : undefined;
  if (!response.ok) {
    throw new Error(`${options.method || "GET"} ${route} 返回 HTTP ${response.status}: ${text}`);
  }
  return body;
}

function editableSettings(settings) {
  const { version: _version, secret_configured: _secrets, updated_at: _updated, ...config } = settings;
  return structuredClone(config);
}

function recoverySettings(settings) {
  const config = editableSettings(settings);
  const model = (name) => ({
    mode: "override",
    provider: "fixture",
    base_url: "http://fixture-provider:8090/v1",
    model: name,
    thinking: false,
    temperature: 0,
    timeout_seconds: 30
  });
  config.review = {
    mode: "manual",
    automatic_fallback: "manual",
    rules: {
      require_media: true,
      require_title: true,
      minimum_description_length: 0,
      maximum_duration_seconds: 0,
      require_subtitle_qc: true,
      minimum_subtitle_qc_score: 70
    }
  };
  config.models.global = {
    enabled: true,
    provider: "fixture",
    base_url: "http://fixture-provider:8090/v1",
    model: "visoraft-fixture-model",
    thinking: false,
    temperature: 0,
    timeout_seconds: 30
  };
  config.models.subtitle_translation = model("fixture-subtitle-translation");
  config.models.subtitle_qc = model("fixture-subtitle-qc");
  config.models.smart_segmentation = model("fixture-smart-segmentation");
  config.subtitle = {
    ...config.subtitle,
    enabled: true,
    source_strategy: "asr_only",
    source_language: "zh",
    target_language: "zh",
    asr: {
      ...config.subtitle.asr,
      enabled: true,
      provider: "fixture",
      base_url: "http://fixture-provider:8090/v1/fail-once",
      model: "visoraft-fixture-asr",
      language: "zh",
      timeout_seconds: 30,
      max_retries: 0
    }
  };
  config.youtube = { ...config.youtube, provider: "fixture", proxy_enabled: false };
  config.automation = { ...config.automation, enabled: false };
  config.moderation = { ...config.moderation, enabled: false, provider: "disabled" };
  config.publishing = { ...config.publishing, auto_publish_after_review: false };
  return config;
}

async function replaceSettings(config) {
  const current = await apiJSON("/api/v1/settings");
  return apiJSON("/api/v1/settings", {
    method: "PUT",
    body: JSON.stringify({ ...config, expected_version: current.version })
  });
}

async function waitForFailedSubtitle() {
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    const task = await apiJSON(`/api/v1/tasks/${taskID}`);
    const subtitle = (task.steps || []).find((step) => step.kind === "subtitles");
    if (task.status === "failed" && subtitle?.status === "failed") return task;
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error("本地故障注入任务未在 120 秒内进入字幕失败状态");
}

async function prepareRecoveryFixture() {
  originalSettings = await apiJSON("/api/v1/settings");
  await replaceSettings(recoverySettings(originalSettings));
  const task = await apiJSON("/api/v1/tasks", {
    method: "POST",
    body: JSON.stringify({
      source_url: "http://fixture-provider:8090/media/sample.wav",
      target_platforms: ["bilibili"],
      repost_statement_version: "brief_v1",
      auto_publish: false
    })
  });
  taskID = task.id;
  selfCreatedTask = true;
  await waitForFailedSubtitle();
}

async function purgeTask() {
  let task = await apiJSON(`/api/v1/tasks/${taskID}`);
  if (["running", "queued", "awaiting_manual_review"].includes(task.status)) {
    task = await apiJSON(`/api/v1/tasks/${taskID}/cancel`, { method: "POST" });
  }
  task = await apiJSON(`/api/v1/tasks/${taskID}/archive`, {
    method: "POST",
    body: JSON.stringify({
      expected_version: task.version,
      delete_assets: true,
      reason: "字幕恢复回归完成后清理本地测试数据"
    })
  });
  const deadline = Date.now() + 60_000;
  while ((task.assets || []).some((asset) => asset.status !== "deleted") && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 500));
    task = await apiJSON(`/api/v1/tasks/${taskID}`);
  }
  await apiJSON(`/api/v1/tasks/${taskID}`, {
    method: "DELETE",
    body: JSON.stringify({
      expected_version: task.version,
      confirmation: `purge:${taskID}`,
      reason: "字幕恢复回归完成后永久清理本地测试任务"
    })
  });
}

async function taskFrom(page) {
  let lastError;
  for (let attempt = 1; attempt <= 5; attempt += 1) {
    try {
      const response = await page.request.get(
        `${baseURL}/api/v1/tasks/${encodeURIComponent(taskID)}`
      );
      if (!response.ok()) {
        throw new Error(`task request failed: ${response.status()} ${await response.text()}`);
      }
      return response.json();
    } catch (error) {
      lastError = error;
      if (attempt < 5) await page.waitForTimeout(500 * attempt);
    }
  }
  throw lastError;
}

(async () => {
  let browser;
  try {
    if (!taskID) await prepareRecoveryFixture();
    browser = await chromium.launch({ headless: true });
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    const browserErrors = [];
    page.on("console", (message) => {
      if (message.type() === "error") browserErrors.push(message.text());
    });
    page.on("pageerror", (error) => browserErrors.push(error.message));

    const before = await taskFrom(page);
    const beforeStep = before.steps.find((step) => step.kind === "subtitles");
    if (before.status !== "failed" || !beforeStep || beforeStep.status !== "failed") {
      throw new Error(`task is not ready for subtitle recovery: ${before.status}`);
    }

    await page.goto(`${baseURL}/tasks/${taskID}`, {
      waitUntil: "domcontentloaded",
      timeout: 20_000
    });
    const retryButton = page.getByRole("button", { name: "重试失败步骤", exact: true });
    await retryButton.waitFor({ state: "visible", timeout: 15_000 });
    const [retryResponse] = await Promise.all([
      page.waitForResponse(
        (response) =>
          response.request().method() === "POST" &&
          response.url().endsWith(`/api/v1/tasks/${taskID}/retry`),
        { timeout: 15_000 }
      ),
      retryButton.click()
    ]);
    if (!retryResponse.ok()) {
      throw new Error(`retry failed: ${retryResponse.status()} ${await retryResponse.text()}`);
    }

    const observations = [];
    const seen = new Set();
    const startedAt = Date.now();
    let finalTask;
    while (Date.now() - startedAt < timeoutMs) {
      const task = await taskFrom(page);
      const step = task.steps.find((item) => item.kind === "subtitles");
      const transcode = task.steps.find((item) => item.kind === "transcode");
      const signature = [
        task.status,
        step?.status,
        step?.attempt,
        step?.progress,
        step?.detail?.phase,
        step?.detail?.batch_index,
        step?.detail?.completed_batches,
        step?.detail?.model_attempt,
        transcode?.status,
        transcode?.progress
      ].join("|");
      if (!seen.has(signature)) {
        seen.add(signature);
        observations.push({
          elapsed_seconds: Math.round((Date.now() - startedAt) / 1000),
          task_status: task.status,
          subtitle_status: step?.status,
          subtitle_attempt: step?.attempt,
          subtitle_progress: step?.progress,
          subtitle_phase: step?.detail?.phase || "",
          subtitle_batch_index: step?.detail?.batch_index || 0,
          subtitle_batch_count: step?.detail?.batch_count || 0,
          subtitle_completed_batches: step?.detail?.completed_batches || 0,
          subtitle_model_attempt: step?.detail?.model_attempt || 0,
          subtitle_checkpoint_reused: Boolean(step?.detail?.checkpoint_reused),
          subtitle_remote_task_id: step?.detail?.remote_task_id || "",
          transcode_status: transcode?.status || "",
          transcode_progress: transcode?.progress || 0
        });
        console.log(JSON.stringify(observations.at(-1)));
      }
      if (terminalStatuses.has(task.status) && step?.attempt > beforeStep.attempt) {
        finalTask = task;
        break;
      }
      await page.waitForTimeout(Date.now() - startedAt < 20_000 ? 250 : 2_000);
    }
    if (!finalTask) throw new Error(`recovery did not finish within ${timeoutMs}ms`);

    await page.reload({ waitUntil: "domcontentloaded" });
    await page.getByRole("heading", { name: "处理步骤", exact: true }).waitFor();
    const layout = await page.locator("body").evaluate(() => {
      const sizes = [...document.querySelectorAll("body *")]
        .filter((element) => {
          const style = getComputedStyle(element);
          return style.display !== "none" && style.visibility !== "hidden" &&
            (element.textContent || "").trim();
        })
        .map((element) => Number.parseFloat(getComputedStyle(element).fontSize))
        .filter(Number.isFinite);
      return {
        minimumFontSize: Math.min(...sizes),
        horizontalOverflow: document.documentElement.scrollWidth > window.innerWidth
      };
    });
    if (layout.minimumFontSize < 12) {
      throw new Error(`visible font below 12px: ${layout.minimumFontSize}`);
    }
    if (layout.horizontalOverflow) throw new Error("page has horizontal overflow");
    if (browserErrors.length) throw new Error(browserErrors.join("\n"));
    if (screenshotPath) await page.screenshot({ path: screenshotPath });

    const finalStep = finalTask.steps.find((step) => step.kind === "subtitles");
    const result = {
      taskID,
      before_attempt: beforeStep.attempt,
      final_status: finalTask.status,
      final_subtitle_status: finalStep?.status,
      final_subtitle_attempt: finalStep?.attempt,
      final_error_code: finalTask.error_code || "",
      observations,
      ...layout
    };
    console.log(JSON.stringify(result));
    if (finalTask.status === "failed") process.exitCode = 2;
  } finally {
    if (browser) await browser.close();
    if (selfCreatedTask) await purgeTask().catch((error) => {
      console.error(`清理字幕恢复测试任务失败：${error.message}`);
    });
    if (originalSettings) await replaceSettings(editableSettings(originalSettings)).catch((error) => {
      console.error(`恢复用户设置失败：${error.message}`);
    });
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
