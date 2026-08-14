from __future__ import annotations

import os
import unittest
from unittest.mock import MagicMock, patch

from visoraft_media.cookie_material import CookieMaterialClient
from visoraft_media.subtitle_processor import SubtitleFailure


class CookieMaterialTests(unittest.TestCase):
    def test_empty_profile_does_not_call_control_plane(self) -> None:
        client = CookieMaterialClient("http://control", "worker-token")
        with patch("visoraft_media.cookie_material.urlopen") as urlopen:
            with client.materialize(None) as path:
                self.assertIsNone(path)
        urlopen.assert_not_called()

    def test_material_is_private_and_removed_after_use(self) -> None:
        response = MagicMock()
        response.__enter__.return_value.read.return_value = (
            b"# Netscape HTTP Cookie File\n"
            b".youtube.com\tTRUE\t/\tTRUE\t1893456000\tSID\tsecret\n"
        )
        client = CookieMaterialClient("http://control", "worker-token")
        with patch("visoraft_media.cookie_material.urlopen", return_value=response):
            with client.materialize("00000000-0000-4000-8000-000000000001") as path:
                assert path is not None
                self.assertTrue(path.exists())
                if os.name != "nt":
                    self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                saved_path = path
        self.assertFalse(saved_path.exists())

    def test_subtitle_failure_can_propagate_through_context_manager(self) -> None:
        client = CookieMaterialClient("http://control", "worker-token")
        expected = SubtitleFailure(
            code="asr_request_failed",
            message="ASR 请求失败",
            retryable=True,
        )

        with self.assertRaises(SubtitleFailure) as raised:
            with client.materialize(None):
                raise expected

        self.assertIs(raised.exception, expected)


if __name__ == "__main__":
    unittest.main()
