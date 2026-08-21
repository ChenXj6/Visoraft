const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://127.0.0.1:4173";
const artifactDir = path.resolve(__dirname, "../../artifacts/v1/test-runs/full-route-style-audit");
const windowsChrome = "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe";

function launchOptions() {
  return fs.existsSync(windowsChrome)
    ? { headless: true, executablePath: windowsChrome }
    : { headless: true };
}

function safeName(value) {
  return value
    .replace(/^\//, "")
    .replace(/[/?#=&]+/g, "-")
    .replace(/[^a-zA-Z0-9\u4e00-\u9fa5-]+/g, "-") || "dashboard";
}

async function main() {
  fs.mkdirSync(artifactDir, { recursive: true });
  const browser = await chromium.launch(launchOptions());
  const page = await browser.newPage({ viewport: { width: 1320, height: 1000 }, locale: "zh-CN" });
  const queue = ["/", "/tasks", "/tasks/new", "/reviews", "/files", "/publishing", "/publishing/settings", "/monitors", "/monitors/new", "/settings", "/cookies"];
  const visited = new Set();
  const report = [];

  try {
    while (queue.length && visited.size < 30) {
      const route = queue.shift();
      if (!route || visited.has(route)) continue;
      visited.add(route);
      const response = await page.goto(new URL(route, baseURL).href, { waitUntil: "domcontentloaded", timeout: 30000 });
      await page.waitForTimeout(700);

      const links = await page.locator("a[href]").evaluateAll((elements) =>
        elements.map((element) => element.getAttribute("href")).filter(Boolean)
      );
      for (const href of links) {
        const url = new URL(href, baseURL);
        if (url.origin !== new URL(baseURL).origin) continue;
        const candidate = `${url.pathname}${url.search}`;
        if (/^\/(tasks|reviews|publishing|monitors)\/[a-zA-Z0-9-]+(?:\/(edit|history))?(?:\?.*)?$/.test(candidate) && !visited.has(candidate)) {
          queue.push(candidate);
        }
      }

      const audit = await page.evaluate(() => {
        const visible = (element) => {
          const style = getComputedStyle(element);
          const rect = element.getBoundingClientRect();
          return style.display !== "none" && style.visibility !== "hidden" && rect.width > 0 && rect.height > 0;
        };
        const textNodes = [...document.querySelectorAll("body *")].filter((element) =>
          visible(element) && [...element.childNodes].some((node) => node.nodeType === Node.TEXT_NODE && node.textContent.trim())
        );
        const undersizedText = textNodes
          .map((element) => ({
            tag: element.tagName.toLowerCase(),
            text: element.textContent.replace(/\s+/g, " ").trim().slice(0, 100),
            fontSize: Number.parseFloat(getComputedStyle(element).fontSize)
          }))
          .filter((item) => item.fontSize < 12);
        const buttons = [...document.querySelectorAll("button, a.button, a.quick-create")]
          .filter(visible)
          .map((element) => {
            const rect = element.getBoundingClientRect();
            return {
              text: (element.textContent || element.getAttribute("aria-label") || "").replace(/\s+/g, " ").trim(),
              width: Number(rect.width.toFixed(2)),
              height: Number(rect.height.toFixed(2)),
              fontSize: getComputedStyle(element).fontSize
            };
          });
        const colorUsage = {};
        const colorSamples = {};
        for (const element of [...document.querySelectorAll("body *")].filter(visible)) {
          const style = getComputedStyle(element);
          for (const value of [style.color, style.backgroundColor, style.borderTopColor, style.borderRightColor, style.borderBottomColor, style.borderLeftColor]) {
            if (!value || value === "rgba(0, 0, 0, 0)") continue;
            colorUsage[value] = (colorUsage[value] || 0) + 1;
            colorSamples[value] ||= [];
            if (colorSamples[value].length < 4) {
              colorSamples[value].push({
                tag: element.tagName.toLowerCase(),
                className: element.className?.toString().slice(0, 160) || "",
                text: element.textContent.replace(/\s+/g, " ").trim().slice(0, 80)
              });
            }
          }
        }
        return {
          title: document.title,
          h1: document.querySelector("h1")?.textContent?.trim() || "",
          scrollWidth: document.documentElement.scrollWidth,
          clientWidth: document.documentElement.clientWidth,
          undersizedText,
          shortButtons: buttons.filter((button) => button.height < 28),
          buttons,
          colorUsage,
          colorSamples
        };
      });

      const pageHeight = Math.min(2200, await page.evaluate(() => document.documentElement.scrollHeight));
      await page.setViewportSize({ width: 1320, height: Math.max(1000, pageHeight) });
      await page.screenshot({
        path: path.join(artifactDir, `${String(report.length + 1).padStart(2, "0")}-${safeName(route)}.png`),
        fullPage: false,
        animations: "disabled"
      });
      report.push({ route, status: response?.status() || 0, ...audit });
      await page.setViewportSize({ width: 1320, height: 1000 });
    }
  } finally {
    await browser.close();
  }

  fs.writeFileSync(path.join(artifactDir, "report.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify({
    status: "captured",
    routes: report.length,
    undersizedText: report.reduce((sum, item) => sum + item.undersizedText.length, 0),
    shortButtons: report.reduce((sum, item) => sum + item.shortButtons.length, 0),
    horizontalOverflow: report.filter((item) => item.scrollWidth > item.clientWidth).map((item) => item.route),
    artifactDir
  }, null, 2));
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
