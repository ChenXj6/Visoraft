const fs = require("node:fs");
const path = require("node:path");

const artifactDir = path.resolve(
  __dirname,
  "../../artifacts/v1/test-runs/pixel-baseline-audit"
);
const metricsPath = path.join(artifactDir, "metrics.json");
const outputPath = path.join(artifactDir, "button-style-diff.json");
const report = JSON.parse(fs.readFileSync(metricsPath, "utf8"));
const styleKeys = [
  "width",
  "height",
  "fontSize",
  "fontWeight",
  "lineHeight",
  "padding",
  "borderRadius",
  "color",
  "backgroundColor",
  "borderColor"
];

function normalizeText(value) {
  return String(value || "").replace(/\s+/g, "").trim();
}

function comparableText(value) {
  return normalizeText(value).replace(/\d+/g, "#");
}

function differences(reference, current) {
  return Object.fromEntries(
    styleKeys
      .filter((key) => String(reference[key]) !== String(current[key]))
      .map((key) => [key, { reference: reference[key], current: current[key] }])
  );
}

const pages = report.pages.map((page) => {
  const used = new Set();
  const rows = [];
  for (const reference of page.referenceButtons) {
    const normalized = comparableText(reference.text);
    if (!normalized) continue;
    let index = page.currentButtons.findIndex(
      (button, candidateIndex) =>
        !used.has(candidateIndex) && comparableText(button.text) === normalized
    );
    if (index < 0) {
      index = page.currentButtons.findIndex(
        (button, candidateIndex) =>
          !used.has(candidateIndex) &&
          (comparableText(button.text).includes(normalized) ||
            normalized.includes(comparableText(button.text)))
      );
    }
    if (index < 0) {
      rows.push({ text: reference.text, status: "missing", reference });
      continue;
    }
    used.add(index);
    const current = page.currentButtons[index];
    const diff = differences(reference, current);
    rows.push({
      text: reference.text,
      status: Object.keys(diff).length ? "different" : "matched",
      differences: diff
    });
  }
  return {
    page: page.name,
    matched: rows.filter((row) => row.status === "matched").length,
    different: rows.filter((row) => row.status === "different").length,
    missing: rows.filter((row) => row.status === "missing").length,
    rows
  };
});

fs.writeFileSync(outputPath, `${JSON.stringify({ generatedAt: new Date().toISOString(), pages }, null, 2)}\n`);
process.stdout.write(
  `${JSON.stringify(
    Object.fromEntries(
      pages.map((page) => [page.page, { matched: page.matched, different: page.different, missing: page.missing }])
    ),
    null,
    2
  )}\n`
);
