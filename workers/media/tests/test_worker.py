from __future__ import annotations

import hashlib
import threading
import unittest
from types import SimpleNamespace
from unittest.mock import ANY, Mock, call

from visoraft_media.downloader import DownloadedFile
from visoraft_media.events import (
    ASSETS_DELETED_V1,
    ASSETS_DELETE_REQUESTED_V1,
    DOWNLOAD_COMPLETED_V1,
    DOWNLOAD_REQUESTED_V1,
    MEDIA_INSPECT_FAILED_V1,
    METADATA_COMPLETED_V1,
    METADATA_FAILED_V1,
    METADATA_REQUESTED_V1,
    METADATA_STARTED_V1,
    SUBTITLE_PROCESS_FAILED_V1,
    SUBTITLE_PROCESS_REQUESTED_V1,
    TRANSCODE_COMPLETED_V1,
    TRANSCODE_FAILED_V1,
    TRANSCODE_PROGRESS_V1,
    TRANSCODE_REQUESTED_V1,
    TRANSCODE_STARTED_V1,
    Envelope,
    InvalidEnvelope,
)
from visoraft_media.extractor import ExtractionFailure
from visoraft_media.media_probe import MediaProbeFailure, MediaProbeResult
from visoraft_media.settings import Settings
from visoraft_media.transcoder import TranscodeFailure
from visoraft_media.worker import MediaWorker


