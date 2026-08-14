const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://localhost:4173";
const artifactsDir = path.resolve(__dirname, "../../artifacts/v1/test-runs/visual-audit");
const diagnostics = [];
let activeBrowser;

fs.mkdirSync(artifactsDir, { recursive: true });

function observe(page) {
  page.on("console", (message) => {
    if (message.type() === "error") diagnostics.push(`console: ${message.text()}`);
  });
  page.on("pageerror", (error) => diagnostics.push(`page: ${error.message}`));
  page.on("response", (response) => {
    if (response.status() >= 500) {
      diagnostics.push(`HTTP ${response.status()} ${response.url()}`);
    }
  });
}

async function assertViewport(page, scope) {
  const dimensions = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth
  }));
  assert.ok(
    dimensions.document <= dimensions.viewport + 1,
    `${scope} horizontal overflow: ${JSON.stringify(dimensions)}`
  );
}

async function waitForAPI(page) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    const response = await page.request
      .get(`${baseURL}/api/v1/system/status`, { timeout: 3_000 })
      .catch(() => undefined);
    if (response?.ok()) return;
    await page.waitForTimeout(500);
  }
  throw new Error("control API did not become ready through the web proxy");
}

async function capture(page, route, name, viewport) {
  await page.setViewportSize(viewport);
  await page.goto(`${baseURL}${route}`, {
    waitUntil: "domcontentloaded",
    timeout: 15_000
  });
  await page.locator(".main-content").waitFor({ timeout: 10_000 });
  await page.waitForTimeout(500);
  await assertViewport(page, name);
  await page.screenshot({
    path: path.join(artifactsDir, `${name}.png`),
    fullPage: false
  });
}

async function readFontSizes(page, selectors) {
  return page.evaluate((targets) => {
    return Object.fromEntries(
      Object.entries(targets).map(([name, selector]) => {
        const element = document.querySelector(selector);
        return [name, element ? getComputedStyle(element).fontSize : null];
      })
    );
  }, selectors);
}

function assertMinFont(sizes, name, minimum) {
  const value = Number.parseFloat(sizes[name]);
  assert.ok(
    Number.isFinite(value) && value >= minimum,
    `${name} must be at least ${minimum}px, received ${sizes[name]}`
  );
}

async function assertVisibleTextMinimum(page, scope, minimum = 12) {
  const offenders = await page.evaluate((min) => {
    const ignored = new Set(["SCRIPT", "STYLE", "NOSCRIPT", "SVG"]);
    const selectorFor = (element) => {
      if (element.id) return `#${element.id}`;
      const classes = [...element.classList].slice(0, 3).join(".");
      return `${element.tagName.toLowerCase()}${classes ? `.${classes}` : ""}`;
    };
    return [...document.body.querySelectorAll("*")]
      .filter((element) => {
        if (ignored.has(element.tagName)) return false;
        if (![...element.childNodes].some((node) => node.nodeType === Node.TEXT_NODE && node.textContent?.trim())) {
          return false;
        }
        const style = getComputedStyle(element);
        const rect = element.getBoundingClientRect();
        return (
          style.display !== "none" &&
          style.visibility !== "hidden" &&
          Number.parseFloat(style.opacity) > 0 &&
          rect.width > 0 &&
          rect.height > 0 &&
          Number.parseFloat(style.fontSize) < min
        );
      })
      .slice(0, 50)
      .map((element) => ({
        selector: selectorFor(element),
        text: element.textContent.trim().replace(/\s+/g, " ").slice(0, 80),
        fontSize: getComputedStyle(element).fontSize
      }));
  }, minimum);
  assert.deepEqual(
    offenders,
    [],
    `${scope} contains visible text below ${minimum}px:\n${JSON.stringify(offenders, null, 2)}`
  );
}

