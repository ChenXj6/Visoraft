from __future__ import annotations

import hashlib
import struct
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path

MAXIMUM_COVER_BYTES = 10 * 1024 * 1024


class CoverImportFailure(RuntimeError):
    """Raised when a remote task thumbnail cannot become a publish cover."""


@dataclass(frozen=True)
class ImportedCover:
    path: Path
    content_type: str
    extension: str
    size_bytes: int
    checksum_sha256: str
    width: int
    height: int


def import_cover(raw_url: str, directory: Path) -> ImportedCover:
    source_url = _validated_url(raw_url)
    request = urllib.request.Request(
        source_url,
        headers={
            "Accept": "image/jpeg,image/png;q=0.9",
            "User-Agent": "Visoraft/0.1 cover-import",
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            _validated_url(response.geturl())
            if response.status != 200:
                raise CoverImportFailure(
                    f"cover download returned HTTP {response.status}"
                )
            body = response.read(MAXIMUM_COVER_BYTES + 1)
    except CoverImportFailure:
        raise
    except (urllib.error.URLError, TimeoutError, OSError) as exc:
        raise CoverImportFailure(f"could not download cover: {exc}") from exc

    if not body or len(body) > MAXIMUM_COVER_BYTES:
        raise CoverImportFailure("cover must be between 1 byte and 10 MiB")
    content_type, extension, width, height = _inspect_image(body)
    if width < 480 or height < 270:
        raise CoverImportFailure(
            f"cover dimensions {width}x{height} are below 480x270"
        )
    checksum = hashlib.sha256(body).hexdigest()
    target = directory / f"cover{extension}"
    target.write_bytes(body)
    return ImportedCover(
        path=target,
        content_type=content_type,
        extension=extension,
        size_bytes=len(body),
        checksum_sha256=checksum,
        width=width,
        height=height,
    )


def _validated_url(raw_url: str) -> str:
    parsed = urllib.parse.urlparse(str(raw_url).strip())
    host = (parsed.hostname or "").lower().rstrip(".")
    trusted = host in {"i.ytimg.com", "img.youtube.com"} or host.endswith(
        ".ytimg.com"
    )
    if (
        parsed.scheme != "https"
        or not trusted
        or parsed.username is not None
        or parsed.password is not None
        or parsed.port not in (None, 443)
    ):
        raise CoverImportFailure("cover URL is not a trusted YouTube HTTPS image")
    return parsed.geturl()


def _inspect_image(body: bytes) -> tuple[str, str, int, int]:
    if body.startswith(b"\x89PNG\r\n\x1a\n") and len(body) >= 24:
        width, height = struct.unpack(">II", body[16:24])
        return "image/png", ".png", width, height
    if body.startswith(b"\xff\xd8\xff"):
        width, height = _jpeg_dimensions(body)
        return "image/jpeg", ".jpg", width, height
    raise CoverImportFailure("cover is not a valid JPEG or PNG image")


def _jpeg_dimensions(body: bytes) -> tuple[int, int]:
    offset = 2
    start_of_frame = {
        0xC0,
        0xC1,
        0xC2,
        0xC3,
        0xC5,
        0xC6,
        0xC7,
        0xC9,
        0xCA,
        0xCB,
        0xCD,
        0xCE,
        0xCF,
    }
    while offset + 4 <= len(body):
        if body[offset] != 0xFF:
            offset += 1
            continue
        while offset < len(body) and body[offset] == 0xFF:
            offset += 1
        if offset >= len(body):
            break
        marker = body[offset]
        offset += 1
        if marker in {0xD8, 0xD9} or 0xD0 <= marker <= 0xD7:
            continue
        if offset + 2 > len(body):
            break
        length = int.from_bytes(body[offset : offset + 2], "big")
        if length < 2 or offset + length > len(body):
            break
        if marker in start_of_frame and length >= 7:
            height = int.from_bytes(body[offset + 3 : offset + 5], "big")
            width = int.from_bytes(body[offset + 5 : offset + 7], "big")
            if width > 0 and height > 0:
                return width, height
        offset += length
    raise CoverImportFailure("JPEG cover dimensions could not be read")
