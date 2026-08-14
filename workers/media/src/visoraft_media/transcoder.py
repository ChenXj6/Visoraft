from __future__ import annotations

import hashlib
import os
import queue
import re
import signal
import subprocess
import threading
import time
import uuid
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .media_probe import (
    FFprobeInspector,
    MediaProbeCancelled,
    MediaProbeFailure,
    MediaProbeResult,
)
from .storage import ObjectStorage, StorageFailure


class TranscodeFailure(Exception):
    def __init__(self, code: str, message: str, retryable: bool) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.retryable = retryable


class TranscodeCancelled(RuntimeError):
    """Raised when a running FFmpeg command is cancelled by the control plane."""


@dataclass(frozen=True)
class FFmpegCapabilities:
    encoders: frozenset[str]
    filters: frozenset[str]


@dataclass(frozen=True)
class TranscodePlan:
    command: tuple[str, ...]
    resolved_video_encoder: str
    resolved_audio_encoder: str
    container: str
    command_summary: dict[str, Any]


class Transcoder:
    def __init__(
        self,
        storage: ObjectStorage,
        inspector: FFprobeInspector,
        binary: str = "ffmpeg",
    ) -> None:
        self.storage = storage
        self.inspector = inspector
        self.binary = binary

    def process(
        self,
        task_id: str,
        config: dict[str, Any],
        working_dir: Path,
        should_cancel: Callable[[], bool],
        on_progress: Callable[[int], None] | None = None,
    ) -> dict[str, Any]:
        transcode = _object(config, "transcode")
        runtime = _object(config, "runtime")
        source_asset = _validated_asset(runtime.get("source_asset"), task_id, "source")
        if not bool(transcode.get("enabled")):
            raise TranscodeFailure(
                "transcode_disabled",
                "任务快照未启用视频转码",
                False,
            )
        if should_cancel():
            raise TranscodeCancelled("视频转码已取消")

        working_dir.mkdir(parents=True, exist_ok=True)
        source_suffix = _safe_suffix(
            str(source_asset.get("original_name") or ""),
            ".bin",
        )
        local_source = working_dir / f"source{source_suffix}"
        try:
            self.storage.download_file(
                str(source_asset["object_key"]),
                local_source,
                str(source_asset["bucket"]),
            )
        except StorageFailure as exc:
            raise TranscodeFailure(
                "transcode_source_download_failed",
                str(exc),
                True,
            ) from exc
        _verify_asset_file(local_source, source_asset, "转码源媒体")

        local_subtitle: Path | None = None
        if bool(transcode.get("burn_subtitles")):
            subtitle_asset = _validated_asset(
                runtime.get("subtitle_asset"),
                task_id,
                "subtitle",
            )
            local_subtitle = working_dir / "burn-subtitles.vtt"
            try:
                self.storage.download_file(
                    str(subtitle_asset["object_key"]),
                    local_subtitle,
                    str(subtitle_asset["bucket"]),
                )
            except StorageFailure as exc:
                raise TranscodeFailure(
                    "transcode_subtitle_download_failed",
                    str(exc),
                    True,
                ) from exc
            _verify_asset_file(local_subtitle, subtitle_asset, "烧录字幕")

        try:
            source_info = self.inspector.inspect(local_source, should_cancel)
        except MediaProbeCancelled as exc:
            raise TranscodeCancelled(str(exc)) from exc
        except MediaProbeFailure as exc:
            raise TranscodeFailure(exc.code, exc.message, exc.retryable) from exc

        capabilities = probe_ffmpeg_capabilities(self.binary)
        container = _normalized_container(transcode.get("container"))
        output_path = working_dir / f"transcoded.{container}"
        plan = build_transcode_plan(
            self.binary,
            transcode,
            capabilities,
            source_info,
            local_source,
            output_path,
            local_subtitle,
        )
        self._run(
            plan.command,
            source_info.duration_seconds,
            should_cancel,
            on_progress,
            working_dir,
        )
        if not output_path.is_file() or output_path.stat().st_size <= 0:
            raise TranscodeFailure(
                "transcode_output_missing",
                "FFmpeg 未生成有效的转码文件",
                True,
            )
        try:
            output_info = self.inspector.inspect(output_path, should_cancel)
        except MediaProbeCancelled as exc:
            raise TranscodeCancelled(str(exc)) from exc
        except MediaProbeFailure as exc:
            raise TranscodeFailure(exc.code, exc.message, exc.retryable) from exc

        checksum = _sha256_file(output_path)
        object_key = f"tasks/{task_id}/transcoded/{output_path.name}"
        content_type = "video/mp4" if container == "mp4" else "video/x-matroska"
        size_bytes = output_path.stat().st_size
        try:
            self.storage.upload_file(
                output_path,
                object_key,
                content_type,
                {
                    "task-id": task_id,
                    "sha256": checksum,
                    "kind": "transcoded",
                    "video-encoder": plan.resolved_video_encoder,
                },
                (
                    None
                    if on_progress is None
                    else lambda transferred: on_progress(
                        92 + min(int(transferred / max(size_bytes, 1) * 7), 7)
                    )
                ),
            )
        except StorageFailure as exc:
            raise TranscodeFailure(
                "transcode_output_upload_failed",
                str(exc),
                True,
            ) from exc
        if should_cancel():
            try:
                self.storage.delete_object(object_key)
            except StorageFailure:
                pass
            raise TranscodeCancelled("视频转码已取消")
        if on_progress is not None:
            on_progress(99)
        return {
            "task_id": task_id,
            "asset_id": str(uuid.uuid4()),
            "input_asset_id": str(source_asset["id"]),
            "kind": "transcoded",
            "bucket": self.storage.bucket,
            "object_key": object_key,
            "original_name": output_path.name,
            "content_type": content_type,
            "size_bytes": size_bytes,
            "checksum_sha256": checksum,
            "media_info": {
                "schema_version": 1,
                **output_info.as_dict(),
            },
            "resolved_encoder": plan.resolved_video_encoder,
            "resolved_audio_encoder": plan.resolved_audio_encoder,
            "command_summary": plan.command_summary,
        }

    def _run(
        self,
        command: tuple[str, ...],
        duration_seconds: float | None,
        should_cancel: Callable[[], bool],
        on_progress: Callable[[int], None] | None,
        working_dir: Path,
    ) -> None:
        stderr_path = working_dir / "ffmpeg.stderr.log"
        try:
            stderr_file = stderr_path.open("wb")
        except OSError as exc:
            raise TranscodeFailure(
                "transcode_log_unavailable",
                "无法创建 FFmpeg 日志文件",
                True,
            ) from exc
        try:
            try:
                process = subprocess.Popen(
                    command,
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.PIPE,
                    stderr=stderr_file,
                    start_new_session=True,
                )
            except FileNotFoundError as exc:
                raise TranscodeFailure(
                    "transcode_tool_unavailable",
                    "FFmpeg 不可用，媒体 Worker 镜像构建不完整",
                    False,
                ) from exc
            except OSError as exc:
                raise TranscodeFailure(
                    "transcode_start_failed",
                    _clean_message(str(exc), "无法启动 FFmpeg"),
                    True,
                ) from exc

            output_queue: queue.Queue[bytes | None] = queue.Queue()

            def read_progress() -> None:
                assert process.stdout is not None
                try:
                    for line in iter(process.stdout.readline, b""):
                        output_queue.put(line)
                finally:
                    output_queue.put(None)

            reader = threading.Thread(
                target=read_progress,
                name="ffmpeg-progress-reader",
                daemon=True,
            )
            reader.start()
            timeout = _transcode_timeout(duration_seconds)
            deadline = time.monotonic() + timeout
            last_progress = 0
            stream_ended = False
            while process.poll() is None or not stream_ended:
                if should_cancel():
                    _kill_process(process)
                    process.wait()
                    raise TranscodeCancelled("视频转码已取消")
                if time.monotonic() >= deadline:
                    _kill_process(process)
                    process.wait()
                    raise TranscodeFailure(
                        "transcode_timeout",
                        f"FFmpeg 在 {timeout} 秒内未完成",
                        True,
                    )
                try:
                    raw_line = output_queue.get(timeout=0.25)
                except queue.Empty:
                    continue
                if raw_line is None:
                    stream_ended = True
                    continue
                line = raw_line.decode("utf-8", errors="replace").strip()
                if not line.startswith("out_time_"):
                    continue
                seconds = _progress_seconds(line)
                if (
                    duration_seconds is None
                    or duration_seconds <= 0
                    or seconds is None
                ):
                    continue
                value = min(90, max(1, int(seconds / duration_seconds * 90)))
                if value > last_progress:
                    last_progress = value
                    if on_progress is not None:
                        on_progress(value)
            reader.join(timeout=2)
            return_code = process.wait()
            if return_code != 0:
                message = _read_error_log(stderr_path)
                raise TranscodeFailure(
                    "transcode_failed",
                    _clean_message(message, f"FFmpeg 退出码 {return_code}"),
                    _ffmpeg_failure_retryable(message),
                )
        finally:
            stderr_file.close()


