import json
from datetime import datetime
import tempfile
import unittest
from pathlib import Path
from events import SessionReader

ROOT = "11111111-1111-7111-8111-111111111111"
T1 = "2026-09-08T01:00:00.000Z"
T2 = "2026-09-08T01:00:01.000Z"
OBS1 = datetime.fromisoformat(T1.replace("Z", "+00:00")).timestamp()
OBS2 = datetime.fromisoformat(T2.replace("Z", "+00:00")).timestamp()

class EventTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(); self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / "rollout.jsonl"
        self.path.write_bytes(b"")
        self.record("session_meta", dict(id=ROOT, session_id=ROOT, source="cli", parent_thread_id=None, cli_version="0.153.4"))

    def record(self, kind, payload, stamp=T1):
        with self.path.open("ab") as f:
            f.write((json.dumps(dict(type=kind, timestamp=stamp, payload=payload)) + "\n").encode())

    def event(self, kind, stamp=T1, **payload):
        self.record("event_msg", dict(type=kind, **payload), stamp)

    def usage(self, **changes):
        value = dict(info=dict(model_context_window=112000, last_token_usage=dict(total_tokens=62000), total_token_usage=dict(total_tokens=999999)))
        value.update(changes); return value

    def limits(self, ident="codex"):
        return dict(limit_id=ident, primary=dict(used_percent=25, window_minutes=300, resets_at=1900000000), secondary=dict(used_percent=80, window_minutes=10080, resets_at=1900000001))

    def test_context_uses_last_usage_and_official_baseline(self):
        self.event("token_count", **self.usage())
        self.assertEqual(SessionReader(self.path, ROOT).read().get("context_remaining"), 50)

    def test_observed_context_and_cache_metrics(self):
        self.event("token_count", info=dict(model_context_window=200000,
            last_token_usage=dict(total_tokens=60000, input_tokens=50000, cached_input_tokens=40000)))
        data = SessionReader(self.path, ROOT).read()
        self.assertEqual([data[k] for k in ("context_used", "context_window", "input_tokens", "cached_tokens")],
                         [60000, 200000, 50000, 40000])

    def test_rust_half_rounding_and_invalid_values(self):
        self.event("token_count", info=dict(model_context_window=12200, last_token_usage=dict(total_tokens=12003)))
        reader = SessionReader(self.path, ROOT)
        self.assertEqual(reader.read().get("context_remaining"), 99)
        self.event("token_count", info=dict(model_context_window=True, last_token_usage=dict(total_tokens=0)))
        self.assertIsNone(reader.read().get("context_remaining"))

    def test_quota_scope_and_observation_time_do_not_follow_tasks(self):
        self.event("token_count", **self.usage(rate_limits=self.limits()))
        reader = SessionReader(self.path, ROOT); first = reader.read()
        self.assertEqual(first.get("five_hour"), dict(remaining=75, resets_at=1900000000))
        self.event("task_started", T2, turn_id="turn-a")
        self.event("token_count", T2, **self.usage(rate_limits=self.limits("codex_bengalfox")))
        value = reader.read()
        self.assertEqual(value.get("quota_updated_at"), OBS1)
        self.assertEqual(value.get("weekly"), dict(remaining=20, resets_at=1900000001))
        self.assertEqual(value.get("updated_at"), OBS2)

    def test_periods_are_matched_by_minutes_not_primary_position(self):
        limits = self.limits(None); limits["primary"], limits["secondary"] = limits["secondary"], limits["primary"]
        self.event("token_count", **self.usage(rate_limits=limits))
        self.assertEqual(SessionReader(self.path, ROOT).read().get("five_hour"), dict(remaining=75, resets_at=1900000000))

    def test_turn_and_tool_events_are_correlated_and_do_not_leak(self):
        self.event("task_started", turn_id="turn-a")
        self.event("exec_command_begin", turn_id="turn-a", call_id="call-a", command=["secret-token"], interaction_input="secret-token")
        reader = SessionReader(self.path, ROOT)
        self.assertEqual(reader.read().get("tool"), "exec_command")
        self.event("task_complete", turn_id="turn-b", last_agent_message="secret-token")
        self.event("exec_command_end", turn_id="turn-b", call_id="call-a", stdout="secret-token")
        wrong = reader.read(); self.assertEqual(wrong.get("activity"), "working"); self.assertEqual(wrong.get("tool"), "exec_command")
        self.event("task_complete", turn_id="turn-a", last_agent_message="secret-token")
        done = reader.read(); self.assertEqual(done.get("activity"), "complete"); self.assertIsNone(done.get("tool"))
        self.assertNotIn("secret-token", json.dumps(done))

    def test_partial_line_is_waited_for_and_reads_are_incremental(self):
        reader = SessionReader(self.path, ROOT); reader.read(); before = reader.bytes_read
        raw = json.dumps(dict(type="turn_context", timestamp=T2, payload=dict(model="gpt-test", effort="high", turn_id="turn-a"))).encode()
        with self.path.open("ab") as f: f.write(raw[:25])
        self.assertIsNone(reader.read().get("model"))
        with self.path.open("ab") as f: f.write(raw[25:] + b"\n")
        self.assertEqual(reader.read().get("model"), "gpt-test")
        self.assertEqual(reader.bytes_read - before, len(raw) + 1)
        before = reader.bytes_read; reader.read(); self.assertEqual(reader.bytes_read, before)

    def test_replacement_and_truncation_discard_previous_state(self):
        self.record("turn_context", dict(model="gpt-test", effort="high"))
        reader = SessionReader(self.path, ROOT); self.assertEqual(reader.read().get("model"), "gpt-test")
        self.path.write_bytes(b"")
        self.assertIsNone(reader.read().get("model")); self.assertTrue(reader.invalidated)
        replacement = self.path.with_suffix(".new"); replacement.write_text("{}\n"); replacement.replace(self.path)
        self.assertIsNone(reader.read().get("model"))

    def test_account_change_skips_past_events_and_partial_record(self):
        self.event("token_count", **self.usage(rate_limits=self.limits()))
        reader = SessionReader(self.path, ROOT); self.assertIsNotNone(reader.read().get("five_hour"))
        old = json.dumps(dict(type="event_msg", timestamp=T1, payload=dict(type="token_count", **self.usage(rate_limits=self.limits())))).encode()
        with self.path.open("ab") as f: f.write(old[:20])
        reader.reset_after_account_change()
        with self.path.open("ab") as f: f.write(old[20:] + b"\n")
        self.assertIsNone(reader.read().get("five_hour"))
        self.event("token_count", T2, **self.usage(rate_limits=self.limits()))
        self.assertEqual(reader.read().get("quota_updated_at"), OBS2)

    def test_large_initial_file_and_huge_rows_are_bounded(self):
        with self.path.open("ab") as f: f.write(b'{"ignored":"' + b"x" * 800000 + b'"}\n')
        self.record("turn_context", dict(model="gpt-tail", effort="medium"), T2)
        reader = SessionReader(self.path, ROOT)
        before=0
        for _ in range(3):
            value=reader.read(); self.assertLessEqual(reader.bytes_read-before,262144); before=reader.bytes_read
        self.assertEqual(value.get("model"), "gpt-tail")
        self.assertLess(reader.bytes_read, self.path.stat().st_size)

    def test_wrong_session_and_unsupported_version_fail_closed(self):
        self.record("turn_context", dict(model="gpt-test", effort="high"))
        reader=SessionReader(self.path,"22222222-2222-7222-8222-222222222222")
        self.assertTrue(reader.read().get("model") is None); self.assertTrue(reader.invalidated)
        text=self.path.read_text().replace("0.153.4","0.144.1");self.path.write_text(text)
        reader=SessionReader(self.path,ROOT);self.assertIsNone(reader.read().get("model"));self.assertTrue(reader.invalidated)

    def test_thread_settings_require_owner_and_text_is_sanitized(self):
        self.record("turn_context",dict(model="\x1b[31mgpt-test\x1b[0m",effort="high"))
        self.event("thread_settings_applied",thread_id="another-thread",thread_settings=dict(model="wrong",reasoning_effort="low"))
        reader=SessionReader(self.path,ROOT);value=reader.read()
        self.assertEqual(value.get("model"),"gpt-test");self.assertNotIn("\x1b",json.dumps(value))
        self.event("thread_settings_applied",T2,thread_id=ROOT,thread_settings=dict(model="gpt-next",reasoning_effort="medium"))
        self.assertEqual(reader.read().get("model"),"gpt-next")

if __name__ == "__main__": unittest.main()
