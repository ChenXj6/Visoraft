const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_WEB_URL || "http://localhost:4173";
const taskID = process.env.VISORAFT_TASK_ID;
const accountID = process.env.VISORAFT_BILIBILI_ACCOUNT_ID;
const cookieProfileID = process.env.VISORAFT_COOKIE_PROFILE_ID || "";
const categoryID = process.env.VISORAFT_BILIBILI_CATEGORY_ID || "250";
const screenshotPath = process.env.VISORAFT_SCREENSHOT_PATH || "";
const timeoutMs = Number(process.env.VISORAFT_PUBLISH_TIMEOUT_MS || 1_800_000);
const realPublishGuard =
  process.env.VISORAFT_REAL_PUBLISH_CONFIRM ===
  "I_UNDERSTAND_THIS_CREATES_A_REAL_BILIBILI_SUBMISSION";

if (!taskID) throw new Error("VISORAFT_TASK_ID is required");
if (!accountID) throw new Error("VISORAFT_BILIBILI_ACCOUNT_ID is required");

const localizedMetadata = {
  title: "在中国最具未来感的城市深圳待了4天",
  description:
    "深圳像一座按下快进键建成的城市。这个视频实地走访福田、南山、华强北、深圳湾、蛇口、前海、华侨城创意园和南头古城，体验无人驾驶出租车、无人机配送、电动公交、机器人与智慧交通如何进入日常生活。\n\n从小渔村到科技之都，深圳只用了几十年。它真的代表未来城市的样子吗？这次用4天时间给出真实体验。",
  tags: ["深圳", "中国旅行", "未来城市", "科技", "城市探索"],
  category: "生活 / 出行"
};

async function decode(response, label) {
  if (!response.ok()) {
    throw new Error(`${label}: ${response.status()} ${await response.text()}`);
  }
  return response.json();
}

function terminal(status) {
  return ["published", "failed", "reconciliation_required", "cancelled"].includes(status);
}

async function checkLayout(page) {
  return page.locator("body").evaluate(() => {
    const sizes = [...document.querySelectorAll("body *")]
      .filter((element) => {
        const style = getComputedStyle(element);
        return style.display !== "none" && style.visibility !== "hidden" &&
          (element.textContent || "").trim();
      })
      .map((element) => Number.parseFloat(getComputedStyle(element).fontSize))
      .filter(Number.isFinite);
    return {
      minimum_font_size: Math.min(...sizes),
      horizontal_overflow: document.documentElement.scrollWidth > window.innerWidth
    };
  });
}

