const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const apiBaseURL = process.env.VISORAFT_API_URL || "http://localhost:8080";
const webBaseURL = process.env.VISORAFT_BASE_URL || "http://localhost:4173";
const fixtureURL = "http://fixture-provider:8090/media/sample.wav";
const artifactsDir = path.resolve(
  __dirname,
  "../../artifacts/v1/acceptance/automatic-review-branches",
);

fs.mkdirSync(artifactsDir, { recursive: true });

async function requestJSON(route, options = {}) {
  const response = await fetch(`${apiBaseURL}${route}`, {
    ...options,
    headers: {
      ...(options.body ? { "content-type": "application/json" } : {}),
      ...(options.headers || {}),
    },
  });
  const text = await response.text();
  const body = text ? JSON.parse(text) : undefined;
  if (!response.ok) {
    throw new Error(
      `${options.method || "GET"} ${route} failed: HTTP ${response.status} ${text}`,
    );
  }
  return body;
}

function editableSettings(settings) {
  const { version, secret_configured, updated_at, ...config } = settings;
  return structuredClone(config);
}

function automaticSettings(settings, { fallback, shouldPass }) {
  const config = editableSettings(settings);
  config.review = {
    mode: "automatic",
    automatic_fallback: fallback,
    rules: {
      require_media: true,
      require_title: true,
      minimum_description_length: shouldPass ? 0 : 500,
      maximum_duration_seconds: 0,
      require_subtitle_qc: false,
      minimum_subtitle_qc_score: 0,
    },
  };
  config.automation = {
    ...config.automation,
    enabled: false,
    translate_title: false,
    translate_description: false,
    generate_tags: false,
    recommend_categories: false,
    process_cover: false,
  };
  config.subtitle = { ...config.subtitle, enabled: false };
  config.transcode = { ...config.transcode, enabled: false };
  config.moderation = { ...config.moderation, enabled: false };
  config.publishing = {
    ...config.publishing,
    auto_publish_after_review: false,
  };
  return config;
}

async function updateSettings(config) {
  const current = await requestJSON("/api/v1/settings");
  return requestJSON("/api/v1/settings", {
    method: "PUT",
    body: JSON.stringify({ ...config, expected_version: current.version }),
  });
}

async function createTask() {
  return requestJSON("/api/v1/tasks", {
    method: "POST",
    body: JSON.stringify({
      source_url: fixtureURL,
      target_platforms: ["bilibili"],
      repost_statement_version: "brief_v1",
      auto_publish: false,
    }),
  });
}

async function waitForTask(taskID, predicate, timeoutMs = 120_000) {
  const deadline = Date.now() + timeoutMs;
  let latest;
  while (Date.now() < deadline) {
    latest = await requestJSON(`/api/v1/tasks/${taskID}`);
    if (predicate(latest)) return latest;
    await new Promise((resolve) => setTimeout(resolve, 1_000));
  }
  throw new Error(
    `task ${taskID} did not reach expected state: ${JSON.stringify({
      status: latest?.status,
      review_status: latest?.review_status,
      error_code: latest?.error_code,
      error_message: latest?.error_message,
      steps: latest?.steps?.map((step) => ({
        kind: step.kind,
        status: step.status,
        attempt: step.attempt,
      })),
    })}`,
  );
}

function automaticRun(review) {
  const run = review.runs.find((item) => item.mode === "automatic");
  assert.ok(run, `task ${review.task.id} should retain its automatic review run`);
  return run;
}

function assertRuleEvidence(run, shouldPass) {
  assert.equal(run.status, "completed");
  assert.ok(run.completed_at, "automatic run should have a completion timestamp");
  assert.ok(run.rule_results.length >= 2, "automatic run should persist enabled rule evidence");
  assert.ok(
    run.rule_results.some((rule) => rule.key === "media_present" && rule.passed),
    "media availability rule should pass",
  );
  assert.ok(
    run.rule_results.some((rule) => rule.key === "title_present" && rule.passed),
    "title rule should pass",
  );
  const descriptionRule = run.rule_results.find(
    (rule) => rule.key === "description_length",
  );
  if (shouldPass) {
    assert.equal(
      descriptionRule,
      undefined,
      "disabled minimum-description rule should not create misleading evidence",
    );
  } else {
    assert.ok(descriptionRule, "enabled description rule should be persisted");
    assert.equal(descriptionRule.passed, false);
  }
}

