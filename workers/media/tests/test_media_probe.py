from __future__ import annotations

import shutil
import tempfile
import unittest
import wave
from pathlib import Path

from visoraft_media.media_probe import FFprobeInspector, MediaProbeFailure, _normalize_probe


class MediaProbeTests(unittest.TestCase):
    def test_normalize_prefers_average_frame_rate_and_first_audio_video(self) -> None:
        result = _normalize_probe(
            {
                "format": {
                    "format_name": "mov,mp4",
                    "duration": "3.25",
                    "size": "1024",
                    "bit_rate": "2520",
                },
                "streams": [
                    {
                        "codec_type": "video",
                        "codec_name": "h264",
                        "width": 1920,
                        "height": 1080,
                        "pix_fmt": "yuv420p",
                        "avg_frame_rate": "30000/1001",
                    },
                    {
                        "codec_type": "audio",
                        "codec_name": "aac",
                        "sample_rate": "48000",
                        "channels": 2,
                        "channel_layout": "stereo",
                    },
                    {
                        "index": 2,
                        "codec_type": "subtitle",
                        "codec_name": "mov_text",
                        "tags": {"language": "zh-Hans", "title": "简体中文"},
                        "disposition": {"default": 1, "forced": 0},
                    },
                ],
            }
        )

        self.assertEqual(result.video_codec, "h264")
        self.assertEqual(result.frame_rate, "30000/1001")
        self.assertEqual(result.audio_codec, "aac")
        self.assertEqual(result.duration_seconds, 3.25)
        self.assertEqual(result.stream_count, 3)
        self.assertEqual(len(result.subtitle_streams), 1)
        self.assertEqual(result.subtitle_streams[0].language, "zh-Hans")
        self.assertTrue(result.subtitle_streams[0].default)

    def test_missing_streams_is_a_permanent_failure(self) -> None:
        with self.assertRaises(MediaProbeFailure) as caught:
            _normalize_probe({"format": {"format_name": "unknown"}, "streams": []})
        self.assertEqual(caught.exception.code, "media_streams_missing")
        self.assertFalse(caught.exception.retryable)

    @unittest.skipUnless(shutil.which("ffprobe"), "ffprobe is not installed locally")
    def test_real_ffprobe_inspects_a_generated_wave_file(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            media_path = Path(directory) / "probe.wav"
            with wave.open(str(media_path), "wb") as stream:
                stream.setnchannels(1)
                stream.setsampwidth(2)
                stream.setframerate(8000)
                stream.writeframes(b"\x00\x00" * 800)

            result = FFprobeInspector(timeout_seconds=10).inspect(media_path)

        self.assertEqual(result.audio_codec, "pcm_s16le")
        self.assertEqual(result.sample_rate, 8000)
        self.assertEqual(result.channels, 1)
        self.assertEqual(result.stream_count, 1)


if __name__ == "__main__":
    unittest.main()
