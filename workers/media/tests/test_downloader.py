from __future__ import annotations

import hashlib
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from visoraft_media.downloader import (
    DownloadFailure,
    MediaDownloader,
    _resolve_downloaded_path,
    _sha256,
    _telemetry_from_status,
)


class DownloadOutputTests(unittest.TestCase):
    def test_resolve_uses_prepared_file_inside_destination(self) -> None:
        with tempfile.TemporaryDirectory() as raw_dir:
            destination = Path(raw_dir)
            media = destination / "source.mp4"
            media.write_bytes(b"media")

            self.assertEqual(_resolve_downloaded_path(destination, media), media)

    def test_resolve_rejects_missing_output(self) -> None:
        with tempfile.TemporaryDirectory() as raw_dir:
            destination = Path(raw_dir)

            with self.assertRaises(DownloadFailure) as raised:
                _resolve_downloaded_path(destination, destination / "missing.mp4")

            self.assertEqual(raised.exception.code, "download_output_missing")

    def test_sha256_is_streamed_and_exact(self) -> None:
        with tempfile.TemporaryDirectory() as raw_dir:
            path = Path(raw_dir) / "source.mp4"
            payload = b"visoraft-media"
            path.write_bytes(payload)

            self.assertEqual(_sha256(path), hashlib.sha256(payload).hexdigest())

    def test_estimated_total_never_reports_download_complete(self) -> None:
        telemetry = _telemetry_from_status(
            {
                "status": "downloading",
                "downloaded_bytes": 150_000_000,
                "total_bytes_estimate": 120_000_000,
                "speed": 500_000,
                "eta": 30,
            }
        )

        self.assertLess(telemetry.progress, 82)
        self.assertEqual(telemetry.downloaded_bytes, 150_000_000)
        self.assertEqual(telemetry.total_bytes, 120_000_000)
        self.assertTrue(telemetry.total_bytes_is_estimate)
        self.assertEqual(telemetry.speed_bytes_per_second, 500_000)

    def test_finished_download_switches_to_finalizing_phase(self) -> None:
        telemetry = _telemetry_from_status(
            {
                "status": "finished",
                "downloaded_bytes": 200,
                "total_bytes": 200,
            }
        )

        self.assertEqual(telemetry.progress, 82)
        self.assertEqual(telemetry.phase, "finalizing")
        self.assertFalse(telemetry.total_bytes_is_estimate)

    def test_stalled_download_stops_with_retryable_failure(self) -> None:
        captured_options = {}

        class FakeYoutubeDL:
            def __init__(self, options):
                captured_options.update(options)

            def __enter__(self):
                return self

            def __exit__(self, *_args):
                return False

            def extract_info(self, _source_url, *, download):
                self._emit(
                    {
                        "status": "downloading",
                        "filename": "source.mp4",
                        "downloaded_bytes": 128,
                        "total_bytes": 1024,
                    }
                )
                self._emit(
                    {
                        "status": "downloading",
                        "filename": "source.mp4",
                        "downloaded_bytes": 128,
                        "total_bytes": 1024,
                    }
                )
                raise AssertionError("stalled progress should stop yt-dlp")

            def _emit(self, status):
                captured_options["progress_hooks"][0](status)

        downloader = MediaDownloader(
            max_download_bytes=1024 * 1024,
            stall_timeout_seconds=30,
            overall_timeout_seconds=300,
        )
        telemetry = []
        with (
            tempfile.TemporaryDirectory() as raw_dir,
            patch("visoraft_media.downloader.assert_public_source"),
            patch("visoraft_media.downloader.yt_dlp.YoutubeDL", FakeYoutubeDL),
            patch(
                "visoraft_media.downloader.time.monotonic",
                side_effect=[0.0, 0.0, 31.0],
            ),
        ):
            with self.assertRaises(DownloadFailure) as raised:
                downloader.download(
                    "https://example.com/video",
                    Path(raw_dir),
                    telemetry.append,
                    lambda: False,
                )

        self.assertEqual(raised.exception.code, "download_stalled")
        self.assertTrue(raised.exception.retryable)
        self.assertEqual(len(telemetry), 1)
        self.assertEqual(captured_options["retries"], 10)
        self.assertTrue(captured_options["continuedl"])

    def test_overall_timeout_stops_with_retryable_failure(self) -> None:
        class FakeYoutubeDL:
            def __init__(self, options):
                self._progress_hook = options["progress_hooks"][0]

            def __enter__(self):
                return self

            def __exit__(self, *_args):
                return False

            def extract_info(self, _source_url, *, download):
                self._progress_hook(
                    {
                        "status": "downloading",
                        "filename": "source.mp4",
                        "downloaded_bytes": 128,
                        "total_bytes": 1024,
                    }
                )
                raise AssertionError("timed-out progress should stop yt-dlp")

        downloader = MediaDownloader(
            max_download_bytes=1024 * 1024,
            stall_timeout_seconds=30,
            overall_timeout_seconds=30,
        )
        with (
            tempfile.TemporaryDirectory() as raw_dir,
            patch("visoraft_media.downloader.assert_public_source"),
            patch("visoraft_media.downloader.yt_dlp.YoutubeDL", FakeYoutubeDL),
            patch(
                "visoraft_media.downloader.time.monotonic",
                side_effect=[0.0, 31.0],
            ),
        ):
            with self.assertRaises(DownloadFailure) as raised:
                downloader.download(
                    "https://example.com/video",
                    Path(raw_dir),
                    lambda _telemetry: None,
                    lambda: False,
                )

        self.assertEqual(raised.exception.code, "download_timeout")
        self.assertTrue(raised.exception.retryable)


if __name__ == "__main__":
    unittest.main()
