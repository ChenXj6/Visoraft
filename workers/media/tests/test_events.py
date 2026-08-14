import json
import unittest

from visoraft_media.events import (
    METADATA_COMPLETED_V1,
    Envelope,
    InvalidEnvelope,
)


class EnvelopeTests(unittest.TestCase):
    def test_round_trip_preserves_unicode(self) -> None:
        envelope = Envelope.create(
            METADATA_COMPLETED_V1,
            "task/11111111-1111-4111-8111-111111111111",
            {"title": "测试标题"},
        )

        decoded = Envelope.decode(envelope.encode())

        self.assertEqual(decoded.id, envelope.id)
        self.assertEqual(decoded.data["title"], "测试标题")

    def test_missing_subject_is_rejected(self) -> None:
        body = json.dumps(
            {
                "specversion": "1.0",
                "id": "message",
                "type": METADATA_COMPLETED_V1,
                "source": "test",
                "subject": "",
                "time": "2026-07-23T00:00:00Z",
                "data": {},
            }
        ).encode()

        with self.assertRaises(InvalidEnvelope):
            Envelope.decode(body)


if __name__ == "__main__":
    unittest.main()