def probe_ffmpeg_capabilities(binary: str = "ffmpeg") -> FFmpegCapabilities:
    encoders_output = _run_capability_command(
        [binary, "-hide_banner", "-encoders"],
        "transcode_encoder_probe_failed",
    )
    filters_output = _run_capability_command(
        [binary, "-hide_banner", "-filters"],
        "transcode_filter_probe_failed",
    )
    encoders: set[str] = set()
    for line in encoders_output.splitlines():
        match = re.match(r"^\s*[VAS]\S{5}\s+([A-Za-z0-9_]+)\s", line)
        if match:
            encoders.add(match.group(1))
    filters = _parse_filter_names(filters_output)
    return FFmpegCapabilities(
        encoders=frozenset(encoders),
        filters=frozenset(filters),
    )


def _parse_filter_names(output: str) -> set[str]:
    filters: set[str] = set()
    for line in output.splitlines():
        # FFmpeg 8 emits two filter capability columns ("TS", "T.", ".."),
        # while older builds may include a third command-support column.
        match = re.match(r"^\s*[TSC.]{2,3}\s+([A-Za-z0-9_]+)\s", line)
        if match:
            filters.add(match.group(1))
    return filters


def build_transcode_plan(
    binary: str,
    config: dict[str, Any],
    capabilities: FFmpegCapabilities,
    source_info: MediaProbeResult,
    input_path: Path,
    output_path: Path,
    subtitle_path: Path | None,
) -> TranscodePlan:
    encoder_mode = _normalized_choice(
        config.get("encoder_mode"),
        {"auto", "cpu", "nvidia", "intel", "amd"},
        "auto",
        "transcode_encoder_mode_invalid",
    )
    video_codec = _normalized_choice(
        config.get("video_codec"),
        {"h264", "hevc", "copy"},
        "h264",
        "transcode_video_codec_invalid",
    )
    audio_codec = _normalized_choice(
        config.get("audio_codec"),
        {"aac", "copy"},
        "aac",
        "transcode_audio_codec_invalid",
    )
    container = _normalized_container(config.get("container"))
    burn_subtitles = bool(config.get("burn_subtitles"))
    maximum_height = _bounded_int(config.get("maximum_height"), 0, 0, 4320)
    video_bitrate = _bounded_int(
        config.get("video_bitrate_kbps"),
        0,
        0,
        200_000,
    )
    audio_bitrate = _bounded_int(
        config.get("audio_bitrate_kbps"),
        192,
        32,
        1024,
    )
    needs_scale = bool(
        maximum_height
        and source_info.height
        and source_info.height > maximum_height
    )
    has_video = bool(source_info.video_codec)
    has_audio = bool(source_info.audio_codec)
    synthesize_video = not has_video and has_audio
    if not has_video and not has_audio:
        raise TranscodeFailure(
            "transcode_streams_missing",
            "媒体中没有可用于转码的音视频流",
            False,
        )
    if synthesize_video and video_codec == "copy":
        raise TranscodeFailure(
            "transcode_audio_only_copy_unsupported",
            "纯音频媒体需要生成视频画布，不能直接复制视频流",
            False,
        )
    if synthesize_video and "color" not in capabilities.filters:
        raise TranscodeFailure(
            "transcode_color_filter_unavailable",
            "当前 LGPL FFmpeg 构建没有纯音频转视频所需的 color 滤镜",
            False,
        )
    if video_codec == "copy" and (needs_scale or burn_subtitles):
        raise TranscodeFailure(
            "transcode_copy_filter_conflict",
            "直接复制视频流时不能缩放或烧录字幕",
            False,
        )
    if burn_subtitles and subtitle_path is None:
        raise TranscodeFailure(
            "transcode_subtitle_missing",
            "已启用字幕烧录，但任务没有可用 VTT 字幕",
            False,
        )
    if burn_subtitles and "subtitles" not in capabilities.filters:
        raise TranscodeFailure(
            "transcode_subtitle_filter_unavailable",
            "当前 LGPL FFmpeg 构建没有 subtitles/libass 滤镜",
            False,
        )

    resolved_video = _resolve_video_encoder(
        video_codec,
        encoder_mode,
        capabilities.encoders,
    )
    resolved_audio = audio_codec
    if audio_codec != "copy" and audio_codec not in capabilities.encoders:
        raise TranscodeFailure(
            "transcode_audio_encoder_unavailable",
            f"当前 FFmpeg 构建没有 {audio_codec} 音频编码器",
            False,
        )

    filters: list[str] = []
    if needs_scale:
        filters.append(f"scale=-2:min(ih\\,{maximum_height})")
    if burn_subtitles:
        assert subtitle_path is not None
        filters.append("subtitles=filename=" + _escape_filter_path(subtitle_path))

    command: list[str] = [
        binary,
        "-hide_banner",
        "-nostdin",
        "-y",
        "-loglevel",
        "error",
        "-progress",
        "pipe:1",
        "-nostats",
    ]
    if synthesize_video:
        command.extend(
            [
                "-f",
                "lavfi",
                "-i",
                "color=c=0x0B1020:s=1280x720:r=25",
                "-i",
                str(input_path),
                "-map",
                "0:v:0",
                "-map",
                "1:a:0",
                "-map_metadata",
                "1",
                "-shortest",
            ]
        )
    else:
        command.extend(
            [
                "-i",
                str(input_path),
                "-map",
                "0:v:0",
                "-map",
                "0:a?",
                "-map_metadata",
                "0",
            ]
        )
    command.extend(["-c:v", resolved_video])
    if resolved_video != "copy":
        command.extend(["-pix_fmt", "yuv420p"])
        if video_bitrate:
            command.extend(["-b:v", f"{video_bitrate}k"])
    if filters:
        command.extend(["-vf", ",".join(filters)])
    command.extend(["-c:a", resolved_audio])
    if resolved_audio != "copy":
        command.extend(["-b:a", f"{audio_bitrate}k"])
    if container == "mp4":
        command.extend(["-movflags", "+faststart"])
    command.extend(_safe_custom_arguments(config))
    command.append(str(output_path))

    return TranscodePlan(
        command=tuple(command),
        resolved_video_encoder=resolved_video,
        resolved_audio_encoder=resolved_audio,
        container=container,
        command_summary={
            "video_codec": video_codec,
            "video_encoder": resolved_video,
            "audio_encoder": resolved_audio,
            "container": container,
            "scaled": needs_scale,
            "synthesized_video": synthesize_video,
            "synthesized_canvas": (
                {"width": 1280, "height": 720, "frame_rate": 25}
                if synthesize_video
                else None
            ),
            "maximum_height": maximum_height,
            "burn_subtitles": burn_subtitles,
            "video_bitrate_kbps": video_bitrate,
            "audio_bitrate_kbps": audio_bitrate,
            "custom_argument_count": len(_safe_custom_arguments(config)),
        },
    )