async function main() {
  const browser = await chromium.launch({ headless: true });
  activeBrowser = browser;
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  observe(page);
  await waitForAPI(page);

  await capture(page, "/", "01-dashboard-1440", { width: 1440, height: 900 });
  await assertVisibleTextMinimum(page, "dashboard-1440");
  assert.equal(await page.locator(".command-context").count(), 0);
  assert.equal(await page.locator(".page-heading > .eyebrow").count(), 0);

  const alignment = await page.evaluate(() => {
    const main = document.querySelector(".page-header").getBoundingClientRect();
    const nav = document.querySelector(".console-nav").getBoundingClientRect();
    return { mainLeft: main.left, navRight: nav.right, gap: main.left - nav.right };
  });
  assert.ok(
    alignment.gap >= 20 && alignment.gap <= 32,
    `desktop content should start at the shell padding: ${JSON.stringify(alignment)}`
  );

  await capture(page, "/settings", "02-settings-1440", {
    width: 1440,
    height: 900
  });
  await assertVisibleTextMinimum(page, "settings-1440");

  await page.goto(`${baseURL}/reviews`, {
    waitUntil: "domcontentloaded",
    timeout: 15_000
  });
  await page.locator(".main-content").waitFor({ timeout: 10_000 });
  await page.waitForTimeout(500);
  const reviewLinks = page.locator('a[href^="/reviews/"]');
  const reviewHref = (await reviewLinks.count()) > 0
    ? await reviewLinks.first().getAttribute("href")
    : null;
  if (reviewHref) {
    await capture(page, reviewHref, "03-review-1440", {
      width: 1440,
      height: 900
    });
    await assertVisibleTextMinimum(page, "review-1440");
  }

  await capture(page, "/tasks", "04-tasks-390", {
    width: 390,
    height: 844
  });
  const taskTypography = await readFontSizes(page, {
    resultCaption: ".task-list-caption",
    taskTitle: ".task-track h2",
    taskMeta: ".track-kicker",
    railLabel: ".workflow-rail-compact .rail-label"
  });
  assertMinFont(taskTypography, "resultCaption", 12);
  assertMinFont(taskTypography, "taskTitle", 15);
  assertMinFont(taskTypography, "taskMeta", 12);
  assertMinFont(taskTypography, "railLabel", 12);

  await capture(page, "/settings", "05-settings-390", {
    width: 390,
    height: 844
  });
  const settingsTypography = await readFontSizes(page, {
    sectionTitle: ".section-heading h2",
    sectionDescription: ".section-heading p",
    modeTitle: ".mode-selector strong",
    modeDescription: ".mode-selector small",
    tabTitle: ".settings-index strong",
    tabDescription: ".settings-index small"
  });
  assertMinFont(settingsTypography, "sectionTitle", 22);
  assertMinFont(settingsTypography, "sectionDescription", 13);
  assertMinFont(settingsTypography, "modeTitle", 14);
  assertMinFont(settingsTypography, "modeDescription", 12);
  assertMinFont(settingsTypography, "tabTitle", 14);
  assertMinFont(settingsTypography, "tabDescription", 12);
  if (reviewHref) {
    await capture(page, reviewHref, "06-review-320", {
      width: 320,
      height: 800
    });
  }

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${baseURL}/`, {
    waitUntil: "domcontentloaded",
    timeout: 15_000
  });
  await page.locator(".main-content").waitFor({ timeout: 10_000 });
  await page.waitForTimeout(500);
  const typography = await readFontSizes(page, {
    navTitle: ".primary-nav strong",
    navDescription: ".primary-nav small",
    pageDescription: ".page-description",
    quickCreate: ".quick-create"
  });
  typography.body = await page.evaluate(() => getComputedStyle(document.body).fontSize);
  assertMinFont(typography, "body", 14);
  assertMinFont(typography, "navTitle", 14);
  assertMinFont(typography, "navDescription", 12);
  assertMinFont(typography, "pageDescription", 14);
  assertMinFont(typography, "quickCreate", 13);

  await browser.close();
  activeBrowser = undefined;
  assert.deepEqual(diagnostics, [], diagnostics.join("\n"));
  console.log(
    JSON.stringify(
      {
        ok: true,
        alignment,
        typography,
        taskTypography,
        settingsTypography,
        reviewHref,
        diagnostics
      },
      null,
      2
    )
  );
}

main().catch(async (error) => {
  if (activeBrowser) await activeBrowser.close().catch(() => {});
  console.error(error);
  process.exitCode = 1;
});
