const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://127.0.0.1:4173";
const artifactDir = path.resolve(__dirname, "../../artifacts/v1/test-runs/pixel-baseline-audit");
const windowsChrome = "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe";

function browserLaunchOptions() {
  const executablePath = process.env.VISORAFT_BROWSER_PATH || (fs.existsSync(windowsChrome) ? windowsChrome : undefined);
  return executablePath ? { headless: true, executablePath } : { headless: true };
}

async function createPage(browser, dark = false) {
  const context = await browser.newContext({
    viewport: { width: 1320, height: 860 },
    locale: "zh-CN",
    permissions: ["clipboard-read", "clipboard-write"]
  });
  await context.addInitScript((useDark) => {
    window.localStorage.setItem("visoraft-theme", useDark ? "dark" : "light");
    window.localStorage.setItem("visoraft-accent", "blue");
  }, dark);
  return { context, page: await context.newPage() };
}

async function capture(page, name) {
  await page.screenshot({
    path: path.join(artifactDir, `current-${name}.png`),
    animations: "disabled"
  });
}

async function captureInteractive(browser, dark) {
  const suffix = dark ? "dark" : "light";

  {
    const { context, page } = await createPage(browser, dark);
    await page.goto(`${baseURL}/cookies`, { waitUntil: "networkidle" });
    await page.getByRole("button", { name: "删除" }).first().click();
    await page.locator("dialog.confirm-dialog[open]").waitFor();
    await capture(page, `modal-confirm-${suffix}`);
    await context.close();
  }

  {
    const { context, page } = await createPage(browser, dark);
    await page.goto(`${baseURL}/cookies`, { waitUntil: "networkidle" });
    await page.getByRole("button", { name: "去连接" }).click();
    await page.locator("dialog.modal-dialog[open]").waitFor();
    await capture(page, `modal-form-${suffix}`);
    await context.close();
  }

  {
    const { context, page } = await createPage(browser, dark);
    await page.goto(`${baseURL}/tasks`, { waitUntil: "networkidle" });
    await page.getByRole("button", { name: "详情" }).first().click();
    await page.locator("dialog.modal-dialog.unified-modal-wide[open]").waitFor();
    await capture(page, `modal-detail-${suffix}`);
    await context.close();
  }

  {
    const { context, page } = await createPage(browser, dark);
    await page.goto(`${baseURL}/tasks`, { waitUntil: "networkidle" });
    await page.keyboard.press("Control+K");
    await page.locator("dialog.command-palette[open]").waitFor();
    await capture(page, `command-palette-${suffix}`);
    await context.close();
  }

  {
    const { context, page } = await createPage(browser, dark);
    await page.goto(baseURL, { waitUntil: "networkidle" });
    await page.getByRole("button", { name: "查看通知与运行概览" }).click();
    await page.locator("dialog.side-drawer[open]").waitFor();
    await capture(page, `drawer-${suffix}`);
    await context.close();
  }

  {
    const { context, page } = await createPage(browser, dark);
    await page.goto(`${baseURL}/files`, { waitUntil: "networkidle" });
    await page.getByRole("button", { name: "查看本地位置" }).click();
    const drawer = page.locator("dialog.side-drawer[open]");
    await drawer.waitFor();
    await drawer.getByRole("button", { name: "复制路径" }).click();
    await page.locator(".transient-notice").waitFor();
    await drawer.getByRole("button", { name: "关闭抽屉" }).click();
    await drawer.waitFor({ state: "hidden" });
    await capture(page, `toast-${suffix}`);
    await context.close();
  }
}

async function captureDataStates(browser, dark) {
  const suffix = dark ? "dark" : "light";

  {
    const { context, page } = await createPage(browser, dark);
    await page.route(/\/api\/v1\/tasks(?:\?.*)?$/, (route) =>
      route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [] }) })
    );
    await page.goto(`${baseURL}/tasks`, { waitUntil: "networkidle" });
    await page.locator(".empty-state").waitFor();
    await capture(page, `state-empty-${suffix}`);
    await context.close();
  }

  {
    const { context, page } = await createPage(browser, dark);
    let release;
    const held = new Promise((resolve) => { release = resolve; });
    await page.route(/\/api\/v1\/tasks(?:\?.*)?$/, async (route) => {
      await held;
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [] }) });
    });
    await page.goto(`${baseURL}/tasks`, { waitUntil: "domcontentloaded" });
    await page.locator(".loading-block").waitFor();
    await capture(page, `state-loading-${suffix}`);
    release();
    await context.close();
  }

  {
    const { context, page } = await createPage(browser, dark);
    await page.route(/\/api\/v1\/tasks(?:\?.*)?$/, (route) =>
      route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ message: "服务暂时不可用" }) })
    );
    await page.goto(`${baseURL}/tasks`, { waitUntil: "networkidle" });
    await page.locator(".state-panel.state-error").waitFor();
    await capture(page, `state-error-${suffix}`);
    await context.close();
  }

  {
    const { context, page } = await createPage(browser, dark);
    await page.route(/\/api\/v1\/cookie-profiles(?:\?.*)?$/, (route) =>
      route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [] }) })
    );
    await page.goto(`${baseURL}/cookies`, { waitUntil: "networkidle" });
    await page.locator(".empty-state").waitFor();
    await capture(page, `state-cookie-empty-${suffix}`);
    await context.close();
  }

  {
    const { context, page } = await createPage(browser, dark);
    await page.route(/\/api\/v1\/reviews(?:\?.*)?$/, (route) =>
      route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [] }) })
    );
    await page.goto(`${baseURL}/reviews`, { waitUntil: "networkidle" });
    await page.locator(".empty-state").waitFor();
    await capture(page, `state-review-empty-${suffix}`);
    await context.close();
  }

  {
    const { context, page } = await createPage(browser, dark);
    await page.route(/\/api\/v1\/tasks(?:\?.*)?$/, async (route) => {
      const response = await route.fetch();
      const payload = await response.json();
      const source = payload.items?.[0];
      if (!source) {
        await route.fulfill({ response });
        return;
      }
      const failedSteps = Array.isArray(source.steps)
        ? source.steps.map((step, index) => ({
            ...step,
            status: index === 0 ? "failed" : step.status,
            error_code: index === 0 ? "download_timeout" : step.error_code
          }))
        : [];
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          ...payload,
          items: [{
            ...source,
            status: "failed",
            title: "这是一个用于验证超长任务标题截断、省略提示与批量操作边界状态的真实任务标题",
            steps: failedSteps
          }]
        })
      });
    });
    await page.goto(`${baseURL}/tasks`, { waitUntil: "networkidle" });
    const checkbox = page.locator('.prototype-task-check input[type="checkbox"]:not([disabled])').first();
    await checkbox.waitFor();
    await checkbox.check();
    await page.locator(".task-batch-bar").waitFor();
    await capture(page, `state-edge-${suffix}`);
    await context.close();
  }
}

async function main() {
  fs.mkdirSync(artifactDir, { recursive: true });
  const browser = await chromium.launch(browserLaunchOptions());
  try {
    await captureInteractive(browser, false);
    await captureDataStates(browser, false);
    await captureInteractive(browser, true);
    await captureDataStates(browser, true);
  } finally {
    await browser.close();
  }
  process.stdout.write(`${JSON.stringify({ status: "captured", artifactDir }, null, 2)}\n`);
}

main().catch((error) => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
