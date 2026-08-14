const assert = require("node:assert/strict");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://127.0.0.1:4173";
const monitorID =
  process.env.VISORAFT_MONITOR_ID || "8102d777-6cd7-4197-9e26-85a241491c83";
let activeBrowser;

async function visibleTextAudit(page) {
  return page.evaluate(() => {
    const text = [...document.body.querySelectorAll("*")].filter((element) => {
      const style = getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      return (
        style.display !== "none" &&
        style.visibility !== "hidden" &&
        rect.width > 0 &&
        rect.height > 0 &&
        [...element.childNodes].some(
          (node) => node.nodeType === Node.TEXT_NODE && node.textContent?.trim()
        )
      );
    });
    return {
      overflow:
        document.documentElement.scrollWidth >
        document.documentElement.clientWidth + 1,
      smallText: text
        .filter((element) => Number.parseFloat(getComputedStyle(element).fontSize) < 12)
        .map((element) => element.textContent.trim().slice(0, 60))
    };
  });
}

async function main() {
  const browser = await chromium.launch({ headless: true });
  activeBrowser = browser;
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  page.setDefaultTimeout(8_000);
  const diagnostics = [];
  page.on("pageerror", (error) => diagnostics.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") diagnostics.push(message.text());
  });

  await page.goto(`${baseURL}/monitors/${monitorID}/edit`, {
    waitUntil: "domcontentloaded"
  });
  await page.getByRole("heading", { name: "编辑监控配置" }).waitFor();
  console.log("[1/5] 已打开真实剧集监控编辑页");

  assert.equal(await page.getByLabel("节目名称", { exact: true }).inputValue(), "山河令");
  assert.equal(await page.getByLabel("起始集").inputValue(), "1");
  assert.equal(await page.getByLabel("最后一集").inputValue(), "36");
  assert.equal(
    await page.getByLabel(/人物或识别词/).inputValue(),
    "张哲瀚, 龚俊"
  );
  assert.equal(await page.getByLabel("单次请求上限").isVisible(), false);
  await page.getByText(/1 个篇章 · 36 集/).waitFor();
  assert.equal(await page.getByText("保存前还需处理", { exact: true }).count(), 0);
  console.log("[2/5] 剧集基本字段与 1–36 集摘要已确认");

  await page.getByRole("button", { name: /指定频道/ }).click();
  await page.getByLabel(/频道主页链接、@账号或频道 ID/).waitFor();
  assert.equal(await page.getByLabel("节目名称", { exact: true }).count(), 0);
  assert.equal(await page.getByLabel("单次请求上限").isVisible(), false);
  console.log("[3/5] 频道方式只展示链接、账号和频道内搜索字段");

  await page.getByRole("button", { name: /关键词搜索/ }).click();
  await page.getByLabel("搜索词").waitFor();
  assert.equal(await page.getByLabel(/频道主页链接、@账号或频道 ID/).count(), 0);
  console.log("[4/5] 关键词方式只展示必要搜索字段");

  await page.getByRole("button", { name: /完整节目 \/ 剧集/ }).click();
  await page.getByLabel("节目名称", { exact: true }).waitFor();
  assert.equal(await page.getByLabel("起始集").inputValue(), "1");
  assert.equal(await page.getByLabel("最后一集").inputValue(), "24");
  assert.equal(await page.getByLabel("单次请求上限").isVisible(), false);

  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 390, height: 844 },
    { width: 320, height: 720 }
  ]) {
    await page.setViewportSize(viewport);
    const audit = await visibleTextAudit(page);
    assert.equal(audit.overflow, false, `页面横向溢出：${JSON.stringify(viewport)}`);
    assert.deepEqual(audit.smallText, [], `存在小于 12px 的文字：${JSON.stringify(viewport)}`);
  }
  console.log("[5/5] 三种视口字号与横向溢出检查已通过");

  assert.deepEqual(diagnostics, []);
  await browser.close();
  activeBrowser = undefined;
  console.log(JSON.stringify({
    status: "passed",
    modes: ["关键词搜索", "指定频道", "完整节目 / 剧集"],
    advancedDefaultsToCollapsed: true,
    persistedDataChanged: false,
    viewports: [1440, 390, 320]
  }, null, 2));
}

main().catch(async (error) => {
  console.error(error);
  if (activeBrowser) await activeBrowser.close().catch(() => undefined);
  process.exitCode = 1;
});
