from __future__ import annotations

import json
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from unittest.mock import Mock, patch

from visoraft_media.subtitle_processor import (
    SubtitleProcessor,
    SubtitleFailure,
    _chat_json,
    _compose_prompt,
    _decode_model_json,
    _effective_model,
    _json_request,
    _quality_check_sample,
    _transcribe_aliyun_paraformer,
    _segment_batches,
    deterministic_segmentation,
    format_translated_segments,
    parse_vtt,
    postprocess_segments,
    quality_report,
    render_srt,
    render_vtt,
)


class _CheckpointStorage:
    bucket = "test"

    def __init__(self) -> None:
        self.objects: dict[str, bytes] = {}

    def upload_file(
        self,
        path: Path,
        object_key: str,
        _content_type: str,
        _metadata: dict[str, str],
    ) -> None:
        self.objects[object_key] = path.read_bytes()

    def download_file(
        self,
        object_key: str,
        destination: Path,
        _bucket: str,
    ) -> None:
        destination.write_bytes(self.objects[object_key])

    def download_file_if_exists(self, object_key: str, destination: Path) -> bool:
        raw = self.objects.get(object_key)
        if raw is None:
            return False
        destination.write_bytes(raw)
        return True


class _AliyunContractHandler(BaseHTTPRequestHandler):
    poll_count = 0
    fail_subtask = False

    def log_message(self, *_: object) -> None:
        return

    def _json(self, status: int, value: dict[str, object]) -> None:
        raw = json.dumps(value).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self) -> None:
        if self.path.startswith("/api/v1/uploads?"):
            host = f"http://127.0.0.1:{self.server.server_port}"
            self._json(
                200,
                {
                    "data": {
                        "upload_host": host + "/upload",
                        "upload_dir": "temporary/test",
                        "oss_access_key_id": "access-id",
                        "signature": "signature",
                        "policy": "policy",
                        "x_oss_object_acl": "private",
                        "x_oss_forbid_overwrite": "true",
                        "max_file_size_mb": 10,
                    }
                },
            )
            return
        if self.path == "/api/v1/tasks/task-1":
            type(self).poll_count += 1
            if type(self).poll_count == 1:
                self._json(503, {"code": "ServiceUnavailable", "message": "retry"})
                return
            host = f"http://127.0.0.1:{self.server.server_port}"
            result = {
                "subtask_status": "FAILED" if type(self).fail_subtask else "SUCCEEDED",
                "message": "bad audio" if type(self).fail_subtask else "",
                "transcription_url": host + "/result",
            }
            self._json(
                200,
                {"output": {"task_status": "SUCCEEDED", "results": [result]}},
            )
            return
        if self.path == "/result":
            self._json(
                200,
                {
                    "transcripts": [
                        {
                            "sentences": [
                                {
                                    "begin_time": 125,
                                    "end_time": 1625,
                                    "text": "测试字幕",
                                }
                            ]
                        }
                    ]
                },
            )
            return
        self._json(404, {"message": "not found"})

    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length)
        if self.path == "/upload":
            if b'name="OSSAccessKeyId"' not in raw or b'name="file"' not in raw:
                self._json(400, {"message": "invalid multipart"})
                return
            self.send_response(200)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        if self.path == "/api/v1/services/audio/asr/transcription":
            value = json.loads(raw)
            if (
                self.headers.get("X-DashScope-Async") != "enable"
                or self.headers.get("X-DashScope-OssResourceResolve") != "enable"
                or not value["input"]["file_urls"][0].startswith("oss://")
            ):
                self._json(400, {"message": "invalid submission"})
                return
            self._json(
                200,
                {"output": {"task_status": "PENDING", "task_id": "task-1"}},
            )
            return
        self._json(404, {"message": "not found"})


