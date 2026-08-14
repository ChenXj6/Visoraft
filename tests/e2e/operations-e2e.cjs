const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://localhost:4173";
const artifactsDir = path.resolve(
  __dirname,
  "../../artifacts/v1/test-runs/operations-playwright",
);
const diagnostics = [];
const stamp = String(Date.now()).slice(-8);
const keepTestData = process.env.VISORAFT_KEEP_TEST_DATA === "1";
const leaveAwaitingReview = process.env.VISORAFT_LEAVE_AWAITING_REVIEW === "1";

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

function localAcceptanceSettings(settings) {
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
      base_url: "http://fixture-provider:8090/v1",
      model: "visoraft-fixture-asr",
      language: "zh",
      timeout_seconds: 60,
      max_retries: 0
    }
  };
  config.youtube = {
    ...config.youtube,
    provider: "fixture",
    proxy_enabled: false
  };
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

fs.mkdirSync(artifactsDir, { recursive: true });

function observe(page, scope) {
  page.on("console", (message) => {
    if (message.type() === "error") {
      diagnostics.push(`${scope}: console error: ${message.text()}`);
    }
  });
  page.on("pageerror", (error) => {
    diagnostics.push(`${scope}: page error: ${error.message}`);
  });
  page.on("response", (response) => {
    if (response.status() >= 500) {
      diagnostics.push(
        `${scope}: HTTP ${response.status()} ${response.request().method()} ${response.url()}`,
      );
    }
  });
}

async function assertNoHorizontalOverflow(page, scope) {
  const dimensions = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth,
  }));
  assert.ok(
    dimensions.document <= dimensions.viewport + 1,
    `${scope} overflows horizontally: ${JSON.stringify(dimensions)}`,
  );
}

async function waitForStatusText(container, pattern, timeout = 15_000) {
  await container
    .getByRole("status")
    .filter({ hasText: pattern })
    .waitFor({ timeout });
}

async function settingsFlow(page) {
  await page.goto(`${baseURL}/settings`, { waitUntil: "networkidle" });
  await page
    .getByRole("heading", { name: "处理策略与服务接入", exact: true })
    .waitFor();
  await page.getByText(/当前版本 v\d+/).waitFor();
  await page.getByRole("heading", { name: "审核路径", exact: true }).waitFor();
  await page.getByLabel(/人工审核/).waitFor();
  await page.getByLabel("必须有可用媒体", { exact: true }).waitFor();

  await page.getByRole("button", { name: /模型接入/ }).click();
  await page
    .getByRole("heading", { name: "全局模型与专用覆盖", exact: true })
    .waitFor();
  const modelSlabs = page.locator(".config-slab");
  assert.equal(
    await modelSlabs.count(),
    4,
    "global plus three subtitle-specific model routes should be visible",
  );
  for (let index = 0; index < 4; index += 1) {
    const slab = modelSlabs.nth(index);
    await slab.getByRole("button", { name: "测试连接", exact: true }).click();
    await slab.getByRole("status").waitFor();
    assert.match(
      await slab.getByRole("status").innerText(),
      /成功|可用|可访问|已连接/,
      `model connection ${index + 1} should pass`,
    );
  }

  await page.getByRole("button", { name: /字幕与语音/ }).click();
  await page
    .getByRole("heading", { name: "字幕来源与语音识别", exact: true })
    .waitFor();
  await page
    .getByRole("heading", { name: "时间轴后处理", exact: true })
    .waitFor();
  await page
    .getByRole("heading", { name: "智能分段、翻译与质检", exact: true })
    .waitFor();
  await page.getByRole("button", { name: "测试 ASR 连接", exact: true }).click();
  await page.getByText(/ASR.*(?:成功|可用|可访问)|连接成功/).waitFor();

  for (const label of [
    "智能分段",
    "字幕翻译",
    "字幕翻译质检",
    "标准化标点与空白",
  ]) {
    const control = page.getByLabel(label, { exact: true });
    await control.waitFor();
    assert.equal(await control.isChecked(), true, `${label} should be enabled`);
  }

  await page.goto(`${baseURL}/settings?section=prompts`, {
    waitUntil: "domcontentloaded"
  });
  await page
    .getByRole("heading", { name: "内容处理规则", exact: true })
    .waitFor();
  const promptCards = page.locator("textarea");
  assert.equal(await promptCards.count(), 6, "all six prompt routes should exist");
  await page.getByRole("heading", { name: "智能分段提示词", exact: true }).waitFor();
  await promptCards.nth(3).waitFor();

  await page.getByRole("button", { name: /YouTube.*监控/ }).click();
  await page
    .getByRole("heading", { name: "监控数据源", exact: true })
    .waitFor();
  await page
    .getByRole("button", { name: "测试监控数据源", exact: true })
    .click();
  await page
    .getByText(/YouTube.*(?:成功|可用)|数据源连接成功|验收发现器已就绪/)
    .waitFor();

  await assertNoHorizontalOverflow(page, "desktop settings");
  await page.screenshot({
    path: path.join(artifactsDir, "01-settings-desktop.png"),
    fullPage: true,
  });
}

