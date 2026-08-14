from __future__ import annotations

import csv
import math
import os
import re
import signal
import subprocess
import time
from dataclasses import asdict, dataclass
from difflib import SequenceMatcher
from io import StringIO
from pathlib import Path
from typing import Any, Callable

from .media_probe import FFprobeInspector, MediaProbeCancelled, MediaProbeFailure
_HAN = re.compile(r"[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff]")
_SPACE_OR_PUNCTUATION = re.compile(r"[\s\W_]+", re.UNICODE)
_VTT_TIMESTAMP = re.compile(
    r"(?P<start>(?:\d{2}:)?\d{2}:\d{2}[.,]\d{3})\s+-->\s+"
    r"(?P<end>(?:\d{2}:)?\d{2}:\d{2}[.,]\d{3})"
)
_HTML_TAG = re.compile(r"<[^>]+>")
_CHINESE_LANGUAGE_CODES = {
    "zh",
    "zho",
    "chi",
    "cmn",
    "yue",
    "chs",
    "cht",
}


@dataclass(frozen=True)
class ExistingSubtitleDetection:
    schema_version: int
    state: str
    source: str
    language: str
    disposition: str
    reason: str
    confidence_percent: int
    sample_count: int
    hit_count: int
    stable_pair_count: int
    distinct_text_count: int
    evidence: tuple[str, ...]

    def as_dict(self) -> dict[str, Any]:
        return asdict(self)


@dataclass(frozen=True)
class ExistingSubtitleResult:
    detection: ExistingSubtitleDetection
    segments: tuple[dict[str, Any], ...] = ()


@dataclass(frozen=True)
class OCRSample:
    text: str
    confidence_percent: int


