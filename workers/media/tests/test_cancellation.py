import io
import unittest
from unittest.mock import Mock, patch

from visoraft_media.cancellation import CancellationLatch, CancellationProbe


class CancellationProbeTests(unittest.TestCase):
    @patch("visoraft_media.cancellation.urlopen")
    def test_paused_task_is_a_cooperative_stop_with_a_distinct_reason(self, urlopen) -> None:
        urlopen.return_value.__enter__.return_value = io.BytesIO(
            b'{"status":"downloading","paused_at":"2026-08-14T10:00:00Z"}'
        )
        probe = CancellationProbe("http://control-api:8080", interval_seconds=60)

        self.assertTrue(probe.is_cancelled("task-id"))

    def test_attempt_latch_keeps_pause_reason_after_control_plane_resumes(self) -> None:
        probe = Mock()
        probe.is_cancelled.side_effect = [True, False]
        probe.last_state.return_value = "paused"
        latch = CancellationLatch(probe, "task-id")

        self.assertTrue(latch.should_cancel())
        self.assertEqual("paused", latch.state)
        self.assertTrue(latch.should_cancel(force=True))
        self.assertEqual("paused", latch.state)
        probe.is_cancelled.assert_called_once_with("task-id", force=False)
        self.assertEqual("paused", probe.last_state("task-id"))

    @patch("visoraft_media.cancellation.urlopen")
    def test_cancelled_task_keeps_terminal_control_reason(self, urlopen) -> None:
        urlopen.return_value.__enter__.return_value = io.BytesIO(
            b'{"status":"cancelled"}'
        )
        probe = CancellationProbe("http://control-api:8080", interval_seconds=60)

        self.assertEqual("cancelled", probe.control_state("task-id"))
        self.assertTrue(probe.is_cancelled("task-id"))


if __name__ == "__main__":
    unittest.main()
