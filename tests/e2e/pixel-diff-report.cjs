const fs = require("node:fs");
const path = require("node:path");
const { chromium } = require("playwright");

const root = path.resolve(__dirname, "../..");
const artifactDir = path.join(root, "artifacts/v1/test-runs/pixel-baseline-audit");
const pages = ["dashboard", "tasks", "newtask", "settings-home", "settings-detail", "cookies", "monitors", "review", "files"];
const windowsChrome = "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe";

function dataURL(file) {
  return `data:image/png;base64,${fs.readFileSync(file).toString("base64")}`;
}

function writeDataURL(file, value) {
  fs.writeFileSync(file, Buffer.from(value.replace(/^data:image\/png;base64,/, ""), "base64"));
}

async function compare(page, referenceFile, currentFile, fill) {
  return page.evaluate(async ({ reference, current, fillColor }) => {
    const load = (src) => new Promise((resolve, reject) => {
      const image = new Image();
      image.onload = () => resolve(image);
      image.onerror = reject;
      image.src = src;
    });
    const [referenceImage, currentImage] = await Promise.all([load(reference), load(current)]);
    const width = Math.max(referenceImage.width, currentImage.width);
    const height = Math.max(referenceImage.height, currentImage.height);
    const makeCanvas = () => Object.assign(document.createElement("canvas"), { width, height });
    const normalize = (image) => {
      const canvas = makeCanvas();
      const context = canvas.getContext("2d");
      context.fillStyle = fillColor;
      context.fillRect(0, 0, width, height);
      context.drawImage(image, 0, 0);
      return canvas;
    };
    const referenceCanvas = normalize(referenceImage);
    const currentCanvas = normalize(currentImage);
    const referencePixels = referenceCanvas.getContext("2d").getImageData(0, 0, width, height);
    const currentPixels = currentCanvas.getContext("2d").getImageData(0, 0, width, height);
    const overlayCanvas = normalize(currentImage);
    const overlayContext = overlayCanvas.getContext("2d");
    const overlayPixels = overlayContext.getImageData(0, 0, width, height);
    let changed = 0;
    for (let index = 0; index < referencePixels.data.length; index += 4) {
      const red = Math.abs(referencePixels.data[index] - currentPixels.data[index]);
      const green = Math.abs(referencePixels.data[index + 1] - currentPixels.data[index + 1]);
      const blue = Math.abs(referencePixels.data[index + 2] - currentPixels.data[index + 2]);
      const luminance = Math.round(red * 0.299 + green * 0.587 + blue * 0.114);
      if (luminance <= 12) continue;
      changed += 1;
      overlayPixels.data[index] = Math.round(currentPixels.data[index] * 0.57 + 255 * 0.43);
      overlayPixels.data[index + 1] = Math.round(currentPixels.data[index + 1] * 0.57 + 45 * 0.43);
      overlayPixels.data[index + 2] = Math.round(currentPixels.data[index + 2] * 0.57 + 85 * 0.43);
    }
    overlayContext.putImageData(overlayPixels, 0, 0);
    return {
      referenceWidth: referenceImage.width,
      referenceHeight: referenceImage.height,
      currentWidth: currentImage.width,
      currentHeight: currentImage.height,
      changedPixels: changed,
      totalPixels: width * height,
      changedRatio: Number((changed / (width * height)).toFixed(6)),
      overlay: overlayCanvas.toDataURL("image/png"),
    };
  }, { reference: dataURL(referenceFile), current: dataURL(currentFile), fillColor: fill });
}

(async () => {
  const executablePath = process.env.VISORAFT_BROWSER_PATH || (fs.existsSync(windowsChrome) ? windowsChrome : undefined);
  const browser = await chromium.launch(executablePath ? { headless: true, executablePath } : { headless: true });
  const page = await browser.newPage();
  const summary = {};
  try {
    for (const name of pages) {
      const light = await compare(page, path.join(artifactDir, `reference-${name}.png`), path.join(artifactDir, `current-${name}.png`), "#f0f2f5");
      writeDataURL(path.join(artifactDir, `diff-${name}.png`), light.overlay);
      delete light.overlay;
      const dark = await compare(page, path.join(artifactDir, `target-dark-${name}.png`), path.join(artifactDir, `current-dark-${name}.png`), "#0f1216");
      writeDataURL(path.join(artifactDir, `diff-dark-${name}.png`), dark.overlay);
      delete dark.overlay;
      summary[name] = {
        ...light,
        darkChangedPixels: dark.changedPixels,
        darkTotalPixels: dark.totalPixels,
        darkChangedRatio: dark.changedRatio,
      };
    }
  } finally {
    await browser.close();
  }
  fs.writeFileSync(path.join(artifactDir, "pixel-summary.json"), `${JSON.stringify(summary, null, 2)}\n`);
  process.stdout.write(`${JSON.stringify(summary, null, 2)}\n`);
})().catch((error) => {
  console.error(error);
  process.exit(1);
});
