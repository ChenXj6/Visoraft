const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_WEB_URL || "http://localhost:4173";
const taskID = process.env.VISORAFT_TASK_ID;
const screenshotPath = process.env.VISORAFT_SCREENSHOT_PATH;
const viewportWidth = Number(process.env.VISORAFT_VIEWPORT_WIDTH || 1440);
if (!taskID) throw new Error("VISORAFT_TASK_ID is required");

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: viewportWidth, height: 1000 } });
    const browserErrors = [];
    page.on("console", (message) => {
      if (message.type() === "error") browserErrors.push(message.text());
    });
    page.on("pageerror", (error) => browserErrors.push(error.message));
    const taskResponse = await page.request.get(`${baseURL}/api/v1/tasks/${taskID}`);
    if (!taskResponse.ok()) throw new Error(`task API returned ${taskResponse.status()}`);
    const task = await taskResponse.json();
    await page.goto(`${baseURL}/reviews/${taskID}`, {
      waitUntil: "domcontentloaded",
      timeout: 20_000
    });
    await page.getByRole("heading", { name: "字幕与质检", exact: true }).waitFor();
    const activeTab = page.getByRole("tab", { selected: true });
    const activeTabText = (await activeTab.textContent())?.replace(/\s+/g, " ").trim() || "";
    if (!activeTabText.startsWith("译文")) {
      throw new Error(`latest translated subtitle is not selected: ${activeTabText}`);
    }
    const qcScore = (await page.locator(".subtitle-qc-strip strong").textContent())?.trim();
    const bodyText = await page.locator("body").innerText();
    for (const forbidden of ["QC SCORE", "Worker 产物", "审核操作"]) {
      if (bodyText.includes(forbidden)) throw new Error(`raw fallback label remains: ${forbidden}`);
    }
    for (const expected of ["质检分数", "退回并重新处理字幕"]) {
      if (!bodyText.includes(expected)) throw new Error(`Chinese label is missing: ${expected}`);
    }
    if (task.status === "awaiting_manual_review") {
      if (!bodyText.includes("修订当前字幕")) {
        throw new Error("待审核任务缺少字幕修订入口");
      }
    } else if (task.status === "ready_to_publish") {
      for (const expected of ["审核后的下一步", "进入投稿准备"]) {
        if (!bodyText.includes(expected)) throw new Error(`approved task is missing: ${expected}`);
      }
    }
    const metadataSpacing = await page.locator(".review-edit-panel").evaluate((panel) => {
      const panelRect = panel.getBoundingClientRect();
      const headingRect = panel.querySelector(":scope > .section-heading").getBoundingClientRect();
      const headingContentRect = panel
        .querySelector(":scope > .section-heading > .sequence-mark")
        .getBoundingClientRect();
      const formRect = panel.querySelector(":scope > .settings-form-grid").getBoundingClientRect();
      const buttonRect = panel.querySelector(":scope > .button").getBoundingClientRect();
      return {
        headingContentLeft: Math.round(headingContentRect.left - panelRect.left),
        formLeft: Math.round(formRect.left - panelRect.left),
        formRight: Math.round(panelRect.right - formRect.right),
        buttonLeft: Math.round(buttonRect.left - panelRect.left),
        formHeadingGap: Math.round(formRect.top - headingRect.bottom),
        buttonGap: Math.round(buttonRect.top - formRect.bottom)
      };
    });
    const minimumPanelPadding = viewportWidth <= 520 ? 13 : 18;
    for (const [name, value] of Object.entries({
      headingContentLeft: metadataSpacing.headingContentLeft,
      formLeft: metadataSpacing.formLeft,
      formRight: metadataSpacing.formRight,
      buttonLeft: metadataSpacing.buttonLeft
    })) {
      if (value < minimumPanelPadding) {
        throw new Error(`${name} padding is too small: ${value}px`);
      }
    }
    if (metadataSpacing.formHeadingGap < 14) {
      throw new Error(`form heading gap is too small: ${metadataSpacing.formHeadingGap}px`);
    }
    if (metadataSpacing.buttonGap < 12) {
      throw new Error(`button gap is too small: ${metadataSpacing.buttonGap}px`);
    }
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
    if (layout.minimumFontSize < 12) throw new Error(`font below 12px: ${layout.minimumFontSize}`);
    if (layout.horizontalOverflow) throw new Error("review page has horizontal overflow");
    if (browserErrors.length) throw new Error(browserErrors.join("\n"));
    if (screenshotPath) await page.screenshot({ path: screenshotPath, fullPage: true });
    console.log(JSON.stringify({ task_id: taskID, task_status: task.status, active_tab: activeTabText, qc_score: qcScore, metadataSpacing, ...layout }));
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
