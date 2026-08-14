const { chromium } = require("playwright");

const baseURL = process.env.ORIGINAL_BASE_URL || "http://127.0.0.1:5000";
const routes = [
  "/",
  "/tasks",
  "/manual_review",
  "/youtube_monitor",
  "/youtube_monitor/config",
  "/settings",
];

function normalize(value) {
  return (value || "").replace(/\s+/g, " ").trim();
}

async function describePage(page, route) {
  await page.goto(`${baseURL}${route}`, {
    waitUntil: "domcontentloaded",
    timeout: 15_000,
  });
  await page.waitForTimeout(500);

  const result = await page.evaluate(() => {
    const clean = (value) => (value || "").replace(/\s+/g, " ").trim();
    const visible = (element) => {
      const style = window.getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      return (
        style.display !== "none" &&
        style.visibility !== "hidden" &&
        rect.width > 0 &&
        rect.height > 0
      );
    };
    const labelFor = (element) => {
      if (element.id) {
        const escaped = CSS.escape(element.id);
        const explicit = document.querySelector(`label[for="${escaped}"]`);
        if (explicit) return clean(explicit.textContent);
      }
      const parent = element.closest("label");
      if (parent) return clean(parent.textContent);
      const group = element.closest(
        ".form-group,.mb-3,.mb-2,.form-check,.input-group,.setting-item",
      );
      const nearby = group?.querySelector("label,.form-label,.form-check-label");
      return clean(nearby?.textContent || element.getAttribute("aria-label"));
    };

    const headings = [...document.querySelectorAll("h1,h2,h3,h4")]
      .filter(visible)
      .map((element) => clean(element.textContent))
      .filter(Boolean);
    const nav = [...document.querySelectorAll("nav a,a.nav-link")]
      .filter(visible)
      .map((element) => ({
        text: clean(element.textContent),
        href: element.getAttribute("href"),
      }))
      .filter((item) => item.text);
    const actions = [
      ...document.querySelectorAll(
        "button,input[type=submit],input[type=button],a.btn",
      ),
    ]
      .filter(visible)
      .map((element) =>
        clean(
          element.textContent ||
            element.value ||
            element.getAttribute("aria-label") ||
            element.title,
        ),
      )
      .filter(Boolean);
    const controls = [
      ...document.querySelectorAll("input:not([type=hidden]),select,textarea"),
    ].map((element) => ({
      label: labelFor(element),
      tag: element.tagName.toLowerCase(),
      type: element.getAttribute("type") || undefined,
      name: element.getAttribute("name") || undefined,
      id: element.id || undefined,
      visible: visible(element),
      options:
        element.tagName === "SELECT"
          ? [...element.options].map((option) => clean(option.textContent))
          : undefined,
    }));
    const statusTexts = [
      ...document.querySelectorAll(
        ".badge,.alert,.status,.empty-state,.text-muted,.card-text",
      ),
    ]
      .filter(visible)
      .map((element) => clean(element.textContent))
      .filter(Boolean)
      .slice(0, 40);

    return {
      title: document.title,
      path: location.pathname,
      headings,
      nav,
      actions,
      controls,
      statusTexts,
    };
  });

  console.log(`\n=== ${route} -> ${result.path} | ${result.title} ===`);
  console.log(`HEADINGS ${JSON.stringify(result.headings)}`);
  console.log(`NAV ${JSON.stringify(result.nav)}`);
  console.log(`ACTIONS ${JSON.stringify(result.actions)}`);
  console.log(
    `CONTROLS ${result.controls.length} ${JSON.stringify(result.controls)}`,
  );
  console.log(`STATES ${JSON.stringify(result.statusTexts)}`);

  return result;
}

async function inspectSettingsGroups(page) {
  await page.goto(`${baseURL}/settings`, {
    waitUntil: "domcontentloaded",
    timeout: 15_000,
  });
  await page.waitForTimeout(300);

  const toggles = page.locator(
    '[data-bs-toggle="collapse"],[data-toggle="collapse"],button[aria-expanded]',
  );
  const toggleCount = await toggles.count();
  for (let index = 0; index < toggleCount; index += 1) {
    const toggle = toggles.nth(index);
    const text = normalize(await toggle.textContent());
    if (!text) continue;
    const expanded = await toggle.getAttribute("aria-expanded");
    if (expanded === "false" && (await toggle.isVisible())) {
      await toggle.click({ timeout: 2_000 });
      await page.waitForTimeout(80);
    }
  }

  const groups = await page.evaluate(() => {
    const clean = (value) => (value || "").replace(/\s+/g, " ").trim();
    return [...document.querySelectorAll("h2,h3,h4,.accordion-header")]
      .map((heading) => {
        const container =
          heading.closest(".accordion-item,.card,.settings-section") ||
          heading.parentElement;
        const fields = [
          ...(container?.querySelectorAll(
            "input:not([type=hidden]),select,textarea",
          ) || []),
        ].map((field) => {
          const label =
            (field.id &&
              document.querySelector(`label[for="${CSS.escape(field.id)}"]`)) ||
            field.closest("label") ||
            field
              .closest(".form-group,.mb-3,.mb-2,.form-check,.setting-item")
              ?.querySelector("label,.form-label,.form-check-label");
          return clean(
            label?.textContent ||
              field.getAttribute("aria-label") ||
              field.name ||
              field.id,
          );
        });
        return {
          title: clean(heading.textContent),
          fields: [...new Set(fields.filter(Boolean))],
        };
      })
      .filter((group) => group.title && group.fields.length);
  });

  console.log("\n=== SETTINGS GROUPS ===");
  for (const group of groups) {
    console.log(`${group.title}: ${JSON.stringify(group.fields)}`);
  }
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
    locale: "zh-CN",
  });
  const page = await context.newPage();
  page.on("dialog", async (dialog) => dialog.dismiss());

  try {
    for (const route of routes) {
      await describePage(page, route);
    }
    await inspectSettingsGroups(page);
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
