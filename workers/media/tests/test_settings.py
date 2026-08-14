from __future__ import annotations

import os
import unittest
from unittest.mock import patch

from visoraft_media.settings import Settings


class SettingsTests(unittest.TestCase):
    def test_command_roles_and_heartbeats_are_configurable(self) -> None:
        with patch.dict(
            os.environ,
            {
                "VISORAFT_MEDIA_QUEUE": "visoraft.media.download.v1",
                "VISORAFT_MEDIA_COMMANDS": "download,assets_delete,download",
                "VISORAFT_RABBITMQ_HEARTBEAT": "30",
                "VISORAFT_RABBITMQ_PUBLISHER_HEARTBEAT": "0",
                "VISORAFT_YTDLP_HTTP_CHUNK_SIZE_BYTES": "8388608",
                "VISORAFT_YTDLP_CONCURRENT_FRAGMENTS": "6",
                "VISORAFT_DOWNLOAD_STALL_TIMEOUT_SECONDS": "240",
                "VISORAFT_DOWNLOAD_TIMEOUT_SECONDS": "10800",
            },
            clear=True,
        ):
            settings = Settings.from_environment()

        self.assertEqual(settings.queue_name, "visoraft.media.download.v1")
        self.assertEqual(settings.command_types, ("download", "assets_delete"))
        self.assertEqual(settings.rabbitmq_heartbeat, 30)
        self.assertEqual(settings.publisher_heartbeat, 0)
        self.assertEqual(settings.ytdlp_http_chunk_size_bytes, 8 * 1024 * 1024)
        self.assertEqual(settings.ytdlp_concurrent_fragments, 6)
        self.assertEqual(settings.download_stall_timeout_seconds, 240)
        self.assertEqual(settings.download_overall_timeout_seconds, 10800)

    def test_unknown_command_role_is_rejected(self) -> None:
        with patch.dict(
            os.environ,
            {"VISORAFT_MEDIA_COMMANDS": "metadata,unknown"},
            clear=True,
        ):
            with self.assertRaisesRegex(ValueError, "unsupported commands"):
                Settings.from_environment()


if __name__ == "__main__":
    unittest.main()
