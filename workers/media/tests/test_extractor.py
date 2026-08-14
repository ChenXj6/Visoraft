import unittest
from unittest.mock import patch

from visoraft_media.extractor import (
    SourceRejected,
    assert_public_source,
    classify_error,
    clean_text,
    friendly_error,
    is_retryable,
)


class ExtractorSafetyTests(unittest.TestCase):
    def test_localhost_is_rejected_without_dns(self) -> None:
        with self.assertRaises(SourceRejected):
            assert_public_source("http://localhost/internal")

    def test_exact_trusted_host_can_be_used_for_local_fixture(self) -> None:
        assert_public_source(
            "http://fixture-provider:8090/media/sample.wav",
            ("fixture-provider",),
        )
        with self.assertRaises(SourceRejected):
            assert_public_source(
                "http://localhost/media/sample.wav",
                ("fixture-provider",),
            )

    @patch("visoraft_media.extractor.socket.getaddrinfo")
    def test_private_dns_result_is_rejected(self, getaddrinfo) -> None:
        getaddrinfo.return_value = [
            (2, 1, 6, "", ("10.10.0.8", 443)),
        ]
        with self.assertRaises(SourceRejected):
            assert_public_source("https://example.invalid/video")

    def test_clean_text_removes_nulls_and_limits_length(self) -> None:
        self.assertEqual(clean_text(" ab\x00cdef ", 4), "abcd")

    def test_youtube_bot_challenge_is_actionable_and_retryable(self) -> None:
        message = (
            "ERROR: [youtube] abc: Sign in to confirm you’re not a bot. "
            "Use --cookies-from-browser or --cookies"
        )
        code = classify_error(message)
        self.assertEqual(code, "source_auth_required")
        self.assertTrue(is_retryable(message))
        self.assertIn("CookieCloud", friendly_error(code, message))

    def test_javascript_challenge_failure_has_its_own_code(self) -> None:
        message = (
            "n challenge solving failed: ensure you have a supported "
            "JavaScript runtime and challenge solver script distribution"
        )
        code = classify_error(message)
        self.assertEqual(code, "source_js_challenge_failed")
        self.assertTrue(is_retryable(message))
        self.assertIn("Deno", friendly_error(code, message))

    def test_missing_formats_is_actionable_and_retryable(self) -> None:
        message = "Requested format is not available. Only images are available"
        code = classify_error(message)
        self.assertEqual(code, "source_formats_unavailable")
        self.assertTrue(is_retryable(message))
        self.assertIn("PO Token", friendly_error(code, message))


if __name__ == "__main__":
    unittest.main()
