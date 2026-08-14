const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_WEB_URL || "http://localhost:4173";
const taskID = process.env.VISORAFT_TASK_ID;
const screenshotPath = process.env.VISORAFT_SCREENSHOT_PATH;
const timeoutMs = Number(process.env.VISORAFT_RECOVERY_TIMEOUT_MS || 1_200_000);

if (!taskID) throw new Error("VISORAFT_TASK_ID is required");

async function json(response, label) {
  if (!response.ok()) {
    throw new Error(`${label}: ${response.status()} ${await response.text()}`);
  }
  return response.json();
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

    const before = await json(
      await page.request.get(`${baseURL}/api/v1/tasks/${taskID}`),
      "load task before reprocessing"
    );
    const beforeStep = before.steps.find((step) => step.kind === "subtitles");
    if (before.status !== "awaiting_manual_review" || !beforeStep) {
      throw new Error(`task is not awaiting manual review: ${before.status}`);
    }

    const invalid = await page.request.post(
      `${baseURL}/api/v1/reviews/${taskID}/reprocess_subtitles`,
      { data: { reason: "" } }
    );
    if (invalid.status() !== 422) {
      throw new Error(`empty reason should be rejected with 422, got ${invalid.status()}`);
    }

    await page.goto(`${baseURL}/reviews/${taskID}`, {
      waitUntil: "domcontentloaded",
      timeout: 20_000
    });
    await page.getByRole("heading", { name: "审核判定", exact: true }).waitFor();
    await page.getByLabel("意见 / 原因").fill("真实联调发现译文时间轴错位，退回并重新生成字幕与转码产物");
    const button = page.getByRole("button", {
      name: "退回并重新处理字幕",
      exact: true
    });
    const [actionResponse] = await Promise.all([
      page.waitForResponse(
        (response) =>
          response.request().method() === "POST" &&
          response.url().endsWith(`/api/v1/reviews/${taskID}/reprocess_subtitles`),
        { timeout: 20_000 }
      ),
      button.click()
    ]);
    const actionResult = await json(actionResponse, "reprocess subtitle action");
    if (actionResult.task.status !== "processing") {
      throw new Error(`reprocess action did not enter processing: ${actionResult.task.status}`);
    }

    const observations = [];
    const seen = new Set();
    const startedAt = Date.now();
    let finalTask;
    while (Date.now() - startedAt < timeoutMs) {
      const task = await json(
        await page.request.get(`${baseURL}/api/v1/tasks/${taskID}`),
        "poll reprocessing task"
      );
      const subtitle = task.steps.find((step) => step.kind === "subtitles");
      const transcode = task.steps.find((step) => step.kind === "transcode");
      const signature = [
        task.status,
        subtitle?.status,
        subtitle?.attempt,
        subtitle?.progress,
        subtitle?.detail?.phase,
        transcode?.status,
        transcode?.progress
      ].join("|");
      if (!seen.has(signature)) {
        seen.add(signature);
        const observation = {
          elapsed_seconds: Math.round((Date.now() - startedAt) / 1000),
          task_status: task.status,
          subtitle_status: subtitle?.status || "",
          subtitle_attempt: subtitle?.attempt || 0,
          subtitle_progress: subtitle?.progress || 0,
          subtitle_phase: subtitle?.detail?.phase || "",
          subtitle_restored_items: subtitle?.detail?.restored_items || 0,
          subtitle_sample_count: subtitle?.detail?.sample_count || 0,
          transcode_status: transcode?.status || "",
          transcode_progress: transcode?.progress || 0
        };
        observations.push(observation);
        console.log(JSON.stringify(observation));
      }
      if (
        ["awaiting_manual_review", "ready_to_publish", "failed", "cancelled"].includes(task.status) &&
        (subtitle?.attempt || 0) > beforeStep.attempt
      ) {
        finalTask = task;
        break;
      }
      await page.waitForTimeout(Date.now() - startedAt < 20_000 ? 500 : 2_000);
    }
    if (!finalTask) throw new Error(`reprocessing did not finish within ${timeoutMs}ms`);
    if (finalTask.status === "failed") {
      throw new Error(`reprocessing failed: ${finalTask.error_code} ${finalTask.error_message}`);
    }

    const review = await json(
      await page.request.get(`${baseURL}/api/v1/reviews/${taskID}`),
      "load review after reprocessing"
    );
    const original = review.subtitles
      .filter((item) => item.kind === "original")
      .sort((a, b) => b.version - a.version)[0];
    const translated = review.subtitles
      .filter((item) => item.kind === "translated")
      .sort((a, b) => b.version - a.version)[0];
    if (!original || !translated) throw new Error("latest subtitle documents are missing");
    if (original.segments.length !== translated.segments.length) {
      throw new Error(
        `subtitle counts are not aligned: ${original.segments.length}/${translated.segments.length}`
      );
    }
    for (let index = 0; index < original.segments.length; index += 1) {
      const source = original.segments[index];
      const target = translated.segments[index];
      if (
        source.index !== target.index ||
        source.start !== target.start ||
        source.end !== target.end
      ) {
        throw new Error(`subtitle timeline mismatch at position ${index + 1}`);
      }
    }
    const latestRun = review.runs[0];
    const qcRule = latestRun?.rule_results?.find((rule) => rule.key === "subtitle_qc");
    if (qcRule && qcRule.actual !== translated.qc_report.score) {
      throw new Error(
        `review QC score does not use translated document: ${qcRule.actual}/${translated.qc_report.score}`
      );
    }
    if (!review.actions.some((item) => item.action === "reprocess_subtitles")) {
      throw new Error("reprocess subtitle review action was not persisted");
    }

    await page.goto(`${baseURL}/reviews/${taskID}`, {
      waitUntil: "domcontentloaded",
      timeout: 20_000
    });
    await page.getByRole("heading", { name: "审核判定", exact: true }).waitFor();
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
    if (layout.horizontalOverflow) throw new Error("review page has horizontal overflow");
    if (browserErrors.length) throw new Error(browserErrors.join("\n"));
    if (screenshotPath) await page.screenshot({ path: screenshotPath, fullPage: true });

    console.log(JSON.stringify({
      task_id: taskID,
      before_attempt: beforeStep.attempt,
      final_status: finalTask.status,
      final_attempt: finalTask.steps.find((step) => step.kind === "subtitles")?.attempt,
      original_segments: original.segments.length,
      translated_segments: translated.segments.length,
      translated_qc_score: translated.qc_report.score,
      translated_qc_passed: translated.qc_report.passed,
      review_qc_actual: qcRule?.actual,
      observations,
      ...layout
    }));
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