async function createManualTask(page) {
  await page.goto(`${baseURL}/tasks/new`, { waitUntil: "networkidle" });
  await page
    .getByRole("heading", { name: "把视频送入处理轨道", exact: true })
    .waitFor();
  await page
    .locator('input[name="source_url"]')
    .fill("http://fixture-provider:8090/media/sample.wav");
  const bilibili = page.locator(
    'input[name="target_platforms"][value="bilibili"]',
  );
  if (!(await bilibili.isChecked())) {
    await bilibili.check();
  }
  await page.getByLabel("完整版", { exact: true }).check();
  await Promise.all([
    page.waitForURL(/\/tasks\/[0-9a-f-]{36}$/),
    page
      .getByRole("button", { name: "创建并开始处理", exact: true })
      .click(),
  ]);
  const taskID = page.url().split("/").at(-1);
  assert.match(taskID, /^[0-9a-f-]{36}$/);
  await page.getByRole("heading", { name: "等待人工复核", exact: true }).waitFor({
    timeout: 60_000,
  });
  return taskID;
}

async function reviewFlow(page) {
  const taskID = await createManualTask(page);
  await page.goto(`${baseURL}/reviews/${taskID}`, {
    waitUntil: "networkidle",
  });
  await page.getByRole("heading", { name: "媒体与产物", exact: true }).waitFor();
  await page.getByRole("heading", { name: "字幕与质检", exact: true }).waitFor();
  await page.locator("audio[controls]").waitFor();
  assert.equal(
    await page.getByRole("tab", { name: /原文/ }).count(),
    1,
    "original subtitle tab should exist",
  );
  assert.equal(
    await page.getByRole("tab", { name: /译文/ }).count(),
    1,
    "translated subtitle tab should exist",
  );
  await page.getByText(/^(?:96|100)(?:\.0)?$/).first().waitFor();

  await page.getByLabel("标题", { exact: true }).fill("Playwright 人工审核验收");
  await page
    .getByLabel("简介", { exact: true })
    .fill("验证媒体预览、字幕质检、元数据版本和人工审核状态机。");
  await page
    .getByLabel("标签（逗号分隔）", { exact: true })
    .fill("审核, 字幕, 本地验收");
  await page.getByLabel("分区", { exact: true }).fill("科技");
  await page
    .getByLabel("本次修改原因", { exact: true })
    .fill("第一次人工整理");
  await page
    .getByRole("button", { name: "保存元数据版本", exact: true })
    .click();
  await waitForStatusText(page, /元数据新版本已保存/);

  await page.getByRole("button", { name: "修订当前字幕", exact: true }).click();
  const firstSubtitleText = page.getByLabel(/第 1 条字幕文本/);
  await firstSubtitleText.fill(
    `${await firstSubtitleText.inputValue()}（人工修订）`,
  );
  await page
    .getByLabel("字幕修改原因", { exact: true })
    .fill("修正第一条字幕术语");
  await page.locator(".review-subtitle-panel").screenshot({
    path: path.join(artifactsDir, "02a-subtitle-editor-desktop.png"),
  });
  await page
    .getByRole("button", { name: "保存字幕新版本", exact: true })
    .click();
  await waitForStatusText(page, /字幕新版本已保存并重新计算质检/);
  await page.getByRole("tab", { name: /译文.*v2/ }).waitFor();

  await page
    .getByLabel("意见 / 原因", { exact: true })
    .fill("请复核专有名词后再次提交");
  await page.getByRole("button", { name: "退回修改", exact: true }).click();
  await waitForStatusText(page, /任务已退回修改/);
  await page
    .getByRole("button", { name: "修改完成，重新提交", exact: true })
    .waitFor();

  await page
    .getByLabel("标题", { exact: true })
    .fill("Playwright 人工审核验收（已修订）");
  await page
    .getByLabel("本次修改原因", { exact: true })
    .fill("按退回意见完成专有名词复核");
  await page
    .getByRole("button", { name: "保存元数据版本", exact: true })
    .click();
  await waitForStatusText(page, /元数据新版本已保存/);
  await page
    .getByLabel("意见 / 原因", { exact: true })
    .fill("修改已完成，申请重新审核");
  await page
    .getByRole("button", { name: "修改完成，重新提交", exact: true })
    .click();
  await waitForStatusText(page, /任务已重新提交；若启用了字幕烧录/);
  await page
    .getByRole("button", { name: "批准并进入待发布", exact: true })
    .waitFor();
  if (!leaveAwaitingReview) {
    await page
      .getByLabel("意见 / 原因", { exact: true })
      .fill("人工复核通过");
    await page
      .getByRole("button", { name: "批准并进入待发布", exact: true })
      .click();
    await waitForStatusText(page, /审核已批准/);
  } else {
    const deadline = Date.now() + 120_000;
    let latest;
    do {
      const response = await page.request.get(`${baseURL}/api/v1/tasks/${taskID}`);
      assert.ok(response.ok(), `读取重提任务返回 HTTP ${response.status()}`);
      latest = await response.json();
      if (latest.status === "awaiting_manual_review") break;
      await page.waitForTimeout(500);
    } while (Date.now() < deadline);
    assert.equal(latest?.status, "awaiting_manual_review");
  }

  const persisted = await page.evaluate(async (id) => {
    const response = await fetch(`/api/v1/reviews/${id}`);
    if (!response.ok) throw new Error(`review API failed: ${response.status}`);
    return response.json();
  }, taskID);
  assert.equal(
    persisted.task.status,
    leaveAwaitingReview ? "awaiting_manual_review" : "ready_to_publish"
  );
  assert.equal(
    persisted.task.review_status,
    leaveAwaitingReview ? "pending" : "approved"
  );
  assert.ok(persisted.subtitles.length >= 3);
  assert.ok(persisted.actions.some((item) => item.action === "subtitle_edit"));
  assert.ok(persisted.actions.some((item) => item.action === "request_changes"));
  assert.ok(persisted.actions.some((item) => item.action === "resubmit"));
  assert.equal(
    persisted.actions.some((item) => item.action === "approve"),
    !leaveAwaitingReview
  );
  if (!leaveAwaitingReview) {
    assert.ok(
      persisted.runs.every((run) => run.status === "completed"),
      "completed manual flow must not leave a pending review run",
    );
  }
  assert.equal(await page.getByText("来源记录", { exact: true }).count(), 0);
  assert.equal(await page.getByText("权利人", { exact: true }).count(), 0);
  assert.equal(await page.getByText("授权类型", { exact: true }).count(), 0);

  await assertNoHorizontalOverflow(page, "desktop review");
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.waitForTimeout(100);
  await page.screenshot({
    path: path.join(artifactsDir, "02-review-desktop.png"),
    fullPage: true,
  });
  return taskID;
}

