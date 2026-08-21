const fs = require("node:fs");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://127.0.0.1:4173";
const prototypePath = process.env.VISORAFT_PROTOTYPE_PATH
  ? path.resolve(process.env.VISORAFT_PROTOTYPE_PATH)
  : path.resolve(__dirname, "../../../visoraft-ui-design-interactive.html");
const logoPath = path.resolve(__dirname, "../../apps/web/public/visoraft-mark.svg");
const artifactDir = path.resolve(__dirname, "../../artifacts/v1/test-runs/pixel-baseline-audit");
const windowsChrome = "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe";

function browserLaunchOptions() {
  const executablePath = process.env.VISORAFT_BROWSER_PATH || (fs.existsSync(windowsChrome) ? windowsChrome : undefined);
  return executablePath ? { headless: true, executablePath } : { headless: true };
}

const pages = [
  {
    name: "dashboard",
    route: "/",
    pairs: [
      ["指标区", ".dash-stat-row", ".dashboard-stats"],
      ["指标卡", ".stat-card", ".dashboard-stats > a"],
      ["主工作区", ".dash-grid", ".dashboard-workbench"],
      ["流水线", ".pipeline-flow", ".pipeline-overview"]
    ]
  },
  {
    name: "tasks",
    route: "/tasks",
    pairs: [
      ["筛选栏", ".filter-bar", ".prototype-task-toolbar"],
      ["任务表头", ".list-row.head.table", ".prototype-task-table-head"],
      ["任务行", ".list-row.table:not(.head)", ".prototype-task-row"],
      ["进度条", ".progress", ".prototype-task-progress .progress-track"]
    ]
  },
  {
    name: "newtask",
    route: "/tasks/new",
    pairs: [
      ["新建任务主区", ".new-task-grid", ".new-task-workbench"],
      ["步骤导航", ".wizard-steps", ".new-task-steps"],
      ["内容卡片", ".new-task-grid > div > .card", ".new-task-main > .wizard-step-panel"],
      ["平台卡", ".platform-choice", ".platform-choice"]
    ]
  },
  {
    name: "settings-home",
    route: "/settings",
    pairs: [
      ["设置横幅", ".settings-banner", ".settings-banner"],
      ["设置卡网格", ".settings-category-grid", ".settings-category-grid"],
      ["设置分类卡", ".settings-cat-card", ".settings-cat-card"]
    ]
  },
  {
    name: "settings-detail",
    route: "/settings?section=subtitles",
    pairs: [
      ["设置详情主区", ".settings-detail", ".settings-detail"],
      ["二级导航", ".settings-sub-nav", ".settings-sub-nav"],
      ["二级导航项", ".settings-sub-nav-item", ".settings-sub-nav-item"],
      ["配置折叠组", ".collapse-section", ".settings-collapse"]
    ]
  },
  {
    name: "cookies",
    route: "/cookies",
    pairs: [
      ["Cookie 双栏", ".cookie-cards", ".cookie-input-grid"],
      ["Cookie 添加卡", ".cookie-cards > .card", ".cookie-input-panel"],
      ["Cookie 列表行", ".list-row", ".cookie-profile-row"]
    ]
  },
  {
    name: "monitors",
    route: "/monitors",
    pairs: [
      ["监控网格", ".monitor-grid", ".prototype-monitor-grid"],
      ["监控卡", ".monitor-row", ".prototype-monitor-card"],
      ["监控操作", ".monitor-row .btn", ".prototype-monitor-actions .button"]
    ]
  },
  {
    name: "review",
    route: "/reviews",
    pairs: [
      ["审核主区", ".content-inner > div[style*='grid-template-columns']", ".prototype-review-workbench"],
      ["审核页签", ".filter-tabs", ".prototype-review-tabs"],
      ["审核页签项", ".filter-tab", ".prototype-review-tabs span"],
      ["审核队列行", ".list-row", ".prototype-review-queue button"]
    ]
  },
  {
    name: "files",
    route: "/files",
    pairs: [
      ["文件工作区", ".content-inner > div[style*='grid-template-columns']", ".prototype-file-browser"],
      ["文件卡", ".card", ".prototype-file-stage"],
      ["文件条目", ".sidebar-item", ".prototype-file-tile"]
    ]
  }
];

