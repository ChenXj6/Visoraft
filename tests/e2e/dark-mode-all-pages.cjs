const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://127.0.0.1:4173";
const artifactDir = path.resolve(
  __dirname,
  "../../artifacts/v1/test-runs/dark-mode-audit/automated"
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
  ["compact", 768, 900]
];

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

  assert.match(taskID || "", /^[0-9a-f-]{36}$/, "缺少可用于深色回归的任务");
  assert.match(
    monitorID || "",
    /^[0-9a-f-]{36}$/,
    "缺少可用于深色回归的监控"
  );

  return [
    ["dashboard", "/"],
    ["tasks", "/tasks"],
    ["task-new", "/tasks/new"],
    ["task-detail", `/tasks/${taskID}`],
    ["reviews", "/reviews"],
    ["review-detail", `/reviews/${taskID}`],
    ["files", "/files"],
    ["publishing", "/publishing"],
    ["publishing-settings", "/publishing/settings"],
    ["publishing-detail", `/publishing/${taskID}`],
    ["monitors", "/monitors"],
    ["monitor-new", "/monitors/new"],
    ["monitor-edit", `/monitors/${monitorID}/edit`],
    ["monitor-history", `/monitors/${monitorID}/history`],
    ["cookies", "/cookies"],
    ...settingsSections.map((section) => [
      `settings-${section}`,
      `/settings?section=${section}`
    ])
  ];
}

