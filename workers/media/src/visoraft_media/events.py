from __future__ import annotations

import json
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any

SPEC_VERSION = "1.0"
METADATA_REQUESTED_V1 = "io.visoraft.media.metadata.requested.v1"
METADATA_STARTED_V1 = "io.visoraft.media.metadata.started.v1"
METADATA_COMPLETED_V1 = "io.visoraft.media.metadata.completed.v1"
METADATA_FAILED_V1 = "io.visoraft.media.metadata.failed.v1"
DOWNLOAD_REQUESTED_V1 = "io.visoraft.media.download.requested.v1"
DOWNLOAD_STARTED_V1 = "io.visoraft.media.download.started.v1"
DOWNLOAD_PROGRESS_V1 = "io.visoraft.media.download.progress.v1"
DOWNLOAD_COMPLETED_V1 = "io.visoraft.media.download.completed.v1"
DOWNLOAD_FAILED_V1 = "io.visoraft.media.download.failed.v1"
DOWNLOAD_CANCELLED_V1 = "io.visoraft.media.download.cancelled.v1"
MEDIA_INSPECT_STARTED_V1 = "io.visoraft.media.inspect.started.v1"
MEDIA_INSPECT_COMPLETED_V1 = "io.visoraft.media.inspect.completed.v1"
MEDIA_INSPECT_FAILED_V1 = "io.visoraft.media.inspect.failed.v1"
ASSETS_DELETE_REQUESTED_V1 = "io.visoraft.media.assets.delete.requested.v1"
ASSETS_DELETED_V1 = "io.visoraft.media.assets.deleted.v1"
ASSETS_DELETE_FAILED_V1 = "io.visoraft.media.assets.delete.failed.v1"
SUBTITLE_PROCESS_REQUESTED_V1 = "io.visoraft.subtitle.process.requested.v1"
SUBTITLE_PROCESS_STARTED_V1 = "io.visoraft.subtitle.process.started.v1"
SUBTITLE_PROCESS_PROGRESS_V1 = "io.visoraft.subtitle.process.progress.v1"
SUBTITLE_PROCESS_COMPLETED_V1 = "io.visoraft.subtitle.process.completed.v1"
SUBTITLE_PROCESS_FAILED_V1 = "io.visoraft.subtitle.process.failed.v1"
TRANSCODE_REQUESTED_V1 = "io.visoraft.media.transcode.requested.v1"
TRANSCODE_STARTED_V1 = "io.visoraft.media.transcode.started.v1"
TRANSCODE_PROGRESS_V1 = "io.visoraft.media.transcode.progress.v1"
TRANSCODE_COMPLETED_V1 = "io.visoraft.media.transcode.completed.v1"
TRANSCODE_FAILED_V1 = "io.visoraft.media.transcode.failed.v1"
TRANSCODE_CANCELLED_V1 = "io.visoraft.media.transcode.cancelled.v1"


class InvalidEnvelope(ValueError):
    """Raised when a queue message does not match the event contract."""


@dataclass(frozen=True)
class Envelope:
    specversion: str
    id: str
    type: str
    source: str
    subject: str
    time: str
    data: dict[str, Any]

    @classmethod
    def decode(cls, body: bytes) -> "Envelope":
        try:
            value = json.loads(body)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise InvalidEnvelope("message is not valid JSON") from exc

        required = ("specversion", "id", "type", "source", "subject", "time", "data")
        if not isinstance(value, dict) or any(key not in value for key in required):
            raise InvalidEnvelope("message is missing required envelope fields")
        if value["specversion"] != SPEC_VERSION:
            raise InvalidEnvelope("unsupported event specversion")
        if not isinstance(value["data"], dict):
            raise InvalidEnvelope("event data must be an object")
        if not str(value["subject"]).startswith("task/"):
            raise InvalidEnvelope("event subject must identify a task")

        return cls(
            specversion=value["specversion"],
            id=str(value["id"]),
            type=str(value["type"]),
            source=str(value["source"]),
            subject=str(value["subject"]),
            time=str(value["time"]),
            data=value["data"],
        )

    @classmethod
    def create(
        cls,
        event_type: str,
        subject: str,
        data: dict[str, Any],
    ) -> "Envelope":
        now = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
        return cls(
            specversion=SPEC_VERSION,
            id=str(uuid.uuid4()),
            type=event_type,
            source="visoraft/media-worker",
            subject=subject,
            time=now,
            data=data,
        )

    def encode(self) -> bytes:
        return json.dumps(
            {
                "specversion": self.specversion,
                "id": self.id,
                "type": self.type,
                "source": self.source,
                "subject": self.subject,
                "time": self.time,
                "data": self.data,
            },
            ensure_ascii=False,
            separators=(",", ":"),
        ).encode("utf-8")
