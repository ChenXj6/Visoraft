from __future__ import annotations

import hashlib
import http.client
import json
import mimetypes
import re
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from yt_dlp import YoutubeDL

from .storage import ObjectStorage, StorageFailure
from .subtitle_detector import (
    ExistingChineseSubtitleDetector,
    ExistingSubtitleDetection,
    is_chinese_language,
    segments_contain_chinese,
)


def _resolved_language(*values: object) -> str:
    for value in values:
        normalized = str(value or "").strip()
        if normalized and normalized.lower() != "auto":
            return normalized
    return "und"

_TIMESTAMP = re.compile(
    r"(?P<start>(?:\d{2}:)?\d{2}:\d{2}[.,]\d{3})\s+-->\s+"
    r"(?P<end>(?:\d{2}:)?\d{2}:\d{2}[.,]\d{3})"
)
_HTML_TAG = re.compile(r"<[^>]+>")
_SPACE = re.compile(r"\s+")


@dataclass
class SubtitleFailure(Exception):
    code: str
    message: str
    retryable: bool

    def __str__(self) -> str:
        return self.message


class SubtitleProcessor:
    def __init__(
        self,
        storage: ObjectStorage,
        existing_subtitle_detector: ExistingChineseSubtitleDetector | None = None,
    ) -> None:
        self.storage = storage
        self.existing_subtitle_detector = (
            existing_subtitle_detector or ExistingChineseSubtitleDetector()
        )

    def process(
        self,
        task_id: str,
        config: dict[str, Any],
        working_dir: Path,
        cookie_file: Path | None,
        should_cancel: callable,
        on_progress: callable | None = None,
    ) -> dict[str, Any]:
        def report(
            progress: int,
            phase: str,
            detail: dict[str, Any] | None = None,
            **extra: Any,
        ) -> None:
            if on_progress is not None:
                combined = dict(detail or {})
                combined.update(extra)
                on_progress(progress, phase, combined)

        subtitle = _object(config, "subtitle")
        runtime = _object(config, "runtime")
        secrets = _object(config, "secrets")
        source_asset = _object(runtime, "source_asset")
        if not source_asset:
            raise SubtitleFailure(
                "subtitle_source_asset_missing",
                "字幕处理找不到已下载的源媒体",
                True,
            )
        if should_cancel():
            raise SubtitleFailure("subtitle_cancelled", "字幕处理已取消", True)

        source_name = str(source_asset.get("original_name") or "source.bin")
        suffix = Path(source_name).suffix or ".bin"
        local_media = working_dir / f"source{suffix}"
        try:
            self.storage.download_file(
                str(source_asset.get("object_key") or ""),
                local_media,
                str(source_asset.get("bucket") or self.storage.bucket),
            )
        except StorageFailure as exc:
            raise SubtitleFailure(
                "subtitle_source_download_failed",
                str(exc),
                True,
            ) from exc
        report(8, "source_ready")

        segments: list[dict[str, Any]] = []
        source_kind = ""
        configured_source_language = str(
            subtitle.get("source_language") or "auto"
        ).strip()
        source_language = configured_source_language
        detection_config = _object(subtitle, "existing_chinese")
        detection: dict[str, Any] = {
            "schema_version": 1,
            "state": "disabled",
            "source": "",
            "language": "",
            "disposition": "continue_pipeline",
            "reason": "已有中文字幕识别未启用",
            "confidence_percent": 0,
            "sample_count": 0,
            "hit_count": 0,
            "stable_pair_count": 0,
            "distinct_text_count": 0,
            "evidence": [],
        }
        target_language = str(subtitle.get("target_language") or "zh")
        target_is_chinese = is_chinese_language(target_language)
        if bool(detection_config.get("enabled")):
            report(9, "existing_subtitle_detection_started")
            if bool(detection_config.get("inspect_platform_subtitles", True)):
                (
                    platform_segments,
                    platform_source_kind,
                    platform_source_language,
                ) = self._youtube_subtitles(
                    str(runtime.get("source_url") or ""),
                    subtitle,
                    working_dir,
                    cookie_file,
                    prefer_chinese=True,
                )
                if platform_segments and segments_contain_chinese(platform_segments):
                    segments = platform_segments
                    source_kind = platform_source_kind
                    source_language = _resolved_language(
                        platform_source_language, configured_source_language
                    )
                    detection = ExistingSubtitleDetection(
                        schema_version=1,
                        state="found",
                        source=source_kind,
                        language=source_language or "zh",
                        disposition="reuse_soft_subtitles",
                        reason="平台提供可复用的中文字幕",
                        confidence_percent=100,
                        sample_count=0,
                        hit_count=len(segments),
                        stable_pair_count=0,
                        distinct_text_count=len(
                            {str(item.get("text") or "") for item in segments}
                        ),
                        evidence=(
                            "平台字幕轨道",
                            f"语言 {source_language or 'zh'}",
                            f"字幕片段 {len(segments)} 条",
                        ),
                    ).as_dict()
                    report(30, "existing_soft_subtitle_found", detection=detection)
                else:
                    segments = []
                    source_kind = ""
            if not segments:
                try:
                    local_detection = self.existing_subtitle_detector.inspect_local_media(
                        local_media,
                        detection_config,
                        working_dir,
                        should_cancel,
                        report,
                    )
                except RuntimeError as exc:
                    raise SubtitleFailure(
                        "subtitle_cancelled", "字幕识别已取消", True
                    ) from exc
                detection = local_detection.detection.as_dict()
                if local_detection.segments:
                    segments = [dict(item) for item in local_detection.segments]
                    source_kind = "embedded"
                    source_language = local_detection.detection.language or "zh"
                    report(30, "existing_soft_subtitle_found", detection=detection)
                elif (
                    local_detection.detection.state == "found"
                    and local_detection.detection.source == "hardcoded"
                    and target_is_chinese
                ):
                    report(93, "existing_hardcoded_subtitle_kept", detection=detection)
                    return {
                        "task_id": task_id,
                        "assets": [],
                        "documents": [],
                        "decision": {
                            "schema_version": 1,
                            "disposition": "existing_hardcoded_chinese",
                            "translation_skipped": True,
                            "burn_subtitles": False,
                            "detection": detection,
                        },
                    }
                else:
                    report(30, "existing_subtitle_detection_finished", detection=detection)

        source_strategy = str(
            subtitle.get("source_strategy") or "youtube_then_asr"
        )
        if not segments and source_strategy in {
            "youtube_then_asr",
            "youtube_only",
            "youtube_manual_then_asr",
        }:
            (
                platform_segments,
                platform_source_kind,
                platform_source_language,
            ) = self._youtube_subtitles(
                str(runtime.get("source_url") or ""),
                subtitle,
                working_dir,
                cookie_file,
            )
            if platform_segments:
                segments = platform_segments
                source_kind = platform_source_kind
                source_language = _resolved_language(
                    platform_source_language, configured_source_language
                )
        if not segments and source_strategy != "youtube_only":
            asr = _object(subtitle, "asr")
            if bool(asr.get("enabled")):
                segments = self._load_asr_checkpoint(
                    task_id, source_asset, asr, working_dir
                )
                if segments:
                    report(38, "asr_checkpoint_reused")
                else:
                    report(10, "audio_extracting")
                    segments = self._transcribe(
                        local_media,
                        asr,
                        secrets,
                        working_dir,
                        should_cancel,
                        report,
                    )
                    self._save_asr_checkpoint(
                        task_id, source_asset, asr, segments, working_dir
                    )
                    report(38, "asr_completed")
                source_kind = (
                    "fixture" if str(asr.get("provider")) == "fixture" else "asr"
                )
                source_language = _resolved_language(
                    asr.get("language"), configured_source_language
                )
        if not segments:
            raise SubtitleFailure(
                "subtitle_unavailable",
                "未找到可用的 YouTube 字幕，且 ASR 未启用或未返回内容",
                False,
            )

        report(40, "subtitle_postprocessing")
        segments = postprocess_segments(
            segments,
            _object(subtitle, "postprocess"),
        )
        if not segments:
            raise SubtitleFailure(
                "subtitle_postprocess_empty",
                "字幕后处理移除了全部有效片段，请检查过滤参数",
                False,
            )

        models = _object(config, "models")
        prompts = _object(config, "prompts")
        segmentation = _object(subtitle, "segmentation")
        if bool(segmentation.get("enabled")):
            segmentation_digest = _checkpoint_digest(
                "smart-segmentation",
                segments,
                {
                    "segmentation": segmentation,
                    "model": _effective_model(models, "smart_segmentation")[0],
                    "prompt": _object(prompts, "smart_segmentation"),
                },
            )
            checkpoint_segments = self._load_segment_checkpoint(
                task_id,
                segmentation_digest,
                working_dir,
            )
            if checkpoint_segments:
                segments = checkpoint_segments
                report(57, "smart_segmentation_checkpoint_reused")
            else:
                report(45, "smart_segmentation")
                segments = self._segment(
                    segments,
                    segmentation,
                    models,
                    secrets,
                    prompts,
                    report,
                )
                self._save_segment_checkpoint(
                    task_id,
                    segmentation_digest,
                    segments,
                    working_dir,
                )
        report(58, "segmentation_completed")

        original_language = _resolved_language(
            source_language, configured_source_language
        )
        original_qc = quality_report(
            segments,
            _object(subtitle, "qc"),
            _object(subtitle, "postprocess"),
        )
        documents: list[dict[str, Any]] = [
            {
                "document_id": str(uuid.uuid4()),
                "kind": "original",
                "language": original_language,
                "source": source_kind,
                "segments": segments,
                "qc_report": original_qc,
            }
        ]

        translated: list[dict[str, Any]] | None = None
        translation = _object(subtitle, "translation")
        existing_soft_chinese = bool(
            target_is_chinese
            and source_kind in {"youtube_manual", "youtube_auto", "embedded"}
            and (
                is_chinese_language(original_language)
                or segments_contain_chinese(segments)
            )
        )
        if existing_soft_chinese and detection.get("state") != "found":
            detection = ExistingSubtitleDetection(
                schema_version=1,
                state="found",
                source=source_kind,
                language=original_language or "zh",
                disposition="reuse_soft_subtitles",
                reason="字幕来源已经提供可复用的中文字幕",
                confidence_percent=100,
                sample_count=0,
                hit_count=len(segments),
                stable_pair_count=0,
                distinct_text_count=len(
                    {str(item.get("text") or "") for item in segments}
                ),
                evidence=(
                    f"字幕来源 {source_kind}",
                    f"语言 {original_language or 'zh'}",
                    f"字幕片段 {len(segments)} 条",
                ),
            ).as_dict()
        translation_skipped = bool(
            translation.get("enabled") and existing_soft_chinese
        )
        if translation_skipped:
            report(85, "existing_chinese_translation_skipped", detection=detection)
        if bool(translation.get("enabled")) and not translation_skipped:
            translation_digest = _checkpoint_digest(
                "subtitle-translation",
                segments,
                {
                    "target_language": target_language,
                    "translation": translation,
                    "model": _effective_model(models, "subtitle_translation")[0],
                    "prompt": _object(prompts, "subtitle_translation"),
                    "strict_prompt": _object(
                        prompts, "subtitle_translation_strict"
                    ),
                },
            )
            resumed_translations = self._load_translation_checkpoint(
                task_id,
                translation_digest,
                working_dir,
            )
            report(
                62,
                "subtitle_translation_checkpoint_reused"
                if resumed_translations
                else "subtitle_translation",
                restored_items=len(resumed_translations),
            )
            translated = self._translate(
                segments,
                target_language,
                translation,
                models,
                secrets,
                prompts,
                report,
                initial_translations=resumed_translations,
                on_checkpoint=lambda current: self._save_translation_checkpoint(
                    task_id,
                    translation_digest,
                    current,
                    working_dir,
                ),
            )
            translated = format_translated_segments(
                segments,
                translated,
                _object(subtitle, "postprocess"),
            )
            qc = quality_report(
                translated,
                _object(subtitle, "qc"),
                _object(subtitle, "postprocess"),
            )
            if bool(_object(subtitle, "qc").get("enabled")):
                report(86, "subtitle_quality_check")
                qc = self._ai_quality_check(
                    segments,
                    translated,
                    qc,
                    _object(subtitle, "qc"),
                    models,
                    secrets,
                    prompts,
                    report,
                )
            documents.append(
                {
                    "document_id": str(uuid.uuid4()),
                    "kind": "translated",
                    "language": str(subtitle.get("target_language") or "zh"),
                    "source": "fixture"
                    if _effective_model(models, "subtitle_translation")[0].get(
                        "provider"
                    )
                    == "fixture"
                    else "model",
                    "segments": translated,
                    "qc_report": qc,
                }
            )

        report(93, "subtitle_artifact_saving")
        assets = self._write_and_upload_artifacts(
            task_id,
            documents,
            working_dir,
        )
        return {
            "task_id": task_id,
            "assets": assets,
            "documents": documents,
            "decision": {
                "schema_version": 1,
                "disposition": (
                    "existing_soft_chinese"
                    if existing_soft_chinese
                    else "generated_subtitles"
                ),
                "translation_skipped": translation_skipped,
                "burn_subtitles": False
                if existing_soft_chinese
                else bool(_object(config, "transcode").get("burn_subtitles")),
                "detection": detection,
            },
        }

    def _load_asr_checkpoint(
        self,
        task_id: str,
        source_asset: dict[str, Any],
        asr: dict[str, Any],
        working_dir: Path,
    ) -> list[dict[str, Any]]:
        path = working_dir / "asr-checkpoint.json"
        object_key = f"tasks/{task_id}/subtitles/asr-checkpoint.json"
        if not self.storage.download_file_if_exists(object_key, path):
            return []
        try:
            value = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return []
        if not isinstance(value, dict):
            return []
        if str(value.get("source_checksum") or "") != str(
            source_asset.get("checksum_sha256") or ""
        ):
            return []
        if str(value.get("provider") or "") != str(asr.get("provider") or ""):
            return []
        if str(value.get("model") or "") != str(asr.get("model") or ""):
            return []
        raw_segments = value.get("segments")
        if not isinstance(raw_segments, list):
            return []
        return _normalize_external_segments(raw_segments)

    def _save_asr_checkpoint(
        self,
        task_id: str,
        source_asset: dict[str, Any],
        asr: dict[str, Any],
        segments: list[dict[str, Any]],
        working_dir: Path,
    ) -> None:
        path = working_dir / "asr-checkpoint.json"
        value = {
            "schema_version": 1,
            "source_checksum": str(source_asset.get("checksum_sha256") or ""),
            "provider": str(asr.get("provider") or ""),
            "model": str(asr.get("model") or ""),
            "segments": segments,
        }
        path.write_text(json.dumps(value, ensure_ascii=False), encoding="utf-8")
        self.storage.upload_file(
            path,
            f"tasks/{task_id}/subtitles/asr-checkpoint.json",
            "application/json",
            {"task-id": task_id, "kind": "asr-checkpoint"},
        )

    def _load_segment_checkpoint(
        self,
        task_id: str,
        input_digest: str,
        working_dir: Path,
    ) -> list[dict[str, Any]]:
        path = working_dir / "smart-segmentation-checkpoint.json"
        object_key = (
            f"tasks/{task_id}/subtitles/smart-segmentation-checkpoint.json"
        )
        if not self.storage.download_file_if_exists(object_key, path):
            return []
        try:
            value = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return []
        if not isinstance(value, dict) or value.get("input_digest") != input_digest:
            return []
        raw_segments = value.get("segments")
        if not isinstance(raw_segments, list):
            return []
        return _normalize_external_segments(raw_segments)

    def _save_segment_checkpoint(
        self,
        task_id: str,
        input_digest: str,
        segments: list[dict[str, Any]],
        working_dir: Path,
    ) -> None:
        path = working_dir / "smart-segmentation-checkpoint.json"
        path.write_text(
            json.dumps(
                {
                    "schema_version": 1,
                    "input_digest": input_digest,
                    "segments": segments,
                },
                ensure_ascii=False,
            ),
            encoding="utf-8",
        )
        self.storage.upload_file(
            path,
            f"tasks/{task_id}/subtitles/smart-segmentation-checkpoint.json",
            "application/json",
            {"task-id": task_id, "kind": "smart-segmentation-checkpoint"},
        )

    def _load_translation_checkpoint(
        self,
        task_id: str,
        input_digest: str,
        working_dir: Path,
    ) -> dict[int, str]:
        path = working_dir / "translation-checkpoint.json"
        object_key = f"tasks/{task_id}/subtitles/translation-checkpoint.json"
        if not self.storage.download_file_if_exists(object_key, path):
            return {}
        try:
            value = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return {}
        if not isinstance(value, dict) or value.get("input_digest") != input_digest:
            return {}
        raw_translations = value.get("translations")
        if not isinstance(raw_translations, dict):
            return {}
        translations: dict[int, str] = {}
        for raw_index, raw_text in raw_translations.items():
            try:
                index = int(raw_index)
            except (TypeError, ValueError):
                continue
            text = str(raw_text or "").strip()
            if index > 0 and text:
                translations[index] = text
        return translations

    def _save_translation_checkpoint(
        self,
        task_id: str,
        input_digest: str,
        translations: dict[int, str],
        working_dir: Path,
    ) -> None:
        path = working_dir / "translation-checkpoint.json"
        path.write_text(
            json.dumps(
                {
                    "schema_version": 1,
                    "input_digest": input_digest,
                    "translations": {
                        str(index): text
                        for index, text in sorted(translations.items())
                    },
                },
                ensure_ascii=False,
            ),
            encoding="utf-8",
        )
        self.storage.upload_file(
            path,
            f"tasks/{task_id}/subtitles/translation-checkpoint.json",
            "application/json",
            {"task-id": task_id, "kind": "translation-checkpoint"},
        )

    def _youtube_subtitles(
        self,
        source_url: str,
        subtitle: dict[str, Any],
        working_dir: Path,
        cookie_file: Path | None,
        prefer_chinese: bool = False,
    ) -> tuple[list[dict[str, Any]], str, str]:
        if not source_url:
            return [], "", ""
        configured_language = str(subtitle.get("source_language") or "auto")
        metadata_options: dict[str, Any] = {
            "quiet": True,
            "no_warnings": True,
            "skip_download": True,
        }
        if cookie_file is not None:
            metadata_options["cookiefile"] = str(cookie_file)
        try:
            with YoutubeDL(metadata_options) as ydl:
                info = ydl.extract_info(source_url, download=False)
        except Exception:
            return [], "", ""
        if not isinstance(info, dict):
            return [], "", ""
        manual = info.get("subtitles") if isinstance(info.get("subtitles"), dict) else {}
        automatic = (
            info.get("automatic_captions")
            if isinstance(info.get("automatic_captions"), dict)
            else {}
        )
        language, source_kind = _choose_subtitle_language(
            manual,
            automatic if bool(subtitle.get("download_auto_subtitles", True)) else {},
            configured_language,
            prefer_chinese,
            manual_only=str(subtitle.get("source_strategy") or "")
            == "youtube_manual_then_asr",
        )
        if not language:
            return [], "", ""
        options: dict[str, Any] = {
            "quiet": True,
            "no_warnings": True,
            "skip_download": True,
            "writesubtitles": source_kind == "youtube_manual",
            "writeautomaticsub": source_kind == "youtube_auto",
            "subtitleslangs": [language],
            "subtitlesformat": "vtt/best",
            "convertsubtitles": "vtt",
            "outtmpl": str(working_dir / "youtube-subtitle-%(id)s"),
            "restrictfilenames": True,
        }
        if cookie_file is not None:
            options["cookiefile"] = str(cookie_file)
        try:
            with YoutubeDL(options) as ydl:
                ydl.download([source_url])
        except Exception:
            return [], "", ""
        candidates = sorted(
            working_dir.glob("youtube-subtitle*.vtt"),
            key=lambda path: path.stat().st_mtime,
            reverse=True,
        )
        if not candidates:
            return [], "", ""
        chosen = candidates[0]
        return (
            parse_vtt(chosen.read_text(encoding="utf-8", errors="replace")),
            source_kind,
            language,
        )

    def _transcribe(
        self,
        media_path: Path,
        asr: dict[str, Any],
        secrets: dict[str, Any],
        working_dir: Path,
        should_cancel: callable,
        report: callable,
    ) -> list[dict[str, Any]]:
        audio_path = working_dir / "asr-audio.wav"
        command = [
            "ffmpeg",
            "-nostdin",
            "-hide_banner",
            "-loglevel",
            "error",
            "-i",
            str(media_path),
            "-vn",
            "-ac",
            "1",
            "-ar",
            "16000",
            "-c:a",
            "pcm_s16le",
            "-y",
            str(audio_path),
        ]
        try:
            completed = subprocess.run(
                command,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.PIPE,
                timeout=max(30, int(asr.get("timeout_seconds") or 600)),
                check=False,
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise SubtitleFailure(
                "audio_extraction_failed",
                f"无法提取 ASR 音频：{exc}",
                True,
            ) from exc
        if completed.returncode != 0 or not audio_path.exists():
            detail = completed.stderr.decode("utf-8", errors="replace")[-1200:]
            raise SubtitleFailure(
                "audio_extraction_failed",
                f"FFmpeg 音频提取失败：{detail}",
                False,
            )

        provider = str(asr.get("provider") or "openai_compatible")
        base_url = str(asr.get("base_url") or "").rstrip("/")
        api_key = str(secrets.get("subtitle.asr.api_key") or "")
        if provider != "fixture" and not api_key:
            raise SubtitleFailure(
                "asr_credentials_missing",
                "任务快照中没有 ASR API Key",
                False,
            )
        if provider == "aliyun_paraformer":
            return _transcribe_aliyun_paraformer(
                audio_path,
                asr,
                api_key,
                should_cancel,
                report,
            )

        endpoint = f"{base_url}/audio/transcriptions"
        fields = {
            "model": str(asr.get("model") or "whisper-1"),
            "response_format": "verbose_json",
            "timestamp_granularities[]": "segment",
        }
        language = str(asr.get("language") or "")
        if language:
            fields["language"] = language
        prompt = str(asr.get("prompt") or "")
        if prompt:
            fields["prompt"] = prompt
        value = _multipart_json_request(
            endpoint,
            fields,
            "file",
            audio_path,
            api_key,
            timeout=max(10, int(asr.get("timeout_seconds") or 600)),
            retries=max(0, int(asr.get("max_retries") or 0)),
        )
        raw_segments = value.get("segments")
        if isinstance(raw_segments, list):
            return _normalize_external_segments(raw_segments)
        text = str(value.get("text") or "").strip()
        if text:
            return [{"index": 1, "start": 0.0, "end": 2.0, "text": text}]
        raise SubtitleFailure(
            "asr_empty_result",
            "ASR 服务返回了空转写结果",
            True,
        )

    def _segment(
        self,
        segments: list[dict[str, Any]],
        segmentation: dict[str, Any],
        models: dict[str, Any],
        secrets: dict[str, Any],
        prompts: dict[str, Any],
        on_progress: callable | None = None,
    ) -> list[dict[str, Any]]:
        endpoint, secret_key = _effective_model(models, "smart_segmentation")
        if not endpoint or str(endpoint.get("mode")) == "disabled":
            return deterministic_segmentation(segments, segmentation)
        prompt = _compose_prompt(
            "smart_segmentation",
            (
                "你是字幕分段器。只返回 JSON 对象 {\"segments\":"
                "[{\"index\":1,\"start\":0.0,\"end\":1.2,\"text\":\"...\"}]}。"
                "不得改写原意，保持时间顺序并满足给定时长和 CPS。"
            ),
            prompts,
        )
        maximum_characters = max(
            500, min(int(segmentation.get("maximum_batch_characters") or 6000), 12000)
        )
        window_seconds = max(
            30.0, min(float(segmentation.get("batch_window_seconds") or 180), 600.0)
        )
        pending_batches = list(
            _segment_batches(segments, maximum_characters, window_seconds)
        )
        merged: list[dict[str, Any]] = []
        completed_batches = 0
        completed_source_segments = 0
        total_source_segments = max(1, len(segments))
        while pending_batches:
            batch = pending_batches.pop(0)
            batch_index = completed_batches + 1
            batch_count = completed_batches + 1 + len(pending_batches)

            def report_attempt(
                model_attempt: int,
                model_attempts: int,
                *,
                current_batch: int = batch_index,
            ) -> None:
                if on_progress is None:
                    return
                progress = 45 + int(
                    12 * completed_source_segments / total_source_segments
                )
                on_progress(
                    progress,
                    "smart_segmentation",
                    {
                        "batch_index": current_batch,
                        "batch_count": batch_count,
                        "completed_batches": completed_batches,
                        "batch_segment_count": len(batch),
                        "model_attempt": model_attempt,
                        "model_attempts": model_attempts,
                    },
                )

            try:
                value = _chat_json(
                    endpoint,
                    secret_key,
                    secrets,
                    prompt,
                    {"segments": batch, "constraints": segmentation},
                    retries=(
                        max(0, int(segmentation.get("max_retries") or 0))
                        if len(batch) == 1
                        else 0
                    ),
                    on_attempt=report_attempt,
                )
            except SubtitleFailure as exc:
                timed_out = exc.code == "model_request_failed" and re.search(
                    r"timed?\s*out|timeout",
                    exc.message,
                    flags=re.IGNORECASE,
                )
                if timed_out and len(batch) > 1:
                    midpoint = max(1, len(batch) // 2)
                    pending_batches = [
                        batch[:midpoint],
                        batch[midpoint:],
                        *pending_batches,
                    ]
                    if on_progress is not None:
                        on_progress(
                            45
                            + int(
                                12
                                * completed_source_segments
                                / total_source_segments
                            ),
                            "smart_segmentation",
                            {
                                "batch_index": batch_index,
                                "batch_count": (
                                    completed_batches + len(pending_batches)
                                ),
                                "completed_batches": completed_batches,
                                "batch_segment_count": len(batch),
                                "batch_split": True,
                            },
                        )
                    continue
                raise
            raw = value.get("segments")
            if not isinstance(raw, list):
                raise SubtitleFailure(
                    "smart_segmentation_invalid",
                    "智能分段模型没有返回 segments 数组",
                    True,
                )
            merged.extend(_normalize_external_segments(raw))
            completed_batches += 1
            completed_source_segments += len(batch)
            if on_progress is not None:
                on_progress(
                    45
                    + int(
                        12 * completed_source_segments / total_source_segments
                    ),
                    "smart_segmentation",
                    {
                        "batch_index": completed_batches,
                        "batch_count": completed_batches + len(pending_batches),
                        "completed_batches": completed_batches,
                        "batch_segment_count": len(batch),
                    },
                )
        return [
            {**item, "index": index}
            for index, item in enumerate(merged, start=1)
        ]

    def _translate(
        self,
        segments: list[dict[str, Any]],
        target_language: str,
        translation: dict[str, Any],
        models: dict[str, Any],
        secrets: dict[str, Any],
        prompts: dict[str, Any],
        on_progress: callable | None = None,
        initial_translations: dict[int, str] | None = None,
        on_checkpoint: callable | None = None,
    ) -> list[dict[str, Any]]:
        endpoint, secret_key = _effective_model(models, "subtitle_translation")
        if not endpoint:
            raise SubtitleFailure(
                "subtitle_translation_model_missing",
                "字幕翻译已启用，但没有可用的全局或字幕专用模型",
                False,
            )
        prompt = _compose_prompt(
            "subtitle_translation",
            (
                "你是专业字幕译者。只返回 JSON 对象 "
                "{\"translations\":[{\"index\":1,\"text\":\"译文\"}]}。"
                "索引必须与输入一致，不得遗漏，不改变时间轴。"
            ),
            prompts,
        )
        batch_size = max(1, min(int(translation.get("batch_size") or 20), 100))
        translated: dict[int, str] = dict(initial_translations or {})
        batch_count = max(1, (len(segments) + batch_size - 1) // batch_size)
        completed_batches = 0
        for batch_index, start in enumerate(
            range(0, len(segments), batch_size),
            start=1,
        ):
            batch = segments[start : start + batch_size]
            batch_indexes = [int(item["index"]) for item in batch]
            if batch_indexes and all(
                bool(translated.get(index)) for index in batch_indexes
            ):
                completed_batches += 1
                if on_progress is not None:
                    on_progress(
                        62 + int(22 * completed_batches / batch_count),
                        "subtitle_translation",
                        {
                            "batch_index": batch_index,
                            "batch_count": batch_count,
                            "completed_batches": completed_batches,
                            "checkpoint_reused": True,
                        },
                    )
                continue

            def report_attempt(
                model_attempt: int,
                model_attempts: int,
                *,
                current_batch: int = batch_index,
                completed_before: int = completed_batches,
            ) -> None:
                if on_progress is None:
                    return
                progress = 62 + int(22 * completed_before / batch_count)
                on_progress(
                    progress,
                    "subtitle_translation",
                    {
                        "batch_index": current_batch,
                        "batch_count": batch_count,
                        "completed_batches": completed_before,
                        "model_attempt": model_attempt,
                        "model_attempts": model_attempts,
                    },
                )

            value = _chat_json(
                endpoint,
                secret_key,
                secrets,
                prompt,
                {
                    "target_language": target_language,
                    "segments": [
                        {"index": item["index"], "text": item["text"]}
                        for item in batch
                    ],
                },
                retries=max(0, int(translation.get("max_retries") or 0)),
                on_attempt=report_attempt,
            )
            items = _translation_items(value)
            if items is None:
                raise SubtitleFailure(
                    "subtitle_translation_invalid",
                    "字幕翻译模型没有返回可识别的翻译数组",
                    True,
                )
            for item in items:
                if not isinstance(item, dict):
                    continue
                try:
                    index = int(item.get("index"))
                except (TypeError, ValueError):
                    continue
                text = str(item.get("text") or "").strip()
                if text:
                    translated[index] = text
            completed_batches += 1
            if on_checkpoint is not None:
                on_checkpoint(dict(translated))
            if on_progress is not None:
                on_progress(
                    62 + int(22 * completed_batches / batch_count),
                    "subtitle_translation",
                    {
                        "batch_index": batch_index,
                        "batch_count": batch_count,
                        "completed_batches": completed_batches,
                    },
                )

        missing = [
            int(item["index"])
            for item in segments
            if int(item["index"]) not in translated
        ]
        if missing:
            strict_prompt = _compose_prompt(
                "subtitle_translation_strict",
                (
                    "修复缺失字幕翻译。只返回 JSON 对象 "
                    "{\"translations\":[{\"index\":1,\"text\":\"译文\"}]}，"
                    "必须逐项返回所有输入索引。"
                ),
                prompts,
            )
            missing_items = [
                {"index": item["index"], "text": item["text"]}
                for item in segments
                if int(item["index"]) in missing
            ]
            value = _chat_json(
                endpoint,
                secret_key,
                secrets,
                strict_prompt,
                {
                    "target_language": target_language,
                    "segments": missing_items,
                },
                on_attempt=(
                    lambda model_attempt, model_attempts: on_progress(
                        84,
                        "subtitle_translation",
                        {
                            "batch_index": batch_count,
                            "batch_count": batch_count,
                            "completed_batches": batch_count,
                            "model_attempt": model_attempt,
                            "model_attempts": model_attempts,
                            "repairing_missing": True,
                        },
                    )
                )
                if on_progress is not None
                else None,
            )
            for item in _translation_items(value) or []:
                if isinstance(item, dict):
                    try:
                        translated[int(item.get("index"))] = str(
                            item.get("text") or ""
                        ).strip()
                    except (TypeError, ValueError):
                        continue
            if on_checkpoint is not None:
                on_checkpoint(dict(translated))
        still_missing = [
            int(item["index"])
            for item in segments
            if not translated.get(int(item["index"]))
        ]
        if still_missing:
            raise SubtitleFailure(
                "subtitle_translation_incomplete",
                f"字幕翻译仍缺少 {len(still_missing)} 个片段",
                True,
            )
        return [
            {
                "index": position,
                "start": float(item["start"]),
                "end": float(item["end"]),
                "text": translated[int(item["index"])],
            }
            for position, item in enumerate(segments, start=1)
        ]

    def _ai_quality_check(
        self,
        original: list[dict[str, Any]],
        translated: list[dict[str, Any]],
        deterministic: dict[str, Any],
        qc_config: dict[str, Any],
        models: dict[str, Any],
        secrets: dict[str, Any],
        prompts: dict[str, Any],
        on_progress: callable | None = None,
    ) -> dict[str, Any]:
        endpoint, secret_key = _effective_model(models, "subtitle_qc")
        if not endpoint:
            return deterministic
        prompt = _compose_prompt(
            "subtitle_qc",
            (
                "你是字幕翻译质检员。只返回 JSON 对象 "
                "{\"score\":0到100,\"issues\":[{\"index\":1,"
                "\"severity\":\"warning\",\"message\":\"...\"}],"
                "\"summary\":\"...\"}。检查错译、漏译、时间轴和可读性。"
                "只报告确实存在的代表性问题，issues 最多返回 20 条，"
                "不要逐条复述输入字幕。"
            ),
            prompts,
        )
        sample = _quality_check_sample(original, translated, qc_config)
        total_count = min(len(original), len(translated))
        value = _chat_json(
            endpoint,
            secret_key,
            secrets,
            prompt,
            {
                "total_segments": total_count,
                "sampled_segments": len(sample),
                "samples": sample,
            },
            on_attempt=(
                lambda model_attempt, model_attempts: on_progress(
                    86,
                    "subtitle_quality_check",
                    {
                        "model_attempt": model_attempt,
                        "model_attempts": model_attempts,
                        "sample_count": len(sample),
                        "total_count": total_count,
                    },
                )
            )
            if on_progress is not None
            else None,
        )
        try:
            ai_score = max(0.0, min(float(value.get("score")), 100.0))
        except (TypeError, ValueError):
            raise SubtitleFailure(
                "subtitle_qc_invalid",
                "字幕质检模型没有返回有效分数",
                True,
            )
        deterministic_score = float(deterministic.get("score") or 0)
        ai_issues = (
            value.get("issues")
            if isinstance(value.get("issues"), list)
            else []
        )
        has_ai_error = any(
            isinstance(issue, dict)
            and str(issue.get("severity") or "").strip().lower() == "error"
            for issue in ai_issues
        )
        threshold = max(0, min(int(qc_config.get("threshold") or 80), 100))
        combined = round(min(ai_score, deterministic_score), 2)
        if has_ai_error:
            combined = min(combined, float(max(0, threshold - 1)))
        return {
            **deterministic,
            "score": combined,
            "passed": bool(deterministic.get("passed"))
            and combined >= threshold
            and not has_ai_error,
            "ai_score": ai_score,
            "ai_summary": str(value.get("summary") or ""),
            "ai_sample_count": len(sample),
            "ai_total_count": total_count,
            "ai_issues": ai_issues,
        }

    def _write_and_upload_artifacts(
        self,
        task_id: str,
        documents: list[dict[str, Any]],
        working_dir: Path,
    ) -> list[dict[str, Any]]:
        assets: list[dict[str, Any]] = []
        for document in documents:
            kind = str(document["kind"])
            segments = document["segments"]
            for extension, content, content_type in (
                ("vtt", render_vtt(segments), "text/vtt; charset=utf-8"),
                (
                    "srt",
                    render_srt(segments),
                    "application/x-subrip; charset=utf-8",
                ),
            ):
                filename = f"{kind}.{extension}"
                path = working_dir / filename
                path.write_text(content, encoding="utf-8", newline="\n")
                assets.append(
                    self._upload_artifact(
                        task_id,
                        f"subtitle_{kind}_{extension}",
                        path,
                        content_type,
                    )
                )
            qc_path = working_dir / f"{kind}.qc.json"
            qc_path.write_text(
                json.dumps(
                    document["qc_report"],
                    ensure_ascii=False,
                    indent=2,
                ),
                encoding="utf-8",
                newline="\n",
            )
            assets.append(
                self._upload_artifact(
                    task_id,
                    f"subtitle_{kind}_qc",
                    qc_path,
                    "application/json",
                )
            )
        return assets

    def _upload_artifact(
        self,
        task_id: str,
        kind: str,
        path: Path,
        content_type: str,
    ) -> dict[str, Any]:
        checksum = hashlib.sha256(path.read_bytes()).hexdigest()
        object_key = f"tasks/{task_id}/subtitles/{path.name}"
        try:
            self.storage.upload_file(
                path,
                object_key,
                content_type,
                {
                    "task-id": task_id,
                    "sha256": checksum,
                    "kind": kind,
                },
            )
        except StorageFailure as exc:
            raise SubtitleFailure(
                "subtitle_artifact_upload_failed",
                str(exc),
                True,
            ) from exc
        return {
            "asset_id": str(uuid.uuid4()),
            "kind": kind,
            "bucket": self.storage.bucket,
            "object_key": object_key,
            "original_name": path.name,
            "content_type": content_type,
            "size_bytes": path.stat().st_size,
            "checksum_sha256": checksum,
        }


def parse_vtt(content: str) -> list[dict[str, Any]]:
    lines = content.replace("\r\n", "\n").replace("\r", "\n").split("\n")
    result: list[dict[str, Any]] = []
    index = 0
    while index < len(lines):
        match = _TIMESTAMP.search(lines[index])
        if not match:
            index += 1
            continue
        start = parse_timestamp(match.group("start"))
        end = parse_timestamp(match.group("end"))
        index += 1
        text_lines: list[str] = []
        while index < len(lines) and lines[index].strip():
            line = _HTML_TAG.sub("", lines[index]).strip()
            if line and not line.startswith("NOTE"):
                text_lines.append(line)
            index += 1
        text = _SPACE.sub(" ", " ".join(text_lines)).strip()
        if text and end > start:
            result.append(
                {
                    "index": len(result) + 1,
                    "start": start,
                    "end": end,
                    "text": text,
                }
            )
    return result


def parse_timestamp(value: str) -> float:
    normalized = value.replace(",", ".")
    parts = normalized.split(":")
    if len(parts) == 2:
        hours = "0"
        minutes, seconds = parts
    else:
        hours, minutes, seconds = parts
    return int(hours) * 3600 + int(minutes) * 60 + float(seconds)


def _choose_subtitle_language(
    manual: dict[str, Any],
    automatic: dict[str, Any],
    configured_language: str,
    prefer_chinese: bool,
    manual_only: bool,
) -> tuple[str, str]:
    configured = configured_language.strip().lower().replace("_", "-")

    def candidates(values: dict[str, Any]) -> list[str]:
        result = [str(language) for language in values if language != "live_chat"]
        if prefer_chinese:
            result = [language for language in result if is_chinese_language(language)]
        elif configured and configured != "auto":
            result = [
                language
                for language in result
                if _language_matches(language, configured)
            ]
        return sorted(result, key=_subtitle_language_priority)

    manual_candidates = candidates(manual)
    if manual_candidates:
        return manual_candidates[0], "youtube_manual"
    if not manual_only:
        automatic_candidates = candidates(automatic)
        if automatic_candidates:
            return automatic_candidates[0], "youtube_auto"
    return "", ""


def _language_matches(candidate: str, configured: str) -> bool:
    normalized = candidate.strip().lower().replace("_", "-")
    return (
        normalized == configured
        or normalized.startswith(configured + "-")
        or configured.startswith(normalized + "-")
    )


def _subtitle_language_priority(value: str) -> tuple[int, str]:
    normalized = value.strip().lower().replace("_", "-")
    chinese_order = {
        "zh-hans": 0,
        "zh-cn": 1,
        "zh": 2,
        "cmn-hans": 3,
        "zh-hant": 4,
        "zh-tw": 5,
        "yue": 6,
    }
    return chinese_order.get(normalized, 20), normalized


def postprocess_segments(
    segments: list[dict[str, Any]],
    config: dict[str, Any],
) -> list[dict[str, Any]]:
    offset = float(config.get("time_offset_seconds") or 0)
    minimum_duration = max(0.1, float(config.get("minimum_cue_seconds") or 0.7))
    merge_gap = max(0.0, float(config.get("merge_gap_seconds") or 0.15))
    minimum_text = max(1, int(config.get("minimum_text_length") or 1))
    max_chars = max(5, int(config.get("maximum_characters_per_line") or 24))
    max_lines = max(1, int(config.get("maximum_lines") or 2))
    normalize_punctuation = bool(config.get("normalize_punctuation", True))
    filter_fillers = bool(config.get("filter_filler_words", False))
    fillers = {"um", "uh", "erm", "嗯", "呃", "额"}

    prepared: list[dict[str, Any]] = []
    for raw in segments:
        try:
            start = max(0.0, float(raw.get("start")) + offset)
            end = max(start + 0.01, float(raw.get("end")) + offset)
        except (TypeError, ValueError):
            continue
        text = _SPACE.sub(" ", str(raw.get("text") or "")).strip()
        if normalize_punctuation:
            text = (
                text.replace(" ,", ",")
                .replace(" .", ".")
                .replace(" ！", "！")
                .replace(" ？", "？")
            )
        if filter_fillers and text.lower() in fillers:
            continue
        if len(text) < minimum_text:
            continue
        if end - start < minimum_duration:
            end = start + minimum_duration
        prepared.append({"start": start, "end": end, "text": text})
    prepared.sort(key=lambda item: (item["start"], item["end"]))

    merged: list[dict[str, Any]] = []
    for item in prepared:
        if (
            merged
            and item["start"] - merged[-1]["end"] <= merge_gap
            and len(merged[-1]["text"] + item["text"]) <= max_chars * max_lines
        ):
            merged[-1]["end"] = max(merged[-1]["end"], item["end"])
            merged[-1]["text"] = f"{merged[-1]['text']} {item['text']}".strip()
        else:
            merged.append(dict(item))
    for index, item in enumerate(merged, start=1):
        item["index"] = index
        item["text"] = wrap_text(item["text"], max_chars, max_lines)
        if index > 1 and item["start"] < merged[index - 2]["end"]:
            item["start"] = merged[index - 2]["end"]
            item["end"] = max(item["end"], item["start"] + minimum_duration)
    return merged


def format_translated_segments(
    original: list[dict[str, Any]],
    translated: list[dict[str, Any]],
    config: dict[str, Any],
) -> list[dict[str, Any]]:
    """Format translated text without changing the source cue structure."""
    max_chars = max(5, int(config.get("maximum_characters_per_line") or 24))
    max_lines = max(1, int(config.get("maximum_lines") or 2))
    normalize_punctuation = bool(config.get("normalize_punctuation", True))
    by_index = {
        int(item.get("index")): item
        for item in translated
        if isinstance(item, dict) and item.get("index") is not None
    }
    result: list[dict[str, Any]] = []
    for position, source in enumerate(original, start=1):
        source_index = int(source.get("index") or position)
        target = by_index.get(source_index)
        if target is None:
            raise SubtitleFailure(
                "subtitle_translation_incomplete",
                f"字幕翻译缺少索引 {source_index}",
                True,
            )
        text = _SPACE.sub(" ", str(target.get("text") or "")).strip()
        if normalize_punctuation:
            text = (
                text.replace(" ,", ",")
                .replace(" .", ".")
                .replace(" ！", "！")
                .replace(" ？", "？")
            )
        if not text:
            raise SubtitleFailure(
                "subtitle_translation_incomplete",
                f"字幕翻译索引 {source_index} 的内容为空",
                True,
            )
        result.append(
            {
                "index": source_index,
                "start": float(source["start"]),
                "end": float(source["end"]),
                "text": wrap_text(text, max_chars, max_lines),
            }
        )
    return result


def _segment_batches(
    segments: list[dict[str, Any]],
    maximum_characters: int,
    window_seconds: float,
) -> list[list[dict[str, Any]]]:
    batches: list[list[dict[str, Any]]] = []
    current: list[dict[str, Any]] = []
    character_count = 0
    window_start = 0.0
    for segment in segments:
        text_length = len(str(segment.get("text") or ""))
        start = float(segment.get("start") or 0)
        end = float(segment.get("end") or start)
        exceeds_characters = bool(current) and character_count + text_length > maximum_characters
        exceeds_window = bool(current) and end - window_start > window_seconds
        if exceeds_characters or exceeds_window:
            batches.append(current)
            current = []
            character_count = 0
        if not current:
            window_start = start
        current.append(segment)
        character_count += text_length
    if current:
        batches.append(current)
    return batches


def _quality_check_sample(
    original: list[dict[str, Any]],
    translated: list[dict[str, Any]],
    config: dict[str, Any],
) -> list[dict[str, Any]]:
    total = min(len(original), len(translated))
    if total <= 0:
        return []
    maximum_items = max(
        1,
        min(int(config.get("sample_max_items") or 80), 200),
    )
    maximum_characters = max(
        500,
        min(int(config.get("maximum_characters") or 12000), 40000),
    )
    sample_count = min(total, maximum_items)
    if sample_count == 1:
        positions = [total // 2]
    else:
        positions = sorted(
            {
                round(position * (total - 1) / (sample_count - 1))
                for position in range(sample_count)
            }
        )

    samples: list[dict[str, Any]] = []
    used_characters = 0
    for position in positions:
        source = original[position]
        target = translated[position]
        source_text = str(source.get("text") or "").strip()
        target_text = str(target.get("text") or "").strip()
        remaining = maximum_characters - used_characters
        if remaining <= 0:
            break
        if len(source_text) + len(target_text) > remaining:
            source_budget = min(len(source_text), max(1, remaining // 2))
            target_budget = max(0, remaining - source_budget)
            source_text = source_text[:source_budget]
            target_text = target_text[:target_budget]
        used_characters += len(source_text) + len(target_text)
        samples.append(
            {
                "index": int(source.get("index") or position + 1),
                "start": float(source.get("start") or 0),
                "end": float(source.get("end") or 0),
                "original": source_text,
                "translated": target_text,
            }
        )
    return samples


def deterministic_segmentation(
    segments: list[dict[str, Any]],
    config: dict[str, Any],
) -> list[dict[str, Any]]:
    maximum_duration = max(1.0, float(config.get("maximum_cue_seconds") or 7))
    maximum_cps = max(5.0, float(config.get("maximum_cps") or 18))
    result: list[dict[str, Any]] = []
    for segment in segments:
        duration = max(0.1, float(segment["end"]) - float(segment["start"]))
        parts = max(
            1,
            int(
                max(
                    duration / maximum_duration,
                    len(str(segment["text"]).replace("\n", "")) / (duration * maximum_cps),
                )
                + 0.999
            ),
        )
        text_parts = _split_evenly(str(segment["text"]).replace("\n", " "), parts)
        for part_index, text in enumerate(text_parts):
            start = float(segment["start"]) + duration * part_index / len(text_parts)
            end = float(segment["start"]) + duration * (part_index + 1) / len(
                text_parts
            )
            result.append(
                {
                    "index": len(result) + 1,
                    "start": round(start, 3),
                    "end": round(end, 3),
                    "text": text,
                }
            )
    return result


def quality_report(
    segments: list[dict[str, Any]],
    qc: dict[str, Any],
    postprocess: dict[str, Any],
) -> dict[str, Any]:
    threshold = max(0, min(int(qc.get("threshold") or 80), 100))
    max_cps = 22.0
    issues: list[dict[str, Any]] = []
    overlaps = 0
    high_cps = 0
    short = 0
    previous_end = 0.0
    for item in segments:
        duration = max(0.001, float(item["end"]) - float(item["start"]))
        cps = len(str(item["text"]).replace("\n", "")) / duration
        if float(item["start"]) < previous_end - 0.01:
            overlaps += 1
            issues.append(
                {
                    "index": item["index"],
                    "severity": "error",
                    "message": "时间轴与前一条重叠",
                }
            )
        if cps > max_cps:
            high_cps += 1
            issues.append(
                {
                    "index": item["index"],
                    "severity": "warning",
                    "message": f"字符速率过高（{cps:.1f} CPS）",
                }
            )
        if duration < float(postprocess.get("minimum_cue_seconds") or 0.7):
            short += 1
        previous_end = max(previous_end, float(item["end"]))
    penalty = overlaps * 12 + high_cps * 4 + short * 3
    score = max(0.0, round(100.0 - penalty, 2))
    return {
        "score": score,
        "threshold": threshold,
        "passed": score >= threshold,
        "segment_count": len(segments),
        "overlap_count": overlaps,
        "high_cps_count": high_cps,
        "short_cue_count": short,
        "issues": issues[:200],
    }


def render_vtt(segments: list[dict[str, Any]]) -> str:
    blocks = ["WEBVTT", ""]
    for item in segments:
        blocks.extend(
            [
                str(item["index"]),
                f"{format_timestamp(item['start'], '.')}"
                f" --> {format_timestamp(item['end'], '.')}",
                str(item["text"]),
                "",
            ]
        )
    return "\n".join(blocks)


def render_srt(segments: list[dict[str, Any]]) -> str:
    blocks: list[str] = []
    for item in segments:
        blocks.extend(
            [
                str(item["index"]),
                f"{format_timestamp(item['start'], ',')}"
                f" --> {format_timestamp(item['end'], ',')}",
                str(item["text"]),
                "",
            ]
        )
    return "\n".join(blocks)


def format_timestamp(value: float, decimal: str) -> str:
    milliseconds = max(0, round(float(value) * 1000))
    hours, remainder = divmod(milliseconds, 3_600_000)
    minutes, remainder = divmod(remainder, 60_000)
    seconds, millis = divmod(remainder, 1000)
    return f"{hours:02d}:{minutes:02d}:{seconds:02d}{decimal}{millis:03d}"


def wrap_text(text: str, max_chars: int, max_lines: int) -> str:
    clean = text.replace("\n", " ").strip()
    if len(clean) <= max_chars or max_lines <= 1:
        return clean
    parts = _split_evenly(clean, min(max_lines, (len(clean) + max_chars - 1) // max_chars))
    return "\n".join(parts[:max_lines])


def _split_evenly(text: str, count: int) -> list[str]:
    if count <= 1 or len(text) <= 1:
        return [text]
    result: list[str] = []
    remaining = text
    for position in range(count - 1):
        desired = max(1, round(len(remaining) / (count - position)))
        boundary = desired
        for delta in range(0, min(12, len(remaining) - desired)):
            for candidate in (desired + delta, desired - delta):
                if 0 < candidate < len(remaining) and remaining[candidate] in " ，。！？,.!?":
                    boundary = candidate + 1
                    break
            else:
                continue
            break
        result.append(remaining[:boundary].strip())
        remaining = remaining[boundary:].strip()
    if remaining:
        result.append(remaining)
    return [item for item in result if item]


def _normalize_external_segments(raw: list[Any]) -> list[dict[str, Any]]:
    result: list[dict[str, Any]] = []
    for item in raw:
        if not isinstance(item, dict):
            continue
        try:
            start = float(item.get("start"))
            end = float(item.get("end"))
        except (TypeError, ValueError):
            continue
        text = str(item.get("text") or "").strip()
        if text and end > start:
            result.append(
                {
                    "index": len(result) + 1,
                    "start": start,
                    "end": end,
                    "text": text,
                }
            )
    return result


def _effective_model(
    models: dict[str, Any],
    name: str,
) -> tuple[dict[str, Any], str]:
    endpoint = _object(models, name)
    mode = str(endpoint.get("mode") or "")
    if name == "global":
        return endpoint, "model.global.api_key"
    if mode == "disabled":
        return {}, ""
    if mode == "inherit":
        inherited = dict(_object(models, "global"))
        for field in ("thinking", "temperature", "timeout_seconds"):
            if field in endpoint:
                inherited[field] = endpoint[field]
        return inherited, "model.global.api_key"
    return endpoint, f"model.{name}.api_key"


def _checkpoint_digest(
    stage: str,
    segments: list[dict[str, Any]],
    config: dict[str, Any],
) -> str:
    encoded = json.dumps(
        {
            "schema_version": 1,
            "stage": stage,
            "segments": segments,
            "config": config,
        },
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def _translation_items(value: dict[str, Any]) -> list[Any] | None:
    for key in ("translations", "segments", "items"):
        items = value.get(key)
        if isinstance(items, list):
            return items
    data = value.get("data")
    if isinstance(data, dict):
        for key in ("translations", "segments", "items"):
            items = data.get(key)
            if isinstance(items, list):
                return items
    return None


def _prompt_is_corrupted(value: str) -> bool:
    question_marks = value.count("?") + value.count("？")
    if question_marks < 3:
        return False
    without_placeholders = value.replace("?", "").replace("？", "")
    return not bool(re.search(r"[A-Za-z0-9\u3400-\u9fff]", without_placeholders))


def _compose_prompt(
    name: str,
    builtin: str,
    prompts: dict[str, Any],
) -> str:
    entry = _object(prompts, name)
    mode = str(entry.get("mode") or "builtin")
    custom = str(entry.get("text") or "").strip()
    if _prompt_is_corrupted(custom):
        custom = ""
    if mode == "replace":
        return custom or builtin
    if mode == "append" and custom:
        return f"{builtin}\n\n附加要求：\n{custom}"
    return builtin


def _chat_json(
    endpoint: dict[str, Any],
    secret_key: str,
    secrets: dict[str, Any],
    system_prompt: str,
    payload: dict[str, Any],
    retries: int = 2,
    on_attempt: callable | None = None,
) -> dict[str, Any]:
    provider = str(endpoint.get("provider") or "openai_compatible")
    api_key = str(secrets.get(secret_key) or "")
    if provider != "fixture" and not api_key:
        raise SubtitleFailure(
            "model_credentials_missing",
            f"任务快照缺少模型密钥：{secret_key}",
            False,
        )
    body = {
        "model": str(endpoint.get("model") or ""),
        "temperature": float(endpoint.get("temperature") or 0),
        "messages": [
            {"role": "system", "content": system_prompt},
            {
                "role": "user",
                "content": json.dumps(payload, ensure_ascii=False),
            },
        ],
        "response_format": {"type": "json_object"},
    }
    base_url = str(endpoint.get("base_url") or "").rstrip("/")
    hostname = (urllib.parse.urlsplit(base_url).hostname or "").lower()
    model = str(endpoint.get("model") or "").lower()
    if (
        hostname == "deepseek.com"
        or hostname.endswith(".deepseek.com")
        or model.startswith("deepseek-")
    ):
        body["thinking"] = {
            "type": "enabled" if bool(endpoint.get("thinking")) else "disabled"
        }
    url = base_url + "/chat/completions"
    attempts = max(1, retries + 1)
    last_failure: SubtitleFailure | None = None
    for attempt in range(attempts):
        if on_attempt is not None:
            on_attempt(attempt + 1, attempts)
        try:
            value = _json_request(
                url,
                body,
                api_key,
                timeout=max(5, int(endpoint.get("timeout_seconds") or 90)),
                retries=0,
            )
            return _decode_chat_response(value)
        except SubtitleFailure as exc:
            last_failure = exc
            if not exc.retryable or attempt + 1 >= attempts:
                raise
            time.sleep(min(2**attempt, 5))
    if last_failure is not None:
        raise last_failure
    raise AssertionError("unreachable")


def _decode_chat_response(value: dict[str, Any]) -> dict[str, Any]:
    try:
        choice = value["choices"][0]
        message = choice["message"]
        content = message["content"]
    except (KeyError, IndexError, TypeError) as exc:
        raise SubtitleFailure(
            "model_response_invalid",
            "模型响应缺少标准的消息内容",
            True,
        ) from exc
    try:
        decoded = _decode_model_json(content)
    except (TypeError, json.JSONDecodeError, ValueError) as exc:
        finish_reason = str(choice.get("finish_reason") or "未知")
        content_length = len(content) if isinstance(content, str) else 0
        reasoning = message.get("reasoning_content")
        reasoning_length = len(reasoning) if isinstance(reasoning, str) else 0
        reason_text = (
            "输出达到长度上限"
            if finish_reason == "length"
            else f"结束原因 {finish_reason}"
        )
        raise SubtitleFailure(
            "model_response_invalid",
            (
                "模型没有返回有效的 JSON 内容"
                f"（{reason_text}，正文 {content_length} 字符，"
                f"推理内容 {reasoning_length} 字符）"
            ),
            True,
        ) from exc
    if not isinstance(decoded, dict):
        raise SubtitleFailure(
            "model_response_invalid",
            "模型 JSON 响应必须是对象",
            True,
        )
    return decoded


def _decode_model_json(content: Any) -> Any:
    if isinstance(content, (dict, list)):
        return content
    if not isinstance(content, str):
        raise TypeError("model content is not text or JSON")
    stripped = content.strip()
    if not stripped:
        raise ValueError("model content is empty")

    candidates = [stripped]
    fenced = re.fullmatch(
        r"```(?:json)?\s*(.*?)\s*```",
        stripped,
        flags=re.IGNORECASE | re.DOTALL,
    )
    if fenced:
        candidates.insert(0, fenced.group(1).strip())
    first_object = stripped.find("{")
    last_object = stripped.rfind("}")
    if first_object >= 0 and last_object > first_object:
        candidates.append(stripped[first_object : last_object + 1])

    last_error: json.JSONDecodeError | None = None
    for candidate in dict.fromkeys(candidates):
        try:
            return json.loads(candidate)
        except json.JSONDecodeError as exc:
            last_error = exc
    if last_error is not None:
        raise last_error
    raise ValueError("model content contains no JSON document")


def _json_request(
    url: str,
    body: dict[str, Any],
    api_key: str,
    *,
    timeout: int,
    retries: int,
    on_attempt: callable | None = None,
) -> dict[str, Any]:
    encoded = json.dumps(body, ensure_ascii=False).encode("utf-8")
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"
    for attempt in range(retries + 1):
        if on_attempt is not None:
            on_attempt(attempt + 1, retries + 1)
        try:
            raw = _request_bytes_with_deadline(
                url,
                encoded,
                headers,
                timeout_seconds=timeout,
                maximum_bytes=8 * 1024 * 1024,
            )
            value = json.loads(raw)
            if not isinstance(value, dict):
                raise ValueError("response is not an object")
            return value
        except (urllib.error.URLError, TimeoutError, OSError, json.JSONDecodeError, ValueError) as exc:
            if attempt >= retries:
                raise SubtitleFailure(
                    "model_request_failed",
                    f"模型请求失败：{exc}",
                    True,
                ) from exc
            time.sleep(min(2**attempt, 5))
    raise AssertionError("unreachable")


def _request_bytes_with_deadline(
    url: str,
    body: bytes,
    headers: dict[str, str],
    *,
    timeout_seconds: int,
    maximum_bytes: int,
) -> bytes:
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme not in {"http", "https"} or not parsed.hostname:
        raise ValueError("model endpoint must use http or https")
    connection_type = (
        http.client.HTTPSConnection
        if parsed.scheme == "https"
        else http.client.HTTPConnection
    )
    connection = connection_type(
        parsed.hostname,
        parsed.port,
        timeout=max(1, timeout_seconds),
    )
    path = urllib.parse.urlunsplit(
        ("", "", parsed.path or "/", parsed.query, "")
    )
    deadline = time.monotonic() + max(1, timeout_seconds)
    payload = bytearray()
    received = 0
    response: http.client.HTTPResponse | None = None

    def remaining_timeout() -> float:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError(
                f"total request timeout exceeded ({timeout_seconds}s)"
            )
        if connection.sock is not None:
            connection.sock.settimeout(max(0.1, remaining))
        return remaining

    try:
        connection.request("POST", path, body=body, headers=headers)
        remaining_timeout()
        response = connection.getresponse()
        content_length = response.getheader("Content-Length")
        if content_length:
            try:
                declared_length = int(content_length)
            except ValueError:
                declared_length = None
            if declared_length is not None and declared_length > maximum_bytes:
                raise ValueError("model response is too large")
        while received <= maximum_bytes:
            remaining_timeout()
            read_size = min(64 * 1024, maximum_bytes + 1 - received)
            chunk = response.read1(read_size)
            remaining_timeout()
            if not chunk:
                break
            payload.extend(chunk)
            received += len(chunk)
            try:
                json.loads(payload)
            except (UnicodeDecodeError, json.JSONDecodeError):
                pass
            else:
                # Some OpenAI-compatible gateways send a complete JSON document
                # but keep a chunked HTTP connection open without promptly writing
                # the terminating chunk. Waiting for EOF turns a valid response into
                # a false timeout, so stop as soon as the document is complete.
                break
        raw = bytes(payload)
        if received > maximum_bytes:
            raise ValueError("model response is too large")
        if response.status < 200 or response.status >= 300:
            excerpt = raw.decode("utf-8", errors="replace")[:500]
            raise OSError(
                f"HTTP {response.status} {response.reason}: {excerpt}"
            )
        return raw
    finally:
        if response is not None:
            response.close()
        connection.close()


def _multipart_json_request(
    url: str,
    fields: dict[str, str],
    file_field: str,
    file_path: Path,
    api_key: str,
    *,
    timeout: int,
    retries: int,
) -> dict[str, Any]:
    boundary = f"visoraft-{uuid.uuid4().hex}"
    chunks: list[bytes] = []
    for name, value in fields.items():
        chunks.extend(
            [
                f"--{boundary}\r\n".encode(),
                (
                    f'Content-Disposition: form-data; name="{name}"\r\n\r\n'
                ).encode(),
                str(value).encode("utf-8"),
                b"\r\n",
            ]
        )
    filename = file_path.name
    content_type = mimetypes.guess_type(filename)[0] or "application/octet-stream"
    chunks.extend(
        [
            f"--{boundary}\r\n".encode(),
            (
                f'Content-Disposition: form-data; name="{file_field}"; '
                f'filename="{filename}"\r\n'
            ).encode(),
            f"Content-Type: {content_type}\r\n\r\n".encode(),
            file_path.read_bytes(),
            b"\r\n",
            f"--{boundary}--\r\n".encode(),
        ]
    )
    body = b"".join(chunks)
    headers = {
        "Content-Type": f"multipart/form-data; boundary={boundary}",
        "Accept": "application/json",
    }
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"
    for attempt in range(retries + 1):
        request = urllib.request.Request(url, data=body, headers=headers, method="POST")
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                raw = response.read(16 * 1024 * 1024)
            value = json.loads(raw)
            if not isinstance(value, dict):
                raise ValueError("response is not an object")
            return value
        except (urllib.error.URLError, TimeoutError, OSError, json.JSONDecodeError, ValueError) as exc:
            if attempt >= retries:
                raise SubtitleFailure(
                    "asr_request_failed",
                    f"ASR 请求失败：{exc}",
                    True,
                ) from exc
            time.sleep(min(2**attempt, 5))
    raise AssertionError("unreachable")


def _transcribe_aliyun_paraformer(
    audio_path: Path,
    asr: dict[str, Any],
    api_key: str,
    should_cancel: callable,
    report: callable | None = None,
) -> list[dict[str, Any]]:
    def progress(value: int, phase: str, **detail: Any) -> None:
        if report is not None:
            report(value, phase, **detail)

    base_url = str(asr.get("base_url") or "").rstrip("/")
    model = str(asr.get("model") or "paraformer-v2").strip()
    timeout = max(10, int(asr.get("timeout_seconds") or 600))
    retries = max(0, int(asr.get("max_retries") or 0))
    policy_url = (
        f"{base_url}/uploads?"
        + urllib.parse.urlencode({"action": "getPolicy", "model": model})
    )
    policy_response = _aliyun_json_request(
        policy_url,
        api_key,
        method="GET",
        timeout=min(timeout, 60),
        retries=retries,
        failure_code="asr_upload_policy_failed",
    )
    policy = policy_response.get("data")
    if not isinstance(policy, dict):
        raise SubtitleFailure(
            "asr_upload_policy_failed",
            "阿里云 ASR 未返回临时上传凭证",
            True,
        )
    progress(12, "asr_upload_preparing")

    max_file_size_mb = _number(policy.get("max_file_size_mb"), 0)
    if max_file_size_mb > 0 and audio_path.stat().st_size > max_file_size_mb * 1024 * 1024:
        raise SubtitleFailure(
            "asr_upload_too_large",
            f"ASR 音频超过阿里云临时上传上限 {max_file_size_mb:g} MB",
            False,
        )
    upload_host = str(policy.get("upload_host") or "").strip()
    upload_dir = str(policy.get("upload_dir") or "").strip().strip("/")
    access_key_id = str(
        policy.get("oss_access_key_id") or policy.get("accessid") or ""
    ).strip()
    signature = str(policy.get("signature") or "").strip()
    encoded_policy = str(policy.get("policy") or "").strip()
    if not all((upload_host, upload_dir, access_key_id, signature, encoded_policy)):
        raise SubtitleFailure(
            "asr_upload_policy_failed",
            "阿里云 ASR 临时上传凭证字段不完整",
            True,
        )
    object_key = f"{upload_dir}/visoraft-{uuid.uuid4().hex}{audio_path.suffix}"
    upload_fields = {
        "OSSAccessKeyId": access_key_id,
        "Signature": signature,
        "policy": encoded_policy,
        "x-oss-object-acl": str(policy.get("x_oss_object_acl") or "private"),
        "x-oss-forbid-overwrite": str(
            policy.get("x_oss_forbid_overwrite") or "true"
        ),
        "key": object_key,
        "success_action_status": "200",
    }
    if should_cancel():
        raise SubtitleFailure("subtitle_cancelled", "字幕处理已取消", True)
    _multipart_upload_file(
        upload_host,
        upload_fields,
        "file",
        audio_path,
        timeout=timeout,
    )
    progress(18, "asr_uploaded")

    parameters: dict[str, Any] = {
        "channel_id": [0],
        "timestamp_alignment_enabled": True,
    }
    language = str(asr.get("language") or "").strip()
    if language and language.lower() != "auto":
        parameters["language_hints"] = [
            item.strip() for item in language.split(",") if item.strip()
        ]
    submit_response = _aliyun_json_request(
        f"{base_url}/services/audio/asr/transcription",
        api_key,
        method="POST",
        body={
            "model": model,
            "input": {"file_urls": [f"oss://{object_key}"]},
            "parameters": parameters,
        },
        headers={
            "X-DashScope-Async": "enable",
            "X-DashScope-OssResourceResolve": "enable",
        },
        timeout=min(timeout, 60),
        retries=retries,
        failure_code="asr_submit_failed",
    )
    output = submit_response.get("output")
    task_id = str(output.get("task_id") or "") if isinstance(output, dict) else ""
    if not task_id:
        raise SubtitleFailure(
            "asr_submit_failed",
            "阿里云 ASR 未返回任务 ID",
            True,
        )
    progress(22, "asr_waiting", remote_task_id=task_id, remote_status="PENDING")

    deadline = time.monotonic() + timeout
    task_output: dict[str, Any] = {}
    while time.monotonic() < deadline:
        if should_cancel():
            raise SubtitleFailure("subtitle_cancelled", "字幕处理已取消", True)
        task_response = _aliyun_json_request(
            f"{base_url}/tasks/{urllib.parse.quote(task_id, safe='')}",
            api_key,
            method="GET",
            timeout=min(60, max(10, int(deadline - time.monotonic()))),
            retries=retries,
            failure_code="asr_poll_failed",
        )
        candidate = task_response.get("output")
        task_output = candidate if isinstance(candidate, dict) else {}
        status = str(task_output.get("task_status") or "").upper()
        progress(25, "asr_waiting", remote_task_id=task_id, remote_status=status)
        if status == "SUCCEEDED":
            break
        if status in {"FAILED", "CANCELED", "CANCELLED", "UNKNOWN"}:
            detail = str(
                task_output.get("message")
                or task_output.get("code")
                or "远端任务失败"
            )
            raise SubtitleFailure(
                "asr_remote_failed",
                f"阿里云 ASR 任务未成功：{detail}",
                status != "FAILED",
            )
        time.sleep(min(2.0, max(0.1, deadline - time.monotonic())))
    else:
        raise SubtitleFailure(
            "asr_timeout",
            f"阿里云 ASR 在 {timeout} 秒内未完成",
            True,
        )

    results = task_output.get("results")
    if not isinstance(results, list) or not results:
        raise SubtitleFailure(
            "asr_result_failed",
            "阿里云 ASR 任务成功但没有文件结果",
            True,
        )
    first = results[0]
    if not isinstance(first, dict):
        raise SubtitleFailure("asr_result_failed", "阿里云 ASR 文件结果格式无效", True)
    subtask_status = str(first.get("subtask_status") or "SUCCEEDED").upper()
    if subtask_status != "SUCCEEDED":
        detail = str(first.get("message") or first.get("code") or subtask_status)
        raise SubtitleFailure(
            "asr_result_failed",
            f"阿里云 ASR 文件转写失败：{detail}",
            False,
        )
    transcription_url = str(first.get("transcription_url") or "").strip()
    if not transcription_url:
        raise SubtitleFailure(
            "asr_result_failed",
            "阿里云 ASR 未返回转写结果地址",
            True,
        )
    transcript = _aliyun_json_request(
        transcription_url,
        "",
        method="GET",
        timeout=min(timeout, 60),
        retries=retries,
        failure_code="asr_result_download_failed",
    )
    segments: list[dict[str, Any]] = []
    transcripts = transcript.get("transcripts")
    if isinstance(transcripts, list):
        for track in transcripts:
            if not isinstance(track, dict):
                continue
            sentences = track.get("sentences")
            if not isinstance(sentences, list):
                continue
            for sentence in sentences:
                if not isinstance(sentence, dict):
                    continue
                text = str(sentence.get("text") or "").strip()
                if not text:
                    continue
                start = _number(sentence.get("begin_time"), 0) / 1000
                end = _number(sentence.get("end_time"), 0) / 1000
                if end <= start:
                    end = start + 0.7
                segments.append(
                    {
                        "index": len(segments) + 1,
                        "start": start,
                        "end": end,
                        "text": text,
                    }
                )
    if not segments:
        raise SubtitleFailure(
            "asr_empty_result",
            "阿里云 ASR 返回了空转写结果",
            True,
        )
    return segments


def _aliyun_json_request(
    url: str,
    api_key: str,
    *,
    method: str,
    timeout: int,
    retries: int,
    failure_code: str,
    body: dict[str, Any] | None = None,
    headers: dict[str, str] | None = None,
) -> dict[str, Any]:
    payload = json.dumps(body, ensure_ascii=False).encode("utf-8") if body is not None else None
    request_headers = {"Accept": "application/json"}
    if body is not None:
        request_headers["Content-Type"] = "application/json"
    if api_key:
        request_headers["Authorization"] = f"Bearer {api_key}"
    if headers:
        request_headers.update(headers)
    for attempt in range(retries + 1):
        request = urllib.request.Request(
            url,
            data=payload,
            headers=request_headers,
            method=method,
        )
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                raw = response.read(16 * 1024 * 1024)
            value = json.loads(raw)
            if not isinstance(value, dict):
                raise ValueError("response is not an object")
            return value
        except urllib.error.HTTPError as exc:
            raw = exc.read(32 * 1024).decode("utf-8", errors="replace")
            retryable = exc.code == 429 or exc.code >= 500
            if attempt >= retries or not retryable:
                raise SubtitleFailure(
                    failure_code,
                    f"阿里云 ASR 请求失败（HTTP {exc.code}）：{_safe_remote_message(raw)}",
                    retryable,
                ) from exc
        except (urllib.error.URLError, TimeoutError, OSError, json.JSONDecodeError, ValueError) as exc:
            if attempt >= retries:
                raise SubtitleFailure(
                    failure_code,
                    f"阿里云 ASR 请求失败：{exc}",
                    True,
                ) from exc
        time.sleep(min(2**attempt, 5))
    raise AssertionError("unreachable")


def _multipart_upload_file(
    url: str,
    fields: dict[str, str],
    file_field: str,
    file_path: Path,
    *,
    timeout: int,
) -> None:
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme not in {"http", "https"} or not parsed.hostname:
        raise SubtitleFailure("asr_upload_failed", "阿里云 ASR 上传地址无效", False)
    boundary = f"visoraft-{uuid.uuid4().hex}"
    prefixes: list[bytes] = []
    for name, value in fields.items():
        prefixes.append(
            (
                f"--{boundary}\r\n"
                f'Content-Disposition: form-data; name="{name}"\r\n\r\n'
                f"{value}\r\n"
            ).encode("utf-8")
        )
    content_type = mimetypes.guess_type(file_path.name)[0] or "application/octet-stream"
    file_header = (
        f"--{boundary}\r\n"
        f'Content-Disposition: form-data; name="{file_field}"; filename="{file_path.name}"\r\n'
        f"Content-Type: {content_type}\r\n\r\n"
    ).encode("utf-8")
    closing = f"\r\n--{boundary}--\r\n".encode("utf-8")
    content_length = (
        sum(len(item) for item in prefixes)
        + len(file_header)
        + file_path.stat().st_size
        + len(closing)
    )
    connection_type = (
        http.client.HTTPSConnection if parsed.scheme == "https" else http.client.HTTPConnection
    )
    connection = connection_type(parsed.hostname, parsed.port, timeout=timeout)
    target = parsed.path or "/"
    if parsed.query:
        target += "?" + parsed.query
    try:
        connection.putrequest("POST", target)
        connection.putheader("Content-Type", f"multipart/form-data; boundary={boundary}")
        connection.putheader("Content-Length", str(content_length))
        connection.endheaders()
        for item in prefixes:
            connection.send(item)
        connection.send(file_header)
        with file_path.open("rb") as source:
            while chunk := source.read(1024 * 1024):
                connection.send(chunk)
        connection.send(closing)
        response = connection.getresponse()
        raw = response.read(32 * 1024).decode("utf-8", errors="replace")
        if response.status < 200 or response.status >= 300:
            raise SubtitleFailure(
                "asr_upload_failed",
                f"阿里云 ASR 音频上传失败（HTTP {response.status}）：{_safe_remote_message(raw)}",
                response.status == 429 or response.status >= 500,
            )
    except SubtitleFailure:
        raise
    except (OSError, TimeoutError, http.client.HTTPException) as exc:
        raise SubtitleFailure(
            "asr_upload_failed",
            f"阿里云 ASR 音频上传失败：{exc}",
            True,
        ) from exc
    finally:
        connection.close()


def _safe_remote_message(raw: str) -> str:
    raw = raw.strip()
    if not raw:
        return "远端未返回错误详情"
    try:
        value = json.loads(raw)
    except json.JSONDecodeError:
        return raw[:500]
    if isinstance(value, dict):
        return str(value.get("message") or value.get("code") or "请求未成功")[:500]
    return "请求未成功"


def _number(value: Any, fallback: float) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return fallback


def _object(value: dict[str, Any], key: str) -> dict[str, Any]:
    candidate = value.get(key)
    return candidate if isinstance(candidate, dict) else {}
