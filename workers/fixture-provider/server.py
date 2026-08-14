from __future__ import annotations

import io
import json
import math
import os
import struct
import threading
import time
import wave
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any


def build_wave(duration_seconds: int = 4) -> bytes:
    sample_rate = 16_000
    output = io.BytesIO()
    with wave.open(output, "wb") as target:
        target.setnchannels(1)
        target.setsampwidth(2)
        target.setframerate(sample_rate)
        frames = bytearray()
        for index in range(sample_rate * duration_seconds):
            frequency = 440 if index < sample_rate * 2 else 554
            amplitude = int(
                8_000 * math.sin(2 * math.pi * frequency * index / sample_rate)
            )
            frames.extend(struct.pack("<h", amplitude))
        target.writeframes(bytes(frames))
    return output.getvalue()


WAVE_BYTES = build_wave()
SLOW_WAVE_BYTES = build_wave(duration_seconds=60)
FAIL_ONCE_LOCK = threading.Lock()
FAIL_ONCE_ASR_CALLS = 0


class Handler(BaseHTTPRequestHandler):
    server_version = "VisoraftFixture/1.0"

    def do_HEAD(self) -> None:
        if self.path in {"/media/sample.wav", "/media/slow-sample.wav"}:
            self._send_media(
                head_only=True,
                slow=self.path == "/media/slow-sample.wav",
            )
            return
        if self.path == "/health":
            self.send_response(HTTPStatus.OK)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        self.send_error(HTTPStatus.NOT_FOUND)

    def do_GET(self) -> None:
        if self.path == "/health":
            self._json({"status": "ready", "fixture_only": True})
            return
        if self.path in {"/v1/models", "/models"}:
            self._json(
                {
                    "object": "list",
                    "data": [
                        {"id": "visoraft-fixture-model", "object": "model"},
                        {"id": "visoraft-fixture-asr", "object": "model"},
                    ],
                    "fixture_only": True,
                }
            )
            return
        if self.path in {"/media/sample.wav", "/media/slow-sample.wav"}:
            self._send_media(
                head_only=False,
                slow=self.path == "/media/slow-sample.wav",
            )
            return
        self.send_error(HTTPStatus.NOT_FOUND)

    def do_POST(self) -> None:
        if self.path == "/v1/fail-once/audio/transcriptions":
            if not self._drain_request_body():
                return
            global FAIL_ONCE_ASR_CALLS
            with FAIL_ONCE_LOCK:
                FAIL_ONCE_ASR_CALLS += 1
                call_number = FAIL_ONCE_ASR_CALLS
            if call_number == 1:
                self._json(
                    {
                        "error": {
                            "code": "fixture_asr_transient_failure",
                            "message": "本地故障注入：首次语音转写请求暂时不可用",
                        },
                        "fixture_only": True,
                    },
                    status=HTTPStatus.SERVICE_UNAVAILABLE,
                )
                return
            self._send_transcription()
            return
        if self.path in {
            "/v1/audio/transcriptions",
            "/audio/transcriptions",
        }:
            if not self._drain_request_body():
                return
            self._send_transcription()
            return
        if self.path in {"/v1/chat/completions", "/chat/completions"}:
            self._chat_completion()
            return
        self.send_error(HTTPStatus.NOT_FOUND)

    def _send_transcription(self) -> None:
        self._json(
            {
                "text": "欢迎使用 Visoraft 本地字幕验收流程。",
                "language": "zh",
                "duration": 4,
                "segments": [
                    {
                        "id": 0,
                        "start": 0.0,
                        "end": 1.9,
                        "text": "欢迎使用 Visoraft。",
                    },
                    {
                        "id": 1,
                        "start": 2.0,
                        "end": 4.0,
                        "text": "这是本地字幕验收流程。",
                    },
                ],
                "fixture_only": True,
            }
        )

    def _chat_completion(self) -> None:
        payload = self._read_json()
        messages = payload.get("messages") if isinstance(payload, dict) else []
        system = ""
        user_payload: dict[str, Any] = {}
        if isinstance(messages, list):
            for message in messages:
                if not isinstance(message, dict):
                    continue
                if message.get("role") == "system":
                    system = str(message.get("content") or "")
                if message.get("role") == "user":
                    try:
                        decoded = json.loads(str(message.get("content") or "{}"))
                        if isinstance(decoded, dict):
                            user_payload = decoded
                    except json.JSONDecodeError:
                        pass

        if "translations" in system:
            translations = []
            for item in user_payload.get("segments", []):
                if isinstance(item, dict):
                    translations.append(
                        {
                            "index": item.get("index"),
                            "text": f"译文 · {str(item.get('text') or '').strip()}",
                        }
                    )
            content = {"translations": translations}
        elif "质检" in system or '"score"' in system:
            content = {
                "score": 96,
                "issues": [],
                "summary": "本地验收提供商确认索引完整、时间轴连续。",
            }
        elif '"segments"' in system:
            content = {"segments": user_payload.get("segments", [])}
        else:
            content = {"ok": True, "fixture_only": True}

        self._json(
            {
                "id": "fixture-chat-completion",
                "object": "chat.completion",
                "choices": [
                    {
                        "index": 0,
                        "message": {
                            "role": "assistant",
                            "content": json.dumps(content, ensure_ascii=False),
                        },
                        "finish_reason": "stop",
                    }
                ],
                "usage": {
                    "prompt_tokens": 1,
                    "completion_tokens": 1,
                    "total_tokens": 2,
                },
                "fixture_only": True,
            }
        )

    def _read_json(self) -> dict[str, Any]:
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            length = 0
        raw = self.rfile.read(min(length, 16 * 1024 * 1024))
        try:
            value = json.loads(raw)
        except (UnicodeDecodeError, json.JSONDecodeError):
            return {}
        return value if isinstance(value, dict) else {}

    def _drain_request_body(self) -> bool:
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            length = 0
        max_request_bytes = 2 * 1024 * 1024 * 1024
        if length < 0 or length > max_request_bytes:
            self.send_error(
                HTTPStatus.REQUEST_ENTITY_TOO_LARGE,
                "fixture upload exceeds the 2 GiB local validation limit",
            )
            return False
        remaining = length
        while remaining > 0:
            chunk = self.rfile.read(min(remaining, 1024 * 1024))
            if not chunk:
                self.send_error(
                    HTTPStatus.BAD_REQUEST,
                    "fixture upload ended before Content-Length bytes arrived",
                )
                return False
            remaining -= len(chunk)
        return True

    def _send_media(self, *, head_only: bool, slow: bool = False) -> None:
        media = SLOW_WAVE_BYTES if slow else WAVE_BYTES
        start = 0
        end = len(media) - 1
        status = HTTPStatus.OK
        raw_range = self.headers.get("Range", "")
        if raw_range.startswith("bytes="):
            try:
                start_text, end_text = raw_range[6:].split("-", 1)
                start = int(start_text or "0")
                end = int(end_text) if end_text else end
                start = max(0, min(start, len(media) - 1))
                end = max(start, min(end, len(media) - 1))
                status = HTTPStatus.PARTIAL_CONTENT
            except (ValueError, TypeError):
                start = 0
                end = len(media) - 1
        content = media[start : end + 1]
        self.send_response(status)
        self.send_header("Content-Type", "audio/wav")
        self.send_header("Content-Length", str(len(content)))
        self.send_header("Accept-Ranges", "bytes")
        self.send_header(
            "Content-Disposition",
            (
                'inline; filename="visoraft-slow-download-fixture.wav"'
                if slow
                else 'inline; filename="visoraft-local-fixture.wav"'
            ),
        )
        if status == HTTPStatus.PARTIAL_CONTENT:
            self.send_header(
                "Content-Range",
                f"bytes {start}-{end}/{len(media)}",
            )
        self.end_headers()
        if not head_only:
            if slow:
                self._write_slowly(content)
            else:
                self.wfile.write(content)

    def _write_slowly(self, content: bytes) -> None:
        chunk_size = 8 * 1024
        for offset in range(0, len(content), chunk_size):
            try:
                self.wfile.write(content[offset : offset + chunk_size])
                self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError):
                return
            time.sleep(0.25)

    def _json(self, value: dict[str, Any], status: HTTPStatus = HTTPStatus.OK) -> None:
        encoded = json.dumps(value, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(encoded)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(encoded)

    def log_message(self, format_string: str, *args: Any) -> None:
        print(
            f"fixture-provider {self.address_string()} "
            f"{format_string % args}",
            flush=True,
        )


if __name__ == "__main__":
    port = int(os.getenv("VISORAFT_FIXTURE_PORT", "8090"))
    server = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    print(
        f"Visoraft fixture-only provider listening on :{port}",
        flush=True,
    )
    server.serve_forever()