def _resolve_video_encoder(
    codec: str,
    mode: str,
    available: frozenset[str],
) -> str:
    if codec == "copy":
        return "copy"
    candidates: dict[tuple[str, str], tuple[str, ...]] = {
        ("h264", "auto"): (
            "h264_nvenc",
            "h264_qsv",
            "h264_amf",
            "h264_vaapi",
            "libopenh264",
        ),
        ("h264", "cpu"): ("libopenh264",),
        ("h264", "nvidia"): ("h264_nvenc",),
        ("h264", "intel"): ("h264_qsv",),
        ("h264", "amd"): ("h264_amf", "h264_vaapi"),
        ("hevc", "auto"): (
            "hevc_nvenc",
            "hevc_qsv",
            "hevc_amf",
            "hevc_vaapi",
        ),
        ("hevc", "cpu"): (),
        ("hevc", "nvidia"): ("hevc_nvenc",),
        ("hevc", "intel"): ("hevc_qsv",),
        ("hevc", "amd"): ("hevc_amf", "hevc_vaapi"),
    }
    for candidate in candidates.get((codec, mode), ()):
        if candidate in available:
            return candidate
    if codec == "hevc" and mode == "cpu":
        message = "严格非 GPL 镜像未配置 CPU HEVC 编码器"
    else:
        message = f"当前 FFmpeg 构建没有可用于 {mode}/{codec} 的编码器"
    raise TranscodeFailure(
        "transcode_video_encoder_unavailable",
        message,
        False,
    )


