const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://localhost:4173";
const artifactsDir = path.resolve(
  __dirname,
  "../../test-artifacts/youtube-real-monitor"
);
const monitors = {
  search: {
    id: "3df2b7e0-8002-4d55-9de0-ca3d8ae5562e",
    name: "真实全网搜索验收-20260811"
  },
  channel: {
    id: "b8f1f13a-59c0-4ff9-9c83-72da5a904e8d",
    name: "真实频道监控验收-20260811"
  },
  category: {
    id: "528680f2-026a-4885-a6c2-763893aa91eb",
    name: "真实分类监控验收-20260811"
  },
  recovery: {
    id: "810d2eda-ea77-4c3b-ba86-7e533da7ed52",
    name: "真实失败恢复验收-20260811"
  }
};
const diagnostics = [];
let activeBrowser;

fs.mkdirSync(artifactsDir, { recursive: true });

function observe(page) {
  page.on("console", (message) => {
    if (message.type() === "error") diagnostics.push(`console: ${message.text()}`);
  });
  page.on("pageerror", (error) => diagnostics.push(`page: ${error.message}`));
  page.on("response", (response) => {
    if (response.status() >= 500) {
      diagnostics.push(`HTTP ${response.status()} ${response.url()}`);
    }
  });
}

async function waitForAPI(page) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    const response = await page.request
      .get(`${baseURL}/api/v1/system/status`, { timeout: 3_000 })
      .catch(() => undefined);
    if (response?.ok()) return;
    await page.waitForTimeout(500);
  }
  throw new Error("本地控制接口未就绪");
}

async function assertNoHorizontalOverflow(page, scope) {
  const dimensions = await page.evaluate(() => ({
    viewport: document.documentElement.clientWidth,
    document: document.documentElement.scrollWidth
  }));
  assert.ok(
    dimensions.document <= dimensions.viewport + 1,
    `${scope} 存在页面级横向溢出：${JSON.stringify(dimensions)}`
  );
}

async function assertVisibleTextMinimum(page, scope, minimum = 12) {
  const offenders = await page.evaluate((min) => {
    const ignored = new Set(["SCRIPT", "STYLE", "NOSCRIPT", "SVG"]);
    return [...document.body.querySelectorAll("*")]
      .filter((element) => {
        if (ignored.has(element.tagName)) return false;
        if (
          ![...element.childNodes].some(
            (node) => node.nodeType === Node.TEXT_NODE && node.textContent?.trim()
          )
        ) {
          return false;
        }
        const style = getComputedStyle(element);
        const rect = element.getBoundingClientRect();
        return (
          style.display !== "none" &&
          style.visibility !== "hidden" &&
          Number.parseFloat(style.opacity) > 0 &&
          rect.width > 0 &&
          rect.height > 0 &&
          Number.parseFloat(style.fontSize) < min
        );
      })
      .slice(0, 30)
      .map((element) => ({
        tag: element.tagName.toLowerCase(),
        className: element.className,
        text: element.textContent.trim().replace(/\s+/g, " ").slice(0, 80),
        fontSize: getComputedStyle(element).fontSize
      }));
  }, minimum);
  assert.deepEqual(
    offenders,
    [],
    `${scope} 存在小于 ${minimum}px 的可见文字：\n${JSON.stringify(offenders, null, 2)}`
  );
}

async function openPage(page, route, scope) {
  await page.goto(`${baseURL}${route}`, {
    waitUntil: "domcontentloaded",
    timeout: 15_000
  });
  await page.locator(".main-content").waitFor({ timeout: 10_000 });
  await page.waitForTimeout(500);
  await assertNoHorizontalOverflow(page, scope);
  await assertVisibleTextMinimum(page, scope);
}

async function getJSON(page, route) {
  const response = await page.request.get(`${baseURL}${route}`);
  assert.ok(response.ok(), `${route} 返回 HTTP ${response.status()}`);
  return response.json();
}

