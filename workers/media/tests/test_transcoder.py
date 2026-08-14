from __future__ import annotations

import hashlib
import unittest
from pathlib import Path

from visoraft_media.media_probe import MediaProbeResult
from visoraft_media.transcoder import (
    FFmpegCapabilities,
    TranscodeFailure,
    _parse_filter_names,
    _progress_seconds,
    _validated_asset,
    build_transcode_plan,
)


def media_info(height: int = 1080) -> MediaProbeResult:
    return MediaProbeResult(
        format_name="mov,mp4",
        duration_seconds=120.0,
        size_bytes=1024,
        bit_rate=2_000_000,
        video_codec="h264",
        width=1920,
        height=height,
        pixel_format="yuv420p",
        frame_rate="25/1",
        audio_codec="aac",
        sample_rate=48000,
        channels=2,
        channel_layout="stereo",
        stream_count=2,
    )


def audio_only_info() -> MediaProbeResult:
    return MediaProbeResult(
        format_name="wav",
        duration_seconds=4.0,
        size_bytes=128_000,
        bit_rate=256_000,
        video_codec="",
        width=None,
        height=None,
        pixel_format="",
        frame_rate="",
        audio_codec="pcm_s16le",
        sample_rate=16_000,
        channels=1,
        channel_layout="mono",
        stream_count=1,
    )


