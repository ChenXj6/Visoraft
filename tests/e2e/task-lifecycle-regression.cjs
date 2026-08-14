const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_WEB_URL || "http://localhost:4173";
let cancelledTaskID = "";
let completedTaskID = "";
const screenshotPath = process.env.VISORAFT_SCREENSHOT_PATH;

const taskRow = (page) =>
  page.locator("article.task-track", { hasText: `#${cancelledTaskID.slice(0, 8)}` });

async function open(page, path) {
  console.log(`STEP open ${path}`);
  await page.goto(`${baseURL}${path}`, {
    waitUntil: "domcontentloaded",
    timeout: 15_000
  });
  await page.locator("main").waitFor({ state: "visible", timeout: 10_000 });
}

async function submitLifecycleDialog(page, buttonName, confirmationText, reason) {
  const dialog = page.getByRole("dialog");
  await dialog.waitFor({ state: "visible", timeout: 10_000 });
  await dialog.getByLabel("操作原因").fill(reason);
  await dialog.getByText(confirmationText).click();
  await dialog.getByRole("button", { name: buttonName, exact: true }).click();
}

(async () => {
  let browser;
  try {
    browser = await chromium.launch({ headless: true });
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    page.setDefaultTimeout(10_000);
    const failures = [];
    page.on("console", (message) => {
      if (message.type() === "error") failures.push(`console: ${message.text()}`);
    });
    page.on("pageerror", (error) => failures.push(`page: ${error.message}`));

    const tasksResponse = await page.request.get(`${baseURL}/api/v1/tasks?limit=100`);
    if (!tasksResponse.ok()) throw new Error(`读取任务列表失败：HTTP ${tasksResponse.status()}`);
    const taskList = await tasksResponse.json();
    for (const item of taskList.items || []) {
      const response = await page.request.get(`${baseURL}/api/v1/tasks/${item.id}`);
      if (!response.ok()) continue;
      const detail = await response.json();
      const download = (detail.steps || []).find((step) => step.kind === "download");
      if (download?.status === "succeeded" && detail.status !== "running") {
        completedTaskID = detail.id;
        break;
      }
    }
    if (!completedTaskID) throw new Error("没有可用于已完成下载展示回归的任务");

    const createResponse = await page.request.post(`${baseURL}/api/v1/tasks`, {
      data: {
        source_url: "http://fixture-provider:8090/media/slow-sample.wav",
        target_platforms: ["bilibili"],
        repost_statement_version: "brief_v1",
        auto_publish: false
      }
    });
    if (!createResponse.ok()) throw new Error(`创建生命周期测试任务失败：HTTP ${createResponse.status()}`);
    cancelledTaskID = (await createResponse.json()).id;
    const cancelResponse = await page.request.post(`${baseURL}/api/v1/tasks/${cancelledTaskID}/cancel`);
    if (!cancelResponse.ok()) throw new Error(`取消生命周期测试任务失败：HTTP ${cancelResponse.status()}`);

    await open(page, `/tasks/${completedTaskID}`);
    if (await page.locator(".download-activity").count()) {
      throw new Error("completed task still renders the legacy download box");
    }
    if (await page.locator(".download-inline-metrics").count()) {
      throw new Error("completed task still renders live download metrics");
    }
    console.log("STEP completed download telemetry hidden");

    // The prior timed run completed the archive request before its browser process
    // stayed alive on a later failed wait. Restore it first so this run can replay
    // the exact list delete interaction and leave the task in the recycle bin.
    await open(page, "/tasks?scope=archived");
    const archivedRow = taskRow(page);
    if (await archivedRow.count()) {
      console.log("STEP restore archived fixture");
      const restoreResponse = page.waitForResponse(
        (response) =>
          response.url().endsWith(`/api/v1/tasks/${cancelledTaskID}/restore`) &&
          response.request().method() === "POST",
        { timeout: 10_000 }
      );
      await archivedRow.getByRole("button", { name: "恢复任务" }).click();
      await submitLifecycleDialog(
        page,
        "恢复任务",
        "我确认把该任务恢复到工作列表",
        "回归验证删除任务交互"
      );
      const response = await restoreResponse;
      if (![200, 202].includes(response.status())) {
        throw new Error(`restore request returned HTTP ${response.status()}`);
      }
      await archivedRow.waitFor({ state: "detached", timeout: 10_000 });
    }

    await open(page, `/tasks/${cancelledTaskID}`);
    console.log("STEP verify task detail delete entry");
    const detailDelete = page.getByRole("button", { name: "删除任务", exact: true });
    await detailDelete.waitFor({ state: "visible" });
    await detailDelete.click();
    await page.getByRole("dialog").waitFor({ state: "visible" });
    await page.getByRole("dialog").getByRole("button", { name: "返回", exact: true }).click();

    await open(page, "/tasks");
    const row = taskRow(page);
    await row.waitFor({ state: "visible" });
    const beforeClickURL = page.url();
    console.log("STEP click list delete button");
    await row.getByRole("button", { name: "删除任务", exact: true }).click();
    await page.getByRole("dialog").waitFor({ state: "visible" });
    if (page.url() !== beforeClickURL) {
      throw new Error(`delete button navigated away: ${page.url()}`);
    }

    const archiveResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/v1/tasks/${cancelledTaskID}/archive`) &&
        response.request().method() === "POST",
      { timeout: 10_000 }
    );
    await submitLifecycleDialog(
      page,
      "移入回收站",
      "我已核对影响范围，并确认执行",
      "清理已取消的真实测试任务"
    );
    const response = await archiveResponse;
    if (![200, 202].includes(response.status())) {
      throw new Error(`archive request returned HTTP ${response.status()}`);
    }
    await row.waitFor({ state: "detached", timeout: 10_000 });

    await open(page, "/tasks?scope=archived");
    await taskRow(page).waitFor({ state: "visible" });
    console.log("STEP archived task visible in recycle bin");

    const visibleTextSizes = await page.locator("body *:visible").evaluateAll((elements) =>
      elements
        .filter((element) => (element.textContent || "").trim())
        .map((element) => Number.parseFloat(getComputedStyle(element).fontSize))
        .filter(Number.isFinite)
    );
    const minimumFontSize = Math.min(...visibleTextSizes);
    if (minimumFontSize < 12) {
      throw new Error(`visible font size below 12px: ${minimumFontSize}px`);
    }
    if (screenshotPath) await page.screenshot({ path: screenshotPath, fullPage: true });
    if (failures.length) throw new Error(failures.join("\n"));

    const purgeResponse = await page.request.delete(
      `${baseURL}/api/v1/tasks/${cancelledTaskID}`,
      {
        data: {
          expected_version: (await (await page.request.get(
            `${baseURL}/api/v1/tasks/${cancelledTaskID}`
          )).json()).version,
          confirmation: `purge:${cancelledTaskID}`,
          reason: "生命周期回归完成后清理测试任务"
        }
      }
    );
    if (!purgeResponse.ok()) {
      throw new Error(`清理生命周期测试任务失败：HTTP ${purgeResponse.status()}`);
    }

    console.log(
      JSON.stringify({
        archivedTaskID: cancelledTaskID,
        workListDeleteStayedOnPage: true,
        detailDeleteDialog: true,
        completedDownloadMetricsHidden: true,
        archivedTaskVisible: true,
        testTaskPurged: true,
        minimumFontSize
      })
    );
  } finally {
    if (browser) await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