async function main() {
  const browser = await chromium.launch({ headless: true });
  activeBrowser = browser;
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  page.setDefaultTimeout(12_000);
  observe(page);
  await waitForAPI(page);
  console.log("[1/6] 本地服务已就绪");

  const listPayload = await getJSON(page, "/api/v1/youtube-monitors");
  for (const target of Object.values(monitors)) {
    assert.ok(
      listPayload.items.some((item) => item.id === target.id && item.name === target.name),
      `缺少真实监控配置：${target.name}`
    );
  }
  console.log("[2/6] 四条真实监控持久化数据已确认");

  await openPage(page, "/monitors", "监控列表桌面端");
  for (const target of Object.values(monitors)) {
    await page.getByRole("heading", { name: target.name, exact: true }).waitFor();
  }

  const categoryRow = page
    .locator("article.monitor-row")
    .filter({ hasText: monitors.category.name });
  const categoryInitiallyEnabled = listPayload.items.find(
    (item) => item.id === monitors.category.id
  ).enabled;
  if (!categoryInitiallyEnabled) {
    const [resumeResponse] = await Promise.all([
      page.waitForResponse((response) =>
        response.url().endsWith(`/api/v1/youtube-monitors/${monitors.category.id}/resume`)
      ),
      categoryRow.getByRole("button", { name: "恢复", exact: true }).click()
    ]);
    assert.ok(resumeResponse.ok(), "通过页面恢复监控失败");
  }
  const [pauseResponse] = await Promise.all([
    page.waitForResponse((response) =>
      response.url().endsWith(`/api/v1/youtube-monitors/${monitors.category.id}/pause`)
    ),
    categoryRow.getByRole("button", { name: "暂停", exact: true }).click()
  ]);
  assert.ok(pauseResponse.ok(), "通过页面暂停监控失败");
  assert.equal((await pauseResponse.json()).enabled, false);
  await categoryRow.getByRole("button", { name: "恢复", exact: true }).waitFor();

  const [resumeResponse] = await Promise.all([
    page.waitForResponse((response) =>
      response.url().endsWith(`/api/v1/youtube-monitors/${monitors.category.id}/resume`)
    ),
    categoryRow.getByRole("button", { name: "恢复", exact: true }).click()
  ]);
  assert.ok(resumeResponse.ok(), "通过页面再次恢复监控失败");
  assert.equal((await resumeResponse.json()).enabled, true);
  await categoryRow.getByRole("button", { name: "暂停", exact: true }).waitFor();
  console.log("[3/6] 页面暂停与恢复操作已确认");
  await page.screenshot({
    path: path.join(artifactsDir, "01-monitor-list-desktop.png"),
    fullPage: true
  });
  console.log("[4/6] 全网搜索、去重与中文类型展示已确认");

  const searchHistory = await getJSON(
    page,
    `/api/v1/youtube-monitors/${monitors.search.id}/history`
  );
  assert.ok(searchHistory.runs.some((run) => run.discovered_count === 5));
  assert.ok(searchHistory.runs.some((run) => run.duplicate_count === 5));
  assert.ok(searchHistory.items.some((item) => item.decision === "duplicate"));
  assert.ok(searchHistory.items.every((item) => !item.task_id));

  await openPage(
    page,
    `/monitors/${monitors.search.id}/history`,
    "真实搜索运行记录桌面端"
  );
  await page.getByRole("heading", { name: "运行批次", exact: true }).waitFor();
  await page.getByText("重复", { exact: true }).first().waitFor();
  const typeAndDuration = await page
    .locator(".monitor-table tbody td:nth-child(2) small")
    .first()
    .innerText();
  assert.match(typeAndDuration, /^(常规视频|短视频|直播) · \d{2}(?::\d{2}){1,2}$/);
  assert.doesNotMatch(typeAndDuration, /\b(video|short|live)\b|\d+s$/i);
  await page.screenshot({
    path: path.join(artifactsDir, "02-search-history-desktop.png"),
    fullPage: true
  });
  console.log("[5/6] 失败恢复与旧英文记录中文化已确认");

  const channelHistory = await getJSON(
    page,
    `/api/v1/youtube-monitors/${monitors.channel.id}/history`
  );
  assert.ok(channelHistory.items.length >= 5);
  assert.ok(
    channelHistory.items.every(
      (item) => item.channel_id === "UCs42KS9F5eCOJrSoLkvqtWQ"
    ),
    "频道监控混入了其他频道的视频"
  );

  const recoveryHistory = await getJSON(
    page,
    `/api/v1/youtube-monitors/${monitors.recovery.id}/history`
  );
  assert.ok(recoveryHistory.runs.some((run) => run.status === "failed"));
  assert.ok(recoveryHistory.runs.some((run) => run.status === "completed"));
  await openPage(
    page,
    `/monitors/${monitors.recovery.id}/history`,
    "失败恢复运行记录桌面端"
  );
  await page
    .getByText("单次请求上限不足：至少需要 2 次请求才能读取视频详情", {
      exact: true
    })
    .waitFor();
  assert.equal(
    await page.getByText(
      "monitor request limit reached before video details could be loaded",
      { exact: true }
    ).count(),
    0
  );
  await page.screenshot({
    path: path.join(artifactsDir, "03-recovery-history-desktop.png"),
    fullPage: true
  });

  await openPage(
    page,
    `/monitors/${monitors.category.id}/edit`,
    "分类监控编辑页桌面端"
  );
  const categorySelect = page
    .locator("label.field")
    .filter({ hasText: "YouTube 视频分类" })
    .locator("select");
  await categorySelect.waitFor({ state: "visible", timeout: 12_000 });
  let categoryOptions = [];
  for (let attempt = 0; attempt < 20; attempt += 1) {
    categoryOptions = await categorySelect.locator("option").allTextContents();
    if (categoryOptions.includes("科学和技术 · 28")) break;
    await page.waitForTimeout(250);
  }
  assert.ok(
    categoryOptions.includes("科学和技术 · 28"),
    `未加载中文科学和技术分类：${JSON.stringify(categoryOptions)}`
  );
  assert.equal(await categorySelect.inputValue(), "28");
  await assertVisibleTextMinimum(page, "分类监控编辑页加载完成");
  await page.screenshot({
    path: path.join(artifactsDir, "04-category-edit-desktop.png"),
    fullPage: true
  });

  await page.setViewportSize({ width: 390, height: 844 });
  await openPage(page, "/monitors", "监控列表移动端");
  await page.screenshot({
    path: path.join(artifactsDir, "05-monitor-list-mobile.png"),
    fullPage: true
  });
  await openPage(
    page,
    `/monitors/${monitors.recovery.id}/history`,
    "失败恢复运行记录移动端"
  );
  await page.screenshot({
    path: path.join(artifactsDir, "06-recovery-history-mobile.png"),
    fullPage: true
  });
  console.log("[6/6] 中文分类与移动端规范已确认");

  await browser.close();
  activeBrowser = undefined;
  assert.deepEqual(diagnostics, [], diagnostics.join("\n"));
  console.log(
    JSON.stringify(
      {
        ok: true,
        monitorIDs: Object.fromEntries(
          Object.entries(monitors).map(([key, value]) => [key, value.id])
        ),
        searchRuns: searchHistory.runs.length,
        searchItems: searchHistory.items.length,
        channelItems: channelHistory.items.length,
        recoveryRuns: recoveryHistory.runs.length,
        typeAndDuration,
        screenshots: fs.readdirSync(artifactsDir).sort(),
        diagnostics
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