const designScreens = ["modals", "states", "theme"];

const commonPairs = [
  ["应用外壳", ".app-shell", ".app-shell"],
  ["侧边栏", ".sidebar", ".console-nav"],
  ["品牌区", ".sidebar-brand", ".brand-lockup"],
  ["导航项", ".sidebar-item", ".primary-nav a"],
  ["顶栏", ".top-bar", ".command-bar"],
  ["搜索框", ".top-bar-search", ".command-trigger"],
  ["页面主标题", ".page-header-text h1", ".page-header h1"],
  ["主按钮", ".btn-primary", ".button-primary"]
];

function safeName(value) {
  return String(value || "").replace(/\s+/g, " ").trim().slice(0, 100);
}

async function sample(locator) {
  const count = await locator.count();
  if (!count) return { count: 0, items: [] };
  const items = await locator.evaluateAll((elements) =>
    elements.slice(0, 12).map((element) => {
      const style = getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      return {
        tag: element.tagName.toLowerCase(),
        text: (element.textContent || "").replace(/\s+/g, " ").trim().slice(0, 100),
        x: Number(rect.x.toFixed(2)),
        y: Number(rect.y.toFixed(2)),
        width: Number(rect.width.toFixed(2)),
        height: Number(rect.height.toFixed(2)),
        display: style.display,
        fontSize: style.fontSize,
        fontWeight: style.fontWeight,
        lineHeight: style.lineHeight,
        color: style.color,
        backgroundColor: style.backgroundColor,
        borderColor: style.borderColor,
        borderWidth: style.borderWidth,
        borderRadius: style.borderRadius,
        padding: style.padding,
        margin: style.margin,
        gap: style.gap,
        boxShadow: style.boxShadow,
        gridTemplateColumns: style.gridTemplateColumns,
        whiteSpace: style.whiteSpace,
        textAlign: style.textAlign
      };
    })
  );
  return { count, items };
}

async function buttons(root) {
  return root.locator("button, a.btn, a.button, a.quick-create").evaluateAll((elements) =>
    elements
      .filter((element) => {
        const style = getComputedStyle(element);
        const rect = element.getBoundingClientRect();
        return style.visibility !== "hidden" && style.display !== "none" && rect.width > 0 && rect.height > 0;
      })
      .map((element) => {
        const style = getComputedStyle(element);
        const rect = element.getBoundingClientRect();
        return {
          text: (element.textContent || element.getAttribute("aria-label") || "").replace(/\s+/g, " ").trim(),
          width: Number(rect.width.toFixed(2)),
          height: Number(rect.height.toFixed(2)),
          fontSize: style.fontSize,
          fontWeight: style.fontWeight,
          lineHeight: style.lineHeight,
          padding: style.padding,
          borderRadius: style.borderRadius,
          color: style.color,
          backgroundColor: style.backgroundColor,
          borderColor: style.borderColor
        };
      })
  );
}

async function headings(root) {
  return root.locator("h1, h2, h3").evaluateAll((elements) =>
    elements.map((element) => {
      const style = getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      return {
        level: element.tagName.toLowerCase(),
        text: (element.textContent || "").replace(/\s+/g, " ").trim().slice(0, 120),
        x: Number(rect.x.toFixed(2)),
        y: Number(rect.y.toFixed(2)),
        width: Number(rect.width.toFixed(2)),
        height: Number(rect.height.toFixed(2)),
        fontSize: style.fontSize,
        fontWeight: style.fontWeight,
        lineHeight: style.lineHeight,
        margin: style.margin,
        color: style.color
      };
    })
  );
}

