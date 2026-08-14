const assert = require("node:assert/strict");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://localhost:4173";
const monitorName = "真实监控自动建单验收-20260811";
const cookieProfileID = "7d5318bb-716d-4224-aaeb-b930d2c07330";
let activeBrowser;

async function requestJSON(page, route) {
  const response = await page.request.get(`${baseURL}${route}`);
  assert.ok(response.ok(), `${route} 返回 HTTP ${response.status()}`);
  return response.json();
}

function field(page, labelText, control) {
  return page
    .locator("label.field")
    .filter({ has: page.locator("span", { hasText: labelText }) })
    .locator(control);
}

async function waitForTerminalRun(page, monitorID) {
  const deadline = Date.now() + 90_000;
  while (Date.now() < deadline) {
    const history = await requestJSON(
      page,
      `/api/v1/youtube-monitors/${monitorID}/history`
    );
    const run = history.runs[0];
    if (run && ["completed", "failed"].includes(run.status)) return history;
    await page.waitForTimeout(1_000);
  }
  throw new Error("真实监控运行 90 秒内未结束");
}

async function main() {
  const browser = await chromium.launch({ headless: true });
  activeBrowser = browser;
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  page.setDefaultTimeout(15_000);

  const before = await requestJSON(page, "/api/v1/youtube-monitors");
  let monitor = before.items.find((item) => item.name === monitorName);
  if (!monitor) {
    await page.goto(`${baseURL}/monitors/new`, { waitUntil: "domcontentloaded" });
    await page.getByRole("heading", { name: "建立发现规则", exact: true }).waitFor();
    await field(page, "配置名称", "input").fill(monitorName);
    await field(page, "搜索词", "input").fill("OpenAI #shorts");
    await field(page, "回溯天数", "input").fill("7");
    await field(page, "最大结果数", "input").fill("1");
    await field(page, "单次请求上限", "input").fill("2");
    await page
      .locator(".mode-selector label")
      .filter({ hasText: "仅手动" })
      .locator('input[type="radio"]')
      .check();
    const cookieSelect = field(page, "下载 Cookie", "select");
    await cookieSelect.waitFor({ state: "visible" });
    await cookieSelect.selectOption(cookieProfileID);
    assert.equal(await cookieSelect.inputValue(), cookieProfileID);
    await Promise.all([
      page.waitForURL(/\/monitors\/[0-9a-f-]{36}\/history$/),
      page.getByRole("button", { name: "创建监控", exact: true }).click()
    ]);
    const monitorID = page.url().match(/\/monitors\/([0-9a-f-]{36})\/history$/)?.[1];
    assert.ok(monitorID, "创建后没有进入监控运行记录页");
    monitor = await requestJSON(page, `/api/v1/youtube-monitors/${monitorID}`);
    assert.equal(monitor.name, monitorName);
    assert.equal(monitor.auto_add_to_tasks, true);
    assert.equal(monitor.task_template.cookie_profile_id, cookieProfileID);
    assert.equal(monitor.task_template.auto_publish, false);
    console.log(`[1/3] React 页面已创建真实自动建单监控 ${monitor.id}`);
  } else {
    console.log(`[1/3] 复用已存在的真实自动建单监控 ${monitor.id}`);
  }

  let history = await requestJSON(
    page,
    `/api/v1/youtube-monitors/${monitor.id}/history`
  );
  if (!history.runs.length) {
    await page.goto(`${baseURL}/monitors`, { waitUntil: "domcontentloaded" });
    const row = page.locator("article.monitor-row").filter({ hasText: monitorName });
    await row.getByRole("heading", { name: monitorName, exact: true }).waitFor();
    const [runResponse] = await Promise.all([
      page.waitForResponse((response) =>
        response.url().endsWith(`/api/v1/youtube-monitors/${monitor.id}/run`)
      ),
      row.getByRole("button", { name: "立即执行", exact: true }).click()
    ]);
    assert.ok(runResponse.ok(), `页面立即执行返回 HTTP ${runResponse.status()}`);
    console.log("[2/3] React 页面已把真实监控送入持久化调度队列");
    history = await waitForTerminalRun(page, monitor.id);
  }

  const run = history.runs[0];
  assert.equal(run.status, "completed", run.error_message || "真实监控运行失败");
  assert.equal(run.discovered_count, 1);
  assert.equal(run.accepted_count, 1);
  assert.equal(run.task_count, 1);
  const taskItem = history.items.find((item) => item.run_id === run.id && item.task_id);
  assert.ok(taskItem?.task_id, "监控已接受候选但没有持久化 task_id");
  const task = await requestJSON(page, `/api/v1/tasks/${taskItem.task_id}`);
  assert.equal(task.source_url, taskItem.source_url);
  assert.equal(task.cookie_profile_id, cookieProfileID);
  assert.ok(task.target_platforms.includes("bilibili"));
  console.log(
    `[3/3] 自动建单成功 ${task.id}，当前状态 ${task.status}，媒体 ${taskItem.duration_seconds} 秒`
  );

  await browser.close();
  activeBrowser = undefined;
  console.log(
    JSON.stringify(
      {
        ok: true,
        monitor_id: monitor.id,
        run_id: run.id,
        task_id: task.id,
        source_url: task.source_url,
        task_status: task.status,
        duration_seconds: taskItem.duration_seconds,
        auto_publish: monitor.task_template.auto_publish
      },
      null,
      2
    )
  );
}

main().catch(async (error) => {
  console.error(error);
  if (activeBrowser) await activeBrowser.close().catch(() => undefined);
  process.exit(1);
});
