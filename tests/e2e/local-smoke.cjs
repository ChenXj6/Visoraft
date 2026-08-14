const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://localhost:4173";
const artifactsDir = path.resolve(__dirname, "../../test-artifacts/playwright");
const diagnostics = [];

const routes = [
  ["/", "今天的处理队列"],
  ["/tasks", "媒体任务"],
  ["/reviews", "媒体复核台"],
  ["/publishing", "投稿"],
  ["/monitors", "发现与监控"],
  ["/settings", "处理策略与服务接入"],
  ["/cookies", "登录凭据工作台"]
];

function observe(page, scope) {
  page.on("console", (message) => {
    if (message.type() === "error") diagnostics.push(`${scope}: ${message.text()}`);
  });
  page.on("pageerror", (error) => diagnostics.push(`${scope}: ${error.message}`));
  page.on("response", (response) => {
    if (response.status() >= 500) {
      diagnostics.push(`${scope}: HTTP ${response.status()} ${response.url()}`);
    }
  });
}

async function auditRoute(page, route, heading, scope, screenshotName) {
  await page.goto(`${baseURL}${route}`, { waitUntil: "domcontentloaded", timeout: 20_000 });
  await page.getByRole("heading", { name: heading, exact: true }).waitFor();
  const result = await page.evaluate(() => {
    const offenders = [...document.body.querySelectorAll("*")]
      .filter((element) => {
        const ownText = [...element.childNodes].some(
          (node) => node.nodeType === Node.TEXT_NODE && node.textContent?.trim()
        );
        if (!ownText) return false;
        const style = getComputedStyle(element);
        const rect = element.getBoundingClientRect();
        return style.display !== "none" && style.visibility !== "hidden" &&
          rect.width > 0 && rect.height > 0 && Number.parseFloat(style.fontSize) < 12;
      })
      .map((element) => ({
        text: (element.textContent || "").trim().slice(0, 80),
        fontSize: getComputedStyle(element).fontSize
      }));
    return {
      offenders,
      viewportWidth: document.documentElement.clientWidth,
      documentWidth: document.documentElement.scrollWidth
    };
  });
  assert.deepEqual(result.offenders, [], `${scope} 存在小于 12px 的可见文字`);
  assert.ok(
    result.documentWidth <= result.viewportWidth + 1,
    `${scope} 横向溢出：${JSON.stringify(result)}`
  );
  await page.screenshot({
    path: path.join(artifactsDir, screenshotName),
    fullPage: true
  });
}

async function main() {
  fs.mkdirSync(artifactsDir, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  try {
    const statusContext = await browser.newContext();
    const statusResponse = await statusContext.request.get(`${baseURL}/api/v1/system/status`);
    assert.ok(statusResponse.ok(), `系统状态接口返回 HTTP ${statusResponse.status()}`);
    const status = await statusResponse.json();
    assert.ok(status.database?.healthy ?? status.database === "ready", "数据库未就绪");
    await statusContext.close();

    for (const viewport of [
      { name: "desktop", width: 1440, height: 900 },
      { name: "mobile", width: 390, height: 844 }
    ]) {
      const context = await browser.newContext({
        viewport: { width: viewport.width, height: viewport.height },
        locale: "zh-CN"
      });
      const page = await context.newPage();
      observe(page, viewport.name);
      for (let index = 0; index < routes.length; index += 1) {
        const [route, heading] = routes[index];
        await auditRoute(
          page,
          route,
          heading,
          `${viewport.name} ${route}`,
          `${viewport.name}-${String(index + 1).padStart(2, "0")}.png`
        );
      }
      await context.close();
    }
    assert.deepEqual(diagnostics, [], diagnostics.join("\n"));
    process.stdout.write(`${JSON.stringify({
      status: "passed",
      routes: routes.map(([route]) => route),
      viewports: [1440, 390],
      persistedDataChanged: false,
      diagnostics
    }, null, 2)}\n`);
  } finally {
    await browser.close();
  }
}

main().catch((error) => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