async function verifyBrowser(scenarios) {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    locale: "zh-CN",
  });
  const diagnostics = [];
  const page = await context.newPage();
  page.on("console", (message) => {
    if (message.type() === "error") diagnostics.push(`console: ${message.text()}`);
  });
  page.on("pageerror", (error) => diagnostics.push(`page: ${error.message}`));
  page.on("response", (response) => {
    if (response.status() >= 400) {
      diagnostics.push(`HTTP ${response.status()} ${response.url()}`);
    }
  });

  const expectedLabels = {
    approved: "自动审核通过",
    manual: "自动审核转人工",
    rejected: "自动审核未通过",
  };
  const visualEvidence = {};
  try {
    for (const scenario of scenarios) {
      await page.goto(`${webBaseURL}/reviews/${scenario.task.id}`, {
        waitUntil: "networkidle",
      });
      await page.getByRole("heading", { name: "操作记录", exact: true }).waitFor();
      const bodyText = await page.locator("body").innerText();
      assert.ok(
        bodyText.includes(expectedLabels[scenario.name]),
        `${scenario.name} should show its localized automatic action`,
      );
      assert.equal(
        /automatic_(approve|fallback|reject)/.test(bodyText),
        false,
        `${scenario.name} must not expose backend action enums`,
      );
      const layout = await page.evaluate(() => {
        const visibleText = [...document.querySelectorAll("body *")].filter((element) => {
          const style = getComputedStyle(element);
          const rect = element.getBoundingClientRect();
          return (
            element.childNodes.length > 0 &&
            [...element.childNodes].some(
              (node) => node.nodeType === Node.TEXT_NODE && node.textContent?.trim(),
            ) &&
            style.display !== "none" &&
            style.visibility !== "hidden" &&
            rect.width > 0 &&
            rect.height > 0
          );
        });
        return {
          minimumFontSize: Math.min(
            ...visibleText.map((element) => Number.parseFloat(getComputedStyle(element).fontSize)),
          ),
          viewportWidth: window.innerWidth,
          documentWidth: document.documentElement.scrollWidth,
        };
      });
      assert.ok(layout.minimumFontSize >= 12, JSON.stringify(layout));
      assert.ok(layout.documentWidth <= layout.viewportWidth + 1, JSON.stringify(layout));
      const screenshot = path.join(artifactsDir, `${scenario.name}-review.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      visualEvidence[scenario.name] = { ...layout, screenshot };
    }

    await page.goto(`${webBaseURL}/reviews`, { waitUntil: "networkidle" });
    await page.getByRole("heading", { name: "媒体复核台", exact: true }).waitFor();
    const queueText = await page.locator("body").innerText();
    assert.ok(
      queueText.includes(scenarios.find((item) => item.name === "manual").task.id.slice(0, 8)),
      "fallback task should be visible in manual review queue",
    );
    assert.equal(
      queueText.includes(scenarios.find((item) => item.name === "approved").task.id.slice(0, 8)),
      false,
      "approved task should not be in manual review queue",
    );
    assert.equal(
      queueText.includes(scenarios.find((item) => item.name === "rejected").task.id.slice(0, 8)),
      false,
      "rejected task should not be in manual review queue",
    );
    await page.screenshot({
      path: path.join(artifactsDir, "manual-review-queue.png"),
      fullPage: true,
    });
    assert.deepEqual(diagnostics, []);
    return visualEvidence;
  } finally {
    await context.close();
    await browser.close();
  }
}

async function deleteTestTask(taskID) {
  let task = await requestJSON(`/api/v1/tasks/${taskID}`);
  if (task.steps.some((step) => step.status === "queued" || step.status === "running")) {
    task = await requestJSON(`/api/v1/tasks/${taskID}/cancel`, { method: "POST" });
  }
  task = await requestJSON(`/api/v1/tasks/${taskID}/archive`, {
    method: "POST",
    body: JSON.stringify({
      expected_version: task.version,
      delete_assets: true,
      reason: "自动审核分支验收完成后清理测试数据",
    }),
  });
  task = await waitForTask(
    taskID,
    (item) => item.assets.every((asset) => asset.status === "deleted"),
    60_000,
  );
  await requestJSON(`/api/v1/tasks/${taskID}`, {
    method: "DELETE",
    body: JSON.stringify({
      expected_version: task.version,
      confirmation: `purge:${taskID}`,
      reason: "自动审核分支验收完成后永久清理",
    }),
  });
}

async function main() {
  const original = await requestJSON("/api/v1/settings");
  const originalConfig = editableSettings(original);
  const created = [];
  let restored = false;
  fs.writeFileSync(
    path.join(artifactsDir, "settings-backup.json"),
    `${JSON.stringify(original, null, 2)}\n`,
  );

  try {
    const definitions = [
      { name: "approved", fallback: "manual", shouldPass: true },
      { name: "manual", fallback: "manual", shouldPass: false },
      { name: "rejected", fallback: "reject", shouldPass: false },
    ];
    for (const definition of definitions) {
      await updateSettings(automaticSettings(await requestJSON("/api/v1/settings"), definition));
      const task = await createTask();
      created.push({ ...definition, task });
    }
    await updateSettings(originalConfig);
    restored = true;
  } finally {
    if (!restored) {
      await updateSettings(originalConfig);
    }
  }

  const expectedStates = {
    approved: {
      status: "ready_to_publish",
      review: "approved",
      reviewStep: "succeeded",
      action: "automatic_approve",
    },
    manual: {
      status: "awaiting_manual_review",
      review: "pending",
      reviewStep: "running",
      action: "automatic_fallback",
    },
    rejected: {
      status: "failed",
      review: "rejected",
      reviewStep: "failed",
      action: "automatic_reject",
    },
  };
  const results = [];
  for (const scenario of created) {
    const expected = expectedStates[scenario.name];
    const task = await waitForTask(
      scenario.task.id,
      (item) => item.status === expected.status && item.review_status === expected.review,
    );
    const review = await requestJSON(`/api/v1/reviews/${task.id}`);
    const run = automaticRun(review);
    assert.equal(run.decision, expected.review === "pending" ? "manual_required" : expected.review);
    assertRuleEvidence(run, scenario.shouldPass);
    assert.ok(
      review.actions.some((action) => action.action === expected.action),
      `${scenario.name} should persist ${expected.action}`,
    );
    assert.equal(task.review_mode, "automatic");
    assert.ok(
      task.steps.some(
        (step) => step.kind === "review" && step.status === expected.reviewStep,
      ),
    );
    assert.equal(task.steps.some((step) => step.kind === "subtitles"), false);
    assert.equal(task.steps.some((step) => step.kind === "transcode"), false);
    results.push({ ...scenario, task, review });
  }

  const reviewQueue = await requestJSON("/api/v1/reviews");
  assert.ok(reviewQueue.items.some((item) => item.id === results[1].task.id));
  assert.equal(reviewQueue.items.some((item) => item.id === results[0].task.id), false);
  assert.equal(reviewQueue.items.some((item) => item.id === results[2].task.id), false);

  const visualEvidence = await verifyBrowser(results);
  const report = {
    status: "passed",
    fixtureOnly: true,
    paidASRRequests: 0,
    realPlatformSubmissions: 0,
    originalSettingsVersion: original.version,
    restoredSettingsVersion: (await requestJSON("/api/v1/settings")).version,
    branches: results.map(({ name, task, review }) => ({
      name,
      taskID: task.id,
      status: task.status,
      reviewStatus: task.review_status,
      steps: task.steps.map((step) => ({
        kind: step.kind,
        status: step.status,
        attempt: step.attempt,
      })),
      automaticRun: automaticRun(review),
      actions: review.actions.map((action) => action.action),
      visual: visualEvidence[name],
    })),
  };
  fs.writeFileSync(
    path.join(artifactsDir, "report.json"),
    `${JSON.stringify(report, null, 2)}\n`,
  );

  for (const result of results) {
    await deleteTestTask(result.task.id);
  }
  report.cleanedTaskIDs = results.map((result) => result.task.id);
  fs.writeFileSync(
    path.join(artifactsDir, "report.json"),
    `${JSON.stringify(report, null, 2)}\n`,
  );
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
}

main().catch((error) => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