function labeledControl(page, label) {
  return page
    .locator("label.field")
    .filter({ has: page.getByText(label, { exact: true }) })
    .locator("input, textarea, select")
    .first();
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

    if (cookieProfileID) {
      await decode(
        await page.request.post(
          `${baseURL}/api/v1/cookie-profiles/${cookieProfileID}/sync`
        ),
        "sync CookieCloud profile"
      );
    }
    const account = await decode(
      await page.request.post(
        `${baseURL}/api/v1/platform-accounts/${accountID}/check`
      ),
      "check Bilibili account"
    );
    if (!account.ok || account.status !== "ready" || account.auth_mode !== "cookie") {
      throw new Error(`Bilibili account is not ready: ${account.last_error_message || account.message}`);
    }

    let review = await decode(
      await page.request.get(`${baseURL}/api/v1/reviews/${taskID}`),
      "load review"
    );
    if (review.task.status === "awaiting_manual_review") {
      await page.goto(`${baseURL}/reviews/${taskID}`, {
        waitUntil: "domcontentloaded",
        timeout: 20_000
      });
      await page.getByRole("heading", { name: "最终元数据", exact: true }).waitFor();
      await labeledControl(page, "标题").fill(localizedMetadata.title);
      await labeledControl(page, "简介").fill(localizedMetadata.description);
      await labeledControl(page, "标签（逗号分隔）").fill(localizedMetadata.tags.join(", "));
      await labeledControl(page, "分区").fill(localizedMetadata.category);
      await labeledControl(page, "本次修改原因").fill("真实投稿前完成中文元数据本地化");
      const [metadataResponse] = await Promise.all([
        page.waitForResponse(
          (response) => response.request().method() === "PUT" &&
            response.url().endsWith(`/api/v1/reviews/${taskID}/metadata`),
          { timeout: 20_000 }
        ),
        page.getByRole("button", { name: "保存元数据版本", exact: true }).click()
      ]);
      review = await decode(metadataResponse, "save localized metadata");
      if (review.task.title !== localizedMetadata.title) {
        throw new Error("localized title was not persisted");
      }

      await labeledControl(page, "意见 / 原因").fill("字幕、媒体和元数据复核通过");
      const [approveResponse] = await Promise.all([
        page.waitForResponse(
          (response) => response.request().method() === "POST" &&
            response.url().endsWith(`/api/v1/reviews/${taskID}/approve`),
          { timeout: 30_000 }
        ),
        page.getByRole("button", { name: "批准并进入待发布", exact: true }).click()
      ]);
      review = await decode(approveResponse, "approve review");
    }
    if (!["ready_to_publish", "publishing", "published"].includes(review.task.status)) {
      throw new Error(`unexpected task state after review: ${review.task.status}`);
    }
    if (review.task.review_status !== "approved") {
      throw new Error(`review was not approved: ${review.task.review_status}`);
    }

    let publishing = await decode(
      await page.request.get(`${baseURL}/api/v1/publishing/${taskID}`),
      "load publishing detail"
    );
    if (!publishing.job) {
      publishing = await decode(
        await page.request.post(`${baseURL}/api/v1/publishing/${taskID}/prepare`),
        "prepare publishing draft"
      );
    }
    const publication = publishing.publications.find((item) => item.platform === "bilibili");
    if (!publication) throw new Error("Bilibili publication draft is missing");
    if (publication.simulation) throw new Error("real source is bound to a simulation account");
    if (publication.account_id !== accountID) {
      throw new Error(`unexpected Bilibili account: ${publication.account_id}`);
    }

    if (["draft", "blocked", "failed"].includes(publication.status)) {
      await page.goto(`${baseURL}/publishing/${taskID}`, {
        waitUntil: "domcontentloaded",
        timeout: 20_000
      });
      await page.getByRole("heading", { name: "Bilibili 投稿", exact: true }).waitFor();
      await labeledControl(page, "投稿分区").selectOption(categoryID);
      await labeledControl(page, "标题").fill(localizedMetadata.title);
      await labeledControl(page, "标签（逗号分隔）").fill(
        [...localizedMetadata.tags, "Visoraft"].join(", ")
      );
      const [draftResponse] = await Promise.all([
        page.waitForResponse(
          (response) => response.request().method() === "PUT" &&
            response.url().endsWith(`/api/v1/publishing/${taskID}/platforms/bilibili`),
          { timeout: 20_000 }
        ),
        page.getByRole("button", { name: "保存平台草稿", exact: true }).click()
      ]);
      publishing = await decode(draftResponse, "save Bilibili draft");
    }

    let current = publishing.publications.find((item) => item.platform === "bilibili");
    if (!current) throw new Error("Bilibili publication disappeared after draft save");
    if (current.category_id !== categoryID) {
      throw new Error(`Bilibili category was not persisted: ${current.category_id}`);
    }
    if (current.title !== localizedMetadata.title) {
      throw new Error(`Bilibili localized title was not persisted: ${current.title}`);
    }
    if (publishing.blockers.length) {
      throw new Error(`publishing blockers remain: ${JSON.stringify(publishing.blockers)}`);
    }

    await page.goto(`${baseURL}/publishing/${taskID}`, {
      waitUntil: "domcontentloaded",
      timeout: 20_000
    });
    await page.getByRole("heading", { name: "投稿工作台", exact: true }).waitFor();
    const draftLayout = await checkLayout(page);
    if (draftLayout.minimum_font_size < 12 || draftLayout.horizontal_overflow) {
      throw new Error(`publishing draft layout failed: ${JSON.stringify(draftLayout)}`);
    }

    if (!terminal(current.status) && current.status === "draft") {
      if (!realPublishGuard) {
        if (screenshotPath) await page.screenshot({ path: screenshotPath, fullPage: true });
        if (browserErrors.length) throw new Error(browserErrors.join("\n"));
        console.log(JSON.stringify({
          task_id: taskID,
          phase: "draft_verified",
          real_publish_enqueued: false,
          reason: "VISORAFT_REAL_PUBLISH_CONFIRM guard is absent",
          publication_id: current.id,
          account_id: current.account_id,
          category_id: current.category_id,
          media_asset_id: current.media_asset_id,
          cover_asset_id: current.cover_asset_id,
          ...draftLayout
        }));
        return;
      }
      const [enqueueResponse] = await Promise.all([
        page.waitForResponse(
          (response) => response.request().method() === "POST" &&
            response.url().endsWith(`/api/v1/publishing/${taskID}/enqueue`),
          { timeout: 30_000 }
        ),
        page.getByRole("button", { name: "确认并加入发布队列", exact: true }).click()
      ]);
      publishing = await decode(enqueueResponse, "enqueue real Bilibili submission");
      current = publishing.publications.find((item) => item.platform === "bilibili");
    }

    const observations = [];
    const seen = new Set();
    const startedAt = Date.now();
    while (current && !terminal(current.status) && Date.now() - startedAt < timeoutMs) {
      publishing = await decode(
        await page.request.get(`${baseURL}/api/v1/publishing/${taskID}`),
        "poll real Bilibili publication"
      );
      current = publishing.publications.find((item) => item.platform === "bilibili");
      const latestAttempt = (publishing.attempts[current.id] || [])[0];
      const signature = [publishing.job?.status, current.status, current.attempt, latestAttempt?.stage].join("|");
      if (!seen.has(signature)) {
        seen.add(signature);
        const observation = {
          elapsed_seconds: Math.round((Date.now() - startedAt) / 1000),
          job_status: publishing.job?.status || "",
          publication_status: current.status,
          publication_attempt: current.attempt,
          stage: latestAttempt?.stage || ""
        };
        observations.push(observation);
        console.log(JSON.stringify(observation));
      }
      if (terminal(current.status)) break;
      await page.waitForTimeout(3_000);
    }
    if (!current || !terminal(current.status)) {
      throw new Error(`Bilibili publication did not finish within ${timeoutMs}ms`);
    }

    await page.reload({ waitUntil: "domcontentloaded", timeout: 20_000 });
    await page.getByRole("heading", { name: "投稿工作台", exact: true }).waitFor();
    const finalLayout = await checkLayout(page);
    if (finalLayout.minimum_font_size < 12 || finalLayout.horizontal_overflow) {
      throw new Error(`publishing result layout failed: ${JSON.stringify(finalLayout)}`);
    }
    if (screenshotPath) await page.screenshot({ path: screenshotPath });

    let attemptStageLocalized = true;
    const attemptSummary = page.locator("details.publication-attempts summary");
    if (await attemptSummary.count()) {
      await attemptSummary.click();
      const attemptText = await page.locator("details.publication-attempts").innerText();
      attemptStageLocalized = attemptText.includes("提交稿件") &&
        !/\b(preparing|uploading|submitting|publish|reconcile)\b/i.test(attemptText);
      if (!attemptStageLocalized) {
        throw new Error(`publication attempt stage was not localized: ${attemptText}`);
      }
    }
    if (browserErrors.length) throw new Error(browserErrors.join("\n"));

    const result = {
      task_id: taskID,
      account_remote_user_id: account.remote_user_id,
      publication_id: current.id,
      publication_status: current.status,
      remote_submission_id: current.remote_submission_id,
      remote_url: current.remote_url,
      remote_status: current.remote_status,
      error_code: current.error_code,
      error_message: current.error_message,
      error_retryable: current.error_retryable,
      observations,
      attempt_stage_localized: attemptStageLocalized,
      ...finalLayout
    };
    console.log(JSON.stringify(result));
    if (current.status !== "published") {
      throw new Error(`real Bilibili publication ended as ${current.status}: ${current.error_code} ${current.error_message}`);
    }
    if (!current.remote_submission_id || !current.remote_url) {
      throw new Error("Bilibili accepted the submission without a persisted remote id/url");
    }
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