async function monitorFlow(page) {
  const monitorName = `Playwright 监控验收 ${stamp}`;
  await page.goto(`${baseURL}/monitors/new`, { waitUntil: "networkidle" });
  await page
    .getByRole("heading", { name: "建立发现规则", exact: true })
    .waitFor();
  await page.getByRole("button", { name: /关键词搜索/ }).click();
  await page.getByLabel("配置名称", { exact: true }).fill(monitorName);
  await page.getByLabel("搜索词", { exact: true }).fill("visoraft workflow");
  await page.getByLabel(/仅手动/).check();
  const autoAdd = page.getByLabel(/自动加入任务流水线/);
  if (await autoAdd.isChecked()) {
    await autoAdd.uncheck({ force: true });
  }
  await Promise.all([
    page.waitForURL(/\/monitors\/[0-9a-f-]{36}\/history$/),
    page.getByRole("button", { name: "创建监控", exact: true }).click(),
  ]);
  const monitorID = page.url().split("/").at(-2);
  assert.match(monitorID, /^[0-9a-f-]{36}$/);
  await page.getByRole("status").filter({ hasText: "监控配置已创建" }).waitFor();

  await page.goto(`${baseURL}/monitors`, { waitUntil: "networkidle" });
  const row = page.locator(".monitor-row").filter({ hasText: monitorName });
  await row.waitFor();
  await row.getByRole("button", { name: "立即执行", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "已进入执行队列" }).waitFor();
  await row.getByRole("link", { name: "运行记录", exact: true }).click();
  await page.waitForURL(new RegExp(`/monitors/${monitorID}/history$`));
  await page.locator(".run-completed").waitFor({ timeout: 30_000 });
  await page.getByText("Visoraft 本地监控验收媒体", { exact: true }).waitFor();
  await page.getByText("待建单", { exact: true }).waitFor();
  await page.getByText(/仅记录不建单/).waitFor();

  await page.getByRole("link", { name: "编辑配置", exact: true }).click();
  await page
    .getByRole("heading", { name: "编辑监控配置", exact: true })
    .waitFor();
  const advancedSettings = page.locator("details.monitor-advanced");
  if (!(await advancedSettings.evaluate((element) => element.open))) {
    await advancedSettings.locator(":scope > summary").click();
  }
  await page
    .getByLabel("排除关键词", { exact: true })
    .fill("spam, duplicate");
  await Promise.all([
    page.waitForURL(new RegExp(`/monitors/${monitorID}/history$`)),
    page.getByRole("button", { name: "保存新版本", exact: true }).click(),
  ]);
  await page.getByRole("status").filter({ hasText: "监控配置已更新" }).waitFor();

  await page.goto(`${baseURL}/monitors`, { waitUntil: "networkidle" });
  const updatedRow = page.locator(".monitor-row").filter({ hasText: monitorName });
  await updatedRow.getByRole("button", { name: "暂停", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "已暂停" }).waitFor();
  await updatedRow.getByRole("button", { name: "恢复", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "已恢复" }).waitFor();

  await updatedRow.getByRole("button", { name: "归档", exact: true }).click();
  await page
    .getByRole("heading", { name: "归档监控配置", exact: true })
    .waitFor();
  await page.getByRole("button", { name: "确认归档", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "监控配置已归档" }).waitFor();
  await updatedRow.waitFor({ state: "detached" });

  await assertNoHorizontalOverflow(page, "desktop monitors");
  await page.screenshot({
    path: path.join(artifactsDir, "03-monitors-desktop.png"),
    fullPage: true,
  });
  await apiJSON(`/api/v1/youtube-monitors/${monitorID}`, {
    method: "DELETE",
    body: JSON.stringify({ history_mode: "purge" })
  });
  return monitorID;
}

