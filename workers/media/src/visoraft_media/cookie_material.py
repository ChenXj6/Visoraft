from __future__ import annotations

import contextlib
import os
import tempfile
from collections.abc import Iterator
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import quote
from urllib.request import Request, urlopen

MAX_COOKIE_BYTES = 5 * 1024 * 1024


class CookieMaterialFailure(RuntimeError):
    """Cookie material could not be obtained from the control plane."""


class CookieMaterialClient:
    def __init__(self, control_api_url: str, worker_token: str) -> None:
        self._control_api_url = control_api_url.rstrip("/")
        self._worker_token = worker_token

    @contextlib.contextmanager
    def materialize(self, profile_id: str | None) -> Iterator[Path | None]:
        normalized = (profile_id or "").strip()
        if not normalized:
            yield None
            return

        request = Request(
            (
                f"{self._control_api_url}/internal/v1/cookie-profiles/"
                f"{quote(normalized, safe='')}/netscape"
            ),
            headers={
                "Accept": "text/plain",
                "Authorization": f"Bearer {self._worker_token}",
                "User-Agent": "visoraft-media-worker/0.1",
            },
        )
        try:
            with urlopen(request, timeout=5) as response:
                content = response.read(MAX_COOKIE_BYTES + 1)
        except HTTPError as exc:
            if exc.code == 404:
                raise CookieMaterialFailure("选择的 Cookie 配置不存在") from exc
            if exc.code == 409:
                raise CookieMaterialFailure(
                    "Cookie 配置尚无可用内容，请先同步或重新上传"
                ) from exc
            raise CookieMaterialFailure(
                f"读取 Cookie 配置失败（HTTP {exc.code}）"
            ) from exc
        except (URLError, TimeoutError, OSError) as exc:
            raise CookieMaterialFailure("控制面暂时无法提供 Cookie 配置") from exc

        if len(content) > MAX_COOKIE_BYTES:
            raise CookieMaterialFailure("Cookie 配置超过 5 MiB 安全限制")
        if not content.startswith((b"# HTTP Cookie File", b"# Netscape HTTP Cookie File")):
            raise CookieMaterialFailure("控制面返回的 Cookie 文件格式无效")

        descriptor, raw_path = tempfile.mkstemp(prefix="visoraft-cookie-", suffix=".txt")
        path = Path(raw_path)
        try:
            if hasattr(os, "fchmod"):
                os.fchmod(descriptor, 0o600)
            stream = os.fdopen(descriptor, "wb")
            descriptor = -1
            with stream:
                stream.write(content)
                stream.flush()
            if os.name == "nt":
                # Windows only exposes the read-only bit through chmod. The
                # file remains inside the current user's protected Temp
                # directory; production Linux still enforces exact 0600.
                os.chmod(path, 0o600)
            yield path
        finally:
            if descriptor >= 0:
                os.close(descriptor)
            path.unlink(missing_ok=True)
