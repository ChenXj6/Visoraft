from __future__ import annotations

import hashlib
import mimetypes
import time
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yt_dlp
from yt_dlp.utils import DownloadError

from .extractor import (
    assert_public_source,
    classify_error,
    clean_error,
    friendly_error,
    is_retryable,
)


class DownloadCancelled(RuntimeError):
    """Raised when the control plane cancelled an active download."""


@dataclass
class DownloadFailure(Exception):
    code: str
    message: str
    retryable: bool

    def __str__(self) -> str:
        return self.message


@dataclass(frozen=True)
class DownloadedFile:
    path: Path
    original_name: str
    content_type: str
    size_bytes: int
    checksum_sha256: str


@dataclass(frozen=True)
class DownloadTelemetry:
    progress: int
    phase: str
    downloaded_bytes: int = 0
    total_bytes: int = 0
    total_bytes_is_estimate: bool = False
    speed_bytes_per_second: float = 0
    eta_seconds: int | None = None
    fragment_index: int = 0
    fragment_count: int = 0

    def as_event_data(self) -> dict[str, Any]:
        return {
            "progress": self.progress,
            "phase": self.phase,
            "downloaded_bytes": self.downloaded_bytes,
            "total_bytes": self.total_bytes,
            "total_bytes_is_estimate": self.total_bytes_is_estimate,
            "speed_bytes_per_second": self.speed_bytes_per_second,
            "eta_seconds": self.eta_seconds,
            "fragment_index": self.fragment_index,
            "fragment_count": self.fragment_count,
        }


class MediaDownloader:
    def __init__(
        self,
        max_download_bytes: int,
        trusted_hosts: tuple[str, ...] = (),
        *,
        http_chunk_size_bytes: int = 10 * 1024 * 1024,
        concurrent_fragments: int = 4,
        stall_timeout_seconds: int = 180,
        overall_timeout_seconds: int = 7200,
    ) -> None:
        self._max_download_bytes = max_download_bytes
        self._trusted_hosts = trusted_hosts
        self._http_chunk_size_bytes = http_chunk_size_bytes
        self._concurrent_fragments = concurrent_fragments
        self._stall_timeout_seconds = stall_timeout_seconds
        self._overall_timeout_seconds = overall_timeout_seconds

    def download(
        self,
        source_url: str,
        destination: Path,
        on_progress: Callable[[DownloadTelemetry], None],
        should_cancel: Callable[[], bool],
        cookie_file: Path | None = None,
    ) -> DownloadedFile:
        assert_public_source(source_url, self._trusted_hosts)
        destination.mkdir(parents=True, exist_ok=True)
        started_at = time.monotonic()
        last_byte_change_at = started_at
        last_downloaded = 0
        active_stream = ""

        def progress_hook(status: dict[str, Any]) -> None:
            nonlocal active_stream, last_byte_change_at, last_downloaded
            if should_cancel():
                raise DownloadCancelled("download cancelled by user")
            now = time.monotonic()
            if now - started_at > self._overall_timeout_seconds:
                raise DownloadFailure(
                    code="download_timeout",
                    message="下载超过允许的最长执行时间，请检查网络后重试",
                    retryable=True,
                )
            downloaded = _as_int(status.get("downloaded_bytes"))
            exact_total = _as_int(status.get("total_bytes"))
            estimated_total = _as_int(status.get("total_bytes_estimate"))
            total = exact_total or estimated_total
            stream_key = "|".join(
                (
                    str(status.get("filename", "")),
                    str(
                        (status.get("info_dict") or {}).get("format_id", "")
                        if isinstance(status.get("info_dict"), dict)
                        else ""
                    ),
                )
            )
            if stream_key != active_stream:
                active_stream = stream_key
                last_downloaded = downloaded
                last_byte_change_at = now
            elif downloaded > last_downloaded:
                last_downloaded = downloaded
                last_byte_change_at = now
            elif (
                status.get("status") == "downloading"
                and now - last_byte_change_at > self._stall_timeout_seconds
            ):
                raise DownloadFailure(
                    code="download_stalled",
                    message="下载长时间没有收到新数据，已停止本次尝试以便安全重试",
                    retryable=True,
                )
            if downloaded > self._max_download_bytes:
                raise DownloadFailure(
                    code="download_too_large",
                    message=f"source exceeds the {self._max_download_bytes} byte download limit",
                    retryable=False,
                )
            on_progress(_telemetry_from_status(status, downloaded, total, exact_total > 0))

        options: dict[str, Any] = {
            "quiet": True,
            "no_warnings": True,
            "noplaylist": True,
            "socket_timeout": 30,
            "retries": 10,
            "fragment_retries": 10,
            "file_access_retries": 3,
            "continuedl": True,
            "concurrent_fragment_downloads": self._concurrent_fragments,
            "http_chunk_size": self._http_chunk_size_bytes,
            "cachedir": False,
            "restrictfilenames": True,
            "max_filesize": self._max_download_bytes,
            "format": "best[ext=mp4]/best[ext=webm]/best",
            "outtmpl": str(destination / "source.%(ext)s"),
            "progress_hooks": [progress_hook],
        }
        if cookie_file is not None:
            options["cookiefile"] = str(cookie_file)

        try:
            with yt_dlp.YoutubeDL(options) as downloader:
                info = downloader.extract_info(source_url, download=True)
                prepared_path = Path(downloader.prepare_filename(info))
        except DownloadCancelled:
            raise
        except DownloadFailure:
            raise
        except DownloadError as exc:
            raw_message = clean_error(str(exc))
            lowered = raw_message.lower()
            code = "download_failed"
            retryable = is_retryable(raw_message)
            if "larger than max-filesize" in lowered or "file is larger" in lowered:
                code = "download_too_large"
                retryable = False
            elif "no space left" in lowered:
                code = "worker_storage_full"
                retryable = True
            else:
                classified = classify_error(raw_message)
                if classified != "metadata_extraction_failed":
                    code = classified
            raise DownloadFailure(
                code=code,
                message=friendly_error(code, raw_message),
                retryable=retryable,
            ) from exc
        except (OSError, TimeoutError) as exc:
            raise DownloadFailure(
                code="download_io_error",
                message=clean_error(str(exc)),
                retryable=True,
            ) from exc

        if should_cancel():
            raise DownloadCancelled("download cancelled by user")

        path = _resolve_downloaded_path(destination, prepared_path)
        size_bytes = path.stat().st_size
        if size_bytes > self._max_download_bytes:
            raise DownloadFailure(
                code="download_too_large",
                message=f"downloaded file exceeds the {self._max_download_bytes} byte limit",
                retryable=False,
            )
        if size_bytes <= 0:
            raise DownloadFailure(
                code="download_empty",
                message="yt-dlp produced an empty media file",
                retryable=False,
            )

        content_type = mimetypes.guess_type(path.name)[0] or "application/octet-stream"
        return DownloadedFile(
            path=path,
            original_name=path.name,
            content_type=content_type,
            size_bytes=size_bytes,
            checksum_sha256=_sha256(path),
        )


