const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://127.0.0.1:4173";
const artifactDir = path.resolve(
  __dirname,
  "../../artifacts/v1/test-runs/all-pages-responsive-audit"
);

const settingsSections = [
  "review",
  "automation",
  "models",
  "subtitles",
  "prompts",
  "transcode",
  "moderation",
  "publishing",
  "library",
  "youtube"
];

const viewports = [
  ["desktop", 1440, 900],
  ["compact-desktop", 1024, 768],
  ["tablet", 768, 1024],
  ["mobile", 390, 844],
  ["narrow-mobile", 320, 720]
];

function collectDiagnostics(page, scope, diagnostics) {
  page.on("console", (message) => {
    if (message.type() === "error") {
      diagnostics.push(`${scope}: console ${message.text()}`);
    }
  });
  page.on("pageerror", (error) => {
    diagnostics.push(`${scope}: pageerror ${error.message}`);
  });
  page.on("response", (response) => {
    if (response.status() >= 500) {
      diagnostics.push(`${scope}: HTTP ${response.status()} ${response.url()}`);
    }
  });
}

async function apiJSON(request, pathname) {
  const response = await request.get(`${baseURL}${pathname}`);
  assert.ok(response.ok(), `${pathname} 返回 HTTP ${response.status()}`);
  return response.json();
}

async function resolveRoutes(request) {
  const [taskList, monitorList] = await Promise.all([
    apiJSON(request, "/api/v1/tasks?limit=100"),
    apiJSON(request, "/api/v1/youtube-monitors?limit=100")
  ]);
  const taskID = process.env.VISORAFT_AUDIT_TASK_ID || taskList.items?.[0]?.id;
  const monitorID =
    process.env.VISORAFT_AUDIT_MONITOR_ID || monitorList.items?.[0]?.id;
  assert.match(taskID || "", /^[0-9a-f-]{36}$/, "缺少可用于页面审计的任务");
  assert.match(monitorID || "", /^[0-9a-f-]{36}$/, "缺少可用于页面审计的监控");
  return [
    ["dashboard", "/"],
    ["tasks", "/tasks"],
    ["task-detail", `/tasks/${taskID}`],
    ["new-task", "/tasks/new"],
    ["reviews", "/reviews"],
    ["review-detail", `/reviews/${taskID}`],
    ["files", "/files"],
    ["publishing", "/publishing"],
    ["publishing-settings", "/publishing/settings"],
    ["publishing-detail", `/publishing/${taskID}`],
    ["monitors", "/monitors"],
    ["new-monitor", "/monitors/new"],
    ["monitor-edit", `/monitors/${monitorID}/edit`],
    ["monitor-history", `/monitors/${monitorID}/history`],
    ["cookies", "/cookies"],
    ...settingsSections.map((section) => [
      `settings-${section}`,
      `/settings?section=${section}`
    ])
  ];
}

async function inspect(page) {
  return page.evaluate(() => {
    const visible = (element) => {
      const style = getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      return (
        style.display !== "none" &&
        style.visibility !== "hidden" &&
        Number.parseFloat(style.opacity || "1") > 0 &&
        rect.width > 0 &&
        rect.height > 0
      );
    };
    const textElements = [...document.body.querySelectorAll("*")].filter(
      (element) =>
        visible(element) &&
        [...element.childNodes].some(
          (node) =>
            node.nodeType === Node.TEXT_NODE && Boolean(node.textContent?.trim())
        )
    );
    const fontOffenders = textElements
      .map((element) => ({
        element,
        size: Number.parseFloat(getComputedStyle(element).fontSize)
      }))
      .filter(({ size }) => Number.isFinite(size) && size < 12)
      .map(({ element, size }) => ({
        tag: element.tagName.toLowerCase(),
        className: String(element.className || "").slice(0, 100),
        text: (element.textContent || "").trim().replace(/\s+/g, " ").slice(0, 100),
        size
      }));
    const main = document.querySelector("main");
    const mainRect = main?.getBoundingClientRect();
    const heading = main?.querySelector("h1");
    const headingRect = heading?.getBoundingClientRect();
    const viewportWidth = document.documentElement.clientWidth;
    return {
      title: heading?.textContent?.trim() || "",
      viewportWidth,
      documentWidth: document.documentElement.scrollWidth,
      bodyWidth: document.body.scrollWidth,
      fontOffenders,
      main: mainRect
        ? { left: mainRect.left, right: mainRect.right, width: mainRect.width }
        : null,
      heading: headingRect
        ? {
            left: headingRect.left,
            right: headingRect.right,
            width: headingRect.width,
            scrollWidth: heading.scrollWidth
          }
        : null
    };
  });
}

async function main() {
  fs.mkdirSync(artifactDir, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  const report = [];
  const diagnostics = [];
  try {
    const requestContext = await browser.newContext();
    const routes = await resolveRoutes(requestContext.request);
    await requestContext.close();
    for (const [viewportName, width, height] of viewports) {
      const context = await browser.newContext({
        viewport: { width, height },
        locale: "zh-CN"
      });
      const page = await context.newPage();
      collectDiagnostics(page, viewportName, diagnostics);
      for (const [routeName, route] of routes) {
        const scope = `${viewportName} ${routeName}`;
        const response = await page.goto(`${baseURL}${route}`, {
          waitUntil: "networkidle",
          timeout: 30_000
        });
        assert.ok(response?.ok(), `${scope}: 页面返回 HTTP ${response?.status()}`);
        await page.locator("main h1").first().waitFor({ timeout: 15_000 });
        const result = await inspect(page);
        assert.ok(result.title, `${scope}: 缺少主标题`);
        assert.deepEqual(
          result.fontOffenders,
          [],
          `${scope}: 存在小于 12px 的可见文字`
        );
        assert.ok(
          result.documentWidth <= result.viewportWidth + 1 &&
            result.bodyWidth <= result.viewportWidth + 1,
          `${scope}: 页面横向溢出 ${JSON.stringify(result)}`
        );
        if (width >= 768) {
          assert.ok(
            result.main && result.main.left >= 0 && result.main.right <= width + 1,
            `${scope}: 主内容超出视口 ${JSON.stringify(result.main)}`
          );
          assert.ok(
            result.main.width >= width * 0.62,
            `${scope}: 主内容区域被挤压 ${JSON.stringify(result.main)}`
          );
        }
        report.push({ viewport: viewportName, route: routeName, ...result });
        if (
          (viewportName === "desktop" || viewportName === "narrow-mobile") &&
          ["dashboard", "new-task", "monitor-edit", "monitor-history", "settings"].includes(
            routeName
          )
        ) {
          await page.screenshot({
            path: path.join(artifactDir, `${viewportName}-${routeName}.png`),
            fullPage: true
          });
        }
      }
      await context.close();
    }
    assert.deepEqual(diagnostics, [], diagnostics.join("\n"));
    const summary = {
      status: "passed",
      routes: routes.map(([name, route]) => ({ name, route })),
      viewports: viewports.map(([name, width, height]) => ({ name, width, height })),
      checks: report.length,
      minimumFontSize: 12,
      persistedDataChanged: false,
      diagnostics
    };
    fs.writeFileSync(
      path.join(artifactDir, "report.json"),
      `${JSON.stringify({ summary, report }, null, 2)}\n`,
      "utf8"
    );
    process.stdout.write(`${JSON.stringify(summary, null, 2)}\n`);
  } finally {
    await browser.close();
  }
}

main().catch((error) => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
