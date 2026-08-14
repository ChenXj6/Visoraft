from __future__ import annotations

import json
import math
import os
import signal
import subprocess
import time
from collections.abc import Callable
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any


class MediaProbeFailure(Exception):
    def __init__(self, code: str, message: str, retryable: bool) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.retryable = retryable


class MediaProbeCancelled(RuntimeError):
    """Raised when a running probe is cancelled by the control plane."""


@dataclass(frozen=True)
class SubtitleStreamInfo:
    index: int
    codec: str
    language: str
    title: str
    default: bool
    forced: bool


@dataclass(frozen=True)
class MediaProbeResult:
    format_name: str
    duration_seconds: float | None
    size_bytes: int | None
    bit_rate: int | None
    video_codec: str
    width: int | None
    height: int | None
    pixel_format: str
    frame_rate: str
    audio_codec: str
    sample_rate: int | None
    channels: int | None
    channel_layout: str
    stream_count: int
    subtitle_streams: tuple[SubtitleStreamInfo, ...] = ()

    def as_dict(self) -> dict[str, Any]:
        return asdict(self)


class FFprobeInspector:
    def __init__(self, binary: str = "ffprobe", timeout_seconds: int = 30) -> None:
        self._binary = binary
        self._timeout_seconds = timeout_seconds

    def inspect(
        self,
        media_path: Path,
        should_cancel: Callable[[], bool] | None = None,
    ) -> MediaProbeResult:
        path = media_path.resolve()
        if not path.is_file():
            raise MediaProbeFailure(
                "media_probe_input_missing",
                "待探测的媒体文件不存在",
                False,
            )

        command = [
            self._binary,
            "-v",
            "error",
            "-max_alloc",
            "268435456",
            "-probesize",
            "10000000",
            "-analyzeduration",
            "10000000",
            "-max_streams",
            "64",
            "-show_format",
            "-show_streams",
            "-show_entries",
            (
                "format=format_name,duration,size,bit_rate:"
                "stream=index,codec_type,codec_name,width,height,pix_fmt,"
                "avg_frame_rate,r_frame_rate,sample_rate,channels,channel_layout:"
                "stream_tags=language,title:stream_disposition=default,forced"
            ),
            "-of",
            "json",
            str(path),
        ]
        try:
            process = subprocess.Popen(
                command,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                start_new_session=True,
            )
        except FileNotFoundError as exc:
            raise MediaProbeFailure(
                "media_tool_unavailable",
                "ffprobe 不可用，媒体 Worker 镜像构建不完整",
                False,
            ) from exc
        except OSError as exc:
            raise MediaProbeFailure(
                "media_probe_start_failed",
                _clean_message(str(exc), "无法启动 ffprobe"),
                True,
            ) from exc

        deadline = time.monotonic() + self._timeout_seconds
        while True:
            if should_cancel is not None and should_cancel():
                _kill_process(process)
                process.communicate()
                raise MediaProbeCancelled("媒体检测已由用户取消")
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                _kill_process(process)
                process.communicate()
                raise MediaProbeFailure(
                    "media_probe_timeout",
                    f"ffprobe 在 {self._timeout_seconds} 秒内未完成",
                    True,
                )
            try:
                stdout, stderr = process.communicate(timeout=min(remaining, 0.25))
                break
            except subprocess.TimeoutExpired:
                continue

        if process.returncode != 0:
            raise MediaProbeFailure(
                "media_probe_failed",
                _clean_message(stderr.decode("utf-8", errors="replace"), "ffprobe 无法识别媒体"),
                False,
            )
        if len(stdout) > 512 * 1024:
            raise MediaProbeFailure(
                "media_probe_output_too_large",
                "ffprobe 输出超过 512 KiB 安全限制",
                False,
            )
        try:
            value = json.loads(stdout)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise MediaProbeFailure(
                "media_probe_invalid_output",
                "ffprobe 返回了无效 JSON",
                False,
            ) from exc
        if not isinstance(value, dict):
            raise MediaProbeFailure(
                "media_probe_invalid_output",
                "ffprobe 返回结构无效",
                False,
            )
        return _normalize_probe(value)


def _normalize_probe(value: dict[str, Any]) -> MediaProbeResult:
    raw_streams = value.get("streams")
    streams = [stream for stream in raw_streams or [] if isinstance(stream, dict)]
    if not streams:
        raise MediaProbeFailure(
            "media_streams_missing",
            "媒体文件中没有可识别的音视频流",
            False,
        )
    video = next((stream for stream in streams if stream.get("codec_type") == "video"), {})
    audio = next((stream for stream in streams if stream.get("codec_type") == "audio"), {})
    subtitle_streams = tuple(
        _subtitle_stream(stream)
        for stream in streams
        if stream.get("codec_type") == "subtitle"
    )
    raw_format = value.get("format")
    media_format = raw_format if isinstance(raw_format, dict) else {}
    return MediaProbeResult(
        format_name=_text(media_format.get("format_name"), 200),
        duration_seconds=_nonnegative_float(media_format.get("duration")),
        size_bytes=_nonnegative_int(media_format.get("size")),
        bit_rate=_nonnegative_int(media_format.get("bit_rate")),
        video_codec=_text(video.get("codec_name"), 100),
        width=_nonnegative_int(video.get("width")),
        height=_nonnegative_int(video.get("height")),
        pixel_format=_text(video.get("pix_fmt"), 100),
        frame_rate=_frame_rate(video),
        audio_codec=_text(audio.get("codec_name"), 100),
        sample_rate=_nonnegative_int(audio.get("sample_rate")),
        channels=_nonnegative_int(audio.get("channels")),
        channel_layout=_text(audio.get("channel_layout"), 100),
        stream_count=len(streams),
        subtitle_streams=subtitle_streams,
    )


def _subtitle_stream(stream: dict[str, Any]) -> SubtitleStreamInfo:
    raw_tags = stream.get("tags")
    tags = raw_tags if isinstance(raw_tags, dict) else {}
    raw_disposition = stream.get("disposition")
    disposition = raw_disposition if isinstance(raw_disposition, dict) else {}
    return SubtitleStreamInfo(
        index=_nonnegative_int(stream.get("index")) or 0,
        codec=_text(stream.get("codec_name"), 100),
        language=_text(tags.get("language"), 50),
        title=_text(tags.get("title"), 200),
        default=bool(disposition.get("default")),
        forced=bool(disposition.get("forced")),
    )


def _frame_rate(stream: dict[str, Any]) -> str:
    for field in ("avg_frame_rate", "r_frame_rate"):
        value = _text(stream.get(field), 50)
        if value and value != "0/0":
            return value
    return ""


def _text(value: Any, limit: int) -> str:
    if value is None:
        return ""
    return str(value).replace("\x00", "").strip()[:limit]


def _nonnegative_int(value: Any) -> int | None:
    try:
        parsed = int(value)
    except (TypeError, ValueError, OverflowError):
        return None
    return parsed if parsed >= 0 else None


def _nonnegative_float(value: Any) -> float | None:
    try:
        parsed = float(value)
    except (TypeError, ValueError, OverflowError):
        return None
    if not math.isfinite(parsed) or parsed < 0:
        return None
    return round(parsed, 6)


def _clean_message(value: str, fallback: str) -> str:
    text = value.replace("\x00", "").strip()
    return text[:1_000] or fallback


def _kill_process(process: subprocess.Popen[bytes]) -> None:
    try:
        if os.name == "posix":
            os.killpg(process.pid, signal.SIGKILL)
        else:
            process.kill()
    except ProcessLookupError:
        return