def _resolve_downloaded_path(destination: Path, prepared_path: Path) -> Path:
    if prepared_path.is_file() and prepared_path.resolve().is_relative_to(destination.resolve()):
        return prepared_path

    candidates = sorted(
        (
            item
            for item in destination.iterdir()
            if item.is_file() and item.suffix not in {".part", ".ytdl"}
        ),
        key=lambda item: item.stat().st_mtime_ns,
        reverse=True,
    )
    if not candidates:
        raise DownloadFailure(
            code="download_output_missing",
            message="yt-dlp completed without a media output file",
            retryable=True,
        )
    return candidates[0]


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _as_int(value: Any) -> int:
    return int(value) if isinstance(value, (int, float)) else 0


def _as_float(value: Any) -> float:
    return float(value) if isinstance(value, (int, float)) and value > 0 else 0


def _telemetry_from_status(
    status: dict[str, Any],
    downloaded: int | None = None,
    total: int | None = None,
    exact_total: bool | None = None,
) -> DownloadTelemetry:
    downloaded = _as_int(status.get("downloaded_bytes")) if downloaded is None else downloaded
    exact = _as_int(status.get("total_bytes"))
    estimate = _as_int(status.get("total_bytes_estimate"))
    total = (exact or estimate) if total is None else total
    exact_total = exact > 0 if exact_total is None else exact_total
    phase = "finalizing" if status.get("status") == "finished" else "downloading"
    progress = 82 if phase == "finalizing" else 2
    if phase == "downloading" and total > 0:
        # An estimated total can be lower than the final file. Never present an
        # in-flight estimate as 100%; the UI shows byte telemetry and marks the
        # total as estimated until yt-dlp finishes.
        ratio = min(max(downloaded / total, 0.0), 0.99)
        progress = 2 + int(ratio * 76)
    eta = status.get("eta")
    return DownloadTelemetry(
        progress=max(1, min(progress, 82)),
        phase=phase,
        downloaded_bytes=max(downloaded, 0),
        total_bytes=max(total, 0),
        total_bytes_is_estimate=total > 0 and not exact_total,
        speed_bytes_per_second=_as_float(status.get("speed")),
        eta_seconds=_as_int(eta) if isinstance(eta, (int, float)) and eta >= 0 else None,
        fragment_index=_as_int(status.get("fragment_index")),
        fragment_count=_as_int(status.get("fragment_count")),
    )
