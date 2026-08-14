from __future__ import annotations

import ipaddress
import socket
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

import yt_dlp
from yt_dlp.utils import DownloadError


class SourceRejected(ValueError):
    """The source URL is not allowed to reach the downloader."""


@dataclass
class ExtractionFailure(Exception):
    code: str
    message: str
    retryable: bool

    def __str__(self) -> str:
        return self.message


class MetadataExtractor:
    def __init__(self, trusted_hosts: tuple[str, ...] = ()) -> None:
        self._trusted_hosts = trusted_hosts

    def extract(self, source_url: str, cookie_file: Path | None = None) -> dict[str, Any]:
        assert_public_source(source_url, self._trusted_hosts)
        options: dict[str, Any] = {
            "quiet": True,
            "no_warnings": True,
            "skip_download": True,
            "ignore_no_formats_error": True,
            "noplaylist": True,
            "socket_timeout": 20,
            "retries": 2,
            "extractor_retries": 2,
            "cachedir": False,
        }
        if cookie_file is not None:
            options["cookiefile"] = str(cookie_file)
        try:
            with yt_dlp.YoutubeDL(options) as downloader:
                info = downloader.extract_info(source_url, download=False)
        except SourceRejected:
            raise
        except DownloadError as exc:
            raw_message = clean_error(str(exc))
            code = classify_error(raw_message)
            raise ExtractionFailure(
                code=code,
                message=friendly_error(code, raw_message),
                retryable=is_retryable(raw_message),
            ) from exc
        except (OSError, TimeoutError) as exc:
            raise ExtractionFailure(
                code="source_network_error",
                message=clean_error(str(exc)),
                retryable=True,
            ) from exc

        if not isinstance(info, dict):
            raise ExtractionFailure(
                code="metadata_empty",
                message="yt-dlp returned no media metadata",
                retryable=False,
            )

        duration = info.get("duration")
        duration_seconds = int(duration) if isinstance(duration, (int, float)) else None
        return {
            "title": clean_text(info.get("title"), 500),
            "description": clean_text(info.get("description"), 20_000),
            "thumbnail_url": clean_text(info.get("thumbnail"), 2_000),
            "duration_seconds": duration_seconds,
            "extractor": clean_text(info.get("extractor_key") or info.get("extractor"), 200),
        }


def assert_public_source(
    source_url: str,
    trusted_hosts: tuple[str, ...] = (),
) -> None:
    parsed = urlsplit(source_url)
    if parsed.scheme not in {"http", "https"} or not parsed.hostname:
        raise SourceRejected("source URL must use http or https")
    if parsed.username or parsed.password:
        raise SourceRejected("source URL must not contain credentials")

    hostname = parsed.hostname.rstrip(".").lower()
    if hostname in trusted_hosts:
        return
    if hostname in {"localhost", "localhost.localdomain"} or hostname.endswith(".local"):
        raise SourceRejected("local network source URLs are not allowed")

    try:
        addresses = socket.getaddrinfo(hostname, parsed.port or 443, type=socket.SOCK_STREAM)
    except socket.gaierror as exc:
        raise ExtractionFailure(
            code="source_dns_failed",
            message=f"could not resolve source host: {hostname}",
            retryable=True,
        ) from exc

    for address in addresses:
        ip = ipaddress.ip_address(address[4][0])
        if not ip.is_global:
            raise SourceRejected("private, loopback, link-local, or reserved source addresses are not allowed")


def clean_text(value: Any, limit: int) -> str:
    if value is None:
        return ""
    text = str(value).replace("\x00", "").strip()
    return text[:limit]


def clean_error(value: str) -> str:
    text = value.replace("\x00", "").strip()
    return text[:2_000] or "media metadata extraction failed"


def is_retryable(message: str) -> bool:
    if classify_error(message) == "source_auth_required":
        return True
    lowered = message.lower()
    permanent_markers = (
        "unsupported url",
        "video unavailable",
        "copyright",
        "not available in your country",
    )
    return not any(marker in lowered for marker in permanent_markers)


def classify_error(message: str) -> str:
    lowered = message.lower()
    if "unsupported url" in lowered:
        return "source_unsupported"
    auth_markers = (
        "private video",
        "login required",
        "sign in to confirm you’re not a bot",
        "sign in to confirm you're not a bot",
        "use --cookies",
        "cookies-from-browser",
        "authentication is required",
    )
    if any(marker in lowered for marker in auth_markers):
        return "source_auth_required"
    if "http error 429" in lowered or "too many requests" in lowered:
        return "source_rate_limited"
    if "does not look like a netscape format cookies file" in lowered:
        return "cookie_file_invalid"
    javascript_markers = (
        "n challenge solving failed",
        "signature solving failed",
        "javascript runtime",
        "challenge solver script",
    )
    if any(marker in lowered for marker in javascript_markers):
        return "source_js_challenge_failed"
    format_markers = (
        "requested format is not available",
        "only images are available",
        "no video formats found",
    )
    if any(marker in lowered for marker in format_markers):
        return "source_formats_unavailable"
    if "video unavailable" in lowered or "copyright" in lowered:
        return "source_unavailable"
    return "metadata_extraction_failed"


def friendly_error(code: str, raw_message: str) -> str:
    messages = {
        "source_auth_required": (
            "站点要求登录或机器人验证。请在“Cookie”中同步 CookieCloud 或上传有效的 "
            "Netscape Cookie 文件，在任务中选择该配置后重试。"
        ),
        "cookie_file_invalid": "Cookie 文件格式无效，请重新同步或上传 Netscape 格式文件。",
        "source_rate_limited": "来源站点限制了当前请求频率，请稍后重试并降低抓取频率。",
        "source_unsupported": "当前链接无法由 yt-dlp 识别，请检查链接是否指向可访问的视频。",
        "source_js_challenge_failed": (
            "YouTube JavaScript 挑战解析失败。请更新媒体 Worker 中固定版本的 "
            "yt-dlp、yt-dlp-ejs 与 Deno 后重试。"
        ),
        "source_formats_unavailable": (
            "已读取视频信息，但当前会话没有可下载的音视频格式。请重新同步 Cookie 后重试；"
            "若仍失败，需要为 YouTube 配置受控的 PO Token 提供器。"
        ),
        "source_unavailable": "来源视频不可用、已删除或受地区/版权限制。",
    }
    return messages.get(code, raw_message)