class SubtitleProcessorFunctionsTest(unittest.TestCase):
    def _run_aliyun_contract(self, *, fail_subtask: bool) -> list[dict[str, object]]:
        _AliyunContractHandler.poll_count = 0
        _AliyunContractHandler.fail_subtask = fail_subtask
        server = ThreadingHTTPServer(("127.0.0.1", 0), _AliyunContractHandler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory() as raw_dir:
                audio_path = Path(raw_dir) / "audio.wav"
                audio_path.write_bytes(b"RIFF-test-audio")
                return _transcribe_aliyun_paraformer(
                    audio_path,
                    {
                        "base_url": f"http://127.0.0.1:{server.server_port}/api/v1",
                        "model": "paraformer-v2",
                        "language": "zh",
                        "timeout_seconds": 10,
                        "max_retries": 1,
                    },
                    "test-key",
                    lambda: False,
                )
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

    def test_aliyun_paraformer_contract_recovers_and_maps_timestamps(self) -> None:
        result = self._run_aliyun_contract(fail_subtask=False)
        self.assertEqual(
            [{"index": 1, "start": 0.125, "end": 1.625, "text": "测试字幕"}],
            result,
        )
        self.assertEqual(2, _AliyunContractHandler.poll_count)

    def test_aliyun_paraformer_rejects_failed_file_subtask(self) -> None:
        with self.assertRaises(SubtitleFailure) as raised:
            self._run_aliyun_contract(fail_subtask=True)
        self.assertEqual("asr_result_failed", raised.exception.code)
        self.assertFalse(raised.exception.retryable)

    def test_parse_postprocess_and_render_round_trip(self) -> None:
        parsed = parse_vtt(
            "WEBVTT\n\n"
            "00:00:00.000 --> 00:00:00.300\n"
            "<b>Hello</b>\n\n"
            "00:00:00.350 --> 00:00:01.200\n"
            "world\n"
        )
        processed = postprocess_segments(
            parsed,
            {
                "minimum_cue_seconds": 0.7,
                "merge_gap_seconds": 0.15,
                "minimum_text_length": 1,
                "maximum_characters_per_line": 24,
                "maximum_lines": 2,
                "normalize_punctuation": True,
            },
        )
        self.assertEqual(1, len(processed))
        self.assertEqual("Hello world", processed[0]["text"])
        self.assertIn("00:00:00.000 --> 00:00:01.200", render_vtt(processed))
        self.assertIn("00:00:00,000 --> 00:00:01,200", render_srt(processed))

    def test_deterministic_segmentation_limits_long_cue(self) -> None:
        segments = [
            {
                "index": 1,
                "start": 0.0,
                "end": 12.0,
                "text": "This is a long sentence that should be split at readable boundaries.",
            }
        ]
        result = deterministic_segmentation(
            segments,
            {"maximum_cue_seconds": 4, "maximum_cps": 18},
        )
        self.assertGreaterEqual(len(result), 3)
        self.assertEqual(list(range(1, len(result) + 1)), [item["index"] for item in result])
        self.assertTrue(
            all(item["end"] - item["start"] <= 4.001 for item in result)
        )

    def test_smart_segmentation_batches_by_characters_and_time_window(self) -> None:
        segments = [
            {"index": 1, "start": 0.0, "end": 20.0, "text": "a" * 400},
            {"index": 2, "start": 20.0, "end": 40.0, "text": "b" * 400},
            {"index": 3, "start": 220.0, "end": 240.0, "text": "c" * 10},
        ]
        batches = _segment_batches(segments, 500, 180)
        self.assertEqual([[1], [2], [3]], [[item["index"] for item in batch] for batch in batches])

    def test_asr_checkpoint_round_trip_reuses_paid_result(self) -> None:
        storage = _CheckpointStorage()
        processor = SubtitleProcessor(storage)  # type: ignore[arg-type]
        source = {"checksum_sha256": "a" * 64}
        asr = {"provider": "aliyun_paraformer", "model": "paraformer-v2"}
        segments = [{"index": 1, "start": 0.0, "end": 1.5, "text": "已转写"}]
        with tempfile.TemporaryDirectory() as raw_dir:
            working_dir = Path(raw_dir)
            processor._save_asr_checkpoint("task-1", source, asr, segments, working_dir)
            (working_dir / "asr-checkpoint.json").unlink()
            loaded = processor._load_asr_checkpoint("task-1", source, asr, working_dir)
        self.assertEqual(segments, loaded)

    def test_process_reuses_asr_checkpoint_after_downstream_failure(self) -> None:
        storage = _CheckpointStorage()
        storage.objects["tasks/task-1/source.mp4"] = b"fixture-media"
        processor = SubtitleProcessor(storage)  # type: ignore[arg-type]
        source_segments = [
            {"index": 1, "start": 0.0, "end": 1.5, "text": "已转写"}
        ]
        transcribe = Mock(return_value=source_segments)
        segment = Mock(
            side_effect=[
                SubtitleFailure("model_request_failed", "读取响应超时", True),
                source_segments,
            ]
        )
        processor._transcribe = transcribe  # type: ignore[method-assign]
        processor._segment = segment  # type: ignore[method-assign]
        config = {
            "runtime": {
                "source_url": "https://example.invalid/watch?v=task-1",
                "source_asset": {
                    "bucket": "test",
                    "object_key": "tasks/task-1/source.mp4",
                    "original_name": "source.mp4",
                    "checksum_sha256": "a" * 64,
                },
            },
            "subtitle": {
                "source_strategy": "asr_only",
                "source_language": "zh",
                "target_language": "zh",
                "asr": {
                    "enabled": True,
                    "provider": "aliyun_paraformer",
                    "model": "paraformer-v2",
                },
                "postprocess": {},
                "segmentation": {"enabled": True},
                "translation": {"enabled": False},
                "qc": {"enabled": False},
            },
            "models": {},
            "prompts": {},
            "secrets": {},
        }
        first_progress: list[tuple[int, str]] = []
        second_progress: list[tuple[int, str]] = []
        with tempfile.TemporaryDirectory() as first_raw_dir:
            with self.assertRaises(SubtitleFailure) as raised:
                processor.process(
                    "task-1",
                    config,
                    Path(first_raw_dir),
                    None,
                    lambda: False,
                    lambda progress, phase, _detail: first_progress.append(
                        (progress, phase)
                    ),
                )
        self.assertEqual("model_request_failed", raised.exception.code)
        self.assertIn((38, "asr_completed"), first_progress)
        self.assertIn(
            "tasks/task-1/subtitles/asr-checkpoint.json",
            storage.objects,
        )

        with tempfile.TemporaryDirectory() as second_raw_dir:
            result = processor.process(
                "task-1",
                config,
                Path(second_raw_dir),
                None,
                lambda: False,
                lambda progress, phase, _detail: second_progress.append(
                    (progress, phase)
                ),
            )

        self.assertEqual(1, transcribe.call_count)
        self.assertEqual(2, segment.call_count)
        self.assertIn((38, "asr_checkpoint_reused"), second_progress)
        self.assertNotIn((10, "audio_extracting"), second_progress)
        self.assertEqual("original", result["documents"][0]["kind"])

    def test_process_reuses_segmentation_checkpoint_after_translation_failure(self) -> None:
        storage = _CheckpointStorage()
        storage.objects["tasks/task-1/source.mp4"] = b"fixture-media"
        processor = SubtitleProcessor(storage)  # type: ignore[arg-type]
        source_segments = [
            {"index": 1, "start": 0.0, "end": 1.5, "text": "source"}
        ]
        translated_segments = [
            {"index": 1, "start": 0.0, "end": 1.5, "text": "译文"}
        ]
        processor._transcribe = Mock(return_value=source_segments)  # type: ignore[method-assign]
        segment = Mock(return_value=source_segments)
        translate = Mock(
            side_effect=[
                SubtitleFailure("model_response_invalid", "无效 JSON", True),
                translated_segments,
            ]
        )
        processor._segment = segment  # type: ignore[method-assign]
        processor._translate = translate  # type: ignore[method-assign]
        config = {
            "runtime": {
                "source_asset": {
                    "bucket": "test",
                    "object_key": "tasks/task-1/source.mp4",
                    "original_name": "source.mp4",
                    "checksum_sha256": "a" * 64,
                },
            },
            "subtitle": {
                "source_strategy": "asr_only",
                "source_language": "en",
                "target_language": "zh",
                "asr": {
                    "enabled": True,
                    "provider": "aliyun_paraformer",
                    "model": "paraformer-v2",
                },
                "postprocess": {},
                "segmentation": {"enabled": True},
                "translation": {"enabled": True, "batch_size": 20},
                "qc": {"enabled": False},
            },
            "models": {},
            "prompts": {},
            "secrets": {},
        }
        with tempfile.TemporaryDirectory() as first_raw_dir:
            with self.assertRaises(SubtitleFailure):
                processor.process(
                    "task-1",
                    config,
                    Path(first_raw_dir),
                    None,
                    lambda: False,
                )
        progress: list[str] = []
        with tempfile.TemporaryDirectory() as second_raw_dir:
            result = processor.process(
                "task-1",
                config,
                Path(second_raw_dir),
                None,
                lambda: False,
                lambda _progress, phase, _detail: progress.append(phase),
            )
        self.assertEqual(1, segment.call_count)
        self.assertEqual(2, translate.call_count)
        self.assertIn("smart_segmentation_checkpoint_reused", progress)
        self.assertEqual("translated", result["documents"][1]["kind"])

    def test_model_translation_is_not_marked_as_human_edited(self) -> None:
        storage = _CheckpointStorage()
        storage.objects["tasks/task-1/source.mp4"] = b"fixture-media"
        processor = SubtitleProcessor(storage)  # type: ignore[arg-type]
        segments = [{"index": 1, "start": 0.0, "end": 1.5, "text": "source"}]
        processor._transcribe = Mock(return_value=segments)  # type: ignore[method-assign]
        processor._segment = Mock(return_value=segments)  # type: ignore[method-assign]
        processor._translate = Mock(  # type: ignore[method-assign]
            return_value=[{**segments[0], "text": "译文"}]
        )
        config = {
            "runtime": {
                "source_asset": {
                    "bucket": "test",
                    "object_key": "tasks/task-1/source.mp4",
                    "original_name": "source.mp4",
                    "checksum_sha256": "a" * 64,
                }
            },
            "subtitle": {
                "source_strategy": "asr_only",
                "source_language": "en",
                "target_language": "zh",
                "asr": {
                    "enabled": True,
                    "provider": "aliyun_paraformer",
                    "model": "paraformer-v2",
                },
                "postprocess": {},
                "segmentation": {"enabled": True},
                "translation": {"enabled": True, "batch_size": 20},
                "qc": {"enabled": False},
            },
            "models": {
                "global": {
                    "provider": "openai_compatible",
                    "base_url": "https://example.invalid/v1",
                    "model": "test-model",
                },
                "subtitle_translation": {"mode": "inherit"},
            },
            "prompts": {},
            "secrets": {},
        }
        with tempfile.TemporaryDirectory() as raw_dir:
            result = processor.process(
                "task-1",
                config,
                Path(raw_dir),
                None,
                lambda: False,
            )
        self.assertEqual("model", result["documents"][1]["source"])

    def test_translate_reuses_completed_batches_and_accepts_segments_alias(self) -> None:
        processor = SubtitleProcessor(_CheckpointStorage())  # type: ignore[arg-type]
        segments = [
            {"index": 1, "start": 0.0, "end": 1.0, "text": "one"},
            {"index": 2, "start": 1.0, "end": 2.0, "text": "two"},
            {"index": 3, "start": 2.0, "end": 3.0, "text": "three"},
        ]
        payload_indexes: list[list[int]] = []
        checkpoints: list[dict[int, str]] = []

        def respond(
            _endpoint: dict[str, object],
            _secret_key: str,
            _secrets: dict[str, object],
            _prompt: str,
            payload: dict[str, object],
            **_kwargs: object,
        ) -> dict[str, object]:
            batch = payload["segments"]
            assert isinstance(batch, list)
            payload_indexes.append([int(item["index"]) for item in batch])
            return {
                "segments": [
                    {"index": item["index"], "text": f"译-{item['text']}"}
                    for item in batch
                ]
            }

        with patch(
            "visoraft_media.subtitle_processor._chat_json",
            side_effect=respond,
        ):
            result = processor._translate(
                segments,
                "zh",
                {"batch_size": 2, "max_retries": 0},
                {
                    "global": {
                        "provider": "openai_compatible",
                        "base_url": "https://example.invalid/v1",
                        "model": "fixture-model",
                    },
                    "subtitle_translation": {"mode": "inherit"},
                },
                {"model.global.api_key": "test-key"},
                {},
                initial_translations={1: "译-one", 2: "译-two"},
                on_checkpoint=lambda value: checkpoints.append(value),
            )
        self.assertEqual([[3]], payload_indexes)
        self.assertEqual(["译-one", "译-two", "译-three"], [item["text"] for item in result])
        self.assertEqual("译-three", checkpoints[-1][3])

    def test_process_forwards_model_batch_detail_to_worker_callback(self) -> None:
        storage = _CheckpointStorage()
        storage.objects["tasks/task-1/source.mp4"] = b"fixture-media"
        processor = SubtitleProcessor(storage)  # type: ignore[arg-type]
        processor._transcribe = Mock(  # type: ignore[method-assign]
            return_value=[
                {"index": 1, "start": 0.0, "end": 1.5, "text": "已转写"}
            ]
        )
        progress_events: list[tuple[int, str, dict[str, object]]] = []
        config = {
            "runtime": {
                "source_asset": {
                    "bucket": "test",
                    "object_key": "tasks/task-1/source.mp4",
                    "original_name": "source.mp4",
                    "checksum_sha256": "a" * 64,
                },
            },
            "subtitle": {
                "source_strategy": "asr_only",
                "source_language": "zh",
                "target_language": "zh",
                "asr": {
                    "enabled": True,
                    "provider": "aliyun_paraformer",
                    "model": "paraformer-v2",
                },
                "postprocess": {},
                "segmentation": {"enabled": True, "max_retries": 2},
                "translation": {"enabled": False},
                "qc": {"enabled": False},
            },
            "models": {
                "global": {
                    "provider": "openai_compatible",
                    "base_url": "https://example.invalid/v1",
                    "model": "fixture-model",
                },
                "smart_segmentation": {"mode": "inherit"},
            },
            "prompts": {},
            "secrets": {"model.global.api_key": "test-key"},
        }

        def respond(
            _endpoint: dict[str, object],
            _secret_key: str,
            _secrets: dict[str, object],
            _prompt: str,
            payload: dict[str, object],
            **kwargs: object,
        ) -> dict[str, object]:
            on_attempt = kwargs.get("on_attempt")
            assert callable(on_attempt)
            on_attempt(1, 3)
            return {"segments": payload["segments"]}

        with tempfile.TemporaryDirectory() as raw_dir, patch(
            "visoraft_media.subtitle_processor._chat_json",
            side_effect=respond,
        ):
            processor.process(
                "task-1",
                config,
                Path(raw_dir),
                None,
                lambda: False,
                lambda progress, phase, detail: progress_events.append(
                    (progress, phase, detail)
                ),
            )

        batch_events = [
            detail
            for _, phase, detail in progress_events
            if phase == "smart_segmentation" and detail.get("batch_count")
        ]
        self.assertTrue(batch_events)
        self.assertEqual(1, batch_events[0]["batch_index"])
        self.assertEqual(3, batch_events[0]["model_attempts"])

    def test_smart_segmentation_calls_model_once_per_batch(self) -> None:
        storage = _CheckpointStorage()
        processor = SubtitleProcessor(storage)  # type: ignore[arg-type]
        segments = [
            {"index": 1, "start": 0.0, "end": 20.0, "text": "a" * 400},
            {"index": 2, "start": 20.0, "end": 40.0, "text": "b" * 400},
            {"index": 3, "start": 220.0, "end": 240.0, "text": "c" * 10},
        ]
        model_calls: list[list[int]] = []
        progress_events: list[tuple[int, str, dict[str, object]]] = []

        def respond(
            _endpoint: dict[str, object],
            _secret_key: str,
            _secrets: dict[str, object],
            _prompt: str,
            payload: dict[str, object],
            **_kwargs: object,
        ) -> dict[str, object]:
            batch = payload["segments"]
            assert isinstance(batch, list)
            model_calls.append([int(item["index"]) for item in batch])
            return {"segments": batch}

        with patch(
            "visoraft_media.subtitle_processor._chat_json",
            side_effect=respond,
        ):
            result = processor._segment(
                segments,
                {
                    "maximum_batch_characters": 500,
                    "batch_window_seconds": 180,
                    "max_retries": 2,
                },
                {
                    "global": {
                        "provider": "openai_compatible",
                        "base_url": "https://example.invalid/v1",
                        "model": "fixture-model",
                    },
                    "smart_segmentation": {"mode": "inherit"},
                },
                {"model.global.api_key": "test-key"},
                {},
                lambda progress, phase, detail: progress_events.append(
                    (progress, phase, detail)
                ),
            )

        self.assertEqual([[1], [2], [3]], model_calls)
        self.assertEqual([1, 2, 3], [item["index"] for item in result])
        self.assertEqual(
            [1, 2, 3],
            [
                int(detail["completed_batches"])
                for _, _, detail in progress_events
            ],
        )
        self.assertTrue(
            all(phase == "smart_segmentation" for _, phase, _ in progress_events)
        )

    def test_smart_segmentation_splits_timed_out_batch_and_recovers(self) -> None:
        processor = SubtitleProcessor(_CheckpointStorage())  # type: ignore[arg-type]
        segments = [
            {"index": 1, "start": 0.0, "end": 10.0, "text": "first"},
            {"index": 2, "start": 10.0, "end": 20.0, "text": "second"},
            {"index": 3, "start": 20.0, "end": 30.0, "text": "third"},
        ]
        model_calls: list[list[int]] = []
        model_retries: list[int] = []
        progress_events: list[dict[str, object]] = []

        def respond(
            _endpoint: dict[str, object],
            _secret_key: str,
            _secrets: dict[str, object],
            _prompt: str,
            payload: dict[str, object],
            **_kwargs: object,
        ) -> dict[str, object]:
            batch = payload["segments"]
            assert isinstance(batch, list)
            indexes = [int(item["index"]) for item in batch]
            model_calls.append(indexes)
            model_retries.append(int(_kwargs["retries"]))
            if len(batch) > 1:
                raise SubtitleFailure(
                    "model_request_failed",
                    "模型请求失败：The read operation timed out",
                    True,
                )
            return {"segments": batch}

        with patch(
            "visoraft_media.subtitle_processor._chat_json",
            side_effect=respond,
        ):
            result = processor._segment(
                segments,
                {
                    "maximum_batch_characters": 6000,
                    "batch_window_seconds": 180,
                    "max_retries": 2,
                },
                {
                    "global": {
                        "provider": "openai_compatible",
                        "base_url": "https://example.invalid/v1",
                        "model": "fixture-model",
                    },
                    "smart_segmentation": {"mode": "inherit"},
                },
                {"model.global.api_key": "test-key"},
                {},
                lambda _progress, _phase, detail: progress_events.append(detail),
            )

        self.assertEqual(
            [[1, 2, 3], [1], [2, 3], [2], [3]],
            model_calls,
        )
        self.assertEqual([0, 2, 0, 2, 2], model_retries)
        self.assertEqual([1, 2, 3], [item["index"] for item in result])
        self.assertEqual(2, sum(bool(item.get("batch_split")) for item in progress_events))

    def test_json_request_reports_each_retry_attempt(self) -> None:
        attempts: list[tuple[int, int]] = []
        with patch(
            "visoraft_media.subtitle_processor._request_bytes_with_deadline",
            side_effect=[
                TimeoutError("first timeout"),
                TimeoutError("second timeout"),
                json.dumps({"ok": True}).encode("utf-8"),
            ],
        ), patch("visoraft_media.subtitle_processor.time.sleep"):
            result = _json_request(
                "https://example.invalid/v1/test",
                {"input": "test"},
                "test-key",
                timeout=5,
                retries=2,
                on_attempt=lambda attempt, total: attempts.append((attempt, total)),
            )

        self.assertEqual({"ok": True}, result)
        self.assertEqual([(1, 3), (2, 3), (3, 3)], attempts)

    def test_json_request_enforces_total_wall_clock_timeout(self) -> None:
        class SlowResponseHandler(BaseHTTPRequestHandler):
            stop_event = threading.Event()

            def log_message(self, *_: object) -> None:
                return

            def do_POST(self) -> None:
                raw = b'{"ok":"slow"}'
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                for byte in raw:
                    if self.stop_event.is_set():
                        break
                    try:
                        self.wfile.write(bytes([byte]))
                        self.wfile.flush()
                    except OSError:
                        break
                    if self.stop_event.wait(0.2):
                        break

        SlowResponseHandler.stop_event.clear()
        server = ThreadingHTTPServer(("127.0.0.1", 0), SlowResponseHandler)
        server.daemon_threads = False
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        started_at = time.monotonic()
        try:
            with self.assertRaises(SubtitleFailure) as raised:
                _json_request(
                    f"http://127.0.0.1:{server.server_port}/slow",
                    {"input": "test"},
                    "",
                    timeout=1,
                    retries=0,
                )
        finally:
            elapsed = time.monotonic() - started_at
            SlowResponseHandler.stop_event.set()
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

        self.assertEqual("model_request_failed", raised.exception.code)
        self.assertIn("total request timeout exceeded (1s)", raised.exception.message)
        self.assertLess(elapsed, 1.8)

    def test_json_request_stops_after_complete_json_on_open_chunked_connection(self) -> None:
        class OpenChunkedResponseHandler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"
            stop_event = threading.Event()

            def handle(self) -> None:
                try:
                    super().handle()
                except ConnectionResetError:
                    return

            def log_message(self, *_: object) -> None:
                return

            def do_POST(self) -> None:
                raw = json.dumps({"ok": True}).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Transfer-Encoding", "chunked")
                self.send_header("Connection", "keep-alive")
                self.end_headers()
                self.wfile.write(f"{len(raw):X}\r\n".encode("ascii"))
                self.wfile.write(raw + b"\r\n")
                self.wfile.flush()
                self.stop_event.wait(3)
                self.close_connection = True

        OpenChunkedResponseHandler.stop_event.clear()
        server = ThreadingHTTPServer(
            ("127.0.0.1", 0), OpenChunkedResponseHandler
        )
        server.daemon_threads = False
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        started_at = time.monotonic()
        try:
            result = _json_request(
                f"http://127.0.0.1:{server.server_port}/open-chunk",
                {"input": "test"},
                "",
                timeout=2,
                retries=0,
            )
        finally:
            elapsed = time.monotonic() - started_at
            OpenChunkedResponseHandler.stop_event.set()
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

        self.assertEqual({"ok": True}, result)
        self.assertLess(elapsed, 1.0)

    def test_quality_report_detects_overlap_and_high_cps(self) -> None:
        report = quality_report(
            [
                {"index": 1, "start": 0.0, "end": 1.0, "text": "ok"},
                {
                    "index": 2,
                    "start": 0.5,
                    "end": 0.7,
                    "text": "x" * 40,
                },
            ],
            {"threshold": 90},
            {"minimum_cue_seconds": 0.7},
        )
        self.assertFalse(report["passed"])
        self.assertEqual(1, report["overlap_count"])
        self.assertEqual(1, report["high_cps_count"])
        self.assertLess(report["score"], 90)

    def test_quality_check_sampling_is_distributed_and_bounded(self) -> None:
        original = [
            {
                "index": index,
                "start": float(index),
                "end": float(index + 1),
                "text": f"source-{index}",
            }
            for index in range(1, 101)
        ]
        translated = [
            {**item, "text": f"target-{item['index']}"}
            for item in original
        ]
        sample = _quality_check_sample(
            original,
            translated,
            {"sample_max_items": 5, "maximum_characters": 500},
        )
        self.assertEqual(5, len(sample))
        self.assertEqual([1, 26, 51, 75, 100], [item["index"] for item in sample])
        self.assertLessEqual(
            sum(
                len(item["original"]) + len(item["translated"])
                for item in sample
            ),
            500,
        )

    def test_translated_formatting_preserves_source_cues(self) -> None:
        original = [
            {"index": 1, "start": 1.0, "end": 2.0, "text": "one"},
            {"index": 2, "start": 2.05, "end": 3.0, "text": "two"},
        ]
        translated = [
            {"index": 1, "start": 99.0, "end": 100.0, "text": "第一句 ！"},
            {"index": 2, "start": 100.0, "end": 101.0, "text": "第二句"},
        ]
        result = format_translated_segments(
            original,
            translated,
            {
                "merge_gap_seconds": 5,
                "minimum_cue_seconds": 10,
                "maximum_characters_per_line": 24,
                "maximum_lines": 2,
                "normalize_punctuation": True,
            },
        )
        self.assertEqual(2, len(result))
        self.assertEqual([1, 2], [item["index"] for item in result])
        self.assertEqual(
            [(1.0, 2.0), (2.05, 3.0)],
            [(item["start"], item["end"]) for item in result],
        )
        self.assertEqual("第一句！", result[0]["text"])

    def test_ai_quality_check_fails_when_ai_reports_error(self) -> None:
        processor = SubtitleProcessor(_CheckpointStorage())  # type: ignore[arg-type]
        original = [{"index": 1, "start": 0.0, "end": 2.0, "text": "source"}]
        translated = [{**original[0], "text": "译文"}]
        with patch(
            "visoraft_media.subtitle_processor._chat_json",
            return_value={
                "score": 95,
                "issues": [
                    {"index": 1, "severity": "error", "message": "严重错译"}
                ],
                "summary": "需要修改",
            },
        ):
            result = processor._ai_quality_check(
                original,
                translated,
                {"score": 96, "passed": True},
                {"threshold": 80, "sample_max_items": 10},
                {
                    "global": {
                        "provider": "openai_compatible",
                        "base_url": "https://example.invalid/v1",
                        "model": "fixture-model",
                    },
                    "subtitle_qc": {"mode": "inherit"},
                },
                {"model.global.api_key": "test-key"},
                {},
            )
        self.assertFalse(result["passed"])
        self.assertEqual(79, result["score"])
        self.assertEqual(95, result["ai_score"])

    def test_ai_quality_check_uses_configured_sample_instead_of_full_document(self) -> None:
        processor = SubtitleProcessor(_CheckpointStorage())  # type: ignore[arg-type]
        original = [
            {
                "index": index,
                "start": float(index),
                "end": float(index + 1),
                "text": f"source-{index}",
            }
            for index in range(1, 11)
        ]
        translated = [
            {**item, "text": f"target-{item['index']}"}
            for item in original
        ]
        captured: dict[str, object] = {}

        def respond(
            _endpoint: dict[str, object],
            _secret_key: str,
            _secrets: dict[str, object],
            _prompt: str,
            payload: dict[str, object],
            **_kwargs: object,
        ) -> dict[str, object]:
            captured.update(payload)
            return {"score": 95, "issues": [], "summary": "ok"}

        with patch(
            "visoraft_media.subtitle_processor._chat_json",
            side_effect=respond,
        ):
            result = processor._ai_quality_check(
                original,
                translated,
                {"score": 88},
                {"sample_max_items": 2, "maximum_characters": 500},
                {
                    "global": {
                        "provider": "openai_compatible",
                        "base_url": "https://example.invalid/v1",
                        "model": "fixture-model",
                    },
                    "subtitle_qc": {"mode": "inherit"},
                },
                {"model.global.api_key": "test-key"},
                {},
            )
        self.assertEqual(10, captured["total_segments"])
        self.assertEqual(2, captured["sampled_segments"])
        self.assertEqual(2, len(captured["samples"]))
        self.assertEqual(88, result["score"])
        self.assertEqual(2, result["ai_sample_count"])

    def test_model_overrides_and_prompt_modes(self) -> None:
        models = {
            "global": {
                "model": "global",
                "thinking": True,
                "temperature": 0.8,
                "timeout_seconds": 60,
            },
            "subtitle_translation": {
                "mode": "inherit",
                "thinking": False,
                "temperature": 0.2,
                "timeout_seconds": 90,
            },
            "subtitle_qc": {"mode": "override", "model": "qc"},
            "smart_segmentation": {"mode": "disabled"},
        }
        endpoint, secret = _effective_model(models, "subtitle_translation")
        self.assertEqual("global", endpoint["model"])
        self.assertFalse(endpoint["thinking"])
        self.assertEqual(0.2, endpoint["temperature"])
        self.assertEqual(90, endpoint["timeout_seconds"])
        self.assertEqual("model.global.api_key", secret)
        endpoint, secret = _effective_model(models, "subtitle_qc")
        self.assertEqual("qc", endpoint["model"])
        self.assertEqual("model.subtitle_qc.api_key", secret)
        endpoint, secret = _effective_model(models, "smart_segmentation")
        self.assertEqual({}, endpoint)
        self.assertEqual("", secret)

        prompts = {
            "subtitle_translation": {
                "mode": "append",
                "text": "保留专有名词",
            },
            "subtitle_qc": {"mode": "replace", "text": "只返回质检 JSON"},
        }
        self.assertIn(
            "保留专有名词",
            _compose_prompt("subtitle_translation", "内置提示", prompts),
        )
        self.assertEqual(
            "只返回质检 JSON",
            _compose_prompt("subtitle_qc", "内置提示", prompts),
        )
        self.assertEqual(
            "内置提示",
            _compose_prompt(
                "subtitle_translation",
                "内置提示",
                {
                    "subtitle_translation": {
                        "mode": "append",
                        "text": "????:??????,???????",
                    }
                },
            ),
        )

    def test_decode_model_json_accepts_fenced_and_prefixed_objects(self) -> None:
        self.assertEqual(
            {"translations": []},
            _decode_model_json("```json\n{\"translations\": []}\n```"),
        )
        self.assertEqual(
            {"ok": True},
            _decode_model_json("结果如下：\n{\"ok\": true}\n请查收"),
        )


    def test_chat_json_sends_deepseek_thinking_control(self) -> None:
        response = {
            "choices": [{"message": {"content": json.dumps({"ok": True})}}]
        }
        with patch(
            "visoraft_media.subtitle_processor._json_request",
            return_value=response,
        ) as request:
            result = _chat_json(
                {
                    "provider": "openai_compatible",
                    "base_url": "https://api.deepseek.com",
                    "model": "deepseek-v4-flash",
                    "thinking": False,
                    "temperature": 0.1,
                    "timeout_seconds": 30,
                },
                "model.global.api_key",
                {"model.global.api_key": "test-key"},
                "Return JSON.",
                {"input": "test"},
                retries=0,
            )

        self.assertEqual({"ok": True}, result)
        body = request.call_args.args[1]
        self.assertEqual({"type": "disabled"}, body["thinking"])

        with patch(
            "visoraft_media.subtitle_processor._json_request",
            return_value=response,
        ) as request:
            _chat_json(
                {
                    "provider": "openai_compatible",
                    "base_url": "https://models.example.test/v1",
                    "model": "generic-model",
                    "thinking": False,
                    "temperature": 0.1,
                    "timeout_seconds": 30,
                },
                "model.global.api_key",
                {"model.global.api_key": "test-key"},
                "Return JSON.",
                {"input": "test"},
                retries=0,
            )
        self.assertNotIn("thinking", request.call_args.args[1])

    def test_chat_json_retries_invalid_model_content(self) -> None:
        invalid = {
            "choices": [
                {
                    "finish_reason": "stop",
                    "message": {"content": "{invalid-json"},
                }
            ]
        }
        valid = {
            "choices": [
                {
                    "finish_reason": "stop",
                    "message": {
                        "content": json.dumps({"translations": []})
                    },
                }
            ]
        }
        attempts: list[tuple[int, int]] = []
        with patch(
            "visoraft_media.subtitle_processor._json_request",
            side_effect=[invalid, invalid, valid],
        ) as request, patch("visoraft_media.subtitle_processor.time.sleep"):
            result = _chat_json(
                {
                    "provider": "openai_compatible",
                    "base_url": "https://models.example.test/v1",
                    "model": "generic-model",
                    "timeout_seconds": 30,
                },
                "model.global.api_key",
                {"model.global.api_key": "test-key"},
                "Return JSON.",
                {"input": "test"},
                retries=2,
                on_attempt=lambda current, total: attempts.append(
                    (current, total)
                ),
            )
        self.assertEqual({"translations": []}, result)
        self.assertEqual(3, request.call_count)
        self.assertEqual([(1, 3), (2, 3), (3, 3)], attempts)
        self.assertTrue(
            all(call.kwargs["retries"] == 0 for call in request.call_args_list)
        )


if __name__ == "__main__":
    unittest.main()