class TranscodePlanTests(unittest.TestCase):
    def test_ffmpeg_8_filter_capability_columns_are_parsed(self) -> None:
        names = _parse_filter_names(
            """
Filters:
  T.. = Timeline support
 .. color             |->V       Provide an uniformly colored input.
 TS subtitles         V->V       Render text subtitles onto input video.
 T.C legacy           V->V       Older three-column output.
"""
        )

        self.assertEqual(names, {"color", "subtitles", "legacy"})

    def test_lgpl_cpu_h264_resolves_to_openh264(self) -> None:
        plan = build_transcode_plan(
            "ffmpeg",
            {
                "encoder_mode": "cpu",
                "video_codec": "h264",
                "audio_codec": "aac",
                "container": "mp4",
                "maximum_height": 720,
                "video_bitrate_kbps": 4_000,
                "audio_bitrate_kbps": 192,
                "burn_subtitles": False,
            },
            FFmpegCapabilities(
                encoders=frozenset({"libopenh264", "aac"}),
                filters=frozenset({"scale"}),
            ),
            media_info(),
            Path("/work/source.mp4"),
            Path("/work/output.mp4"),
            None,
        )

        self.assertEqual(plan.resolved_video_encoder, "libopenh264")
        self.assertNotIn("libx264", plan.command)
        self.assertIn("scale=-2:min(ih\\,720)", plan.command)
        self.assertIn("+faststart", plan.command)

    def test_audio_only_source_gets_a_video_canvas(self) -> None:
        plan = build_transcode_plan(
            "ffmpeg",
            {
                "encoder_mode": "cpu",
                "video_codec": "h264",
                "audio_codec": "aac",
                "container": "mp4",
                "maximum_height": 720,
                "video_bitrate_kbps": 1_000,
                "audio_bitrate_kbps": 128,
                "burn_subtitles": False,
            },
            FFmpegCapabilities(
                encoders=frozenset({"libopenh264", "aac"}),
                filters=frozenset({"color"}),
            ),
            audio_only_info(),
            Path("/work/source.wav"),
            Path("/work/output.mp4"),
            None,
        )

        self.assertEqual(
            plan.command[plan.command.index("-f") : plan.command.index("-f") + 4],
            ("-f", "lavfi", "-i", "color=c=0x0B1020:s=1280x720:r=25"),
        )
        self.assertIn("1:a:0", plan.command)
        self.assertIn("-shortest", plan.command)
        self.assertTrue(plan.command_summary["synthesized_video"])
        self.assertEqual(
            plan.command_summary["synthesized_canvas"],
            {"width": 1280, "height": 720, "frame_rate": 25},
        )

    def test_audio_only_source_cannot_copy_a_missing_video_stream(self) -> None:
        with self.assertRaisesRegex(
            TranscodeFailure,
            "纯音频媒体需要生成视频画布",
        ):
            build_transcode_plan(
                "ffmpeg",
                {
                    "encoder_mode": "cpu",
                    "video_codec": "copy",
                    "audio_codec": "copy",
                    "container": "mp4",
                },
                FFmpegCapabilities(
                    encoders=frozenset(),
                    filters=frozenset({"color"}),
                ),
                audio_only_info(),
                Path("/work/source.wav"),
                Path("/work/output.mp4"),
                None,
            )

    def test_cpu_hevc_is_an_explicit_non_gpl_blocker(self) -> None:
        with self.assertRaisesRegex(
            TranscodeFailure,
            "严格非 GPL 镜像未配置 CPU HEVC",
        ):
            build_transcode_plan(
                "ffmpeg",
                {
                    "encoder_mode": "cpu",
                    "video_codec": "hevc",
                    "audio_codec": "aac",
                    "container": "mp4",
                },
                FFmpegCapabilities(
                    encoders=frozenset({"libopenh264", "aac"}),
                    filters=frozenset(),
                ),
                media_info(),
                Path("/work/source.mp4"),
                Path("/work/output.mp4"),
                None,
            )

    def test_stream_copy_cannot_hide_filtering(self) -> None:
        with self.assertRaisesRegex(
            TranscodeFailure,
            "直接复制视频流",
        ):
            build_transcode_plan(
                "ffmpeg",
                {
                    "encoder_mode": "auto",
                    "video_codec": "copy",
                    "audio_codec": "copy",
                    "container": "mp4",
                    "maximum_height": 720,
                },
                FFmpegCapabilities(
                    encoders=frozenset(),
                    filters=frozenset({"scale"}),
                ),
                media_info(),
                Path("/work/source.mp4"),
                Path("/work/output.mp4"),
                None,
            )

    def test_burn_subtitles_requires_libass_filter(self) -> None:
        with self.assertRaisesRegex(
            TranscodeFailure,
            "subtitles/libass",
        ):
            build_transcode_plan(
                "ffmpeg",
                {
                    "encoder_mode": "cpu",
                    "video_codec": "h264",
                    "audio_codec": "aac",
                    "container": "mp4",
                    "burn_subtitles": True,
                },
                FFmpegCapabilities(
                    encoders=frozenset({"libopenh264", "aac"}),
                    filters=frozenset(),
                ),
                media_info(),
                Path("/work/source.mp4"),
                Path("/work/output.mp4"),
                Path("/work/subtitles.vtt"),
            )

    def test_custom_arguments_reject_network_input_override(self) -> None:
        with self.assertRaises(TranscodeFailure):
            build_transcode_plan(
                "ffmpeg",
                {
                    "encoder_mode": "cpu",
                    "video_codec": "h264",
                    "audio_codec": "aac",
                    "container": "mp4",
                    "custom_arguments_enabled": True,
                    "custom_arguments": [
                        "-vf",
                        "movie=https://example.invalid/video",
                    ],
                },
                FFmpegCapabilities(
                    encoders=frozenset({"libopenh264", "aac"}),
                    filters=frozenset(),
                ),
                media_info(),
                Path("/work/source.mp4"),
                Path("/work/output.mp4"),
                None,
            )

    def test_asset_must_stay_under_task_prefix(self) -> None:
        task_id = "00000000-0000-4000-8000-000000000001"
        with self.assertRaises(TranscodeFailure):
            _validated_asset(
                {
                    "id": "00000000-0000-4000-8000-000000000002",
                    "bucket": "media",
                    "object_key": "tasks/another-task/source.mp4",
                    "size_bytes": 5,
                    "checksum_sha256": hashlib.sha256(b"media").hexdigest(),
                },
                task_id,
                "source",
            )

    def test_progress_fields_are_normalized(self) -> None:
        self.assertEqual(_progress_seconds("out_time_us=2500000"), 2.5)
        self.assertEqual(_progress_seconds("out_time_ms=2500000"), 2.5)
        self.assertEqual(_progress_seconds("out_time=00:01:02.500000"), 62.5)

if __name__ == "__main__":
    unittest.main()
