from __future__ import annotations

import logging
import signal
import tempfile
import threading
import time
import uuid
from pathlib import Path
from typing import Any

import pika

from .cancellation import CancellationProbe
from .cookie_material import CookieMaterialClient, CookieMaterialFailure
from .cover_importer import CoverImportFailure, import_cover
from .downloader import (
    DownloadCancelled,
    DownloadFailure,
    DownloadTelemetry,
    MediaDownloader,
)
from .events import (
    ASSETS_DELETED_V1,
    ASSETS_DELETE_FAILED_V1,
    ASSETS_DELETE_REQUESTED_V1,
    DOWNLOAD_CANCELLED_V1,
    DOWNLOAD_COMPLETED_V1,
    DOWNLOAD_FAILED_V1,
    DOWNLOAD_PROGRESS_V1,
    DOWNLOAD_REQUESTED_V1,
    DOWNLOAD_STARTED_V1,
    METADATA_COMPLETED_V1,
    METADATA_FAILED_V1,
    METADATA_REQUESTED_V1,
    METADATA_STARTED_V1,
    MEDIA_INSPECT_COMPLETED_V1,
    MEDIA_INSPECT_FAILED_V1,
    MEDIA_INSPECT_STARTED_V1,
    SUBTITLE_PROCESS_COMPLETED_V1,
    SUBTITLE_PROCESS_FAILED_V1,
    SUBTITLE_PROCESS_PROGRESS_V1,
    SUBTITLE_PROCESS_REQUESTED_V1,
    SUBTITLE_PROCESS_STARTED_V1,
    TRANSCODE_CANCELLED_V1,
    TRANSCODE_COMPLETED_V1,
    TRANSCODE_FAILED_V1,
    TRANSCODE_PROGRESS_V1,
    TRANSCODE_REQUESTED_V1,
    TRANSCODE_STARTED_V1,
    Envelope,
    InvalidEnvelope,
)
from .extractor import ExtractionFailure, MetadataExtractor, SourceRejected
from .media_probe import (
    FFprobeInspector,
    MediaProbeCancelled,
    MediaProbeFailure,
)
from .processing_config import ProcessingConfigClient, ProcessingConfigFailure
from .settings import Settings
from .storage import ObjectStorage, StorageFailure
from .subtitle_processor import SubtitleFailure, SubtitleProcessor
from .transcoder import TranscodeCancelled, TranscodeFailure, Transcoder

LOGGER = logging.getLogger("visoraft.media-worker")

COMMAND_ROUTING_KEYS = {
    "metadata": METADATA_REQUESTED_V1,
    "download": DOWNLOAD_REQUESTED_V1,
    "assets_delete": ASSETS_DELETE_REQUESTED_V1,
    "subtitle": SUBTITLE_PROCESS_REQUESTED_V1,
    "transcode": TRANSCODE_REQUESTED_V1,
}


