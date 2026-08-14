const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_WEB_URL || "http://localhost:4173";
const taskID = process.env.VISORAFT_TASK_ID;
const screenshotPath = process.env.VISORAFT_SCREENSHOT_PATH || "";

if (!taskID) throw new Error("VISORAFT_TASK_ID is required");

async function decode(response, label) {
  if (!response.ok()) {
    throw new Error(`${label}: ${response.status()} ${await response.text()}`);
  }
  return response.json();
}

async function inspectLayout(page) {
  return page.locator("body").evaluate(() => {
    const sizes = [...document.querySelectorAll("body *")]
      .filter((element) => {
        const style = getComputedStyle(element);
        const rect = element.getBoundingClientRect();
        return style.display !== "none" && style.visibility !== "hidden" &&
          rect.width > 0 && rect.height > 0 && (element.textContent || "").trim();
      })
      .map((element) => Number.parseFloat(getComputedStyle(element).fontSize))
      .filter(Number.isFinite);
    return {
      minimum_font_size: Math.min(...sizes),
      horizontal_overflow: document.documentElement.scrollWidth > window.innerWidth
    };
  });
}

function assertLayout(layout, scope) {
  if (layout.minimum_font_size < 12) {
    throw new Error(`${scope} contains font below 12px: ${layout.minimum_font_size}`);
  }
  if (layout.horizontal_overflow) throw new Error(`${scope} has horizontal overflow`);
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    const browserErrors = [];
    page.on("console", (message) => {
      if (message.type() === "error") browserErrors.push(message.text());
    });
    page.on("pageerror", (error) => browserErrors.push(error.message));

    const task = await decode(
      await page.request.get(`${baseURL}/api/v1/tasks/${taskID}`),
      "load published task"
    );
    if (task.status !== "published" || task.review_status !== "approved") {
      throw new Error(`task terminal state is inconsistent: ${task.status}/${task.review_status}`);
    }
    if (task.publish_status !== "published" || task.publish_mode !== "remote") {
      throw new Error(`task publish result is not remote: ${task.publish_status}/${task.publish_mode}`);
    }
    const publishing = await decode(
      await page.request.get(`${baseURL}/api/v1/publishing/${taskID}`),
      "load published platform result"
    );
    const remotePublication = (publishing.publications || []).find(
      (item) => item.status === "published" && !item.simulation && item.remote_submission_id
    );
    if (!remotePublication) {
      throw new Error("published task has no persisted remote platform submission ID");
    }
    const expectedSteps = [
      "metadata", "download", "media_inspect", "subtitles", "transcode", "review", "publish"
    ];
    for (const kind of expectedSteps) {
      const step = task.steps.find((item) => item.kind === kind);
      if (!step || step.status !== "succeeded" || step.progress !== 100) {
        throw new Error(`published task step is incomplete: ${kind} ${JSON.stringify(step)}`);
      }
    }

    await page.goto(`${baseURL}/tasks`, {
      waitUntil: "domcontentloaded",
      timeout: 20_000
    });
    const taskCard = page.locator(".task-track").filter({ hasText: task.title }).first();
    await taskCard.waitFor();
    const cardText = await taskCard.innerText();
    for (const expected of ["Bilibili", "投稿已提交"]) {
      if (!cardText.includes(expected)) throw new Error(`task list is missing ${expected}`);
    }
    const listLayout = await inspectLayout(page);
    assertLayout(listLayout, "published task list");

    await page.goto(`${baseURL}/tasks/${taskID}`, {
      waitUntil: "domcontentloaded",
      timeout: 20_000
    });
    await page.getByRole("heading", { name: "处理步骤", exact: true }).waitFor();
    const bodyText = await page.locator("body").innerText();
    for (const expected of [
      "平台已接收投稿", "查看发布结果", "字幕处理", "转码", "审核", "发布"
    ]) {
      if (!bodyText.includes(expected)) throw new Error(`task detail is missing ${expected}`);
    }
    for (const forbidden of ["subtitles", "transcode", "submitting"]) {
      if (bodyText.split(/\s+/).includes(forbidden)) {
        throw new Error(`raw internal stage is visible: ${forbidden}`);
      }
    }
    const detailLayout = await inspectLayout(page);
    assertLayout(detailLayout, "published task detail");
    if (screenshotPath) await page.screenshot({ path: screenshotPath });

    await page.getByRole("link", { name: "查看发布结果", exact: true }).click();
    await page.waitForURL(`**/publishing/${taskID}`);
    await page.getByRole("heading", { name: "投稿工作台", exact: true }).waitFor();
    const publishingText = await page.locator("body").innerText();
    for (const expected of [
      "平台已接收投稿",
      remotePublication.remote_submission_id,
      "等待平台审核或公开"
    ]) {
      if (!publishingText.includes(expected)) {
        throw new Error(`publishing handoff is missing ${expected}`);
      }
    }

    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`${baseURL}/tasks/${taskID}`, {
      waitUntil: "domcontentloaded",
      timeout: 20_000
    });
    await page.getByRole("heading", { name: "处理步骤", exact: true }).waitFor();
    const mobileLayout = await inspectLayout(page);
    assertLayout(mobileLayout, "published task mobile detail");

    if (browserErrors.length) throw new Error(browserErrors.join("\n"));
    console.log(JSON.stringify({
      task_id: taskID,
      title: task.title,
      remote_submission_id: remotePublication.remote_submission_id,
      step_count: task.steps.length,
      list_layout: listLayout,
      detail_layout: detailLayout,
      mobile_layout: mobileLayout
    }));
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
