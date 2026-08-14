const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_WEB_URL || "http://localhost:4173";
const taskID = process.env.VISORAFT_TASK_ID;
const screenshotPath = process.env.VISORAFT_SCREENSHOT_PATH;
const timeoutMs = Number(process.env.VISORAFT_RECOVERY_TIMEOUT_MS || 1_200_000);

if (!taskID) throw new Error("VISORAFT_TASK_ID is required");

async function decode(response, label) {
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

    const before = await decode(
      await page.request.get(`${baseURL}/api/v1/reviews/${taskID}`),
      "load review before subtitle edit"
    );
    if (before.task.status !== "awaiting_manual_review") {
      throw new Error(`task is not awaiting manual review: ${before.task.status}`);
    }
    const translatedBefore = before.subtitles
      .filter((item) => item.kind === "translated")
      .sort((a, b) => b.version - a.version)[0];
    const transcodeBefore = before.task.steps.find((step) => step.kind === "transcode");
    const outputBefore = before.task.assets.find(
      (asset) => asset.kind === "transcoded" && asset.status === "available"
    );
    if (!translatedBefore || !transcodeBefore || !outputBefore) {
      throw new Error("subtitle or transcode baseline is missing");
    }
    const editIndex = Math.min(252, translatedBefore.segments.length - 1);
    if (editIndex < 0) throw new Error("translated subtitle has no editable segments");
    const editedText = `${translatedBefore.segments[editIndex].text}（E2E 修订）`;

    await page.goto(`${baseURL}/reviews/${taskID}`, {
      waitUntil: "domcontentloaded",
      timeout: 20_000
    });
    await page.getByRole("heading", { name: "字幕与质检", exact: true }).waitFor();
    await page.getByRole("tab", {
      name: new RegExp(`^译文.*v${translatedBefore.version}$`)
    }).click();
    await page.getByRole("button", { name: "修订当前字幕", exact: true }).click();
    await page.locator(".subtitle-editor-list > li").nth(editIndex).locator("textarea").fill(
      editedText
    );
    await page.getByLabel("字幕修改原因", { exact: true }).fill(
      `修正第 ${editIndex + 1} 条字幕以验证重绘链路`
    );
    const [saveResponse] = await Promise.all([
      page.waitForResponse(
        (response) =>
          response.request().method() === "PUT" &&
          response.url().includes(`/api/v1/reviews/${taskID}/subtitles/`),
        { timeout: 30_000 }
      ),
      page.getByRole("button", { name: "保存字幕新版本", exact: true }).click()
    ]);
    const afterEdit = await decode(saveResponse, "save edited subtitle version");
    const translatedAfter = afterEdit.subtitles
      .filter((item) => item.kind === "translated")
      .sort((a, b) => b.version - a.version)[0];
    if (translatedAfter.version !== translatedBefore.version + 1) {
      throw new Error(`subtitle version did not advance: ${translatedAfter.version}`);
    }
    if (translatedAfter.segments[editIndex].text !== editedText) {
      throw new Error("edited subtitle text was not persisted");
    }
    const availableArtifacts = afterEdit.task.assets.filter(
      (asset) => asset.status === "available" &&
        ["subtitle_translated_vtt", "subtitle_translated_srt", "subtitle_translated_qc"].includes(asset.kind)
    );
    if (availableArtifacts.length !== 3 ||
      !availableArtifacts.every((asset) => asset.object_key.includes(`/review/translated-v${translatedAfter.version}.`))) {
      throw new Error(`edited subtitle files were not persisted: ${JSON.stringify(availableArtifacts)}`);
    }

    const stale = await page.request.put(
      `${baseURL}/api/v1/reviews/${taskID}/subtitles/${translatedBefore.id}`,
      {
        data: {
          expected_version: translatedBefore.version,
          segments: translatedBefore.segments,
          reason: "验证旧版本冲突"
        }
      }
    );
    if (stale.status() !== 409) {
      throw new Error(`stale subtitle update should return 409, got ${stale.status()}`);
    }

    await page.getByLabel("意见 / 原因", { exact: true }).fill(
      "字幕修订完成，需要退回并重新生成媒体"
    );
    const [requestChangesResponse] = await Promise.all([
      page.waitForResponse(
        (response) => response.request().method() === "POST" &&
          response.url().endsWith(`/api/v1/reviews/${taskID}/request_changes`),
        { timeout: 20_000 }
      ),
      page.getByRole("button", { name: "退回修改", exact: true }).click()
    ]);
    const changesRequested = await decode(requestChangesResponse, "request subtitle changes");
    if (changesRequested.task.review_status !== "changes_requested") {
      throw new Error("review did not enter changes_requested");
    }

    await page.getByLabel("意见 / 原因", { exact: true }).fill(
      `第 ${editIndex + 1} 条已修正，提交重新烧录并再次审核`
    );
    const [resubmitResponse] = await Promise.all([
      page.waitForResponse(
        (response) => response.request().method() === "POST" &&
          response.url().endsWith(`/api/v1/reviews/${taskID}/resubmit`),
        { timeout: 20_000 }
      ),
      page.getByRole("button", { name: "修改完成，重新提交", exact: true }).click()
    ]);
    const resubmitted = await decode(resubmitResponse, "resubmit edited subtitle");
    const initialTranscode = resubmitted.task.steps.find((step) => step.kind === "transcode");
    if (resubmitted.task.status !== "processing" ||
      initialTranscode?.attempt !== transcodeBefore.attempt + 1) {
      throw new Error(`resubmit did not queue a new transcode: ${JSON.stringify(initialTranscode)}`);
    }

    const observations = [];
    const seen = new Set();
    const startedAt = Date.now();
    let finalTask;
    while (Date.now() - startedAt < timeoutMs) {
      const task = await decode(
        await page.request.get(`${baseURL}/api/v1/tasks/${taskID}`),
        "poll subtitle rerender task"
      );
      const transcode = task.steps.find((step) => step.kind === "transcode");
      const signature = [task.status, transcode?.status, transcode?.attempt, transcode?.progress].join("|");
      if (!seen.has(signature)) {
        seen.add(signature);
        const observation = {
          elapsed_seconds: Math.round((Date.now() - startedAt) / 1000),
          task_status: task.status,
          transcode_status: transcode?.status || "",
          transcode_attempt: transcode?.attempt || 0,
          transcode_progress: transcode?.progress || 0
        };
        observations.push(observation);
        console.log(JSON.stringify(observation));
      }
      if (["awaiting_manual_review", "ready_to_publish", "failed"].includes(task.status)) {
        finalTask = task;
        break;
      }
      await page.waitForTimeout(Date.now() - startedAt < 20_000 ? 500 : 2_000);
    }
    if (!finalTask) throw new Error(`subtitle rerender did not finish within ${timeoutMs}ms`);
    if (finalTask.status === "failed") {
      throw new Error(`subtitle rerender failed: ${finalTask.error_code} ${finalTask.error_message}`);
    }

    const reviewAfter = await decode(
      await page.request.get(`${baseURL}/api/v1/reviews/${taskID}`),
      "load review after subtitle rerender"
    );
    const latestRun = reviewAfter.runs[0];
    const qcRule = latestRun?.rule_results?.find((rule) => rule.key === "subtitle_qc");
    if (!qcRule || qcRule.actual !== translatedAfter.qc_report.score) {
      throw new Error(
        `review did not select edited translated QC: ${qcRule?.actual}/${translatedAfter.qc_report.score}`
      );
    }
    const outputAfter = reviewAfter.task.assets.find(
      (asset) => asset.kind === "transcoded" && asset.status === "available"
    );
    if (!outputAfter || outputAfter.checksum_sha256 === outputBefore.checksum_sha256) {
      throw new Error("transcoded media was not regenerated after subtitle edit");
    }
    const actions = reviewAfter.actions.map((item) => item.action);
    for (const expected of ["subtitle_edit", "request_changes", "resubmit"]) {
      if (!actions.includes(expected)) throw new Error(`review action ${expected} is missing`);
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
      subtitle_version_before: translatedBefore.version,
      subtitle_version_after: translatedAfter.version,
      edited_segment_index: editIndex + 1,
      edited_artifact_count: availableArtifacts.length,
      stale_update_status: stale.status(),
      transcode_attempt_before: transcodeBefore.attempt,
      transcode_attempt_after: finalTask.steps.find((step) => step.kind === "transcode")?.attempt,
      output_checksum_changed: outputAfter.checksum_sha256 !== outputBefore.checksum_sha256,
      review_qc_actual: qcRule.actual,
      review_qc_expected: translatedAfter.qc_report.score,
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