class PublishTests(unittest.TestCase):
    def setUp(self) -> None:
        self.worker = MediaWorker(
            Settings(
                rabbitmq_url="amqp://guest:guest@localhost:5672/%2f",
                event_exchange="visoraft.events",
                queue_name="visoraft.media.metadata.v1",
                log_level="INFO",
                s3_endpoint="http://localhost:8333",
                s3_access_key="test",
                s3_secret_key="test",
                s3_bucket="test",
                s3_region="us-east-1",
                control_api_url="http://localhost:8080",
                worker_token="test-worker-token-at-least-24",
                max_download_bytes=1024 * 1024,
            )
        )
        self.envelope = Envelope.create(
            METADATA_STARTED_V1,
            "task/00000000-0000-0000-0000-000000000000",
            {"task_id": "00000000-0000-0000-0000-000000000000", "attempt": 1},
        )

    def test_none_return_is_a_confirmed_publish(self) -> None:
        channel = Mock()
        channel.basic_publish.return_value = None

        self.worker._publish(channel, self.envelope)

        channel.basic_publish.assert_called_once()

    def test_broker_exception_is_not_swallowed(self) -> None:
        channel = Mock()
        channel.basic_publish.side_effect = RuntimeError("broker nack")

        with self.assertRaisesRegex(RuntimeError, "broker nack"):
            self.worker._publish(channel, self.envelope)

    def test_unexpected_handler_error_is_published_once_and_acknowledged(self) -> None:
        task_id = "00000000-0000-4000-8000-000000000009"
        request = Envelope.create(
            METADATA_REQUESTED_V1,
            f"task/{task_id}",
            {
                "task_id": task_id,
                "source_url": "https://example.invalid/video",
                "attempt": 2,
            },
        )
        connection = Mock()
        connection.is_open = True
        publisher = Mock()
        self.worker._open_publisher = Mock(return_value=(connection, publisher))
        self.worker._handle_metadata = Mock(side_effect=TypeError("unexpected"))
        self.worker._publish = Mock()
        self.worker._finish_delivery = Mock()

        self.worker._process_message(
            Mock(),
            7,
            request.encode(),
            threading.Event(),
        )

        published = self.worker._publish.call_args.args[1]
        self.assertEqual(METADATA_FAILED_V1, published.type)
        self.assertEqual("media_worker_unexpected", published.data["code"])
        self.worker._finish_delivery.assert_called_once_with(
            ANY,
            7,
            acknowledge=True,
            requeue=False,
        )

    def test_unexpected_subtitle_error_maps_to_subtitle_failure_event(self) -> None:
        task_id = "00000000-0000-4000-8000-000000000010"
        request = Envelope.create(
            SUBTITLE_PROCESS_REQUESTED_V1,
            f"task/{task_id}",
            {"task_id": task_id, "attempt": 3},
        )

        result = self.worker._unexpected_failure_result(request)

        self.assertEqual(SUBTITLE_PROCESS_FAILED_V1, result.type)
        self.assertEqual(3, result.data["attempt"])
        self.assertTrue(result.data["retryable"])

    def test_asset_deletion_uses_the_persisted_bucket_and_key(self) -> None:
        task_id = "00000000-0000-4000-8000-000000000001"
        asset_id = "00000000-0000-4000-8000-000000000002"
        request = Envelope.create(
            ASSETS_DELETE_REQUESTED_V1,
            f"task/{task_id}",
            {
                "task_id": task_id,
                "assets": [
                    {
                        "asset_id": asset_id,
                        "bucket": "visoraft-media",
                        "object_key": f"tasks/{task_id}/source/source.mp4",
                    }
                ],
            },
        )
        storage = Mock()
        self.worker.storage = storage

        result = self.worker._handle_asset_deletion(request)

        self.assertEqual(result.type, ASSETS_DELETED_V1)
        self.assertEqual(result.data["asset_ids"], [asset_id])
        self.assertEqual(
            [
                call(f"tasks/{task_id}/source/source.mp4", "visoraft-media"),
                call(f"tasks/{task_id}/subtitles/asr-checkpoint.json"),
            ],
            storage.delete_object.call_args_list,
        )

    def test_asset_deletion_rejects_keys_outside_the_task_prefix(self) -> None:
        task_id = "00000000-0000-4000-8000-000000000001"
        request = Envelope.create(
            ASSETS_DELETE_REQUESTED_V1,
            f"task/{task_id}",
            {
                "task_id": task_id,
                "assets": [
                    {
                        "asset_id": "00000000-0000-4000-8000-000000000002",
                        "bucket": "visoraft-media",
                        "object_key": "tasks/another-task/source.mp4",
                    }
                ],
            },
        )

        with self.assertRaises(InvalidEnvelope):
            self.worker._handle_asset_deletion(request)

    def test_extraction_failure_crosses_cookie_context_as_failed_event(self) -> None:
        task_id = "00000000-0000-4000-8000-000000000003"
        request = Envelope.create(
            METADATA_REQUESTED_V1,
            f"task/{task_id}",
            {
                "task_id": task_id,
                "source_url": "https://www.youtube.com/watch?v=test",
                "attempt": 1,
            },
        )
        channel = Mock()
        self.worker.extractor.extract = Mock(
            side_effect=ExtractionFailure(
                code="source_auth_required",
                message="请配置 Cookie 后重试",
                retryable=True,
            )
        )

        result = self.worker._handle_metadata(channel, request)

        self.assertEqual(result.type, METADATA_FAILED_V1)
        self.assertEqual(result.data["code"], "source_auth_required")
        self.assertTrue(result.data["retryable"])

    def test_download_result_contains_normalized_media_info(self) -> None:
        task_id = "00000000-0000-4000-8000-000000000004"
        request = Envelope.create(
            DOWNLOAD_REQUESTED_V1,
            f"task/{task_id}",
            {
                "task_id": task_id,
                "source_url": "https://example.com/video",
                "attempt": 1,
            },
        )
        channel = Mock()

        def create_download(_url, destination, *_args):
            path = destination / "source.mp4"
            path.write_bytes(b"media")
            return DownloadedFile(
                path=path,
                original_name="source.mp4",
                content_type="video/mp4",
                size_bytes=5,
                checksum_sha256=hashlib.sha256(b"media").hexdigest(),
            )

        self.worker.downloader.download = Mock(side_effect=create_download)
        self.worker.inspector.inspect = Mock(
            return_value=MediaProbeResult(
                format_name="mov,mp4",
                duration_seconds=2.5,
                size_bytes=5,
                bit_rate=1280,
                video_codec="h264",
                width=1280,
                height=720,
                pixel_format="yuv420p",
                frame_rate="25/1",
                audio_codec="aac",
                sample_rate=48000,
                channels=2,
                channel_layout="stereo",
                stream_count=2,
            )
        )
        storage = Mock()
        storage.bucket = "test"
        self.worker.storage = storage
        self.worker.cancellation.is_cancelled = Mock(return_value=False)

        result = self.worker._handle_download(channel, request)

        self.assertEqual(result.type, DOWNLOAD_COMPLETED_V1)
        self.assertEqual(result.data["media_info"]["schema_version"], 1)
        self.assertEqual(result.data["media_info"]["video_codec"], "h264")
        self.assertEqual(result.data["media_info"]["width"], 1280)
        storage.upload_file.assert_called_once()

    def test_media_probe_failure_is_a_first_class_event(self) -> None:
        task_id = "00000000-0000-4000-8000-000000000005"
        request = Envelope.create(
            DOWNLOAD_REQUESTED_V1,
            f"task/{task_id}",
            {
                "task_id": task_id,
                "source_url": "https://example.com/video",
                "attempt": 2,
            },
        )
        channel = Mock()

        def create_download(_url, destination, *_args):
            path = destination / "source.bin"
            path.write_bytes(b"not-media")
            return DownloadedFile(
                path=path,
                original_name="source.bin",
                content_type="application/octet-stream",
                size_bytes=9,
                checksum_sha256=hashlib.sha256(b"not-media").hexdigest(),
            )

        self.worker.downloader.download = Mock(side_effect=create_download)
        self.worker.inspector.inspect = Mock(
            side_effect=MediaProbeFailure(
                "media_probe_failed",
                "ffprobe 无法识别媒体",
                False,
            )
        )
        self.worker.cancellation.is_cancelled = Mock(return_value=False)

        result = self.worker._handle_download(channel, request)

        self.assertEqual(result.type, MEDIA_INSPECT_FAILED_V1)
        self.assertEqual(result.data["code"], "media_probe_failed")
        self.assertEqual(result.data["attempt"], 2)
        self.assertFalse(result.data["retryable"])

    def test_transcode_handler_publishes_started_and_progress_before_result(
        self,
    ) -> None:
        task_id = "00000000-0000-4000-8000-000000000008"
        run_id = "00000000-0000-4000-8000-000000000009"
        request = Envelope.create(
            TRANSCODE_REQUESTED_V1,
            f"task/{task_id}",
            {
                "task_id": task_id,
                "run_id": run_id,
                "attempt": 2,
            },
        )
        channel = Mock()
        config = {"runtime": {"source_asset": {"id": "source-asset"}}}
        self.worker.processing_config.get = Mock(return_value=config)
        self.worker._publish = Mock()

        def transcode(
            received_task_id,
            received_config,
            _working_dir,
            _should_cancel,
            report_progress,
        ):
            self.assertEqual(received_task_id, task_id)
            self.assertIs(received_config, config)
            report_progress(2)
            report_progress(4)
            report_progress(42)
            return {
                "task_id": task_id,
                "asset": {
                    "asset_id": "00000000-0000-4000-8000-000000000010",
                    "kind": "transcoded",
                },
                "resolved_video_encoder": "libopenh264",
            }

        self.worker.transcoder.process = Mock(side_effect=transcode)

        result = self.worker._handle_transcode(channel, request)

        self.assertEqual(result.type, TRANSCODE_COMPLETED_V1)
        self.assertEqual(result.data["task_id"], task_id)
        self.assertEqual(result.data["run_id"], run_id)
        self.assertEqual(result.data["attempt"], 2)
        published = [
            call.args[1]
            for call in self.worker._publish.call_args_list
        ]
        self.assertEqual(
            [event.type for event in published],
            [
                TRANSCODE_STARTED_V1,
                TRANSCODE_PROGRESS_V1,
                TRANSCODE_PROGRESS_V1,
            ],
        )
        self.assertEqual([event.data.get("progress") for event in published[1:]], [4, 42])

    def test_transcode_failure_preserves_machine_readable_recovery_fields(
        self,
    ) -> None:
        task_id = "00000000-0000-4000-8000-000000000011"
        run_id = "00000000-0000-4000-8000-000000000012"
        request = Envelope.create(
            TRANSCODE_REQUESTED_V1,
            f"task/{task_id}",
            {
                "task_id": task_id,
                "run_id": run_id,
                "attempt": 3,
            },
        )
        self.worker.processing_config.get = Mock(return_value={"runtime": {}})
        self.worker.transcoder.process = Mock(
            side_effect=TranscodeFailure(
                "transcode_encoder_unavailable",
                "当前媒体镜像不包含请求的编码器",
                False,
            )
        )
        self.worker._publish = Mock()

        result = self.worker._handle_transcode(Mock(), request)

        self.assertEqual(result.type, TRANSCODE_FAILED_V1)
        self.assertEqual(result.data["task_id"], task_id)
        self.assertEqual(result.data["run_id"], run_id)
        self.assertEqual(result.data["attempt"], 3)
        self.assertEqual(result.data["code"], "transcode_encoder_unavailable")
        self.assertFalse(result.data["retryable"])
        self.assertEqual(
            self.worker._publish.call_args.args[1].type,
            TRANSCODE_STARTED_V1,
        )

    def test_consumer_callback_keeps_the_connection_thread_free(self) -> None:
        started = threading.Event()
        release = threading.Event()
        consumer_channel = Mock()
        consumer_channel.is_open = True
        consumer_connection = Mock()
        consumer_connection.is_open = True
        consumer_connection.add_callback_threadsafe.side_effect = lambda callback: callback()
        publisher_connection = Mock()
        publisher_connection.is_open = True
        publisher_channel = Mock()
        self.worker._connection = consumer_connection
        self.worker._open_publisher = Mock(
            return_value=(publisher_connection, publisher_channel)
        )

        def handle_metadata(_channel, request):
            started.set()
            self.assertTrue(release.wait(timeout=2))
            return Envelope.create(
                METADATA_COMPLETED_V1,
                request.subject,
                {"title": "threaded"},
            )

        self.worker._handle_metadata = Mock(side_effect=handle_metadata)
        request = Envelope.create(
            METADATA_REQUESTED_V1,
            "task/00000000-0000-4000-8000-000000000006",
            {
                "task_id": "00000000-0000-4000-8000-000000000006",
                "source_url": "https://example.com/video",
                "attempt": 1,
            },
        )

        self.worker._on_message(
            consumer_channel,
            SimpleNamespace(delivery_tag=7),
            Mock(),
            request.encode(),
        )

        self.assertTrue(started.wait(timeout=1))
        with self.worker._active_job_lock:
            active_job = self.worker._active_job
        self.assertIsNotNone(active_job)
        self.assertTrue(active_job.is_alive())
        release.set()
        active_job.join(timeout=2)

        self.assertFalse(active_job.is_alive())
        consumer_channel.basic_ack.assert_called_once_with(delivery_tag=7)
        consumer_channel.basic_nack.assert_not_called()
        publisher_channel.basic_publish.assert_called_once()

    def test_worker_shutdown_requeues_without_publishing_user_cancellation(self) -> None:
        started = threading.Event()
        release = threading.Event()
        consumer_channel = Mock()
        consumer_channel.is_open = True
        consumer_connection = Mock()
        consumer_connection.is_open = True
        consumer_connection.add_callback_threadsafe.side_effect = lambda callback: callback()
        publisher_connection = Mock()
        publisher_connection.is_open = True
        publisher_channel = Mock()
        self.worker._connection = consumer_connection
        self.worker._consumer_channel = consumer_channel
        self.worker._open_publisher = Mock(
            return_value=(publisher_connection, publisher_channel)
        )

        def handle_metadata(_channel, request):
            started.set()
            self.assertTrue(release.wait(timeout=2))
            return Envelope.create(
                METADATA_COMPLETED_V1,
                request.subject,
                {"title": "interrupted"},
            )

        self.worker._handle_metadata = Mock(side_effect=handle_metadata)
        request = Envelope.create(
            METADATA_REQUESTED_V1,
            "task/00000000-0000-4000-8000-000000000007",
            {
                "task_id": "00000000-0000-4000-8000-000000000007",
                "source_url": "https://example.com/video",
                "attempt": 1,
            },
        )
        self.worker._on_message(
            consumer_channel,
            SimpleNamespace(delivery_tag=8),
            Mock(),
            request.encode(),
        )

        self.assertTrue(started.wait(timeout=1))
        with self.worker._active_job_lock:
            active_job = self.worker._active_job
        self.worker.stop()
        release.set()
        active_job.join(timeout=2)

        consumer_channel.basic_ack.assert_not_called()
        consumer_channel.basic_nack.assert_called_once_with(
            delivery_tag=8,
            requeue=True,
        )
        publisher_channel.basic_publish.assert_not_called()
        consumer_channel.stop_consuming.assert_called_once()


if __name__ == "__main__":
    unittest.main()
