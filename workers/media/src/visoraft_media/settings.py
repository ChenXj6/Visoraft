from __future__ import annotations

import os
import tempfile
from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    rabbitmq_url: str
    event_exchange: str
    queue_name: str
    log_level: str
    s3_endpoint: str
    s3_access_key: str
    s3_secret_key: str
    s3_bucket: str
    s3_region: str
    control_api_url: str
    worker_token: str
    max_download_bytes: int
    work_root: str = os.path.join(tempfile.gettempdir(), "visoraft-work")
    trusted_media_hosts: tuple[str, ...] = ()
    command_types: tuple[str, ...] = (
        "metadata",
        "download",
        "assets_delete",
        "subtitle",
        "transcode",
    )
    rabbitmq_heartbeat: int = 20
    publisher_heartbeat: int = 0
    ytdlp_http_chunk_size_bytes: int = 10 * 1024 * 1024
    ytdlp_concurrent_fragments: int = 4
    download_stall_timeout_seconds: int = 180
    download_overall_timeout_seconds: int = 7200

    @classmethod
    def from_environment(cls) -> "Settings":
        return cls(
            rabbitmq_url=os.getenv(
                "VISORAFT_RABBITMQ_URL",
                "amqp://visoraft:visoraft-local@localhost:5673/",
            ),
            event_exchange=os.getenv(
                "VISORAFT_EVENT_EXCHANGE",
                "visoraft.events",
            ),
            queue_name=os.getenv(
                "VISORAFT_MEDIA_QUEUE",
                os.getenv(
                    "VISORAFT_MEDIA_METADATA_QUEUE",
                    "visoraft.media.metadata.v1",
                ),
            ),
            log_level=os.getenv("VISORAFT_LOG_LEVEL", "INFO").upper(),
            s3_endpoint=os.getenv("VISORAFT_S3_ENDPOINT", "http://localhost:8333"),
            s3_access_key=os.getenv("VISORAFT_S3_ACCESS_KEY", "visoraft-local"),
            s3_secret_key=os.getenv(
                "VISORAFT_S3_SECRET_KEY",
                "visoraft-local-secret",
            ),
            s3_bucket=os.getenv("VISORAFT_S3_BUCKET", "visoraft-media"),
            s3_region=os.getenv("VISORAFT_S3_REGION", "us-east-1"),
            control_api_url=os.getenv(
                "VISORAFT_CONTROL_API_URL",
                "http://localhost:8080",
            ).rstrip("/"),
            worker_token=os.getenv(
                "VISORAFT_WORKER_TOKEN",
                "visoraft-local-worker-token-2026",
            ),
            max_download_bytes=_bounded_int(
                "VISORAFT_MAX_DOWNLOAD_BYTES",
                2 * 1024 * 1024 * 1024,
                minimum=1024,
                maximum=20 * 1024 * 1024 * 1024,
            ),
            work_root=os.getenv(
                "VISORAFT_WORK_ROOT",
                os.path.join(tempfile.gettempdir(), "visoraft-work"),
            ),
            trusted_media_hosts=_trusted_hosts(
                os.getenv("VISORAFT_TRUSTED_MEDIA_HOSTS", "")
            ),
            command_types=_command_types(
                os.getenv(
                    "VISORAFT_MEDIA_COMMANDS",
                    "metadata,download,assets_delete",
                )
            ),
            rabbitmq_heartbeat=_bounded_int(
                "VISORAFT_RABBITMQ_HEARTBEAT",
                20,
                minimum=0,
                maximum=3600,
            ),
            publisher_heartbeat=_bounded_int(
                "VISORAFT_RABBITMQ_PUBLISHER_HEARTBEAT",
                0,
                minimum=0,
                maximum=3600,
            ),
            ytdlp_http_chunk_size_bytes=_bounded_int(
                "VISORAFT_YTDLP_HTTP_CHUNK_SIZE_BYTES",
                10 * 1024 * 1024,
                minimum=1024 * 1024,
                maximum=50 * 1024 * 1024,
            ),
            ytdlp_concurrent_fragments=_bounded_int(
                "VISORAFT_YTDLP_CONCURRENT_FRAGMENTS",
                4,
                minimum=1,
                maximum=16,
            ),
            download_stall_timeout_seconds=_bounded_int(
                "VISORAFT_DOWNLOAD_STALL_TIMEOUT_SECONDS",
                180,
                minimum=30,
                maximum=1800,
            ),
            download_overall_timeout_seconds=_bounded_int(
                "VISORAFT_DOWNLOAD_TIMEOUT_SECONDS",
                7200,
                minimum=300,
                maximum=86400,
            ),
        )


def _bounded_int(key: str, fallback: int, *, minimum: int, maximum: int) -> int:
    raw = os.getenv(key)
    if raw is None or raw == "":
        return fallback
    try:
        value = int(raw)
    except ValueError as exc:
        raise ValueError(f"{key} must be an integer") from exc
    if value < minimum or value > maximum:
        raise ValueError(f"{key} must be between {minimum} and {maximum}")
    return value


def _command_types(raw: str) -> tuple[str, ...]:
    allowed = {"metadata", "download", "assets_delete", "subtitle", "transcode"}
    values = tuple(dict.fromkeys(item.strip().lower() for item in raw.split(",") if item.strip()))
    if not values:
        raise ValueError("VISORAFT_MEDIA_COMMANDS must select at least one command")
    invalid = sorted(set(values) - allowed)
    if invalid:
        raise ValueError(
            "VISORAFT_MEDIA_COMMANDS contains unsupported commands: "
            + ", ".join(invalid)
        )
    return values


def _trusted_hosts(raw: str) -> tuple[str, ...]:
    hosts = tuple(
        dict.fromkeys(
            value.strip().rstrip(".").lower()
            for value in raw.split(",")
            if value.strip()
        )
    )
    for host in hosts:
        if "/" in host or ":" in host or "@" in host:
            raise ValueError(
                "VISORAFT_TRUSTED_MEDIA_HOSTS must contain exact hostnames only"
            )
    return hosts