function attachDiagnostics(page, scope, diagnostics) {
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
    const directTextElements = [...document.body.querySelectorAll("*")].filter(
      (element) =>
        visible(element) &&
        [...element.childNodes].some(
          (node) =>
            node.nodeType === Node.TEXT_NODE && Boolean(node.textContent?.trim())
        )
    );
    const fontOffenders = directTextElements
      .map((element) => ({
        element,
        size: Number.parseFloat(getComputedStyle(element).fontSize)
      }))
      .filter(({ size }) => Number.isFinite(size) && size < 12)
      .map(({ element, size }) => ({
        tag: element.tagName.toLowerCase(),
        className: String(element.className || "").slice(0, 120),
        text: (element.textContent || "").trim().replace(/\s+/g, " ").slice(0, 120),
        size
      }));

    const lightSurfaces = [...document.body.querySelectorAll("*")]
      .filter((element) => visible(element))
      .map((element) => {
        const style = getComputedStyle(element);
        const rect = element.getBoundingClientRect();
        const match = style.backgroundColor.match(
          /^rgba?\((\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\)$/
        );
        return {
          element,
          rect,
          color: match
            ? {
                red: Number(match[1]),
                green: Number(match[2]),
                blue: Number(match[3]),
                alpha: match[4] === undefined ? 1 : Number(match[4])
              }
            : null
        };
      })
      .filter(({ element, rect, color }) => {
        if (!color || color.alpha < 0.2 || rect.width < 80 || rect.height < 20) {
          return false;
        }
        if (element.matches("img, picture, video, canvas, svg")) return false;
        return color.red > 235 && color.green > 235 && color.blue > 235;
      })
      .map(({ element, color }) => ({
        tag: element.tagName.toLowerCase(),
        className: String(element.className || "").slice(0, 120),
        text: (element.textContent || "").trim().replace(/\s+/g, " ").slice(0, 100),
        background: color
      }));

    const viewportWidth = document.documentElement.clientWidth;
    const main = document.querySelector("main");
    const mainRect = main?.getBoundingClientRect();
    return {
      theme: document.documentElement.dataset.theme || "",
      title: main?.querySelector("h1")?.textContent?.trim() || "",
      viewportWidth,
      documentWidth: document.documentElement.scrollWidth,
      bodyWidth: document.body.scrollWidth,
      fontOffenders,
      lightSurfaces,
      main: mainRect
        ? { left: mainRect.left, right: mainRect.right, width: mainRect.width }
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
      await context.addInitScript(() => {
        if (!localStorage.getItem("visoraft-theme")) {
          localStorage.setItem("visoraft-theme", "light");
        }
        if (!localStorage.getItem("visoraft-accent")) {
          localStorage.setItem("visoraft-accent", "blue");
        }
      });
      const page = await context.newPage();
      attachDiagnostics(page, viewportName, diagnostics);
      let themeSwitched = false;

      for (const [routeName, route] of routes) {
        const scope = `${viewportName} ${routeName}`;
        const response = await page.goto(`${baseURL}${route}`, {
          waitUntil: "domcontentloaded",
          timeout: 30_000
        });
        assert.ok(response?.ok(), `${scope}: 页面返回 HTTP ${response?.status()}`);
        try {
          await page.locator("main h1").first().waitFor({ timeout: 15_000 });
        } catch (error) {
          throw new Error(`${scope}: 等待页面主标题失败`, { cause: error });
        }
        await page.waitForTimeout(250);

        if (!themeSwitched) {
          assert.equal(
            await page.locator("html").getAttribute("data-theme"),
            "light",
            `${scope}: 切换前的浅色主题未生效`
          );
          await page.getByRole("button", { name: "切换到暗色模式" }).click();
          await page.waitForFunction(
            () =>
              document.documentElement.dataset.theme === "dark" &&
              localStorage.getItem("visoraft-theme") === "dark"
          );
          await page.waitForTimeout(250);
          themeSwitched = true;
        }

        const result = await inspect(page);
        assert.equal(result.theme, "dark", `${scope}: 深色主题未生效`);
        assert.ok(result.title, `${scope}: 缺少主标题`);
        assert.deepEqual(
          result.fontOffenders,
          [],
          `${scope}: 存在小于 12px 的可见文字`
        );
        assert.deepEqual(
          result.lightSurfaces,
          [],
          `${scope}: 深色页面残留浅色内容面板`
        );
        assert.ok(
          result.documentWidth <= result.viewportWidth + 1 &&
            result.bodyWidth <= result.viewportWidth + 1,
          `${scope}: 页面横向溢出 ${JSON.stringify(result)}`
        );
        assert.ok(
          result.main && result.main.left >= 0 && result.main.right <= width + 1,
          `${scope}: 主内容超出视口 ${JSON.stringify(result.main)}`
        );

        report.push({ viewport: viewportName, route: routeName, path: route, ...result });
        if (viewportName === "desktop") {
          await page.screenshot({
            path: path.join(artifactDir, `${routeName}.png`),
            fullPage: true
          });
        }
      }

      await page.goto(baseURL, { waitUntil: "domcontentloaded", timeout: 30_000 });
      await page.locator("main h1").first().waitFor({ timeout: 15_000 });
      if (viewportName === "compact") {
        await page.getByRole("button", { name: "打开导航" }).click();
        await page.locator(".console-nav-open").waitFor();
        await page.waitForTimeout(220);
        const navigationRect = await page.locator(".console-nav-open").evaluate((element) => {
          const rect = element.getBoundingClientRect();
          const style = getComputedStyle(element);
          return {
            left: rect.left,
            right: rect.right,
            width: rect.width,
            opacity: Number.parseFloat(style.opacity || "1"),
            visibility: style.visibility
          };
        });
        assert.ok(
          navigationRect.left >= 0 &&
            navigationRect.right > 200 &&
            navigationRect.width > 200 &&
            navigationRect.opacity > 0 &&
            navigationRect.visibility === "visible",
          `compact mobile-navigation: 导航未进入可视区域 ${JSON.stringify(navigationRect)}`
        );
        const mobileNavigation = await inspect(page);
        assert.equal(mobileNavigation.theme, "dark", "compact mobile-navigation: 深色主题未保持");
        assert.deepEqual(
          mobileNavigation.lightSurfaces,
          [],
          "compact mobile-navigation: 导航层残留浅色面板"
        );
        await page.screenshot({
          path: path.join(artifactDir, "compact-mobile-navigation.png"),
          fullPage: true
        });
        await page.locator(".console-nav .nav-close").click();
      }

      await page.locator(".command-trigger").click();
      await page.locator("dialog.command-palette").waitFor();
      const commandPalette = await inspect(page);
      assert.equal(commandPalette.theme, "dark", `${viewportName} command-palette: 深色主题未保持`);
      assert.deepEqual(
        commandPalette.lightSurfaces,
        [],
        `${viewportName} command-palette: 命令弹窗残留浅色面板`
      );
      await page.screenshot({
        path: path.join(artifactDir, `${viewportName}-command-palette.png`),
        fullPage: true
      });
      await page.keyboard.press("Escape");

      await page.getByRole("button", { name: "切换到亮色模式" }).click();
      await page.waitForFunction(() => document.documentElement.dataset.theme === "light");
      await page.getByRole("button", { name: "切换到暗色模式" }).click();
      await page.waitForFunction(() => document.documentElement.dataset.theme === "dark");
      await page.waitForTimeout(250);
      const themeToggle = await inspect(page);
      assert.equal(themeToggle.theme, "dark", `${viewportName} theme-toggle: 深色主题未恢复`);
      assert.deepEqual(
        themeToggle.lightSurfaces,
        [],
        `${viewportName} theme-toggle: 深色主题残留浅色区域`
      );
      await page.screenshot({
        path: path.join(artifactDir, `${viewportName}-theme-toggle.png`),
        fullPage: true
      });
      await context.close();
    }

    assert.deepEqual(diagnostics, [], diagnostics.join("\n"));
    const summary = {
      status: "passed",
      theme: "dark",
      routeCount: report.length / viewports.length,
      viewportCount: viewports.length,
      checks: report.length,
      interactiveChecks: 5,
      minimumFontSize: 12,
      lightSurfaceOffenders: 0,
      horizontalOverflowOffenders: 0,
      browserDiagnostics: diagnostics,
      persistedDataChanged: false
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