def _safe_custom_arguments(config: dict[str, Any]) -> list[str]:
    if not bool(config.get("custom_arguments_enabled")):
        return []
    raw = config.get("custom_arguments")
    if not isinstance(raw, list) or len(raw) > 32:
        raise TranscodeFailure(
            "transcode_custom_arguments_invalid",
            "FFmpeg 自定义参数结构无效",
            False,
        )
    allowed = {
        "-af",
        "-bufsize",
        "-g",
        "-keyint_min",
        "-level",
        "-maxrate",
        "-movflags",
        "-pix_fmt",
        "-profile:v",
        "-r",
        "-vf",
    }
    result: list[str] = []
    expect_value = False
    for raw_argument in raw:
        argument = str(raw_argument)
        if (
            not argument
            or len(argument) > 256
            or any(character in argument for character in ("\r", "\n", "\x00"))
        ):
            raise TranscodeFailure(
                "transcode_custom_arguments_invalid",
                "FFmpeg 自定义参数包含非法内容",
                False,
            )
        if expect_value:
            if "://" in argument:
                raise TranscodeFailure(
                    "transcode_custom_arguments_invalid",
                    "FFmpeg 自定义参数不能引用网络地址",
                    False,
                )
            expect_value = False
        elif argument not in allowed:
            raise TranscodeFailure(
                "transcode_custom_arguments_invalid",
                f"不允许的 FFmpeg 参数：{argument}",
                False,
            )
        else:
            expect_value = True
        result.append(argument)
    if expect_value:
        raise TranscodeFailure(
            "transcode_custom_arguments_invalid",
            "FFmpeg 自定义参数缺少取值",
            False,
        )
    return result


