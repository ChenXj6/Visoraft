from __future__ import annotations

import json
import logging
import time
from urllib.error import HTTPError, URLError
from urllib.parse import quote
from urllib.request import Request, urlopen

LOGGER = logging.getLogger("visoraft.media-worker.cancellation")


class CancellationProbe:
    def __init__(self, control_api_url: str, interval_seconds: float = 1.0) -> None:
        self._control_api_url = control_api_url.rstrip("/")
        self._interval_seconds = interval_seconds
        self._cache: dict[str, tuple[float, bool]] = {}

    def is_cancelled(self, task_id: str, *, force: bool = False) -> bool:
        now = time.monotonic()
        last_checked, last_value = self._cache.get(task_id, (0.0, False))
        if not force and now - last_checked < self._interval_seconds:
            return last_value

        request = Request(
            f"{self._control_api_url}/api/v1/tasks/{quote(task_id, safe='')}",
            headers={"Accept": "application/json", "User-Agent": "visoraft-media-worker/0.1"},
        )
        try:
            with urlopen(request, timeout=2) as response:
                payload = json.load(response)
            value = str(payload.get("status", "")) in {"cancelled", "abandoned"}
            self._cache[task_id] = (now, value)
            return value
        except HTTPError as exc:
            if exc.code == 404:
                self._cache[task_id] = (now, True)
                return True
            LOGGER.warning("cancellation probe HTTP error task_id=%s status=%s", task_id, exc.code)
        except (URLError, TimeoutError, OSError, ValueError) as exc:
            LOGGER.warning("cancellation probe unavailable task_id=%s error=%s", task_id, exc)

        self._cache[task_id] = (now, last_value)
        return last_value
