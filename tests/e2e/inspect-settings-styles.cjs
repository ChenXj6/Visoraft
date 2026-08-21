const { chromium } = require("playwright");

(async () => {
  const browser = await chromium.launch({
    headless: true,
    executablePath: process.env.VISORAFT_BROWSER_PATH || "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
  });
  const page = await browser.newPage({ viewport: { width: 1320, height: 1580 } });
  await page.addInitScript(() => {
    localStorage.setItem("visoraft-theme", "dark");
    document.documentElement.dataset.theme = "dark";
  });
  await page.goto(process.env.VISORAFT_BASE_URL || "http://127.0.0.1:4174/settings?section=subtitles", {
    waitUntil: "networkidle",
  });

  const result = await page.locator(".settings-detail input:not([type=checkbox]):not([type=radio])").first().evaluate((element) => {
    const style = getComputedStyle(element);
    const rootStyle = getComputedStyle(document.documentElement);
    const rect = element.getBoundingClientRect();
    const matchedRules = [];
    for (const sheet of document.styleSheets) {
      let rules;
      try { rules = sheet.cssRules; } catch { continue; }
      for (const rule of rules) {
        if (!(rule instanceof CSSStyleRule)) continue;
        try {
          if (element.matches(rule.selectorText) && (rule.style.background || rule.style.backgroundColor || rule.style.borderColor)) {
            matchedRules.push({
              selector: rule.selectorText,
              background: rule.style.background || rule.style.backgroundColor,
              borderColor: rule.style.borderColor,
            });
          }
        } catch {}
      }
    }
    return {
      background: style.backgroundColor,
      border: style.borderColor,
      color: style.color,
      height: rect.height,
      radius: style.borderRadius,
      fontSize: style.fontSize,
      variables: {
        uiSurfaceMuted: rootStyle.getPropertyValue("--ui-surface-muted").trim(),
        v4Surface: rootStyle.getPropertyValue("--v4-surface").trim(),
        v4Fill: rootStyle.getPropertyValue("--v4-fill").trim(),
        uiBorderStrong: rootStyle.getPropertyValue("--ui-border-strong").trim(),
      },
      matchedRules,
    };
  });
  console.log(JSON.stringify(result, null, 2));
  await browser.close();
})().catch((error) => {
  console.error(error);
  process.exit(1);
});
