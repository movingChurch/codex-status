"""Bounded root-rollout reader. Only explicit telemetry fields leave this module."""

import copy
from datetime import datetime
import json
import math
import os
from pathlib import Path
import re
import stat
import uuid

READ_BUDGET = 262144
MAX_RECORD_BYTES = 65536
SUPPORTED_VERSION = "0.153.4"
ANSI = re.compile(r"\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))")
EFFORTS = {"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}


def _integer(value):
    return type(value) is int and 0 <= value <= 2**63 - 1


def _identifier(value):
    return isinstance(value, str) and bool(re.fullmatch(r"[A-Za-z0-9_.:/+-]{1,128}", value))


def _model(value):
    if not isinstance(value, str) or len(value) > 512:
        return None
    value = ANSI.sub("", value)
    return value if _identifier(value) else None


def _timestamp(value):
    if not isinstance(value, str) or len(value) > 40:
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        if parsed.tzinfo is None:
            return None
        stamp = parsed.timestamp()
        return stamp if math.isfinite(stamp) else None
    except (ValueError, OverflowError, OSError):
        return None


def _context(info):
    # Adapted from OpenAI Codex (Copyright 2025 OpenAI), Apache-2.0:
    # rust-v0.153.4, codex-rs/protocol/src/protocol.rs,
    # TokenUsage.percent_of_context_window_remaining.
    # Modified for codex-status: Python translation, input validation, and
    # explicit Rust-compatible rounding. See NOTICE and LICENSES/Apache-2.0.txt.
    if not isinstance(info, dict):
        return None
    window = info.get("model_context_window")
    last = info.get("last_token_usage")
    tokens = last.get("total_tokens") if isinstance(last, dict) else None
    if not _integer(window) or not _integer(tokens):
        return None
    if window <= 12000:
        return 0
    effective = window - 12000
    remaining = max(effective - max(tokens - 12000, 0), 0)
    return math.floor(min(max(remaining / effective * 100, 0), 100) + 0.5)


def _window(value):
    if not isinstance(value, dict):
        return None
    used, reset = value.get("used_percent"), value.get("resets_at")
    if type(used) not in (int, float) or not math.isfinite(used) or not 0 <= used <= 100:
        return None
    if reset is not None and not _integer(reset):
        return None
    return {"remaining": 100 - used, "resets_at": reset}


class SessionReader:
    def __init__(self, path, session_id):
        self.path = Path(path)
        self.session_id = session_id
        self.offset = 0
        self.bytes_read = 0
        self.invalidated = False
        self._identity = None
        self._previous_size = 0
        self._initialized = False
        self._buffer = b""
        self._discarding = False
        self._turn = None
        self._calls = set()
        self._state = self._empty()
        try:
            if str(uuid.UUID(session_id)) != session_id:
                self._invalidate()
        except (ValueError, TypeError, AttributeError):
            self._invalidate()

    def _empty(self):
        return dict(model=None, effort=None, context_remaining=None,
                    five_hour=None, weekly=None, activity="waiting", tool=None,
                    updated_at=None, quota_updated_at=None,
                    session_id=self.session_id, quota_scope=None,
                    context_used=None, context_window=None, input_tokens=None, cached_tokens=None)

    def _invalidate(self):
        self.invalidated = True
        self._state = self._empty()
        self._buffer = b""
        self._turn = None
        self._calls.clear()

    def _open_checked(self):
        descriptor = os.open(self.path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        try:
            info = os.fstat(descriptor)
            current = self.path.stat(follow_symlinks=False)
        except OSError:
            os.close(descriptor)
            raise
        identity = (info.st_dev, info.st_ino)
        if (not stat.S_ISREG(info.st_mode) or not stat.S_ISREG(current.st_mode)
                or identity != (current.st_dev, current.st_ino)
                or (self._identity is not None and identity != self._identity)
                or info.st_size < self._previous_size):
            os.close(descriptor)
            self._invalidate()
            return None
        self._identity = identity
        self._previous_size = info.st_size
        return descriptor

    def _header(self, raw):
        try:
            record = json.loads(raw)
            meta = record.get("payload")
            return (record.get("type") == "session_meta" and isinstance(meta, dict)
                    and meta.get("id") == self.session_id
                    and meta.get("session_id") == self.session_id
                    and meta.get("source") == "cli"
                    and meta.get("parent_thread_id") is None
                    and meta.get("cli_version") == SUPPORTED_VERSION)
        except (ValueError, AttributeError, RecursionError):
            return False

    def read(self):
        if self.invalidated:
            return {}
        descriptor = None
        try:
            descriptor = self._open_checked()
            if descriptor is None:
                return {}
            os.lseek(descriptor, self.offset, os.SEEK_SET)
            data = os.read(descriptor, READ_BUDGET)
            self.bytes_read += len(data)
            self.offset += len(data)
            if not self._initialized:
                boundary = data.find(b"\n")
                if boundary < 0 or not self._header(data[:boundary]):
                    self._invalidate()
                    return {}
                self._initialized = True
                if self._previous_size > READ_BUDGET:
                    self.offset = max(boundary + 1, self._previous_size - READ_BUDGET)
                    self._discarding = self.offset > boundary + 1
                    return copy.deepcopy(self._state)
                data = data[boundary + 1:]
            self._consume(data)
            return {} if self.invalidated else copy.deepcopy(self._state)
        except (OSError, ValueError, OverflowError):
            self._invalidate()
            return {}
        finally:
            if descriptor is not None:
                os.close(descriptor)

    def _consume(self, data):
        if self._discarding:
            boundary = data.find(b"\n")
            if boundary < 0:
                return
            data = data[boundary + 1:]
            self._discarding = False
        data = self._buffer + data
        self._buffer = b""
        pieces = data.split(b"\n")
        for raw in pieces[:-1]:
            if len(raw) <= MAX_RECORD_BYTES:
                self._record(raw)
            if self.invalidated:
                return
        partial = pieces[-1]
        if len(partial) > MAX_RECORD_BYTES:
            self._discarding = True
        else:
            self._buffer = partial

    def _record(self, raw):
        try:
            record = json.loads(raw)
        except (ValueError, RecursionError):
            return
        if not isinstance(record, dict):
            return
        kind, payload = record.get("type"), record.get("payload")
        if not isinstance(payload, dict):
            return
        if kind == "session_meta":
            if not self._header(raw):
                self._invalidate()
            return
        stamp = _timestamp(record.get("timestamp"))
        if stamp is None:
            return
        if kind == "turn_context":
            self._set_model(payload.get("model"), payload.get("effort"))
            self._state["updated_at"] = stamp
        elif kind == "event_msg" and self._event(payload, stamp):
            self._state["updated_at"] = stamp

    def _set_model(self, model, effort):
        self._state["model"] = _model(model)
        self._state["effort"] = effort if isinstance(effort, str) and effort in EFFORTS else None

    def _event(self, payload, stamp):
        kind = payload.get("type")
        if kind == "token_count":
            info = payload.get("info")
            self._state["context_remaining"] = _context(info)
            if isinstance(info, dict):
                last = info.get("last_token_usage")
                window = info.get("model_context_window")
                self._state["context_window"] = window if _integer(window) else None
                if isinstance(last, dict):
                    for target, source in (("context_used", "total_tokens"), ("input_tokens", "input_tokens"), ("cached_tokens", "cached_input_tokens")):
                        value = last.get(source)
                        self._state[target] = value if _integer(value) else None
            self._quota(payload.get("rate_limits"), stamp)
            return True
        if kind == "thread_settings_applied":
            settings = payload.get("thread_settings")
            if payload.get("thread_id") != self.session_id or not isinstance(settings, dict):
                return False
            self._set_model(settings.get("model"), settings.get("reasoning_effort"))
            return True
        turn = payload.get("turn_id")
        if not _identifier(turn):
            return False
        if kind in ("task_started", "turn_started"):
            self._turn = turn
            self._calls.clear()
            self._state.update(activity="working", tool=None)
            return True
        if turn != self._turn:
            return False
        if kind in ("task_complete", "turn_complete", "turn_aborted"):
            self._state.update(activity="interrupted" if kind == "turn_aborted" else "complete", tool=None)
            self._calls.clear()
            self._turn = None
            return True
        call = payload.get("call_id")
        if not _identifier(call):
            return False
        if kind == "exec_command_begin":
            self._calls.add(call)
            self._state["tool"] = "exec_command"
            return True
        if kind == "exec_command_end" and call in self._calls:
            self._calls.remove(call)
            self._state["tool"] = "exec_command" if self._calls else None
            return True
        return False

    def _quota(self, limits, stamp):
        if not isinstance(limits, dict):
            return
        if limits.get("limit_id") not in (None, "codex"):
            if self._state["quota_scope"] != "codex":
                self._state["quota_scope"] = "unsupported"
            return
        result = {"five_hour": None, "weekly": None}
        for slot in ("primary", "secondary"):
            window = limits.get(slot)
            if not isinstance(window, dict):
                continue
            minutes = window.get("window_minutes")
            if type(minutes) is not int:
                continue
            field = {300: "five_hour", 10080: "weekly"}.get(minutes)
            if field is not None:
                result[field] = _window(window)
        self._state.update(result)
        self._state["quota_scope"] = "codex"
        self._state["quota_updated_at"] = stamp if any(result.values()) else None

    def reset_after_account_change(self):
        if self.invalidated:
            return
        if not self._initialized:
            self.read()
        if self.invalidated:
            return
        descriptor = None
        try:
            descriptor = self._open_checked()
            if descriptor is None:
                return
            self.offset = os.fstat(descriptor).st_size
            last = os.pread(descriptor, 1, self.offset - 1) if self.offset else b"\n"
            self.bytes_read += len(last) if self.offset else 0
            self._buffer = b""
            self._discarding = last != b"\n"
            self._state = self._empty()
            self._turn = None
            self._calls.clear()
        except OSError:
            self._invalidate()
        finally:
            if descriptor is not None:
                os.close(descriptor)