def _validated_asset(value: Any, task_id: str, role: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise TranscodeFailure(
            f"transcode_{role}_asset_missing",
            f"任务没有可用的{role}资产",
            True,
        )
    asset = dict(value)
    asset_id = str(asset.get("id") or "")
    bucket = str(asset.get("bucket") or "").strip()
    object_key = str(asset.get("object_key") or "").strip()
    checksum = str(asset.get("checksum_sha256") or "").lower()
    try:
        uuid.UUID(asset_id)
    except (ValueError, AttributeError) as exc:
        raise TranscodeFailure(
            f"transcode_{role}_asset_invalid",
            f"{role}资产 ID 无效",
            False,
        ) from exc
    if (
        not bucket
        or not object_key.startswith(f"tasks/{task_id}/")
        or object_key.startswith("/")
        or ".." in object_key.split("/")
        or not re.fullmatch(r"[0-9a-f]{64}", checksum)
    ):
        raise TranscodeFailure(
            f"transcode_{role}_asset_invalid",
            f"{role}资产位置或校验值无效",
            False,
        )
    try:
        size = int(asset.get("size_bytes"))
    except (TypeError, ValueError) as exc:
        raise TranscodeFailure(
            f"transcode_{role}_asset_invalid",
            f"{role}资产大小无效",
            False,
        ) from exc
    if size <= 0:
        raise TranscodeFailure(
            f"transcode_{role}_asset_invalid",
            f"{role}资产大小无效",
            False,
        )
    asset["id"] = asset_id
    asset["bucket"] = bucket
    asset["object_key"] = object_key
    asset["checksum_sha256"] = checksum
    asset["size_bytes"] = size
    return asset


def _verify_asset_file(path: Path, asset: dict[str, Any], label: str) -> None:
    try:
        size = path.stat().st_size
    except OSError as exc:
        raise TranscodeFailure(
            "transcode_asset_unavailable",
            f"{label}无法读取",
            True,
        ) from exc
    if size != int(asset["size_bytes"]):
        raise TranscodeFailure(
            "transcode_asset_size_mismatch",
            f"{label}大小与数据库记录不一致",
            False,
        )
    if _sha256_file(path) != str(asset["checksum_sha256"]):
        raise TranscodeFailure(
            "transcode_asset_checksum_mismatch",
            f"{label} SHA-256 校验失败",
            False,
        )


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as source:
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as exc:
        raise TranscodeFailure(
            "transcode_asset_hash_failed",
            "无法计算媒体 SHA-256",
            True,
        ) from exc
    return digest.hexdigest()


def _run_capability_command(command: list[str], code: str) -> str:
    try:
        completed = subprocess.run(
            command,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=15,
            check=False,
        )
    except FileNotFoundError as exc:
        raise TranscodeFailure(
            "transcode_tool_unavailable",
            "FFmpeg 不可用，媒体 Worker 镜像构建不完整",
            False,
        ) from exc
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise TranscodeFailure(code, "无法探测 FFmpeg 编码能力", True) from exc
    output = completed.stdout.decode("utf-8", errors="replace")
    if completed.returncode != 0 or len(output) > 4 * 1024 * 1024:
        raise TranscodeFailure(code, "FFmpeg 编码能力探测失败", False)
    return output


def _object(value: dict[str, Any], key: str) -> dict[str, Any]:
    result = value.get(key)
    return result if isinstance(result, dict) else {}


def _normalized_choice(
    value: Any,
    allowed: set[str],
    fallback: str,
    code: str,
) -> str:
    result = str(value or fallback).strip().lower()
    if result not in allowed:
        raise TranscodeFailure(code, f"不支持的转码选项：{result}", False)
    return result


def _normalized_container(value: Any) -> str:
    return _normalized_choice(
        value,
        {"mp4", "mkv"},
        "mp4",
        "transcode_container_invalid",
    )


def _bounded_int(value: Any, fallback: int, minimum: int, maximum: int) -> int:
    try:
        result = int(value)
    except (TypeError, ValueError):
        result = fallback
    if result < minimum or result > maximum:
        raise TranscodeFailure(
            "transcode_numeric_option_invalid",
            "转码数值参数超出安全范围",
            False,
        )
    return result


def _safe_suffix(filename: str, fallback: str) -> str:
    suffix = Path(filename).suffix.lower()
    if not re.fullmatch(r"\.[a-z0-9]{1,10}", suffix):
        return fallback
    return suffix


def _escape_filter_path(path: Path) -> str:
    value = str(path.resolve())
    return (
        value.replace("\\", "\\\\")
        .replace(":", "\\:")
        .replace("'", "\\'")
        .replace(",", "\\,")
    )


def _progress_seconds(line: str) -> float | None:
    key, separator, raw_value = line.partition("=")
    if not separator:
        return None
    try:
        if key == "out_time_us":
            return max(float(raw_value) / 1_000_000, 0)
        if key == "out_time_ms":
            # FFmpeg historically labels this field "ms" while emitting
            # microseconds. Keep the documented progress behavior.
            return max(float(raw_value) / 1_000_000, 0)
        if key == "out_time":
            hours, minutes, seconds = raw_value.split(":", 2)
            return int(hours) * 3600 + int(minutes) * 60 + float(seconds)
    except (ValueError, TypeError):
        return None
    return None


def _transcode_timeout(duration_seconds: float | None) -> int:
    if duration_seconds is None or duration_seconds <= 0:
        return 6 * 60 * 60
    return max(15 * 60, min(12 * 60 * 60, int(duration_seconds * 10) + 300))


def _kill_process(process: subprocess.Popen[bytes]) -> None:
    try:
        if os.name == "posix":
            os.killpg(process.pid, signal.SIGKILL)
        else:
            process.kill()
    except ProcessLookupError:
        return


def _read_error_log(path: Path) -> str:
    try:
        data = path.read_bytes()
    except OSError:
        return ""
    if len(data) > 64 * 1024:
        data = data[-64 * 1024 :]
    return data.decode("utf-8", errors="replace")


def _clean_message(value: str, fallback: str) -> str:
    text = value.replace("\x00", "").strip()
    return text[-2_000:] or fallback


def _ffmpeg_failure_retryable(message: str) -> bool:
    normalized = message.lower()
    permanent_markers = (
        "unknown encoder",
        "no such filter",
        "invalid argument",
        "error parsing",
        "unsupported codec",
    )
    return not any(marker in normalized for marker in permanent_markers)
