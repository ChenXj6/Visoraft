from __future__ import annotations

import copy
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import Mock

from visoraft_media.subtitle_detector import (
    ExistingChineseSubtitleDetector,
    ExistingSubtitleDetection,
    ExistingSubtitleResult,
    OCRSample,
    _calibrate_ocr_sample,
    _select_best_ocr_sample,
    analyze_ocr_samples,
    is_chinese_language,
    parse_tesseract_tsv,
    segments_contain_chinese,
)
from visoraft_media.subtitle_processor import SubtitleProcessor, parse_vtt
from visoraft_media.subtitle_processor import SubtitleFailure


class SubtitleDetectorTests(unittest.TestCase):
    def test_stable_distinct_chinese_pairs_are_detected(self) -> None:
        samples = [
            OCRSample("我们今天出发", 94),
            OCRSample("我们今天出发", 92),
            OCRSample("马上就要到了", 91),
            OCRSample("马上就要到了", 93),
            OCRSample("大家路上小心", 90),
            OCRSample("大家路上小心", 91),
        ]
        result = analyze_ocr_samples(
            samples,
            {
                "confidence_threshold_percent": 85,
                "coverage_threshold_percent": 60,
                "minimum_distinct_texts": 3,
            },
        )
        self.assertEqual(result.state, "found")
        self.assertEqual(result.disposition, "keep_hardcoded_subtitles")
        self.assertEqual(result.stable_pair_count, 3)

    def test_distributed_strong_chinese_with_one_stable_pair_is_detected(
        self,
    ) -> None:
        result = analyze_ocr_samples(
            [
                OCRSample("皇上吉祥", 96),
                OCRSample("皇上吉祥", 95),
                OCRSample("大家快跟上", 94),
                OCRSample("", 0),
                OCRSample("我们现在出发", 93),
                OCRSample("", 0),
                OCRSample("马上回到宫里", 92),
                OCRSample("", 0),
                OCRSample("娘娘请放心", 91),
                OCRSample("", 0),
                OCRSample("今天就到这里", 90),
                OCRSample("", 0),
            ],
            {
                "confidence_threshold_percent": 85,
                "coverage_threshold_percent": 60,
                "minimum_distinct_texts": 3,
            },
        )
        self.assertEqual(result.state, "found")
        self.assertEqual(result.stable_pair_count, 1)
        self.assertGreaterEqual(result.distinct_text_count, 6)

    def test_sparse_chinese_is_not_treated_as_existing_subtitles(self) -> None:
        result = analyze_ocr_samples(
            [
                OCRSample("频道水印", 96),
                OCRSample("", 0),
                OCRSample("hello", 95),
                OCRSample("world", 95),
                OCRSample("", 0),
                OCRSample("", 0),
            ],
            {},
        )
        self.assertEqual(result.state, "not_found")
        self.assertEqual(result.disposition, "continue_pipeline")

    def test_borderline_samples_are_uncertain_and_continue_pipeline(self) -> None:
        result = analyze_ocr_samples(
            [
                OCRSample("只有一组字幕", 91),
                OCRSample("只有一组字幕", 92),
                OCRSample("", 0),
                OCRSample("", 0),
            ],
            {
                "confidence_threshold_percent": 85,
                "coverage_threshold_percent": 60,
                "minimum_distinct_texts": 3,
            },
        )
        self.assertEqual(result.state, "uncertain")
        self.assertEqual(result.disposition, "continue_pipeline")

    def test_tesseract_tsv_parser_uses_weighted_confidence(self) -> None:
        sample = parse_tesseract_tsv(
            "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n"
            "5\t1\t1\t1\t1\t1\t0\t0\t20\t10\t90\t你好\n"
            "5\t1\t1\t1\t1\t2\t20\t0\t20\t10\t80\t世界\n"
        )
        self.assertEqual(sample.text, "你好世界")
        self.assertEqual(sample.confidence_percent, 85)

    def test_low_resolution_chinese_confidence_is_calibrated(self) -> None:
        sample = _calibrate_ocr_sample(OCRSample("夢也渺渺人也渺渺", 44))
        self.assertGreaterEqual(sample.confidence_percent, 85)
        english = _calibrate_ocr_sample(OCRSample("CHANNEL WATERMARK", 96))
        self.assertEqual(english.confidence_percent, 96)

    def test_clean_subtitle_candidate_beats_long_noisy_ocr(self) -> None:
        selected = _select_best_ocr_sample(
            [
                OCRSample("AA一1和4六加s只過六一畫面噪聲", 30),
                OCRSample("都收進宮當格格吧", 87),
            ]
        )
        self.assertEqual(selected.text, "都收進宮當格格吧")

    def test_webvtt_parser_accepts_timestamps_without_hour_component(self) -> None:
        segments = parse_vtt(
            "WEBVTT\n\n00:01.000 --> 00:03.600\n中文字幕\n"
        )
        self.assertEqual(len(segments), 1)
        self.assertEqual((segments[0]["start"], segments[0]["end"]), (1.0, 3.6))

    def test_chinese_language_and_segment_detection(self) -> None:
        self.assertTrue(is_chinese_language("zh-Hans"))
        self.assertTrue(is_chinese_language("cmn"))
        self.assertFalse(is_chinese_language("en"))
        self.assertTrue(
            segments_contain_chinese(
                [{"text": "这是已经存在的中文字幕，不需要再次翻译"}]
            )
        )

    def test_ocr_tool_failure_is_recorded_as_error(self) -> None:
        detector = ExistingChineseSubtitleDetector()
        detector._ocr_frame = Mock(side_effect=OSError("tesseract missing"))  # type: ignore[method-assign]
        with tempfile.TemporaryDirectory() as directory:
            result = detector._inspect_hardcoded(
                Path(directory) / "media.mp4",
                120.0,
                {"sample_count": 8},
                Path(directory),
                lambda: False,
                None,
            )
        self.assertEqual(result.detection.state, "error")
        self.assertEqual(result.detection.disposition, "continue_pipeline")

    def test_ocr_honours_cancellation_before_sampling(self) -> None:
        detector = ExistingChineseSubtitleDetector()
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(RuntimeError, "字幕识别已取消"):
                detector._inspect_hardcoded(
                    Path(directory) / "media.mp4",
                    120.0,
                    {"sample_count": 8},
                    Path(directory),
                    lambda: True,
                    None,
                )

    @unittest.skipUnless(
        shutil.which("ffmpeg") and shutil.which("ffprobe"),
        "FFmpeg tools are not installed",
    )
    def test_real_embedded_chinese_track_is_extracted(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            subtitle = work / "chinese.vtt"
            subtitle.write_text(_chinese_vtt(), encoding="utf-8")
            media = work / "embedded.mkv"
            completed = subprocess.run(
                [
                    "ffmpeg",
                    "-nostdin",
                    "-hide_banner",
                    "-loglevel",
                    "error",
                    "-f",
                    "lavfi",
                    "-i",
                    "color=c=black:s=640x360:d=12:r=12",
                    "-i",
                    str(subtitle),
                    "-map",
                    "0:v:0",
                    "-map",
                    "1:s:0",
                    "-c:v",
                    "libopenh264",
                    "-pix_fmt",
                    "yuv420p",
                    "-c:s",
                    "webvtt",
                    "-metadata:s:s:0",
                    "language=zh-Hans",
                    "-shortest",
                    "-y",
                    str(media),
                ],
                check=False,
                capture_output=True,
                timeout=60,
            )
            self.assertEqual(
                completed.returncode,
                0,
                completed.stderr.decode("utf-8", errors="replace"),
            )
            result = ExistingChineseSubtitleDetector().inspect_local_media(
                media,
                {
                    "inspect_embedded_subtitles": True,
                    "inspect_hardcoded_subtitles": False,
                },
                work,
                lambda: False,
            )
        self.assertEqual(result.detection.state, "found")
        self.assertEqual(result.detection.source, "embedded")
        self.assertGreaterEqual(len(result.segments), 4)

    @unittest.skipUnless(
        shutil.which("ffmpeg")
        and shutil.which("ffprobe")
        and shutil.which("tesseract"),
        "FFmpeg and Tesseract are not installed",
    )
    def test_real_burned_chinese_subtitles_are_detected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            subtitle = work / "chinese.vtt"
            subtitle.write_text(_chinese_vtt(), encoding="utf-8")
            media = work / "hardcoded.mp4"
            filter_value = (
                f"subtitles=filename={subtitle}:"
                "force_style='FontName=Noto Sans CJK SC,FontSize=28,"
                "PrimaryColour=&H0000FFFF,OutlineColour=&H00000000,"
                "Outline=3,Alignment=2,MarginV=45'"
            )
            completed = subprocess.run(
                [
                    "ffmpeg",
                    "-nostdin",
                    "-hide_banner",
                    "-loglevel",
                    "error",
                    "-f",
                    "lavfi",
                    "-i",
                    "color=c=0x304050:s=640x360:d=12:r=12",
                    "-vf",
                    filter_value,
                    "-c:v",
                    "libopenh264",
                    "-pix_fmt",
                    "yuv420p",
                    "-y",
                    str(media),
                ],
                check=False,
                capture_output=True,
                timeout=90,
            )
            self.assertEqual(
                completed.returncode,
                0,
                completed.stderr.decode("utf-8", errors="replace"),
            )
            result = ExistingChineseSubtitleDetector().inspect_local_media(
                media,
                {
                    "inspect_embedded_subtitles": False,
                    "inspect_hardcoded_subtitles": True,
                    "sample_count": 8,
                    "confidence_threshold_percent": 85,
                    "coverage_threshold_percent": 60,
                    "minimum_distinct_texts": 3,
                },
                work,
                lambda: False,
            )
        self.assertEqual(result.detection.state, "found", result.detection)
        self.assertEqual(result.detection.source, "hardcoded")
        self.assertGreaterEqual(result.detection.stable_pair_count, 3)


class _Storage:
    bucket = "test"

    def __init__(self) -> None:
        self.objects = {"tasks/task-1/source.mp4": b"media"}

    def download_file(self, object_key: str, destination: Path, bucket: str) -> None:
        destination.write_bytes(self.objects[object_key])

    def download_file_if_exists(self, object_key: str, destination: Path) -> bool:
        value = self.objects.get(object_key)
        if value is None:
            return False
        destination.write_bytes(value)
        return True

    def upload_file(
        self,
        source: Path,
        object_key: str,
        content_type: str,
        metadata: dict[str, str],
    ) -> None:
        self.objects[object_key] = source.read_bytes()


class _Detector:
    def __init__(
        self,
        result: ExistingSubtitleResult | None = None,
        error: Exception | None = None,
    ) -> None:
        self.result = result
        self.error = error

    def inspect_local_media(self, *_args: object, **_kwargs: object) -> ExistingSubtitleResult:
        if self.error is not None:
            raise self.error
        if self.result is None:
            raise AssertionError("detector result is missing")
        return self.result


def _config() -> dict[str, object]:
    return {
        "runtime": {
            "source_url": "https://example.invalid/watch?v=task-1",
            "source_asset": {
                "bucket": "test",
                "object_key": "tasks/task-1/source.mp4",
                "original_name": "source.mp4",
            },
        },
        "subtitle": {
            "source_strategy": "asr_only",
            "source_language": "auto",
            "target_language": "zh",
            "existing_chinese": {
                "enabled": True,
                "inspect_platform_subtitles": False,
            },
            "asr": {"enabled": False},
            "postprocess": {},
            "segmentation": {"enabled": False},
            "translation": {"enabled": True},
            "qc": {"enabled": False},
        },
        "transcode": {"burn_subtitles": True},
        "models": {},
        "prompts": {},
        "secrets": {},
    }


def _chinese_vtt() -> str:
    return """WEBVTT

00:00:00.000 --> 00:00:03.600
我们今天一起出发

00:00:03.600 --> 00:00:06.600
马上就要到达终点

00:00:06.600 --> 00:00:09.600
大家路上注意安全

00:00:09.600 --> 00:00:12.000
下一段旅程再见
"""


class SubtitleProcessorExistingChineseTests(unittest.TestCase):
    def test_platform_miss_does_not_clear_asr_document_language(self) -> None:
        config = _config()
        subtitle = config["subtitle"]
        assert isinstance(subtitle, dict)
        subtitle["existing_chinese"] = {"enabled": False}
        subtitle["source_strategy"] = "youtube_then_asr"
        subtitle["source_language"] = "zh"
        subtitle["asr"] = {
            "enabled": True,
            "provider": "aliyun_paraformer",
            "model": "paraformer-v2",
            "language": "auto",
        }
        subtitle["translation"] = {"enabled": False}
        processor = SubtitleProcessor(_Storage())  # type: ignore[arg-type]
        processor._youtube_subtitles = Mock(return_value=([], "", ""))  # type: ignore[method-assign]
        processor._transcribe = Mock(  # type: ignore[method-assign]
            return_value=[
                {"index": 1, "start": 0.0, "end": 1.0, "text": "中文字幕"}
            ]
        )
        with tempfile.TemporaryDirectory() as directory:
            result = processor.process(
                "task-1",
                config,
                Path(directory),
                None,
                lambda: False,
            )
        self.assertEqual(result["documents"][0]["language"], "zh")

    def test_embedded_chinese_skips_asr_and_translation(self) -> None:
        detection = ExistingSubtitleDetection(
            1,
            "found",
            "embedded",
            "zh-Hans",
            "reuse_soft_subtitles",
            "媒体包含内嵌中文字幕",
            100,
            0,
            1,
            0,
            1,
            ("字幕流 2",),
        )
        segments = (
            {"index": 1, "start": 0.0, "end": 2.0, "text": "这是中文字幕"},
        )
        processor = SubtitleProcessor(  # type: ignore[arg-type]
            _Storage(), _Detector(ExistingSubtitleResult(detection, segments))
        )
        processor._transcribe = Mock()  # type: ignore[method-assign]
        processor._translate = Mock()  # type: ignore[method-assign]
        with tempfile.TemporaryDirectory() as directory:
            result = processor.process(
                "task-1",
                _config(),
                Path(directory),
                None,
                lambda: False,
            )
        self.assertEqual(result["decision"]["disposition"], "existing_soft_chinese")
        self.assertTrue(result["decision"]["translation_skipped"])
        self.assertFalse(result["decision"]["burn_subtitles"])
        self.assertEqual(result["documents"][0]["source"], "embedded")
        processor._transcribe.assert_not_called()
        processor._translate.assert_not_called()

    def test_not_found_uncertain_and_error_resume_original_asr_path(self) -> None:
        for state in ("not_found", "uncertain", "error"):
            with self.subTest(state=state):
                detection = ExistingSubtitleDetection(
                    1,
                    state,
                    "hardcoded" if state != "error" else "",
                    "",
                    "continue_pipeline",
                    "继续原字幕流水线",
                    0,
                    8,
                    0,
                    0,
                    0,
                    (),
                )
                processor = SubtitleProcessor(  # type: ignore[arg-type]
                    _Storage(), _Detector(ExistingSubtitleResult(detection))
                )
                processor._transcribe = Mock(  # type: ignore[method-assign]
                    return_value=[
                        {
                            "index": 1,
                            "start": 0.0,
                            "end": 2.0,
                            "text": "fallback transcript",
                        }
                    ]
                )
                config = copy.deepcopy(_config())
                subtitle = config["subtitle"]
                assert isinstance(subtitle, dict)
                subtitle["asr"] = {"enabled": True, "provider": "fixture"}
                subtitle["translation"] = {"enabled": False}
                with tempfile.TemporaryDirectory() as directory:
                    result = processor.process(
                        "task-1",
                        config,
                        Path(directory),
                        None,
                        lambda: False,
                    )
                self.assertEqual(
                    result["decision"]["disposition"], "generated_subtitles"
                )
                processor._transcribe.assert_called_once()

    def test_detection_cancellation_stops_before_paid_services(self) -> None:
        processor = SubtitleProcessor(  # type: ignore[arg-type]
            _Storage(), _Detector(error=RuntimeError("字幕识别已取消"))
        )
        processor._transcribe = Mock()  # type: ignore[method-assign]
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(SubtitleFailure) as raised:
                processor.process(
                    "task-1",
                    _config(),
                    Path(directory),
                    None,
                    lambda: False,
                )
        self.assertEqual(raised.exception.code, "subtitle_cancelled")
        processor._transcribe.assert_not_called()

    def test_hardcoded_chinese_skips_asr_translation_and_reburn(self) -> None:
        detection = ExistingSubtitleDetection(
            1,
            "found",
            "hardcoded",
            "zh",
            "keep_hardcoded_subtitles",
            "连续抽帧确认已有字幕",
            92,
            32,
            26,
            13,
            8,
            ("第一条字幕", "第二条字幕"),
        )
        processor = SubtitleProcessor(  # type: ignore[arg-type]
            _Storage(), _Detector(ExistingSubtitleResult(detection))
        )
        processor._transcribe = Mock()  # type: ignore[method-assign]
        processor._translate = Mock()  # type: ignore[method-assign]
        with tempfile.TemporaryDirectory() as directory:
            result = processor.process(
                "task-1",
                _config(),
                Path(directory),
                None,
                lambda: False,
            )
        self.assertEqual(result["documents"], [])
        self.assertEqual(result["assets"], [])
        self.assertEqual(
            result["decision"]["disposition"], "existing_hardcoded_chinese"
        )
        self.assertFalse(result["decision"]["burn_subtitles"])
        processor._transcribe.assert_not_called()
        processor._translate.assert_not_called()


if __name__ == "__main__":
    unittest.main()
