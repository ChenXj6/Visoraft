from __future__ import annotations

import unittest

from visoraft_media.cover_importer import CoverImportFailure, _inspect_image


class CoverImporterTests(unittest.TestCase):
    def test_reads_png_dimensions(self) -> None:
        body = (
            b"\x89PNG\r\n\x1a\n"
            + b"\x00\x00\x00\x0dIHDR"
            + (1280).to_bytes(4, "big")
            + (720).to_bytes(4, "big")
            + b"\x08\x06\x00\x00\x00"
        )
        self.assertEqual(_inspect_image(body), ("image/png", ".png", 1280, 720))

    def test_rejects_unknown_image(self) -> None:
        with self.assertRaises(CoverImportFailure):
            _inspect_image(b"not-an-image")


if __name__ == "__main__":
    unittest.main()
