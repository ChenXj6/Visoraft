from __future__ import annotations

import json
import urllib.error
import urllib.request
from typing import Any


class ProcessingConfigFailure(RuntimeError):
    """Raised when a task's immutable processing snapshot cannot be loaded."""


class ProcessingConfigClient:
    def __init__(self, control_api_url: str, worker_token: str) -> None:
        self._control_api_url = control_api_url.rstrip("/")
        self._worker_token = worker_token

    def get(self, task_id: str) -> dict[str, Any]:
        request = urllib.request.Request(
            (
                f"{self._control_api_url}/internal/v1/tasks/"
                f"{task_id}/processing-config"
            ),
            method="GET",
            headers={
                "Accept": "application/json",
                "Authorization": f"Bearer {self._worker_token}",
            },
        )
        try:
            with urllib.request.urlopen(request, timeout=15) as response:
                raw = response.read(2 * 1024 * 1024)
        except (urllib.error.URLError, TimeoutError, OSError) as exc:
            raise ProcessingConfigFailure(
                f"could not load task processing configuration: {exc}"
            ) from exc
        try:
            value = json.loads(raw)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise ProcessingConfigFailure(
                "task processing configuration is not valid JSON"
            ) from exc
        if not isinstance(value, dict):
            raise ProcessingConfigFailure(
                "task processing configuration must be an object"
            )
        return value
