from __future__ import annotations

import json
from pathlib import Path
from PIL import Image, ImageChops, ImageDraw, ImageFont


ROOT = Path(__file__).resolve().parents[2]
ARTIFACT = ROOT / "artifacts" / "v1" / "test-runs" / "pixel-baseline-audit"
PAGES = [
    "dashboard",
    "tasks",
    "newtask",
    "settings-home",
    "settings-detail",
    "cookies",
    "monitors",
    "review",
    "files",
]
DESIGN_SCREENS = ["modals", "states", "theme"]
TARGET_PAGES = PAGES + DESIGN_SCREENS


def canvas(image: Image.Image, size: tuple[int, int], fill: str = "#F0F2F5") -> Image.Image:
    result = Image.new("RGB", size, fill)
    result.paste(image.convert("RGB"), (0, 0))
    return result


def labeled(image: Image.Image, label: str) -> Image.Image:
    bar = 38
    result = Image.new("RGB", (image.width, image.height + bar), "#15182B")
    result.paste(image, (0, bar))
    draw = ImageDraw.Draw(result)
    draw.text((14, 11), label, fill="#FFFFFF", font=ImageFont.load_default())
    return result


def main() -> None:
    summary: dict[str, dict[str, float | int]] = {}
    sheets = []
    for name in PAGES:
        ref = Image.open(ARTIFACT / f"reference-{name}.png").convert("RGB")
        cur = Image.open(ARTIFACT / f"current-{name}.png").convert("RGB")
        width = max(ref.width, cur.width)
        height = max(ref.height, cur.height)
        ref_n = canvas(ref, (width, height))
        cur_n = canvas(cur, (width, height))
        diff = ImageChops.difference(ref_n, cur_n)
        mask = diff.convert("L").point(lambda value: 255 if value > 12 else 0)
        changed = sum(1 for value in mask.getdata() if value)
        total = width * height
        overlay = cur_n.copy()
        red = Image.new("RGB", (width, height), "#FF2D55")
        overlay.paste(red, mask=mask.point(lambda value: 110 if value else 0))
        overlay.save(ARTIFACT / f"diff-{name}.png")

        target = Image.open(ARTIFACT / f"target-{name}.png").convert("RGB")
        target_n = canvas(target, (width, height))
        triptych = Image.new("RGB", (width * 3, height + 38), "#15182B")
        for index, (image, label) in enumerate(((ref_n, "REFERENCE"), (cur_n, "CURRENT"), (overlay, "DIFF"))):
            panel = labeled(image, label)
            triptych.paste(panel, (width * index, 0))
        triptych.save(ARTIFACT / f"compare-{name}.png")

        summary[name] = {
            "referenceWidth": ref.width,
            "referenceHeight": ref.height,
            "currentWidth": cur.width,
            "currentHeight": cur.height,
            "changedPixels": changed,
            "totalPixels": total,
            "changedRatio": round(changed / total, 6),
        }

        dark_ref = Image.open(ARTIFACT / f"target-dark-{name}.png").convert("RGB")
        dark_cur = Image.open(ARTIFACT / f"current-dark-{name}.png").convert("RGB")
        dark_width = max(dark_ref.width, dark_cur.width)
        dark_height = max(dark_ref.height, dark_cur.height)
        dark_ref_n = canvas(dark_ref, (dark_width, dark_height), "#0F1216")
        dark_cur_n = canvas(dark_cur, (dark_width, dark_height), "#0F1216")
        dark_diff = ImageChops.difference(dark_ref_n, dark_cur_n)
        dark_mask = dark_diff.convert("L").point(lambda value: 255 if value > 12 else 0)
        dark_changed = sum(1 for value in dark_mask.getdata() if value)
        dark_total = dark_width * dark_height
        dark_overlay = dark_cur_n.copy()
        dark_red = Image.new("RGB", (dark_width, dark_height), "#FF2D55")
        dark_overlay.paste(dark_red, mask=dark_mask.point(lambda value: 110 if value else 0))
        dark_overlay.save(ARTIFACT / f"diff-dark-{name}.png")
        dark_triptych = Image.new("RGB", (dark_width * 3, dark_height + 38), "#15182B")
        for index, (image, label) in enumerate(((dark_ref_n, "TARGET DARK"), (dark_cur_n, "CURRENT DARK"), (dark_overlay, "DIFF"))):
            dark_triptych.paste(labeled(image, label), (dark_width * index, 0))
        dark_triptych.save(ARTIFACT / f"compare-dark-{name}.png")
        summary[name]["darkChangedPixels"] = dark_changed
        summary[name]["darkTotalPixels"] = dark_total
        summary[name]["darkChangedRatio"] = round(dark_changed / dark_total, 6)
    for name in TARGET_PAGES:
        target = Image.open(ARTIFACT / f"target-{name}.png").convert("RGB")
        target_w = 660
        target_h = round(target_w * target.height / target.width)
        sheets.append((name, target.resize((target_w, target_h))))

    card_w = 660
    gap = 18
    title_h = 38
    rows = []
    for index in range(0, len(sheets), 2):
        pair = sheets[index:index + 2]
        row_h = max(image.height for _, image in pair) + title_h
        row = Image.new("RGB", (card_w * 2 + gap, row_h), "#E9EDF3")
        for column, (name, image) in enumerate(pair):
            x = column * (card_w + gap)
            row.paste(labeled(image, f"TARGET · {name}"), (x, 0))
        rows.append(row)
    contact_h = sum(row.height for row in rows) + gap * (len(rows) - 1)
    contact = Image.new("RGB", (card_w * 2 + gap, contact_h), "#E9EDF3")
    y = 0
    for row in rows:
        contact.paste(row, (0, y))
        y += row.height + gap
    contact.save(ARTIFACT / "target-contact-sheet.png")

    dark_sheets = []
    for name in TARGET_PAGES:
        target = Image.open(ARTIFACT / f"target-dark-{name}.png").convert("RGB")
        target_w = 660
        target_h = round(target_w * target.height / target.width)
        dark_sheets.append((name, target.resize((target_w, target_h))))
    dark_rows = []
    for index in range(0, len(dark_sheets), 2):
        pair = dark_sheets[index:index + 2]
        row_h = max(image.height for _, image in pair) + title_h
        row = Image.new("RGB", (card_w * 2 + gap, row_h), "#0F1216")
        for column, (name, image) in enumerate(pair):
            x = column * (card_w + gap)
            row.paste(labeled(image, f"TARGET DARK · {name}"), (x, 0))
        dark_rows.append(row)
    dark_contact_h = sum(row.height for row in dark_rows) + gap * (len(dark_rows) - 1)
    dark_contact = Image.new("RGB", (card_w * 2 + gap, dark_contact_h), "#0F1216")
    y = 0
    for row in dark_rows:
        dark_contact.paste(row, (0, y))
        y += row.height + gap
    dark_contact.save(ARTIFACT / "target-dark-contact-sheet.png")
    (ARTIFACT / "pixel-summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(summary, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