class ExistingChineseSubtitleDetector:
    def __init__(
        self,
        inspector: FFprobeInspector | None = None,
        ffmpeg_binary: str = "ffmpeg",
        tesseract_binary: str = "tesseract",
    ) -> None:
        self._inspector = inspector or FFprobeInspector()
        self._ffmpeg_binary = ffmpeg_binary
        self._tesseract_binary = tesseract_binary

    def inspect_local_media(
        self,
        media_path: Path,
        config: dict[str, Any],
        working_dir: Path,
        should_cancel: Callable[[], bool],
        report: Callable[..., None] | None = None,
    ) -> ExistingSubtitleResult:
        try:
            media_info = self._inspector.inspect(media_path, should_cancel)
        except MediaProbeCancelled as exc:
            raise RuntimeError("字幕识别已取消") from exc
        except MediaProbeFailure as exc:
            return ExistingSubtitleResult(
                _detection("error", reason=exc.message)
            )

        if bool(config.get("inspect_embedded_subtitles", True)):
            embedded = self._inspect_embedded(
                media_path, media_info.subtitle_streams, working_dir, should_cancel
            )
            if embedded is not None:
                return embedded

        if not bool(config.get("inspect_hardcoded_subtitles", True)):
            return ExistingSubtitleResult(
                _detection("not_found", reason="未启用画面中文字幕识别")
            )
        if media_info.duration_seconds is None or not media_info.video_codec:
            return ExistingSubtitleResult(
                _detection("not_found", reason="媒体没有可抽帧的视频流")
            )
        if report is not None:
            report(15, "existing_subtitle_ocr_started")
        return self._inspect_hardcoded(
            media_path,
            media_info.duration_seconds,
            config,
            working_dir,
            should_cancel,
            report,
        )

    def _inspect_embedded(
        self,
        media_path: Path,
        streams: tuple[Any, ...],
        working_dir: Path,
        should_cancel: Callable[[], bool],
    ) -> ExistingSubtitleResult | None:
        subtitle_streams = list(streams)
        subtitle_streams.sort(
            key=lambda stream: (
                not (
                    is_chinese_language(stream.language)
                    or contains_chinese(stream.title, minimum_characters=2)
                ),
                not stream.default,
                stream.index,
            )
        )
        for stream in subtitle_streams:
            target = working_dir / f"embedded-subtitle-{stream.index}.vtt"
            command = [
                self._ffmpeg_binary,
                "-nostdin",
                "-hide_banner",
                "-loglevel",
                "error",
                "-i",
                str(media_path),
                "-map",
                f"0:{stream.index}",
                "-c:s",
                "webvtt",
                "-y",
                str(target),
            ]
            try:
                completed = _run_controlled(command, should_cancel, timeout_seconds=120)
            except (OSError, subprocess.TimeoutExpired):
                continue
            if completed.returncode != 0 or not target.exists():
                continue
            segments = _parse_vtt(target.read_text(encoding="utf-8", errors="replace"))
            if not segments or not segments_contain_chinese(segments):
                continue
            return ExistingSubtitleResult(
                ExistingSubtitleDetection(
                    schema_version=1,
                    state="found",
                    source="embedded",
                    language=stream.language or "zh",
                    disposition="reuse_soft_subtitles",
                    reason="媒体包含可复用的内嵌中文字幕轨道",
                    confidence_percent=100,
                    sample_count=0,
                    hit_count=len(segments),
                    stable_pair_count=0,
                    distinct_text_count=len({item["text"] for item in segments}),
                    evidence=(
                        f"字幕流 {stream.index}",
                        f"编码 {stream.codec or '未知'}",
                        f"语言 {stream.language or '根据字幕文本识别'}",
                    ),
                ),
                tuple(segments),
            )
        return None

    def _inspect_hardcoded(
        self,
        media_path: Path,
        duration_seconds: float,
        config: dict[str, Any],
        working_dir: Path,
        should_cancel: Callable[[], bool],
        report: Callable[..., None] | None,
    ) -> ExistingSubtitleResult:
        sample_count = max(8, min(int(config.get("sample_count") or 32), 120))
        pair_count = max(4, sample_count // 2)
        timestamps = _paired_timestamps(duration_seconds, pair_count)
        samples: list[OCRSample] = []
        errors: list[str] = []
        for index, timestamp in enumerate(timestamps):
            if should_cancel():
                raise RuntimeError("字幕识别已取消")
            try:
                samples.append(
                    self._ocr_frame(media_path, timestamp, index, working_dir, should_cancel)
                )
            except (OSError, subprocess.TimeoutExpired, ValueError) as exc:
                errors.append(str(exc)[:160])
                samples.append(OCRSample("", 0))
            if report is not None:
                report(
                    15 + round(15 * (index + 1) / len(timestamps)),
                    "existing_subtitle_ocr_sampling",
                    sample_count=index + 1,
                    total_count=len(timestamps),
                )

        if errors and len(errors) == len(samples):
            return ExistingSubtitleResult(
                _detection(
                    "error",
                    reason="画面字幕识别工具不可用或连续执行失败",
                    sample_count=len(samples),
                    evidence=tuple(errors[:3]),
                )
            )
        detection = analyze_ocr_samples(samples, config)
        return ExistingSubtitleResult(detection)

    def _ocr_frame(
        self,
        media_path: Path,
        timestamp: float,
        index: int,
        working_dir: Path,
        should_cancel: Callable[[], bool],
    ) -> OCRSample:
        frame = working_dir / f"subtitle-sample-{index:03d}.bmp"
        ffmpeg = [
            self._ffmpeg_binary,
            "-nostdin",
            "-hide_banner",
            "-loglevel",
            "error",
            "-ss",
            f"{timestamp:.3f}",
            "-i",
            str(media_path),
            "-frames:v",
            "1",
            "-vf",
            "crop=iw:ih*0.36:0:ih*0.57,scale='max(1920,iw)':-2",
            "-c:v",
            "bmp",
            "-y",
            str(frame),
        ]
        completed = _run_controlled(ffmpeg, should_cancel, timeout_seconds=45)
        if completed.returncode != 0 or not frame.exists():
            detail = completed.stderr.decode("utf-8", errors="replace")[-400:]
            raise ValueError(detail or "FFmpeg 抽帧失败")

        candidates = [
            _run_tesseract(
                self._tesseract_binary, frame, should_cancel, timeout_seconds=30
            )
        ]

        # Low-resolution Chinese TV sources often use yellow subtitles with a
        # dark outline. This fallback suppresses the busy picture while the
        # unmodified frame remains the primary path for white subtitles.
        yellow_mask = working_dir / f"subtitle-sample-{index:03d}-yellow.bmp"
        mask_command = [
            self._ffmpeg_binary,
            "-nostdin",
            "-hide_banner",
            "-loglevel",
            "error",
            "-i",
            str(frame),
            "-vf",
            "format=rgba,colorkey=0xffff00:0.30:0.08,alphaextract",
            "-frames:v",
            "1",
            "-c:v",
            "bmp",
            "-y",
            str(yellow_mask),
        ]
        masked = _run_controlled(mask_command, should_cancel, timeout_seconds=30)
        if masked.returncode == 0 and yellow_mask.exists():
            candidates.append(
                _run_tesseract(
                    self._tesseract_binary,
                    yellow_mask,
                    should_cancel,
                    timeout_seconds=30,
                )
            )
        return _select_best_ocr_sample(candidates)


def analyze_ocr_samples(
    samples: list[OCRSample], config: dict[str, Any]
) -> ExistingSubtitleDetection:
    confidence_threshold = max(
        50, min(int(config.get("confidence_threshold_percent") or 85), 99)
    )
    coverage_threshold = max(
        20, min(int(config.get("coverage_threshold_percent") or 60), 100)
    )
    minimum_distinct = max(
        2, min(int(config.get("minimum_distinct_texts") or 3), 30)
    )
    hits = [
        sample
        for sample in samples
        if sample.confidence_percent >= confidence_threshold
        and contains_chinese(sample.text, minimum_characters=4)
    ]
    stable_pairs: list[tuple[OCRSample, OCRSample]] = []
    active_pairs = 0
    for index in range(0, len(samples) - 1, 2):
        left, right = samples[index], samples[index + 1]
        if left in hits or right in hits:
            active_pairs += 1
        if left in hits and right in hits and _texts_are_stable(left.text, right.text):
            stable_pairs.append((left, right))
    pair_total = max(1, len(samples) // 2)
    coverage = round(100 * len(stable_pairs) / max(1, active_pairs))
    minimum_stable_pairs = max(3, math.ceil(pair_total * 0.20))
    distinct = {
        _normalized_text(sample.text)
        for pair in stable_pairs
        for sample in pair
        if _normalized_text(sample.text)
    }
    hit_distinct = {
        _normalized_text(sample.text)
        for sample in hits
        if _normalized_text(sample.text)
    }
    distributed_hit_threshold = max(6, math.ceil(len(samples) * 0.25))
    distributed_evidence = (
        len(stable_pairs) >= 1
        and len(hits) >= distributed_hit_threshold
        and len(hit_distinct) >= max(5, minimum_distinct)
    )
    average_confidence = (
        round(sum(sample.confidence_percent for sample in hits) / len(hits))
        if hits
        else 0
    )
    evidence = tuple(
        dict.fromkeys(sample.text.strip()[:80] for sample in hits if sample.text.strip())
    )[:5]
    stable_evidence = (
        len(stable_pairs) >= minimum_stable_pairs
        and coverage >= coverage_threshold
        and len(distinct) >= minimum_distinct
    )
    if stable_evidence or distributed_evidence:
        state = "found"
        disposition = "keep_hardcoded_subtitles"
        reason = (
            "连续抽帧确认画面中已有稳定中文字幕"
            if stable_evidence
            else "多个时间窗口检测到高置信度中文字幕"
        )
    elif len(hits) < max(2, pair_total // 5):
        state = "not_found"
        disposition = "continue_pipeline"
        reason = "有效中文文字样本不足，继续原字幕流水线"
    else:
        state = "uncertain"
        disposition = "continue_pipeline"
        reason = "检测结果未达到自动跳过阈值，继续原字幕流水线"
    return ExistingSubtitleDetection(
        schema_version=1,
        state=state,
        source="hardcoded",
        language="zh" if hits else "",
        disposition=disposition,
        reason=reason,
        confidence_percent=average_confidence,
        sample_count=len(samples),
        hit_count=len(hits),
        stable_pair_count=len(stable_pairs),
        distinct_text_count=len(hit_distinct),
        evidence=evidence,
    )


def parse_tesseract_tsv(value: str) -> OCRSample:
    words: list[str] = []
    weighted_confidence = 0.0
    weight = 0
    for row in csv.DictReader(StringIO(value), delimiter="\t"):
        text = str(row.get("text") or "").strip()
        if not text:
            continue
        try:
            confidence = float(row.get("conf") or -1)
        except (TypeError, ValueError):
            continue
        if confidence < 0:
            continue
        words.append(text)
        length = max(1, len(text))
        weighted_confidence += confidence * length
        weight += length
    return OCRSample("".join(words), round(weighted_confidence / weight) if weight else 0)


def _run_tesseract(
    binary: str,
    frame: Path,
    should_cancel: Callable[[], bool],
    timeout_seconds: int,
) -> OCRSample:
    command = [
        binary,
        str(frame),
        "stdout",
        "-l",
        "chi_sim+chi_tra",
        "--psm",
        "6",
        "tsv",
    ]
    completed = _run_controlled(command, should_cancel, timeout_seconds)
    if completed.returncode != 0:
        detail = completed.stderr.decode("utf-8", errors="replace")[-400:]
        raise ValueError(detail or "Tesseract 识别失败")
    return parse_tesseract_tsv(completed.stdout.decode("utf-8", errors="replace"))


def _calibrate_ocr_sample(sample: OCRSample) -> OCRSample:
    han_count = len(_HAN.findall(sample.text))
    if han_count < 4:
        return sample
    visible = [character for character in sample.text if not character.isspace()]
    chinese_ratio = han_count / max(1, len(visible))
    confidence = min(
        99,
        sample.confidence_percent
        + round(30 * chinese_ratio)
        + min(15, han_count * 2),
    )
    return OCRSample(sample.text, confidence)


def _select_best_ocr_sample(samples: list[OCRSample]) -> OCRSample:
    calibrated = [_calibrate_ocr_sample(sample) for sample in samples]

    def score(sample: OCRSample) -> tuple[int, float, int]:
        han_count = len(_HAN.findall(sample.text))
        visible_count = len(
            [character for character in sample.text if not character.isspace()]
        )
        chinese_ratio = han_count / max(1, visible_count)
        return sample.confidence_percent, chinese_ratio, han_count

    return max(calibrated, key=score)


def is_chinese_language(value: str) -> bool:
    normalized = str(value or "").strip().lower().replace("_", "-")
    return normalized.split("-", 1)[0] in _CHINESE_LANGUAGE_CODES


def contains_chinese(value: str, minimum_characters: int = 4) -> bool:
    return len(_HAN.findall(str(value or ""))) >= minimum_characters


def segments_contain_chinese(segments: list[dict[str, Any]]) -> bool:
    text = "".join(str(segment.get("text") or "") for segment in segments[:40])
    visible = [character for character in text if not character.isspace()]
    han = _HAN.findall(text)
    return len(han) >= 8 and len(han) / max(1, len(visible)) >= 0.35


def _paired_timestamps(duration_seconds: float, pair_count: int) -> list[float]:
    start = max(0.0, duration_seconds * 0.05)
    end = max(start, duration_seconds * 0.95)
    interval = (end - start) / max(1, pair_count)
    timestamps: list[float] = []
    for index in range(pair_count):
        base = min(end, start + interval * (index + 0.5))
        timestamps.extend((base, min(end, base + min(0.8, max(0.25, interval / 4)))))
    return timestamps


def _texts_are_stable(left: str, right: str) -> bool:
    first = _normalized_text(left)
    second = _normalized_text(right)
    if not first or not second:
        return False
    if first in second or second in first:
        return min(len(first), len(second)) >= 4
    return SequenceMatcher(None, first, second).ratio() >= 0.55


def _normalized_text(value: str) -> str:
    return _SPACE_OR_PUNCTUATION.sub("", str(value or "")).lower()


def _detection(
    state: str,
    reason: str,
    sample_count: int = 0,
    evidence: tuple[str, ...] = (),
) -> ExistingSubtitleDetection:
    return ExistingSubtitleDetection(
        schema_version=1,
        state=state,
        source="",
        language="",
        disposition="continue_pipeline",
        reason=reason,
        confidence_percent=0,
        sample_count=sample_count,
        hit_count=0,
        stable_pair_count=0,
        distinct_text_count=0,
        evidence=evidence,
    )


def _run_controlled(
    command: list[str],
    should_cancel: Callable[[], bool],
    timeout_seconds: int,
) -> subprocess.CompletedProcess[bytes]:
    process = subprocess.Popen(
        command,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        start_new_session=True,
    )
    deadline = time.monotonic() + timeout_seconds
    while True:
        if should_cancel():
            _kill_process(process)
            process.communicate()
            raise RuntimeError("字幕识别已取消")
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            _kill_process(process)
            process.communicate()
            raise subprocess.TimeoutExpired(command, timeout_seconds)
        try:
            stdout, stderr = process.communicate(timeout=min(remaining, 0.25))
            return subprocess.CompletedProcess(
                command, process.returncode, stdout=stdout, stderr=stderr
            )
        except subprocess.TimeoutExpired:
            continue


def _kill_process(process: subprocess.Popen[bytes]) -> None:
    try:
        if os.name == "posix":
            os.killpg(process.pid, signal.SIGKILL)
        else:
            process.kill()
    except ProcessLookupError:
        return


def _parse_vtt(content: str) -> list[dict[str, Any]]:
    lines = content.replace("\r\n", "\n").replace("\r", "\n").split("\n")
    result: list[dict[str, Any]] = []
    index = 0
    while index < len(lines):
        match = _VTT_TIMESTAMP.search(lines[index])
        if not match:
            index += 1
            continue
        start = _timestamp(match.group("start"))
        end = _timestamp(match.group("end"))
        index += 1
        text_lines: list[str] = []
        while index < len(lines) and lines[index].strip():
            line = _HTML_TAG.sub("", lines[index]).strip()
            if line and not line.startswith("NOTE"):
                text_lines.append(line)
            index += 1
        text = " ".join(text_lines).strip()
        if text and end > start:
            result.append(
                {
                    "index": len(result) + 1,
                    "start": start,
                    "end": end,
                    "text": text,
                }
            )
    return result


def _timestamp(value: str) -> float:
    parts = value.replace(",", ".").split(":")
    if len(parts) == 2:
        hours = "0"
        minutes, seconds = parts
    else:
        hours, minutes, seconds = parts
    return int(hours) * 3600 + int(minutes) * 60 + float(seconds)