class MediaWorker:
    def __init__(self, settings: Settings) -> None:
        self.settings = settings
        self.extractor = MetadataExtractor(settings.trusted_media_hosts)
        self.downloader = MediaDownloader(
            settings.max_download_bytes,
            settings.trusted_media_hosts,
            http_chunk_size_bytes=settings.ytdlp_http_chunk_size_bytes,
            concurrent_fragments=settings.ytdlp_concurrent_fragments,
            stall_timeout_seconds=settings.download_stall_timeout_seconds,
            overall_timeout_seconds=settings.download_overall_timeout_seconds,
        )
        self.inspector = FFprobeInspector()
        self.storage = ObjectStorage(settings)
        self.cancellation = CancellationProbe(settings.control_api_url)
        self.cookie_material = CookieMaterialClient(
            settings.control_api_url,
            settings.worker_token,
        )
        self.processing_config = ProcessingConfigClient(
            settings.control_api_url,
            settings.worker_token,
        )
        self.subtitle_processor = SubtitleProcessor(self.storage)
        self.transcoder = Transcoder(self.storage, self.inspector)
        self._stopping = False
        self._connection: pika.BlockingConnection | None = None
        self._consumer_channel: pika.channel.Channel | None = None
        self._active_job: threading.Thread | None = None
        self._active_job_lock = threading.Lock()
        self._session_cancelled = threading.Event()

    def stop(self, *_: Any) -> None:
        self._stopping = True
        with self._active_job_lock:
            active_job = self._active_job
        if active_job is None or not active_job.is_alive():
            self._request_consumer_stop()

    def run(self) -> None:
        while not self._stopping:
            try:
                self._run_session()
            except KeyboardInterrupt:
                self._stopping = True
            except Exception:
                if self._stopping:
                    break
                LOGGER.exception("media worker session ended")
                time.sleep(3)

    def _run_session(self) -> None:
        self.storage.ensure_bucket()
        self._session_cancelled = threading.Event()
        parameters = pika.URLParameters(self.settings.rabbitmq_url)
        parameters.heartbeat = self.settings.rabbitmq_heartbeat
        parameters.blocked_connection_timeout = 30
        self._connection = pika.BlockingConnection(parameters)
        channel = self._connection.channel()
        self._consumer_channel = channel
        channel.exchange_declare(
            exchange=self.settings.event_exchange,
            exchange_type="topic",
            durable=True,
        )
        channel.exchange_declare(
            exchange=f"{self.settings.event_exchange}.deadletter",
            exchange_type="topic",
            durable=True,
        )
        dead_queue = f"{self.settings.queue_name}.dlq"
        channel.queue_declare(queue=dead_queue, durable=True)
        channel.queue_bind(
            queue=dead_queue,
            exchange=f"{self.settings.event_exchange}.deadletter",
            routing_key="#",
        )
        channel.queue_declare(
            queue=self.settings.queue_name,
            durable=True,
            arguments={
                "x-dead-letter-exchange": f"{self.settings.event_exchange}.deadletter",
            },
        )
        enabled_routing_keys = {
            COMMAND_ROUTING_KEYS[command] for command in self.settings.command_types
        }
        for routing_key in COMMAND_ROUTING_KEYS.values():
            if routing_key in enabled_routing_keys:
                channel.queue_bind(
                    queue=self.settings.queue_name,
                    exchange=self.settings.event_exchange,
                    routing_key=routing_key,
                )
            else:
                channel.queue_unbind(
                    queue=self.settings.queue_name,
                    exchange=self.settings.event_exchange,
                    routing_key=routing_key,
                )
        channel.basic_qos(prefetch_count=1)
        channel.basic_consume(
            queue=self.settings.queue_name,
            on_message_callback=self._on_message,
            auto_ack=False,
        )
        LOGGER.info(
            "media worker connected queue=%s commands=%s",
            self.settings.queue_name,
            ",".join(self.settings.command_types),
        )
        try:
            channel.start_consuming()
        finally:
            self._session_cancelled.set()
            with self._active_job_lock:
                active_job = self._active_job
            if active_job is not None and active_job.is_alive():
                LOGGER.info("waiting for active media command to stop before reconnecting")
                active_job.join()
            if channel.is_open:
                channel.close()
            if self._connection and self._connection.is_open:
                self._connection.close()
            self._consumer_channel = None
            self._connection = None

    def _on_message(
        self,
        channel: pika.channel.Channel,
        method: pika.spec.Basic.Deliver,
        _properties: pika.BasicProperties,
        body: bytes,
    ) -> None:
        with self._active_job_lock:
            if self._active_job is not None and self._active_job.is_alive():
                LOGGER.error("received a media command while another command is active")
                channel.basic_nack(delivery_tag=method.delivery_tag, requeue=True)
                return
            active_job = threading.Thread(
                target=self._process_message,
                args=(channel, method.delivery_tag, body, self._session_cancelled),
                name=f"media-command-{method.delivery_tag}",
                daemon=False,
            )
            self._active_job = active_job
        active_job.start()

    def _process_message(
        self,
        consumer_channel: pika.channel.Channel,
        delivery_tag: int,
        body: bytes,
        session_cancelled: threading.Event,
    ) -> None:
        publisher_connection: pika.BlockingConnection | None = None
        request: Envelope | None = None
        acknowledge = False
        requeue = True
        try:
            publisher_connection, publisher_channel = self._open_publisher()
            request = Envelope.decode(body)
            enabled_routing_keys = {
                COMMAND_ROUTING_KEYS[command] for command in self.settings.command_types
            }
            if request.type not in enabled_routing_keys:
                raise InvalidEnvelope(
                    f"command type is not enabled for this worker: {request.type}"
                )
            try:
                if request.type == METADATA_REQUESTED_V1:
                    result = self._handle_metadata(publisher_channel, request)
                elif request.type == DOWNLOAD_REQUESTED_V1:
                    result = self._handle_download(publisher_channel, request)
                elif request.type == ASSETS_DELETE_REQUESTED_V1:
                    result = self._handle_asset_deletion(request)
                elif request.type == SUBTITLE_PROCESS_REQUESTED_V1:
                    result = self._handle_subtitles(publisher_channel, request)
                elif request.type == TRANSCODE_REQUESTED_V1:
                    result = self._handle_transcode(publisher_channel, request)
                else:
                    raise InvalidEnvelope(
                        f"unsupported command type: {request.type}"
                    )
            except InvalidEnvelope:
                raise
            except Exception:
                LOGGER.exception(
                    "media command handler failed; publishing a terminal failure event"
                )
                result = self._unexpected_failure_result(request)

            if self._stopping or session_cancelled.is_set():
                LOGGER.info(
                    "media command interrupted by worker shutdown message_id=%s",
                    request.id,
                )
            else:
                self._publish(publisher_channel, result)
                acknowledge = True
                requeue = False
                LOGGER.info(
                    "media command completed message_id=%s result_type=%s",
                    request.id,
                    result.type,
                )
        except InvalidEnvelope as exc:
            requeue = False
            LOGGER.error("rejecting invalid media command: %s", exc)
        except Exception:
            LOGGER.exception("media command failed before a result was confirmed")
        finally:
            if publisher_connection is not None and publisher_connection.is_open:
                publisher_connection.close()
            self._finish_delivery(
                consumer_channel,
                delivery_tag,
                acknowledge=acknowledge,
                requeue=requeue,
            )

    def _unexpected_failure_result(self, request: Envelope) -> Envelope:
        failure_types = {
            METADATA_REQUESTED_V1: METADATA_FAILED_V1,
            DOWNLOAD_REQUESTED_V1: DOWNLOAD_FAILED_V1,
            ASSETS_DELETE_REQUESTED_V1: ASSETS_DELETE_FAILED_V1,
            SUBTITLE_PROCESS_REQUESTED_V1: SUBTITLE_PROCESS_FAILED_V1,
            TRANSCODE_REQUESTED_V1: TRANSCODE_FAILED_V1,
        }
        failure_type = failure_types.get(request.type)
        if failure_type is None:
            raise InvalidEnvelope(f"unsupported command type: {request.type}")
        data: dict[str, Any] = {
            "task_id": str(request.data.get("task_id") or ""),
            "attempt": int(request.data.get("attempt") or 1),
            "code": "media_worker_unexpected",
            "message": "媒体处理发生未预期错误，已停止自动重投，请查看服务日志",
            "retryable": True,
        }
        if request.type == ASSETS_DELETE_REQUESTED_V1:
            data["asset_ids"] = [
                str(item.get("asset_id") or "")
                for item in request.data.get("assets", [])
                if isinstance(item, dict) and item.get("asset_id")
            ]
        if request.type == TRANSCODE_REQUESTED_V1:
            data["run_id"] = str(request.data.get("run_id") or "")
        return Envelope.create(failure_type, request.subject, data)

    def _open_publisher(
        self,
    ) -> tuple[pika.BlockingConnection, pika.channel.Channel]:
        parameters = pika.URLParameters(self.settings.rabbitmq_url)
        parameters.heartbeat = self.settings.publisher_heartbeat
        parameters.blocked_connection_timeout = 30
        connection = pika.BlockingConnection(parameters)
        channel = connection.channel()
        channel.exchange_declare(
            exchange=self.settings.event_exchange,
            exchange_type="topic",
            durable=True,
        )
        channel.confirm_delivery()
        return connection, channel

    def _finish_delivery(
        self,
        consumer_channel: pika.channel.Channel,
        delivery_tag: int,
        *,
        acknowledge: bool,
        requeue: bool,
    ) -> None:
        connection = self._connection

        def finish() -> None:
            try:
                if consumer_channel.is_open:
                    if acknowledge:
                        consumer_channel.basic_ack(delivery_tag=delivery_tag)
                    else:
                        consumer_channel.basic_nack(
                            delivery_tag=delivery_tag,
                            requeue=requeue,
                        )
            finally:
                with self._active_job_lock:
                    self._active_job = None
                if self._stopping and consumer_channel.is_open:
                    consumer_channel.stop_consuming()

        if connection is None or not connection.is_open:
            with self._active_job_lock:
                self._active_job = None
            return
        try:
            connection.add_callback_threadsafe(finish)
        except pika.exceptions.AMQPError:
            with self._active_job_lock:
                self._active_job = None

    def _request_consumer_stop(self) -> None:
        connection = self._connection
        consumer_channel = self._consumer_channel
        if (
            connection is None
            or not connection.is_open
            or consumer_channel is None
        ):
            return

        def stop_consuming() -> None:
            if consumer_channel.is_open:
                consumer_channel.stop_consuming()

        try:
            connection.add_callback_threadsafe(stop_consuming)
        except pika.exceptions.AMQPError:
            return

    def _handle_metadata(
        self,
        channel: pika.channel.Channel,
        request: Envelope,
    ) -> Envelope:
        source_url = str(request.data.get("source_url", "")).strip()
        cookie_profile_id = _optional_cookie_profile_id(request.data)
        if not source_url:
            raise InvalidEnvelope("metadata command is missing source_url")

        self._publish(
            channel,
            Envelope.create(
                METADATA_STARTED_V1,
                request.subject,
                {
                    "task_id": request.data.get("task_id"),
                    "attempt": request.data.get("attempt", 1),
                },
            ),
        )
        try:
            with self.cookie_material.materialize(cookie_profile_id) as cookie_file:
                metadata = self.extractor.extract(source_url, cookie_file)
            return Envelope.create(METADATA_COMPLETED_V1, request.subject, metadata)
        except CookieMaterialFailure as exc:
            return Envelope.create(
                METADATA_FAILED_V1,
                request.subject,
                {
                    "code": "cookie_profile_unavailable",
                    "message": str(exc),
                    "retryable": True,
                },
            )
        except SourceRejected as exc:
            return Envelope.create(
                METADATA_FAILED_V1,
                request.subject,
                {
                    "code": "source_rejected",
                    "message": str(exc),
                    "retryable": False,
                },
            )
        except ExtractionFailure as exc:
            return Envelope.create(
                METADATA_FAILED_V1,
                request.subject,
                {
                    "code": exc.code,
                    "message": exc.message,
                    "retryable": exc.retryable,
                },
            )

    def _handle_download(
        self,
        channel: pika.channel.Channel,
        request: Envelope,
    ) -> Envelope:
        source_url = str(request.data.get("source_url", "")).strip()
        task_id = str(request.data.get("task_id", "")).strip()
        cookie_profile_id = _optional_cookie_profile_id(request.data)
        if not source_url:
            raise InvalidEnvelope("download command is missing source_url")
        try:
            uuid.UUID(task_id)
        except (ValueError, AttributeError) as exc:
            raise InvalidEnvelope("download command has an invalid task_id") from exc
        if request.subject != f"task/{task_id}":
            raise InvalidEnvelope("download command task_id does not match its subject")

        attempt = request.data.get("attempt", 1)
        self._publish(
            channel,
            Envelope.create(
                DOWNLOAD_STARTED_V1,
                request.subject,
                {"task_id": task_id, "attempt": attempt},
            ),
        )

        last_progress = 0
        last_phase = ""
        last_reported_at = 0.0

        def report_progress(telemetry: DownloadTelemetry) -> None:
            nonlocal last_phase, last_progress, last_reported_at
            value = max(1, min(int(telemetry.progress), 99))
            now = time.monotonic()
            phase_changed = telemetry.phase != last_phase
            heartbeat_due = now - last_reported_at >= 3
            advanced = value > last_progress and value-last_progress >= 2
            if not phase_changed and not heartbeat_due and not advanced:
                return
            last_progress = max(last_progress, value)
            last_phase = telemetry.phase
            last_reported_at = now
            event_data = telemetry.as_event_data()
            event_data.update(
                {
                    "task_id": task_id,
                    "attempt": attempt,
                    "progress": last_progress,
                }
            )
            self._publish(
                channel,
                Envelope.create(
                    DOWNLOAD_PROGRESS_V1,
                    request.subject,
                    event_data,
                ),
            )

        def should_cancel() -> bool:
            return (
                self._stopping
                or self._session_cancelled.is_set()
                or self.cancellation.is_cancelled(task_id)
            )

        try:
            with self.cookie_material.materialize(cookie_profile_id) as cookie_file:
                with tempfile.TemporaryDirectory(
                    prefix=f"visoraft-{task_id[:8]}-"
                ) as raw_dir:
                    downloaded = self.downloader.download(
                        source_url,
                        Path(raw_dir),
                        report_progress,
                        should_cancel,
                        cookie_file,
                    )
                    report_progress(
                        DownloadTelemetry(
                            progress=84,
                            phase="verifying",
                            downloaded_bytes=downloaded.size_bytes,
                            total_bytes=downloaded.size_bytes,
                        )
                    )

                    self._publish(
                        channel,
                        Envelope.create(
                            MEDIA_INSPECT_STARTED_V1,
                            request.subject,
                            {"task_id": task_id, "attempt": attempt},
                        ),
                    )
                    try:
                        inspected = self.inspector.inspect(downloaded.path, should_cancel)
                    except MediaProbeCancelled as exc:
                        raise DownloadCancelled(str(exc)) from exc
                    except MediaProbeFailure as exc:
                        return Envelope.create(
                            MEDIA_INSPECT_FAILED_V1,
                            request.subject,
                            {
                                "task_id": task_id,
                                "attempt": attempt,
                                "code": exc.code,
                                "message": exc.message,
                                "retryable": exc.retryable,
                            },
                        )

                    media_info = {
                        "schema_version": 1,
                        **inspected.as_dict(),
                    }
                    self._publish(
                        channel,
                        Envelope.create(
                            MEDIA_INSPECT_COMPLETED_V1,
                            request.subject,
                            {
                                "task_id": task_id,
                                "attempt": attempt,
                                "media_info": media_info,
                            },
                        ),
                    )

                    suffix = downloaded.path.suffix.lower() or ".bin"
                    object_key = f"tasks/{task_id}/source/source{suffix}"

                    def upload_progress(transferred: int) -> None:
                        ratio = min(transferred / downloaded.size_bytes, 1.0)
                        report_progress(
                            DownloadTelemetry(
                                progress=86 + int(ratio * 12),
                                phase="uploading_to_storage",
                                downloaded_bytes=transferred,
                                total_bytes=downloaded.size_bytes,
                            )
                        )

                    self.storage.upload_file(
                        downloaded.path,
                        object_key,
                        downloaded.content_type,
                        {
                            "task-id": task_id,
                            "sha256": downloaded.checksum_sha256,
                            "kind": "source",
                        },
                        upload_progress,
                    )

                    additional_assets: list[dict[str, Any]] = []
                    try:
                        config = self.processing_config.get(task_id)
                        automation = config.get("automation")
                        runtime = config.get("runtime")
                        process_cover = isinstance(automation, dict) and bool(
                            automation.get("process_cover")
                        )
                        thumbnail_url = ""
                        if isinstance(runtime, dict):
                            thumbnail_url = str(runtime.get("thumbnail_url") or "")
                        if process_cover and thumbnail_url:
                            cover = import_cover(thumbnail_url, Path(raw_dir))
                            cover_key = (
                                f"tasks/{task_id}/cover/source-"
                                f"{cover.checksum_sha256[:16]}{cover.extension}"
                            )
                            self.storage.upload_file(
                                cover.path,
                                cover_key,
                                cover.content_type,
                                {
                                    "task-id": task_id,
                                    "sha256": cover.checksum_sha256,
                                    "kind": "thumbnail",
                                },
                            )
                            additional_assets.append(
                                {
                                    "asset_id": str(uuid.uuid4()),
                                    "kind": "thumbnail",
                                    "bucket": self.storage.bucket,
                                    "object_key": cover_key,
                                    "original_name": cover.path.name,
                                    "content_type": cover.content_type,
                                    "size_bytes": cover.size_bytes,
                                    "checksum_sha256": cover.checksum_sha256,
                                    "media_info": {
                                        "schema_version": 1,
                                        "format_name": cover.content_type.removeprefix(
                                            "image/"
                                        ),
                                        "width": cover.width,
                                        "height": cover.height,
                                    },
                                }
                            )
                    except (
                        CoverImportFailure,
                        ProcessingConfigFailure,
                        StorageFailure,
                    ) as exc:
                        LOGGER.warning(
                            "cover import deferred to publish preparation task_id=%s reason=%s",
                            task_id,
                            exc,
                        )

                    if self.cancellation.is_cancelled(task_id, force=True):
                        try:
                            self.storage.delete_object(object_key)
                        except StorageFailure:
                            LOGGER.exception(
                                "could not remove object uploaded after cancellation task_id=%s key=%s",
                                task_id,
                                object_key,
                            )
                        raise DownloadCancelled("download cancelled by user")

                    return Envelope.create(
                        DOWNLOAD_COMPLETED_V1,
                        request.subject,
                        {
                            "task_id": task_id,
                            "attempt": attempt,
                            "asset_id": str(uuid.uuid4()),
                            "kind": "source",
                            "bucket": self.storage.bucket,
                            "object_key": object_key,
                            "original_name": downloaded.original_name,
                            "content_type": downloaded.content_type,
                            "size_bytes": downloaded.size_bytes,
                            "checksum_sha256": downloaded.checksum_sha256,
                            "media_info": media_info,
                            "additional_assets": additional_assets,
                        },
                    )
        except CookieMaterialFailure as exc:
            return Envelope.create(
                DOWNLOAD_FAILED_V1,
                request.subject,
                {
                    "code": "cookie_profile_unavailable",
                    "message": str(exc),
                    "retryable": True,
                },
            )
        except DownloadCancelled as exc:
            return Envelope.create(
                DOWNLOAD_CANCELLED_V1,
                request.subject,
                {
                    "task_id": task_id,
                    "attempt": attempt,
                    "message": str(exc),
                },
            )
        except SourceRejected as exc:
            return Envelope.create(
                DOWNLOAD_FAILED_V1,
                request.subject,
                {
                    "code": "source_rejected",
                    "message": str(exc),
                    "retryable": False,
                },
            )
        except DownloadFailure as exc:
            return Envelope.create(
                DOWNLOAD_FAILED_V1,
                request.subject,
                {
                    "code": exc.code,
                    "message": exc.message,
                    "retryable": exc.retryable,
                },
            )
        except StorageFailure as exc:
            return Envelope.create(
                DOWNLOAD_FAILED_V1,
                request.subject,
                {
                    "code": "object_storage_error",
                    "message": str(exc),
                    "retryable": True,
                },
            )

    def _handle_asset_deletion(self, request: Envelope) -> Envelope:
        task_id = str(request.data.get("task_id", "")).strip()
        try:
            uuid.UUID(task_id)
        except (ValueError, AttributeError) as exc:
            raise InvalidEnvelope("asset deletion command has an invalid task_id") from exc
        if request.subject != f"task/{task_id}":
            raise InvalidEnvelope("asset deletion task_id does not match its subject")

        raw_assets = request.data.get("assets")
        if not isinstance(raw_assets, list) or not 1 <= len(raw_assets) <= 100:
            raise InvalidEnvelope("asset deletion command must contain 1 to 100 assets")

        assets: list[dict[str, str]] = []
        for raw_asset in raw_assets:
            if not isinstance(raw_asset, dict):
                raise InvalidEnvelope("asset deletion command contains an invalid asset")
            asset_id = str(raw_asset.get("asset_id", "")).strip()
            bucket = str(raw_asset.get("bucket", "")).strip()
            object_key = str(raw_asset.get("object_key", "")).strip()
            try:
                uuid.UUID(asset_id)
            except (ValueError, AttributeError) as exc:
                raise InvalidEnvelope("asset deletion command has an invalid asset_id") from exc
            if (
                not bucket
                or not object_key
                or object_key.startswith("/")
                or ".." in object_key.split("/")
                or not object_key.startswith(f"tasks/{task_id}/")
            ):
                raise InvalidEnvelope("asset deletion command has an unsafe object location")
            assets.append(
                {
                    "asset_id": asset_id,
                    "bucket": bucket,
                    "object_key": object_key,
                }
            )

        asset_ids = [asset["asset_id"] for asset in assets]
        try:
            for asset in assets:
                self.storage.delete_object(asset["object_key"], asset["bucket"])
            self.storage.delete_object(
                f"tasks/{task_id}/subtitles/asr-checkpoint.json"
            )
            return Envelope.create(
                ASSETS_DELETED_V1,
                request.subject,
                {
                    "task_id": task_id,
                    "asset_ids": asset_ids,
                },
            )
        except StorageFailure as exc:
            return Envelope.create(
                ASSETS_DELETE_FAILED_V1,
                request.subject,
                {
                    "task_id": task_id,
                    "asset_ids": asset_ids,
                    "code": "object_delete_failed",
                    "message": str(exc),
                    "retryable": True,
                },
            )

    def _handle_subtitles(
        self,
        channel: pika.channel.Channel,
        request: Envelope,
    ) -> Envelope:
        task_id = str(request.data.get("task_id", "")).strip()
        try:
            uuid.UUID(task_id)
        except (ValueError, AttributeError) as exc:
            raise InvalidEnvelope("subtitle command has an invalid task_id") from exc
        if request.subject != f"task/{task_id}":
            raise InvalidEnvelope("subtitle command task_id does not match its subject")
        attempt = int(request.data.get("attempt", 1))
        self._publish(
            channel,
            Envelope.create(
                SUBTITLE_PROCESS_STARTED_V1,
                request.subject,
                {"task_id": task_id, "attempt": attempt},
            ),
        )

        def should_cancel() -> bool:
            return (
                self._stopping
                or self._session_cancelled.is_set()
                or self.cancellation.is_cancelled(task_id)
            )

        def on_progress(
            progress: int,
            phase: str,
            detail: dict[str, Any] | None = None,
        ) -> None:
            data: dict[str, Any] = {
                "task_id": task_id,
                "attempt": attempt,
                "progress": progress,
                "phase": phase,
            }
            if detail:
                data.update(detail)
            self._publish(
                channel,
                Envelope.create(SUBTITLE_PROCESS_PROGRESS_V1, request.subject, data),
            )

        try:
            config = self.processing_config.get(task_id)
            runtime = config.get("runtime")
            cookie_profile_id = None
            if isinstance(runtime, dict):
                raw_profile_id = runtime.get("cookie_profile_id")
                if raw_profile_id:
                    cookie_profile_id = str(raw_profile_id)
            with self.cookie_material.materialize(cookie_profile_id) as cookie_file:
                with tempfile.TemporaryDirectory(
                    prefix=f"visoraft-subtitle-{task_id[:8]}-"
                ) as raw_dir:
                    result = self.subtitle_processor.process(
                        task_id,
                        config,
                        Path(raw_dir),
                        cookie_file,
                        should_cancel,
                        on_progress,
                    )
            result["attempt"] = attempt
            return Envelope.create(
                SUBTITLE_PROCESS_COMPLETED_V1,
                request.subject,
                result,
            )
        except ProcessingConfigFailure as exc:
            return Envelope.create(
                SUBTITLE_PROCESS_FAILED_V1,
                request.subject,
                {
                    "task_id": task_id,
                    "attempt": attempt,
                    "code": "processing_config_unavailable",
                    "message": str(exc),
                    "retryable": True,
                },
            )
        except CookieMaterialFailure as exc:
            return Envelope.create(
                SUBTITLE_PROCESS_FAILED_V1,
                request.subject,
                {
                    "task_id": task_id,
                    "attempt": attempt,
                    "code": "cookie_profile_unavailable",
                    "message": str(exc),
                    "retryable": True,
                },
            )
        except SubtitleFailure as exc:
            return Envelope.create(
                SUBTITLE_PROCESS_FAILED_V1,
                request.subject,
                {
                    "task_id": task_id,
                    "attempt": attempt,
                    "code": exc.code,
                    "message": exc.message,
                    "retryable": exc.retryable,
                },
            )

    def _handle_transcode(
        self,
        channel: pika.channel.Channel,
        request: Envelope,
    ) -> Envelope:
        task_id = str(request.data.get("task_id", "")).strip()
        try:
            uuid.UUID(task_id)
        except (ValueError, AttributeError) as exc:
            raise InvalidEnvelope("transcode command has an invalid task_id") from exc
        if request.subject != f"task/{task_id}":
            raise InvalidEnvelope("transcode command task_id does not match its subject")
        run_id = str(request.data.get("run_id", "")).strip()
        try:
            uuid.UUID(run_id)
        except (ValueError, AttributeError) as exc:
            raise InvalidEnvelope("transcode command has an invalid run_id") from exc
        try:
            attempt = int(request.data.get("attempt", 1))
        except (TypeError, ValueError) as exc:
            raise InvalidEnvelope("transcode command has an invalid attempt") from exc
        if attempt < 1 or attempt > 100:
            raise InvalidEnvelope("transcode command has an invalid attempt")

        self._publish(
            channel,
            Envelope.create(
                TRANSCODE_STARTED_V1,
                request.subject,
                {
                    "task_id": task_id,
                    "run_id": run_id,
                    "attempt": attempt,
                },
            ),
        )
        last_progress = 0

        def report_progress(value: int) -> None:
            nonlocal last_progress
            value = max(1, min(int(value), 99))
            if value <= last_progress:
                return
            if value < 98 and value - last_progress < 3:
                return
            last_progress = value
            self._publish(
                channel,
                Envelope.create(
                    TRANSCODE_PROGRESS_V1,
                    request.subject,
                    {
                        "task_id": task_id,
                        "run_id": run_id,
                        "attempt": attempt,
                        "progress": value,
                    },
                ),
            )

        def should_cancel() -> bool:
            return (
                self._stopping
                or self._session_cancelled.is_set()
                or self.cancellation.is_cancelled(task_id)
            )

        try:
            config = self.processing_config.get(task_id)
            with tempfile.TemporaryDirectory(
                prefix=f"visoraft-transcode-{task_id[:8]}-"
            ) as raw_dir:
                result = self.transcoder.process(
                    task_id,
                    config,
                    Path(raw_dir),
                    should_cancel,
                    report_progress,
                )
            result["attempt"] = attempt
            result["run_id"] = run_id
            return Envelope.create(
                TRANSCODE_COMPLETED_V1,
                request.subject,
                result,
            )
        except ProcessingConfigFailure as exc:
            return Envelope.create(
                TRANSCODE_FAILED_V1,
                request.subject,
                {
                    "task_id": task_id,
                    "run_id": run_id,
                    "attempt": attempt,
                    "code": "processing_config_unavailable",
                    "message": str(exc),
                    "retryable": True,
                },
            )
        except TranscodeCancelled as exc:
            return Envelope.create(
                TRANSCODE_CANCELLED_V1,
                request.subject,
                {
                    "task_id": task_id,
                    "run_id": run_id,
                    "attempt": attempt,
                    "message": str(exc),
                },
            )
        except TranscodeFailure as exc:
            return Envelope.create(
                TRANSCODE_FAILED_V1,
                request.subject,
                {
                    "task_id": task_id,
                    "run_id": run_id,
                    "attempt": attempt,
                    "code": exc.code,
                    "message": exc.message,
                    "retryable": exc.retryable,
                },
            )

    def _publish(self, channel: pika.channel.Channel, envelope: Envelope) -> None:
        # BlockingChannel.basic_publish returns None in pika 1.4.2. With
        # confirm_delivery enabled, delivery failures are reported by raising
        # NackError or UnroutableError; returning normally is the confirmation.
        channel.basic_publish(
            exchange=self.settings.event_exchange,
            routing_key=envelope.type,
            body=envelope.encode(),
            mandatory=True,
            properties=pika.BasicProperties(
                content_type="application/cloudevents+json",
                delivery_mode=pika.DeliveryMode.Persistent,
                message_id=envelope.id,
                timestamp=int(time.time()),
            ),
        )


def _optional_cookie_profile_id(data: dict[str, Any]) -> str | None:
    value = data.get("cookie_profile_id")
    if value is None or str(value).strip() == "":
        return None
    normalized = str(value).strip()
    try:
        uuid.UUID(normalized)
    except (ValueError, AttributeError) as exc:
        raise InvalidEnvelope("command has an invalid cookie_profile_id") from exc
    return normalized


def configure_logging(level: str) -> None:
    logging.basicConfig(
        level=getattr(logging, level, logging.INFO),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )


def main() -> None:
    settings = Settings.from_environment()
    configure_logging(settings.log_level)
    worker = MediaWorker(settings)
    signal.signal(signal.SIGTERM, worker.stop)
    signal.signal(signal.SIGINT, worker.stop)
    worker.run()


if __name__ == "__main__":
    main()
