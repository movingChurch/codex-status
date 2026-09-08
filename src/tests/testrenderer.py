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
    text = re.sub(r"\x1b\]8;;[^\x1b]*\x1b\\", "", text)
    text = re.sub(r"\x1b\[[0-9;]*m", "", text)
    return sum(0 if unicodedata.combining(c) else 2 if unicodedata.east_asian_width(c) in "WF" else 1 for c in text)


class RendererTests(unittest.TestCase):
    def linked_snapshot(self):
        snapshot = self.snapshot()
        snapshot.update(directory_url="file:///work/project%20files", branch_url="https://github.com/example/project/tree/main",
                        session_url="file:///work/session.jsonl", usage_url="https://chatgpt.com/codex/settings/usage")
        return snapshot

    def links(self, text):
        return re.findall(r"\x1b\]8;;([^\x1b]+)\x1b\\(.*?)\x1b\]8;;\x1b\\", text)

    def test_links_target_directory_branch_quota_resets_and_session(self):
        snapshot = self.linked_snapshot()
        snapshot["five_hour"]["resets_at"] = 13600
        snapshot["weekly"]["resets_at"] = 100000
        with mock.patch("renderer.time.time", return_value=10000):
            rows = renderer.render(snapshot, 500, True)
        self.assertEqual(self.links(rows[0]), [(snapshot["directory_url"], "work/한글 작업"), (snapshot["branch_url"], "main")])
        self.assertEqual(self.links(rows[1]), [(snapshot["usage_url"], label) for label in ("5H", "1hr", "7D", "1d 1hr")])
        self.assertEqual(self.links(rows[2])[0], (snapshot["session_url"], "1:25"))
        self.assertEqual(self.links(rows[2])[1][0], snapshot["usage_url"])
        self.assertRegex(self.links(rows[2])[1][1], r"\d\d/\d\d \d\d:\d\d:\d\d")

    def test_plain_output_and_visible_width_ignore_links(self):
        snapshot = self.linked_snapshot()
        plain_snapshot = self.snapshot()
        with mock.patch("renderer.time.time", return_value=10000):
            for width in (1, 12, 40, 65, 80, 120, 220, 500):
                plain = renderer.render(snapshot, width, False)
                self.assertEqual(plain, renderer.render(plain_snapshot, width, False))
                self.assertNotIn("\x1b", "\n".join(plain))
                styled = renderer.render(snapshot, width, True)
                self.assertEqual([cells(row) for row in styled], [cells(row) for row in plain])
                self.assertTrue(all(cells(row) <= width for row in styled))
                for row in styled:
                    self.assertEqual(row.count("\x1b]8;;"), 2 * len(self.links(row)))

    def test_truncation_closes_links_before_following_segments(self):
        url = "https://example.com/branch?view=tree#code"
        for width, visible in ((0, ""), (1, "!"), (2, "한"), (3, "한!"), (4, "한글"), (5, "한글!")):
            with self.subTest(width=width):
                row = renderer.line([("한글", "mint", url), ("!", "dim")], width, True)
                self.assertEqual(renderer.clean(row), visible)
                self.assertLessEqual(cells(row), width)
                self.assertEqual(row.count("\x1b]8;;"), 2 * len(self.links(row)))
                for target, label in self.links(row):
                    self.assertEqual(target, url)
                    self.assertNotIn("!", label)

    def test_hostile_or_unsupported_urls_never_create_terminal_links(self):
        invalid = (None, {}, "http://example.com", "javascript:alert(1)", "//example.com", "https://",
                   "https://user:password@example.com", "file://user@localhost/path", "file://remote-host/path", "file://localhost:80/path",
                   "file://localhost", "https://example.com:bad/path",
                   "https://example.com/\x1b]52;c;private\x07", "https://example.com/\npath", "https://example.com/\u202epath",
                   "https://example.com/\x9cpath", "https://example.com/with space", "https://example.com\\@evil.test", "https://example.com/" + "x" * 4096)
        for url in invalid:
            with self.subTest(url_type=type(url).__name__):
                snapshot = self.snapshot()
                snapshot.update({field: url for field in ("directory_url", "branch_url", "session_url", "usage_url")})
                rows = renderer.render(snapshot, 500, True)
                self.assertNotIn("\x1b]", "\n".join(rows))
                self.assertIn("Directory", rows[0])

    def test_local_file_and_https_targets_preserve_escaped_paths(self):
        for url in ("file:///work/project%20files/session.jsonl", "file://localhost/work/%ED%95%9C%EA%B8%80",
                    "https://example.com:443/tree/feature%2Fhud?view=tree#code"):
            row = renderer.line([("Open", "mint", url)], 4, True)
            self.assertEqual(self.links(row), [(url, "Open")])
            self.assertEqual(cells(row), 4)

    def test_long_branch_label_truncates_without_truncating_target(self):
        snapshot = self.linked_snapshot()
        snapshot["branch"] = "feature/" + "long-name-" * 10
        snapshot["branch_url"] = "https://example.com/tree/" + snapshot["branch"]
        row = renderer.render(snapshot, 220, True)[0]
        self.assertEqual(self.links(row)[1], (snapshot["branch_url"], snapshot["branch"][:26]))

    def test_ready_links_available_weekly_quota_without_claiming_connection(self):
        snapshot = self.linked_snapshot()
        snapshot.update(state="ready", model=None, effort=None, session_id=None, session_url=None, five_hour=None)
        rows = renderer.render(snapshot, 220, True)
        self.assertIn((snapshot["usage_url"], "5H"), self.links(rows[1]))
        self.assertIn("5H n/a", renderer.clean(rows[1]))
        self.assertIn("Ready · Send a message", rows[2])
        self.assertNotIn("Connected", "\n".join(rows))
        self.assertNotIn("Elapsed", rows[2])

    def test_incoming_text_cannot_inject_links_even_with_valid_target(self):
        snapshot = self.linked_snapshot()
        snapshot["branch"] = "\x1b]8;;https://evil.test\x1b\\main\x1b]8;;\x1b\\"
        row = renderer.render(snapshot, 220, True)[0]
        self.assertNotIn("evil.test", row)
        self.assertIn((snapshot["branch_url"], "main"), self.links(row))

    def snapshot(self):
        return dict(state="live", model="gpt-test", effort="high", cwd="/work/한글 작업", branch="main",
                    session_id="11111111-2222-4333-8444-555555555555", context_remaining=70, context_used=60000, context_window=200000, input_tokens=10000, cached_tokens=4000, five_hour={"remaining":55, "resets_at":4102444800},
                    weekly={"remaining":20, "resets_at":4102444800}, elapsed=85, activity="working",
                    tool="exec_command", updated_at="2026-09-08T00:00:00Z", quota_updated_at="2026-09-08T00:00:00Z")

    def test_three_lines_and_usage_vs_remaining(self):
        lines = renderer.render(self.snapshot(), 180, color=False)
        self.assertEqual(len(lines), 3)
        self.assertIn("60k/200k (30%)", lines[1])
        self.assertIn("▓▓▓░░░░░░░", lines[1])
        self.assertIn("gpt-test", lines[0])
        self.assertIn("Working", lines[2])

    def test_owned_labels_are_english_and_use_claude_used_percentages(self):
        snapshot = self.snapshot()
        snapshot["cwd"] = "/work/project"
        for state in ("pending", "live", "stale", "account-changed", "error", "ended", "hidden", "unknown"):
            snapshot["state"] = state
            text = "\n".join(renderer.render(snapshot, 220, color=False))
            self.assertNotRegex(text, r"[가-힣ㄱ-ㅎㅏ-ㅣ]")
            self.assertIn("Working", text)
            self.assertIn("60k/200k (30%)", text)
            self.assertIn("Quota observed", text)
            self.assertIn("ago)", text)
        snapshot["state"] = "live"
        rows = renderer.render(snapshot, 40, color=False)
        self.assertEqual(rows[1], "Ctx 30% │ 5H 45.0% │ 7D 80.0%")
        self.assertIn("Connected", rows[2])

    def test_capacity_row_has_ten_cell_used_sliders_and_countdowns(self):
        snapshot = self.snapshot()
        snapshot["five_hour"]["resets_at"] = 13600
        snapshot["weekly"]["resets_at"] = 10000 + 90000
        with mock.patch("renderer.time.time", return_value=10000):
            row = renderer.render(snapshot, 220, color=False)[1]
        self.assertIn("Context ▓▓▓░░░░░░░ 60k/200k (30%)", row)
        self.assertIn("5H ▓▓▓▓▓░░░░░ 45.0% ↻ 1hr", row)
        self.assertIn("7D ▓▓▓▓▓▓▓▓░░ 80.0% ↻ 1d 1hr", row)
        self.assertNotIn("left", row)

    def test_ready_has_account_quota_without_claiming_session_connection(self):
        snapshot = dict(state="ready", model=None, effort=None, cwd="/work/project", branch="main",
                        session_id=None, context_remaining=None, five_hour=None,
                        weekly={"remaining":13}, quota_source="account", activity="waiting")
        rows = renderer.render(snapshot, 160, color=False)
        self.assertTrue(rows[0].startswith("Codex"))
        self.assertNotIn("Effort", rows[0])
        self.assertIn("5H n/a", rows[1])
        self.assertIn("87.0%", rows[1])
        self.assertIn("Ready · Send a message to start session tracking", rows[2])
        self.assertNotIn("Connected", "\n".join(rows))

    def test_unbound_live_state_never_claims_connected(self):
        snapshot=self.snapshot()
        snapshot["session_id"]=None
        self.assertNotIn("Connected", "\n".join(renderer.render(snapshot,160,False)))

    def test_supported_cache_metrics_use_cached_input_share(self):
        row=renderer.render(self.snapshot(),220,False)[2]
        self.assertIn("Cached 4k",row)
        self.assertIn("Hit rate 40.0%",row)
        self.assertNotIn("Cost",row)
        self.assertNotIn("Compact",row)

    def test_account_error_is_visible_without_raw_detail(self):
        snapshot=dict(state="ready", quota_error="rate-limited", activity="waiting")
        row=renderer.render(snapshot,180,False)[2]
        self.assertIn("Quota rate-limited",row)
        snapshot["quota_error"]="private arbitrary error contents"
        self.assertNotIn("private",renderer.render(snapshot,180,False)[2])

    def test_palette_uses_installed_claude_truecolors(self):
        rows=renderer.render(self.snapshot(),220,True)
        self.assertIn("38;2;58;207;137",rows[0])
        self.assertIn("38;2;0;181;113",rows[1])
        self.assertIn("38;2;72;166;255",rows[1])
        self.assertIn("38;2;229;194;38",rows[1])

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
        self.assertIn("5H", lines[1])
        self.assertIn("7D", lines[1])
        self.assertIn("Working", lines[2])

    def test_invalid_numbers_stay_unknown(self):
        for value in (None, True, float("nan"), float("inf"), -1, 101, "50"):
            snapshot = self.snapshot()
            snapshot.update(context_remaining=value, five_hour={"remaining":value}, weekly=None)
            row = renderer.render(snapshot, 180, color=False)[1]
            self.assertNotIn("0% left", row)
            self.assertNotIn("nan", row)
            self.assertNotIn("inf", row)
            self.assertIn("n/a", row)

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
        self.assertIn("88.0%", text)
        self.assertIn("reset pending", text)

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