async function seriesMonitorFlow(page) {
  const monitorName = `Playwright 通用多篇章 ${stamp}`;
  await page.goto(`${baseURL}/monitors/new`, { waitUntil: "networkidle" });
  await page.getByRole("button", { name: /完整节目.*剧集/ }).click();
  await page.getByLabel("配置名称", { exact: true }).fill(monitorName);
  await page.getByLabel("节目名称", { exact: true }).fill("本地节目验收");
  await page.getByLabel("范围名称（可选）", { exact: true }).fill("第一部");
  await page.getByLabel("节目别名（可选）", { exact: true }).fill("PART I");
  await page.getByLabel("起始集", { exact: true }).fill("1");
  await page.getByLabel("最后一集", { exact: true }).fill("2");
  await page.getByRole("button", { name: "添加分部", exact: true }).click();
  await page.getByLabel("范围名称（可选）", { exact: true }).nth(1).fill("第二部");
  await page.getByLabel("节目别名（可选）", { exact: true }).nth(1).fill("PART II");
  await page.getByLabel("起始集", { exact: true }).nth(1).fill("1");
  await page.getByLabel("最后一集", { exact: true }).nth(1).fill("2");
  const advancedSettings = page.locator("details.monitor-advanced");
  if (!(await advancedSettings.evaluate((element) => element.open))) {
    await advancedSettings.locator(":scope > summary").click();
  }
  await page.getByLabel("最短时长（秒）", { exact: true }).fill("0");
  await Promise.all([
    page.waitForURL(/\/monitors\/[0-9a-f-]{36}\/history$/),
    page.getByRole("button", { name: "创建监控", exact: true }).click()
  ]);
  const monitorID = page.url().split("/").at(-2);
  assert.match(monitorID, /^[0-9a-f-]{36}$/);

  await page.goto(`${baseURL}/monitors`, { waitUntil: "networkidle" });
  const row = page.locator(".monitor-row").filter({ hasText: monitorName });
  await row.getByRole("button", { name: "立即执行", exact: true }).click();
  await row.getByRole("link", { name: "运行记录", exact: true }).click();
  await page.locator(".run-completed").waitFor({ timeout: 30_000 });
  const coverageCards = page.locator(".series-coverage-grid article");
  await coverageCards.nth(0).getByText("第一部", { exact: true }).waitFor();
  await coverageCards.nth(0).getByText("2/2", { exact: true }).waitFor();
  await coverageCards.nth(1).getByText("第二部", { exact: true }).waitFor();
  await coverageCards.nth(1).getByText("2/2", { exact: true }).waitFor();
  await page.getByLabel("选择全部待建单结果", { exact: true }).check();
  await page.getByRole("button", { name: "加入任务队列", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "新建 4 条" }).waitFor({ timeout: 30_000 });
  const persisted = await apiJSON(`/api/v1/youtube-monitors/${monitorID}/history`);
  const taskIDs = persisted.items.map((item) => item.task_id).filter(Boolean);
  assert.equal(new Set(taskIDs).size, 4, "four series candidates should enter the task pipeline");
  await page.getByRole("link", { name: "查看任务" }).first().waitFor();
  await assertNoHorizontalOverflow(page, "desktop generic series handoff");
  await page.screenshot({
    path: path.join(artifactsDir, "03b-series-handoff-desktop.png"),
    fullPage: true,
  });
  await apiJSON(`/api/v1/youtube-monitors/${monitorID}`, {
    method: "DELETE",
    body: JSON.stringify({ history_mode: "purge" })
  });
  return { monitorID, taskIDs };
}

