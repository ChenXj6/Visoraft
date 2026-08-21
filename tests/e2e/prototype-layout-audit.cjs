const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
const { chromium } = require("playwright");

const baseURL = process.env.VISORAFT_BASE_URL || "http://127.0.0.1:4173";
const artifactDir = path.resolve(__dirname, "../../artifacts/v1/test-runs/prototype-layout-audit");
const prototypePath = path.resolve(__dirname, "../../../visoraft-ui-design.html");

async function gridColumns(locator) {
  return locator.evaluate((element) =>
    getComputedStyle(element).gridTemplateColumns.split(" ").filter(Boolean).length
  );
}

async function inspectMain(page) {
  return page.locator("main").evaluate((element) => {
    const rect = element.getBoundingClientRect();
    return {
      left: Math.round(rect.left),
      right: Math.round(rect.right),
      width: Math.round(rect.width),
      maxWidth: getComputedStyle(element).maxWidth
    };
  });
}

async function main() {
  fs.mkdirSync(artifactDir, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  const report = [];
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, locale: "zh-CN" });
    const cases = [
      {
        name: "dashboard",
        route: "/",
        check: async () => {
          assert.equal(await page.locator(".dashboard-stats > a").count(), 4);
          assert.equal(await page.locator(".pipeline-overview > .pipeline-stage").count(), 5);
          assert.equal(await gridColumns(page.locator(".dashboard-workbench")), 2);
          const workbench = await page.locator(".dashboard-workbench").evaluate((element) => {
            const [left, right] = [...element.children].map((child) => child.getBoundingClientRect().width);
            return { left, right, total: element.getBoundingClientRect().width };
          });
          assert.ok(workbench.left > workbench.total * 0.6, `首页优先队列没有铺满剩余宽度：${JSON.stringify(workbench)}`);
        }
      },
      {
        name: "tasks",
        route: "/tasks",
        check: async () => {
          assert.equal(await page.locator(".prototype-task-table-head > *").count(), 6);
          const firstCheckbox = page.locator(".prototype-task-row input[type=checkbox]").first();
          await firstCheckbox.check();
          assert.equal(await firstCheckbox.isChecked(), true, "任务复选框无法选中");
          assert.ok(await page.locator(".prototype-task-row").count() > 0);
          const firstHeight = await page.locator(".prototype-task-row").first().evaluate((element) => element.getBoundingClientRect().height);
          assert.ok(firstHeight <= 96, `任务表格行过高：${firstHeight}`);
        }
      },
      {
        name: "newtask",
        route: "/tasks/new",
        check: async () => {
          assert.equal(await page.locator(".new-task-steps > li").count(), 4);
          assert.equal(await gridColumns(page.locator(".new-task-workbench")), 2);
          assert.ok(await page.locator(".new-task-main > .wizard-step-panel").count() >= 3);
        }
      },
      {
        name: "settings-home",
        route: "/settings",
        check: async () => {
          assert.equal(await page.locator(".settings-category-grid > button").count(), 10);
          assert.equal(await gridColumns(page.locator(".settings-category-grid")), 2);
        }
      },
      {
        name: "settings-detail",
        route: "/settings?section=subtitles",
        check: async () => {
          assert.equal(await gridColumns(page.locator(".settings-detail.settings-workbench")), 2);
          const navWidth = await page.locator(".settings-sub-nav").evaluate((element) => element.getBoundingClientRect().width);
          assert.ok(navWidth >= 190 && navWidth <= 210, `设置二级导航宽度异常：${navWidth}`);
          assert.ok(await page.locator("details.settings-collapse").count() >= 4);
          assert.equal(await page.locator(".settings-save-bar").count(), 1);
        }
      },
      {
        name: "cookies",
        route: "/cookies",
        check: async () => {
          assert.equal(await gridColumns(page.locator(".prototype-cookie-layout")), 2);
          assert.equal(await page.locator(".prototype-cookie-layout > section").count(), 2);
        }
      },
      {
        name: "monitors",
        route: "/monitors",
        check: async () => {
          assert.ok(await gridColumns(page.locator(".prototype-monitor-grid")) >= 1);
          assert.ok(await page.locator(".prototype-monitor-card").count() > 0);
          assert.equal(await page.locator(".prototype-monitor-card").first().locator(".prototype-monitor-metrics > div").count(), 3);
        }
      },
      {
        name: "review",
        route: "/reviews",
        check: async () => {
          assert.equal(await gridColumns(page.locator(".prototype-review-workbench")), 2);
          assert.ok(await page.locator(".prototype-review-queue button").count() > 0);
          assert.equal(await page.locator(".prototype-review-focus").count(), 1);
          const queueHeight = await page.locator(".prototype-review-queue").evaluate((element) => element.getBoundingClientRect().height);
          assert.ok(queueHeight > 500, `审核左栏仍被限制高度：${queueHeight}`);
          assert.ok(await page.locator(".prototype-review-media video").count() <= 1);
          assert.equal(await page.locator(".prototype-review-tabs button").count(), 4);
          await page.locator(".prototype-review-tabs button", { hasText: "封面与媒体" }).click();
          assert.equal(await page.locator(".prototype-review-tabs button[aria-selected=true]").innerText(), "封面与媒体");
          await page.locator(".prototype-review-tabs button", { hasText: "简介与标签" }).click();
          assert.equal(await page.locator(".prototype-review-meta select").isEnabled(), true);
        }
      },
      {
        name: "files",
        route: "/files",
        check: async () => {
          assert.equal(await gridColumns(page.locator(".prototype-file-browser")), 2);
          assert.equal(await page.locator(".prototype-file-search input").isVisible(), true);
          assert.ok(await page.locator(".prototype-folder-tree > button").count() > 0);
          assert.ok(await page.locator(".prototype-file-tile").count() > 0);
          const previewHeight = await page.locator(".prototype-file-preview").first().evaluate((element) => element.getBoundingClientRect().height);
          assert.ok(previewHeight >= 136, `文件预览高度仍过小：${previewHeight}`);
          const folderTop = await page.locator(".prototype-folder-tree").evaluate((element) => Math.round(element.getBoundingClientRect().top));
          const collections = page.locator(".local-collections > button");
          if (await collections.count() > 1) {
            await collections.last().click();
            const nextFolderTop = await page.locator(".prototype-folder-tree").evaluate((element) => Math.round(element.getBoundingClientRect().top));
            assert.equal(nextFolderTop, folderTop, "切换独立任务后文件夹面板发生纵向位移");
          }
        }
      }
    ];

    for (const item of cases) {
      const response = await page.goto(`${baseURL}${item.route}`, { waitUntil: "networkidle", timeout: 30_000 });
      assert.ok(response?.ok(), `${item.name} 返回 HTTP ${response?.status()}`);
      await page.locator("main > *").first().waitFor();
      await item.check();
      assert.equal(await page.locator(".global-health").count(), 1, `${item.name} 缺少固定服务状态`);
      assert.equal(await page.locator(".theme-toggle-button").count(), 1, `${item.name} 缺少固定主题切换`);
      assert.equal(await page.locator(".topbar-icon-button[aria-label='查看通知与运行概览']").count(), 1, `${item.name} 缺少固定通知入口`);
      assert.equal(await page.locator(".quick-create").count(), 1, `${item.name} 缺少固定新建任务入口`);
      const mainBox = await inspectMain(page);
      assert.equal(mainBox.right, 1440, `${item.name} 主内容没有铺到视口右侧`);
      assert.ok(mainBox.maxWidth === "none" || Number.parseFloat(mainBox.maxWidth) >= mainBox.width, `${item.name} 仍有居中限宽`);
      await page.screenshot({ path: path.join(artifactDir, `current-${item.name}.png`), fullPage: true });
      report.push({ name: item.name, route: item.route, main: mainBox });
    }

    if (fs.existsSync(prototypePath)) {
      await page.goto(pathToFileURL(prototypePath).href, { waitUntil: "load" });
      for (const name of ["dashboard", "tasks", "newtask", "settings-home", "settings-detail", "cookies", "monitors", "review", "files"]) {
        await page.locator(`.spec-tab[data-screen="${name}"]`).click();
        await page.locator(`#screen-${name}.active .window-frame`).screenshot({
          path: path.join(artifactDir, `reference-${name}.png`)
        });
      }
    }

    const summary = {
      status: "passed",
      viewport: { width: 1440, height: 900 },
      pageCount: cases.length,
      referenceScreenshots: fs.existsSync(prototypePath) ? 9 : 0,
      persistedDataChanged: false
    };
    fs.writeFileSync(path.join(artifactDir, "report.json"), `${JSON.stringify({ summary, report }, null, 2)}\n`, "utf8");
    process.stdout.write(`${JSON.stringify(summary, null, 2)}\n`);
  } finally {
    await browser.close();
  }
}

main().catch((error) => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
