import datetime
import contextlib
import io
import json
import importlib.util
import math
from pathlib import Path
import re
import socket
import tempfile
import threading
import types
import unittest
import unicodedata
import uuid
from unittest import mock

import renderer


def cells(text):
    text = re.sub(r"\x1b\[[0-9;]*m", "", text)
    return sum(0 if unicodedata.combining(c) else 2 if unicodedata.east_asian_width(c) in "WF" else 1 for c in text)


class RendererTests(unittest.TestCase):
    def snapshot(self):
        return dict(state="live", model="gpt-test", effort="high", cwd="/work/한글 작업", branch="main",
                    context_remaining=70, five_hour={"remaining":55, "resets_at":4102444800},
                    weekly={"remaining":20, "resets_at":4102444800}, elapsed=85, activity="working",
                    tool="exec_command", updated_at="2026-09-08T00:00:00Z", quota_updated_at="2026-09-08T00:00:00Z")

    def test_three_lines_and_usage_vs_remaining(self):
        lines = renderer.render(self.snapshot(), 180, color=False)
        self.assertEqual(len(lines), 3)
        self.assertIn("70% left", lines[1])
        self.assertIn("used", lines[1])
        self.assertIn("gpt-test", lines[0])
        self.assertIn("Working", lines[2])

    def test_owned_labels_are_english_and_explain_remaining_values(self):
        snapshot = self.snapshot()
        snapshot["cwd"] = "/work/project"
        for state in ("pending", "live", "stale", "account-changed", "error", "ended", "hidden", "unknown"):
            snapshot["state"] = state
            text = "\n".join(renderer.render(snapshot, 220, color=False))
            self.assertNotRegex(text, r"[가-힣ㄱ-ㅎㅏ-ㅣ]")
            self.assertIn("Working", text)
            self.assertIn("70% left", text)
            self.assertIn("Quota observed", text)
            self.assertIn("ago)", text)
        snapshot["state"] = "live"
        rows = renderer.render(snapshot, 40, color=False)
        self.assertEqual(rows[1], "Ctx 70% · 5h 55% · Week 20%")
        self.assertIn("Connected", rows[2])

    def test_control_sequences_and_bidi_cannot_escape(self):
        snapshot = self.snapshot()
        snapshot.update(model="evil\x1b[2J\x1b]52;c;secret\x07model\n\r\u202e", branch="b\x00\t")
        text = "\n".join(renderer.render(snapshot, 180, color=False))
        self.assertNotIn("\x1b", text)
        self.assertNotIn("secret", text)
        self.assertNotIn("\u202e", text)
        self.assertEqual(text.count("\n"), 2)

    def test_cell_width_limits_wide_and_combining_characters(self):
        snapshot = self.snapshot()
        snapshot["model"] = "한글🙂e\u0301" * 80
        for width in (1, 12, 25, 40, 80, 120):
            for color in (False, True):
                lines = renderer.render(snapshot, width, color=color)
                self.assertEqual(len(lines), 3)
                self.assertTrue(all(cells(line) <= width for line in lines), (width, lines))

    def test_narrow_retains_quota_and_state_before_location(self):
        lines = renderer.render(self.snapshot(), 40, color=False)
        self.assertIn("Ctx", lines[1])
        self.assertIn("5h", lines[1])
        self.assertIn("Week", lines[1])
        self.assertIn("Working", lines[2])

    def test_invalid_numbers_stay_unknown(self):
        for value in (None, True, float("nan"), float("inf"), -1, 101, "50"):
            snapshot = self.snapshot()
            snapshot.update(context_remaining=value, five_hour={"remaining":value}, weekly=None)
            row = renderer.render(snapshot, 180, color=False)[1]
            self.assertNotIn("0% left", row)
            self.assertNotIn("nan", row)
            self.assertNotIn("inf", row)
            self.assertIn("—", row)

    def test_stale_quota_keeps_working_and_last_observation(self):
        snapshot = self.snapshot()
        snapshot["state"] = "stale"
        text = "\n".join(renderer.render(snapshot, 180, color=False))
        self.assertIn("Stale", text)
        self.assertIn("Working", text)
        self.assertIn("observed", text)

    def test_past_reset_does_not_replenish(self):
        snapshot = self.snapshot()
        snapshot["five_hour"] = {"remaining":12, "resets_at":1}
        text = "\n".join(renderer.render(snapshot, 220, color=False))
        self.assertIn("12% left", text)
        self.assertIn("Reset pending", text)

    def test_account_change_reason_is_visible(self):
        snapshot = self.snapshot()
        snapshot.update(state="account-changed", context_remaining=None, five_hour=None, weekly=None)
        text = "\n".join(renderer.render(snapshot, 80, color=False))
        self.assertIn("Account changed", text)

    def test_unsupported_quota_scope_has_reason_in_state_row(self):
        snapshot = self.snapshot()
        snapshot.update(quota_scope="unsupported", five_hour=None, weekly=None)
        for width in (40, 80, 180):
            lines = renderer.render(snapshot, width, color=False)
            self.assertEqual(len(lines), 3)
            self.assertIn("Unsupported quota", lines[2])

    def test_only_known_tool_names_are_rendered(self):
        snapshot = self.snapshot()
        snapshot["tool"] = "secret arbitrary output"
        self.assertNotIn("secret", "\n".join(renderer.render(snapshot, 180, False)))

    def test_malformed_field_types_cannot_crash_display(self):
        snapshot=self.snapshot()
        snapshot.update(state=[],activity={},tool=["exec_command"],elapsed=10**500,context_remaining=10**500)
        lines=renderer.render(snapshot,80,False)
        self.assertEqual(len(lines),3)
        self.assertIn("Pending",lines[2])

    def drive_socket(self, record):
        with tempfile.TemporaryDirectory() as root:
            root=Path(root)
            (root/"runs").mkdir()
            run=str(uuid.uuid4())
            (root/"runs"/(run+".json")).write_text("{}")
            client,server=socket.socketpair()
            received=[]
            def serve():
                with server:
                    received.append(server.recv(4096))
                    server.sendall(record)
            thread=threading.Thread(target=serve)
            thread.start()
            collector=types.SimpleNamespace(validate_runtime=lambda p:root,ensure_collector=lambda p:client)
            output=io.StringIO()
            try:
                with mock.patch.dict("sys.modules",{"collector":collector}),contextlib.redirect_stdout(output):
                    result=renderer.run_renderer(root,run)
            finally:
                client.close()
                thread.join(timeout=2)
            return result,output.getvalue(),json.loads(received[0])

    def test_socket_disconnect_ends_renderer_and_request_has_no_private_data(self):
        snapshot=self.snapshot()
        snapshot["v"]=1
        result,output,request=self.drive_socket((json.dumps(snapshot)+"\n").encode())
        self.assertEqual(result,0)
        self.assertIn("gpt-test",output)
        self.assertEqual(set(request),{"v","run","width"})

    def test_malformed_snapshot_is_not_printed(self):
        result,output,_=self.drive_socket(b'private invalid payload\n')
        self.assertEqual(result,2)
        self.assertNotIn("private",output)

    def test_huge_snapshot_is_rejected_without_output(self):
        result,output,_=self.drive_socket(b'x'*65537+b'\n')
        self.assertEqual(result,2)
        self.assertEqual(output,"")


if __name__ == "__main__":
    unittest.main()