async function purgeTask(taskID) {
  let task = await apiJSON(`/api/v1/tasks/${taskID}`);
  if (["running", "queued", "awaiting_manual_review"].includes(task.status)) {
    task = await apiJSON(`/api/v1/tasks/${taskID}/cancel`, { method: "POST" });
  }
  task = await apiJSON(`/api/v1/tasks/${taskID}/archive`, {
    method: "POST",
    body: JSON.stringify({
      expected_version: task.version,
      delete_assets: true,
      reason: "操作台 E2E 完成后清理测试数据"
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
      reason: "操作台 E2E 完成后永久清理测试任务"
    })
  });
}

async function mobileFlow(browser, taskID) {
  const context = await browser.newContext({
    viewport: { width: 390, height: 844 },
    locale: "zh-CN",
  });
  const page = await context.newPage();
  observe(page, "mobile");

  for (const [route, fileName] of [
    ["/settings", "04-settings-mobile.png"],
    ["/monitors", "05-monitors-mobile.png"],
    [`/reviews/${taskID}`, "06-review-mobile.png"],
  ]) {
    await page.goto(`${baseURL}${route}`, { waitUntil: "networkidle" });
    await page.locator("main h1").waitFor();
    await assertNoHorizontalOverflow(page, `mobile ${route}`);
    await page.screenshot({
      path: path.join(artifactsDir, fileName),
      fullPage: true,
    });
  }
  await context.close();
}

