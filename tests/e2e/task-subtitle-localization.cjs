const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_WEB_URL || "http://localhost:4173";
let taskID = process.env.VISORAFT_TASK_ID || "";
const screenshotPath = process.env.VISORAFT_SCREENSHOT_PATH;

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    const browserErrors = [];
    page.on("console", (message) => {
      if (message.type() === "error") browserErrors.push(message.text());
    });
    page.on("pageerror", (error) => browserErrors.push(error.message));

    if (!taskID) {
      const listResponse = await page.request.get(`${baseURL}/api/v1/tasks?limit=100`);
      if (!listResponse.ok()) throw new Error(`读取任务列表失败：HTTP ${listResponse.status()}`);
      const list = await listResponse.json();
      for (const item of list.items || []) {
        const response = await page.request.get(`${baseURL}/api/v1/tasks/${item.id}`);
        if (!response.ok()) continue;
        const detail = await response.json();
        if ((detail.steps || []).some((step) => step.kind === "subtitles")) {
          taskID = item.id;
          break;
        }
      }
    }
    if (!taskID) throw new Error("没有可用于字幕中文化回归的任务");

    await page.goto(`${baseURL}/tasks/${taskID}`, {
      waitUntil: "domcontentloaded",
      timeout: 15_000
    });
    await page.getByRole("heading", { name: "处理步骤", exact: true }).waitFor();

    const bodyText = await page.locator("body").innerText();
    for (const expected of ["字幕处理"]) {
      if (!bodyText.includes(expected)) throw new Error(`missing localized text: ${expected}`);
    }
    for (const forbidden of ["model_request_failed", "The read operation timed out"]) {
      if (bodyText.includes(forbidden)) throw new Error(`internal English value is visible: ${forbidden}`);
    }

    const rawEnums = await page.locator("body").evaluate(() => {
      const forbidden = new Set([
        "queued",
        "running",
        "succeeded",
        "failed",
        "cancelled",
        "metadata",
        "download",
        "media_inspect",
        "subtitles",
        "transcode",
        "review",
        "publish"
      ]);
      const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
      const matches = [];
      while (walker.nextNode()) {
        const value = (walker.currentNode.textContent || "").trim();
        if (forbidden.has(value)) matches.push(value);
      }
      return matches;
    });
    if (rawEnums.length) throw new Error(`raw enum text nodes: ${rawEnums.join(", ")}`);

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
    console.log(JSON.stringify({ taskID, rawEnums, ...layout }));
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
