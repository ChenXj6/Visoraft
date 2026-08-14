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
        self._cache: dict[str, tuple[float, str]] = {}

    def is_cancelled(self, task_id: str, *, force: bool = False) -> bool:
        return self.control_state(task_id, force=force) != "active"

    def last_state(self, task_id: str) -> str:
        return self._cache.get(task_id, (0.0, "active"))[1]

    def control_state(self, task_id: str, *, force: bool = False) -> str:
        now = time.monotonic()
        last_checked, last_value = self._cache.get(task_id, (0.0, "active"))
        if not force and now - last_checked < self._interval_seconds:
            return last_value

        request = Request(
            f"{self._control_api_url}/api/v1/tasks/{quote(task_id, safe='')}",
            headers={"Accept": "application/json", "User-Agent": "visoraft-media-worker/0.1"},
        )
        try:
            with urlopen(request, timeout=2) as response:
                payload = json.load(response)
            status = str(payload.get("status", ""))
            value = "active"
            if payload.get("paused_at"):
                value = "paused"
            elif status in {"cancelled", "abandoned"}:
                value = "cancelled"
            self._cache[task_id] = (now, value)
            return value
        except HTTPError as exc:
            if exc.code == 404:
                self._cache[task_id] = (now, "cancelled")
                return "cancelled"
            LOGGER.warning("cancellation probe HTTP error task_id=%s status=%s", task_id, exc.code)
        except (URLError, TimeoutError, OSError, ValueError) as exc:
            LOGGER.warning("cancellation probe unavailable task_id=%s error=%s", task_id, exc)

        self._cache[task_id] = (now, last_value)
        return last_value


class CancellationLatch:
    """Keep the first cooperative stop reason stable for one worker attempt."""

    def __init__(self, probe: CancellationProbe, task_id: str) -> None:
        self._probe = probe
        self._task_id = task_id
        self._state = "active"

    @property
    def state(self) -> str:
        return self._state

    def cancel_locally(self) -> bool:
        if self._state == "active":
            self._state = "cancelled"
        return True

    def should_cancel(self, *, force: bool = False) -> bool:
        if self._state != "active":
            return True
        if not self._probe.is_cancelled(self._task_id, force=force):
            return False
        observed = self._probe.last_state(self._task_id)
        self._state = observed if observed in {"paused", "cancelled"} else "cancelled"
        return True
