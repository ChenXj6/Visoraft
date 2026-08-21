const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://127.0.0.1:4173";
const artifactDir = path.resolve(
  __dirname,
  "../../artifacts/v1/test-runs/local-library/automated"
);

function observe(page, diagnostics) {
  page.on("console", (message) => {
    if (message.type() === "error") diagnostics.push(`console: ${message.text()}`);
  });
  page.on("pageerror", (error) => diagnostics.push(`pageerror: ${error.message}`));
  page.on("response", (response) => {
    if (response.status() >= 500) diagnostics.push(`HTTP ${response.status()} ${response.url()}`);
  });
}

async function inspectLayout(page) {
  return page.evaluate(() => {
    const visible = (element) => {
      const style = getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      return style.display !== "none" && style.visibility !== "hidden" && rect.width > 0 && rect.height > 0;
    };
    const fontOffenders = [...document.body.querySelectorAll("*")]
      .filter((element) =>
        visible(element) &&
        [...element.childNodes].some(
          (node) => node.nodeType === Node.TEXT_NODE && Boolean(node.textContent?.trim())
        )
      )
      .map((element) => ({
        text: (element.textContent || "").trim().replace(/\s+/g, " ").slice(0, 80),
        size: Number.parseFloat(getComputedStyle(element).fontSize)
      }))
      .filter((item) => Number.isFinite(item.size) && item.size < 12);
    return {
      documentWidth: document.documentElement.scrollWidth,
      viewportWidth: document.documentElement.clientWidth,
      fontOffenders,
      theme: document.documentElement.dataset.theme || ""
    };
  });
}

async function checkTheme(browser, theme, diagnostics) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  await context.addInitScript((value) => localStorage.setItem("visoraft-theme", value), theme);
  const page = await context.newPage();
  observe(page, diagnostics);

  await page.goto(`${baseURL}/files`, { waitUntil: "networkidle", timeout: 30_000 });
  await page.getByRole("heading", { name: "本地文件", exact: true }).waitFor();
  await page.locator(".local-path-bar code").waitFor();
  assert.match(await page.locator(".local-path-bar code").innerText(), /[\\/]/, "未展示真实存储路径");
  assert.ok(await page.locator(".local-collections > button").count() >= 1, "没有文件集合");
  assert.ok(await page.locator(".local-task-folder").count() >= 1, "没有任务或剧集目录");
  assert.ok(await page.locator(".local-file-row").count() >= 1, "没有文件记录");
  const filePath = await page.locator(".local-file-location code").first().innerText();
  assert.match(filePath, /[\\/]/, "文件行未展示绝对路径");

  const fileLayout = await inspectLayout(page);
  assert.ok(fileLayout.documentWidth <= fileLayout.viewportWidth + 1, `文件页横向溢出: ${JSON.stringify(fileLayout)}`);
  assert.deepEqual(fileLayout.fontOffenders, [], `文件页存在小于 12px 的文字: ${JSON.stringify(fileLayout.fontOffenders)}`);
  assert.equal(fileLayout.theme, theme, `文件页未使用 ${theme} 主题`);
  await page.screenshot({ path: path.join(artifactDir, `files-${theme}.png`), fullPage: true });

  await page.goto(`${baseURL}/settings?section=library`, { waitUntil: "networkidle", timeout: 30_000 });
  await page.getByRole("heading", { name: "本地媒体库", exact: true }).first().waitFor();
  await page.getByText("当前生效位置", { exact: true }).waitFor();
  const input = page.getByLabel("电脑上的存储位置");
  assert.ok((await input.inputValue()).length > 2, "存储路径输入框为空");
  const settingsLayout = await inspectLayout(page);
  assert.ok(settingsLayout.documentWidth <= settingsLayout.viewportWidth + 1, `设置页横向溢出: ${JSON.stringify(settingsLayout)}`);
  assert.deepEqual(settingsLayout.fontOffenders, [], `设置页存在小于 12px 的文字: ${JSON.stringify(settingsLayout.fontOffenders)}`);
  assert.equal(settingsLayout.theme, theme, `设置页未使用 ${theme} 主题`);
  await page.screenshot({ path: path.join(artifactDir, `settings-${theme}.png`), fullPage: true });
  await context.close();
}

async function main() {
  fs.mkdirSync(artifactDir, { recursive: true });
  const diagnostics = [];
  const browser = await chromium.launch({ headless: true });
  try {
    await checkTheme(browser, "light", diagnostics);
    await checkTheme(browser, "dark", diagnostics);
    assert.deepEqual(diagnostics, [], `浏览器诊断异常: ${JSON.stringify(diagnostics)}`);
    fs.writeFileSync(
      path.join(artifactDir, "report.json"),
      JSON.stringify({ baseURL, themes: ["light", "dark"], diagnostics, status: "passed" }, null, 2)
    );
    console.log("local library browser acceptance passed");
  } finally {
    await browser.close();
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
