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
    if (!taskID) throw new Error("没有可用于字幕进度回归的任务");
    const taskResponse = await page.request.get(`${baseURL}/api/v1/tasks/${taskID}`);
    if (!taskResponse.ok()) throw new Error(`读取任务失败：HTTP ${taskResponse.status()}`);
    const task = await taskResponse.json();
    const subtitleStep = task.steps.find((step) => step.kind === "subtitles");
    if (!subtitleStep) throw new Error("任务缺少字幕处理步骤");
    await page.goto(`${baseURL}/tasks/${taskID}`, {
      waitUntil: "domcontentloaded",
      timeout: 20_000
    });
    await page.getByRole("heading", { name: "处理步骤", exact: true }).waitFor();
    const result = await page.locator("body").evaluate(() => {
      const text = document.body.innerText;
      const progressText = text
        .split("\n")
        .find((line) => line.includes("正在智能分段") && line.includes("批"));
      const sizes = [...document.querySelectorAll("body *")]
        .filter((element) => {
          const style = getComputedStyle(element);
          return style.display !== "none" && style.visibility !== "hidden" &&
            (element.textContent || "").trim();
        })
        .map((element) => Number.parseFloat(getComputedStyle(element).fontSize))
        .filter(Number.isFinite);
      return {
        progressText,
        minimumFontSize: Math.min(...sizes),
        horizontalOverflow: document.documentElement.scrollWidth > window.innerWidth
      };
    });
    if (subtitleStep.status === "running") {
      if (!result.progressText) throw new Error("运行中的字幕批次没有显示进度");
    } else if (result.progressText) {
      throw new Error("已结束的字幕步骤仍显示瞬时批次进度");
    }
    if (result.minimumFontSize < 12) {
      throw new Error(`visible font below 12px: ${result.minimumFontSize}`);
    }
    if (result.horizontalOverflow) throw new Error("page has horizontal overflow");
    if (browserErrors.length) throw new Error(browserErrors.join("\n"));
    if (screenshotPath) await page.screenshot({ path: screenshotPath });
    console.log(JSON.stringify({ taskID, subtitleStatus: subtitleStep.status, ...result }));
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