async function breakpointFlow(browser, taskID) {
  const cases = [
    {
      width: 1024,
      height: 768,
      route: "/settings",
      fileName: "07-settings-1024.png",
    },
    {
      width: 768,
      height: 900,
      route: "/monitors",
      fileName: "08-monitors-768.png",
    },
    {
      width: 320,
      height: 720,
      route: `/reviews/${taskID}`,
      fileName: "09-review-320.png",
    },
  ];
  for (const item of cases) {
    const context = await browser.newContext({
      viewport: { width: item.width, height: item.height },
      locale: "zh-CN",
    });
    const page = await context.newPage();
    observe(page, `${item.width}px`);
    await page.goto(`${baseURL}${item.route}`, { waitUntil: "networkidle" });
    await page.locator("main h1").waitFor();
    await assertNoHorizontalOverflow(
      page,
      `${item.width}px ${item.route}`,
    );
    await page.screenshot({
      path: path.join(artifactsDir, item.fileName),
      fullPage: true,
    });
    await context.close();
  }
}

async function main() {
  const browser = await chromium.launch({ headless: true });
  const originalSettings = await apiJSON("/api/v1/settings");
  let taskID = "";
  let seriesTaskIDs = [];
  try {
    await replaceSettings(localAcceptanceSettings(originalSettings));
    const context = await browser.newContext({
      viewport: { width: 1440, height: 900 },
      locale: "zh-CN",
    });
    const page = await context.newPage();
    observe(page, "desktop");

    await settingsFlow(page);
    taskID = await reviewFlow(page);
    const monitorID = await monitorFlow(page);
    const seriesResult = await seriesMonitorFlow(page);
    const seriesMonitorID = seriesResult.monitorID;
    seriesTaskIDs = seriesResult.taskIDs;
    await context.close();
    await mobileFlow(browser, taskID);
    await breakpointFlow(browser, taskID);

    assert.deepEqual(
      diagnostics,
      [],
      `browser diagnostics were not clean:\n${diagnostics.join("\n")}`,
    );
    const report = {
          ok: true,
          taskID,
          monitorID,
          seriesMonitorID,
          artifactsDir,
          diagnostics: diagnostics.length,
          testDataPurged: !keepTestData,
          leftAwaitingReview: leaveAwaitingReview,
        };
    fs.writeFileSync(
      path.join(artifactsDir, "operations-result.json"),
      `${JSON.stringify(report, null, 2)}\n`,
      "utf8"
    );
    console.log(JSON.stringify(report, null, 2));
  } finally {
    if (taskID && !keepTestData) {
      await purgeTask(taskID).catch((error) => {
        diagnostics.push(`cleanup: ${error.message}`);
      });
    }
    if (!keepTestData) {
      for (const seriesTaskID of seriesTaskIDs) {
        await purgeTask(seriesTaskID).catch((error) => {
          diagnostics.push(`series task cleanup ${seriesTaskID}: ${error.message}`);
        });
      }
    }
    await replaceSettings(editableSettings(originalSettings)).catch((error) => {
      diagnostics.push(`settings restore: ${error.message}`);
    });
    await browser.close();
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
