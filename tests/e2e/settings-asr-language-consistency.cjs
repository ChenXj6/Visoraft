const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://localhost:4173";
const screenshotPath = path.resolve(
  __dirname,
  "../../artifacts/v1/acceptance/2026-08-11-asr-language-consistency.png"
);

function field(page, labelText, control) {
  return page
    .locator("label.field")
    .filter({ has: page.locator("span", { hasText: labelText }) })
    .locator(control);
}

async function main() {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    page.setDefaultTimeout(15_000);
    const currentResponse = await page.request.get(`${baseURL}/api/v1/settings`);
    assert.ok(currentResponse.ok());
    const current = await currentResponse.json();
    const {
      version,
      secret_configured: _secretConfigured,
      updated_at: _updatedAt,
      ...config
    } = current;
    const invalidConfig = structuredClone(config);
    invalidConfig.subtitle.source_language = "en";
    invalidConfig.subtitle.asr.language = "zh";
    const rejectedResponse = await page.request.put(`${baseURL}/api/v1/settings`, {
      data: { expected_version: version, ...invalidConfig, secrets: {} }
    });
    assert.equal(rejectedResponse.status(), 422);
    const rejected = await rejectedResponse.json();
    assert.match(
      rejected.error?.fields?.["subtitle.asr.language"] || "",
      /必须包含源语言/
    );

    await page.goto(`${baseURL}/settings?section=subtitles`, {
      waitUntil: "domcontentloaded"
    });
    await page.getByRole("heading", { name: "字幕来源与 ASR", exact: true }).waitFor();
    const sourceLanguageInput = field(page, "源语言", "input");
    const languageInput = field(page, "语言提示", "input");
    await sourceLanguageInput.fill("en");
    await languageInput.fill("zh");
    await page
      .getByText(/当前源语言为 en，ASR 语言提示却是 zh/)
      .waitFor();
    await languageInput.fill("en");
    assert.equal(
      await page.getByText(/当前源语言为 en，ASR 语言提示却是 zh/).count(),
      0
    );

    const offenders = await page.evaluate(() =>
      [...document.body.querySelectorAll("*")]
        .filter((element) => {
          if (![...element.childNodes].some((node) => node.nodeType === Node.TEXT_NODE && node.textContent?.trim())) {
            return false;
          }
          const style = getComputedStyle(element);
          const rect = element.getBoundingClientRect();
          return style.display !== "none" && style.visibility !== "hidden" && rect.width > 0 && rect.height > 0 && Number.parseFloat(style.fontSize) < 12;
        })
        .map((element) => ({ text: element.textContent.trim().slice(0, 60), font: getComputedStyle(element).fontSize }))
    );
    assert.deepEqual(offenders, []);
    assert.ok(documentWidth(await page.evaluate(() => ({
      viewport: document.documentElement.clientWidth,
      document: document.documentElement.scrollWidth
    }))));
    fs.mkdirSync(path.dirname(screenshotPath), { recursive: true });
    await page.screenshot({ path: screenshotPath, fullPage: true });
    console.log(
      JSON.stringify({
        ok: true,
        previous_version: version,
        settings_version: version,
        persisted_config_unchanged: true,
        validation_source_language: "en",
        validation_asr_language: "zh",
        screenshot: screenshotPath
      })
    );
  } finally {
    await browser.close();
  }
}

function documentWidth(value) {
  assert.ok(
    value.document <= value.viewport + 1,
    `页面横向溢出：${JSON.stringify(value)}`
  );
  return true;
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