async function screenshotCurrent(page, name, height) {
  await page.setViewportSize({ width: 1320, height: Math.max(720, Math.ceil(height)) });
  await page.screenshot({
    path: path.join(artifactDir, `current-${name}.png`),
    fullPage: false,
    animations: "disabled"
  });
}

async function main() {
  fs.mkdirSync(artifactDir, { recursive: true });
  const browser = await chromium.launch(browserLaunchOptions());
  const referencePage = await browser.newPage({ viewport: { width: 1320, height: 1000 }, locale: "zh-CN" });
  const currentPage = await browser.newPage({ viewport: { width: 1320, height: 900 }, locale: "zh-CN" });
  const report = { generatedAt: new Date().toISOString(), baseURL, viewportWidth: 1320, pages: [] };

  try {
    await referencePage.goto(pathToFileURL(prototypePath).href, { waitUntil: "load" });
    await referencePage.addStyleTag({
      content: "*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important}"
    });
    for (const item of pages) {
      await referencePage.evaluate((screenId) => {
        document.querySelectorAll(".screen").forEach((screen) => {
          screen.classList.toggle("active", screen.id === `screen-${screenId}`);
        });
        const tabBar = document.querySelector(".spec-tabs");
        if (tabBar instanceof HTMLElement) {
          tabBar.style.visibility = "hidden";
          tabBar.style.pointerEvents = "none";
        }
        window.scrollTo(0, 0);
      }, item.name);
      const referenceRoot = referencePage.locator(`#screen-${item.name} .window-frame`);
      await referenceRoot.scrollIntoViewIfNeeded();
      const referenceBox = await referenceRoot.boundingBox();
      if (!referenceBox) throw new Error(`原型页面 ${item.name} 不可见`);
      await referenceRoot.screenshot({
        path: path.join(artifactDir, `reference-${item.name}.png`),
        animations: "disabled"
      });

      await referencePage.locator(`#screen-${item.name} .sidebar-brand-icon`).evaluateAll((elements, logoURL) => {
        for (const element of elements) {
          element.innerHTML = `<img alt="" src="${logoURL}" style="display:block;width:32px;height:32px">`;
          element.style.background = "transparent";
          element.style.width = "32px";
          element.style.height = "32px";
        }
      }, pathToFileURL(logoPath).href);
      await referenceRoot.screenshot({
        path: path.join(artifactDir, `target-${item.name}.png`),
        animations: "disabled"
      });

      await referencePage.evaluate(() => {
        const tabBar = document.querySelector(".spec-tabs");
        if (tabBar instanceof HTMLElement) {
          tabBar.style.visibility = "visible";
          tabBar.style.pointerEvents = "auto";
        }
      });

      const response = await currentPage.goto(`${baseURL}${item.route}`, { waitUntil: "domcontentloaded", timeout: 30_000 });
      if (!response?.ok()) throw new Error(`${item.route} HTTP ${response?.status()}`);
      await currentPage.locator("main").waitFor({ state: "visible", timeout: 15_000 });
      await currentPage.addStyleTag({
        content: "*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important}"
      });
      await currentPage.evaluate(() => {
        document.documentElement.dataset.theme = "light";
      });
      await currentPage.waitForTimeout(1_200);
      await screenshotCurrent(currentPage, item.name, referenceBox.height);
      await currentPage.evaluate(() => {
        document.documentElement.dataset.theme = "dark";
      });
      await screenshotCurrent(currentPage, `dark-${item.name}`, referenceBox.height);
      await currentPage.evaluate(() => {
        document.documentElement.dataset.theme = "light";
      });
      await currentPage.waitForTimeout(50);

      const referenceScreen = referencePage.locator(`#screen-${item.name}`);
      const currentRoot = currentPage.locator("body");
      const metrics = [];
      for (const [label, referenceSelector, currentSelector] of [...commonPairs, ...item.pairs]) {
        metrics.push({
          label,
          referenceSelector,
          currentSelector,
          reference: await sample(referenceScreen.locator(referenceSelector)),
          current: await sample(currentRoot.locator(currentSelector))
        });
      }

      report.pages.push({
        name: item.name,
        route: item.route,
        referenceFrame: {
          width: Number(referenceBox.width.toFixed(2)),
          height: Number(referenceBox.height.toFixed(2))
        },
        referenceButtons: await buttons(referenceScreen),
        currentButtons: await buttons(currentRoot),
        referenceHeadings: await headings(referenceScreen),
        currentHeadings: await headings(currentPage.locator("main")),
        metrics
      });
    }

    for (const name of designScreens) {
      await referencePage.evaluate((screenId) => {
        document.querySelectorAll(".screen").forEach((screen) => {
          screen.classList.toggle("active", screen.id === `screen-${screenId}`);
        });
        const tabBar = document.querySelector(".spec-tabs");
        if (tabBar instanceof HTMLElement) {
          tabBar.style.visibility = "hidden";
          tabBar.style.pointerEvents = "none";
        }
        window.scrollTo(0, 0);
      }, name);
      const referenceRoot = referencePage.locator(`#screen-${name} .window-frame`);
      await referenceRoot.scrollIntoViewIfNeeded();
      await referenceRoot.screenshot({
        path: path.join(artifactDir, `reference-${name}.png`),
        animations: "disabled"
      });
      await referencePage.locator(`#screen-${name} .sidebar-brand-icon`).evaluateAll((elements, logoURL) => {
        for (const element of elements) {
          element.innerHTML = `<img alt="" src="${logoURL}" style="display:block;width:32px;height:32px">`;
          element.style.background = "transparent";
          element.style.width = "32px";
          element.style.height = "32px";
        }
      }, pathToFileURL(logoPath).href);
      await referenceRoot.screenshot({
        path: path.join(artifactDir, `target-${name}.png`),
        animations: "disabled"
      });
      await referencePage.evaluate(() => {
        const tabBar = document.querySelector(".spec-tabs");
        if (tabBar instanceof HTMLElement) {
          tabBar.style.visibility = "visible";
          tabBar.style.pointerEvents = "auto";
        }
      });
    }

    await referencePage.evaluate(() => document.body.classList.add("dark"));
    for (const name of [...pages.map((item) => item.name), ...designScreens]) {
      await referencePage.evaluate((screenId) => {
        document.querySelectorAll(".screen").forEach((screen) => {
          screen.classList.toggle("active", screen.id === `screen-${screenId}`);
        });
        const tabBar = document.querySelector(".spec-tabs");
        if (tabBar instanceof HTMLElement) tabBar.style.visibility = "hidden";
        window.scrollTo(0, 0);
      }, name);
      const referenceRoot = referencePage.locator(`#screen-${name} .window-frame`);
      await referenceRoot.scrollIntoViewIfNeeded();
      await referencePage.locator(`#screen-${name} .sidebar-brand-icon`).evaluateAll((elements, logoURL) => {
        for (const element of elements) {
          element.innerHTML = `<img alt="" src="${logoURL}" style="display:block;width:32px;height:32px">`;
          element.style.background = "transparent";
          element.style.width = "32px";
          element.style.height = "32px";
        }
      }, pathToFileURL(logoPath).href);
      await referenceRoot.screenshot({
        path: path.join(artifactDir, `target-dark-${name}.png`),
        animations: "disabled"
      });
      await referencePage.evaluate(() => {
        const tabBar = document.querySelector(".spec-tabs");
        if (tabBar instanceof HTMLElement) tabBar.style.visibility = "visible";
      });
    }
    await referencePage.evaluate(() => document.body.classList.remove("dark"));

    fs.writeFileSync(path.join(artifactDir, "metrics.json"), `${JSON.stringify(report, null, 2)}\n`, "utf8");
    process.stdout.write(`${JSON.stringify({ status: "captured", pages: report.pages.length, artifactDir }, null, 2)}\n`);
  } finally {
    await browser.close();
  }
}

main().catch((error) => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
