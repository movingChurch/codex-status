"""Render a subscribed HUD without collecting session data."""

import datetime as dt
import json
import math
import re
import shutil
import signal
import sys
import time
import unicodedata
import uuid


COLORS = {"model": "\x1b[0;1;38;2;58;207;137m", "mint": "\x1b[0;38;2;0;181;113m",
          "blue": "\x1b[0;38;2;72;166;255m", "yellow": "\x1b[0;38;2;229;194;38m",
          "dim": "\x1b[0;38;2;161;165;167m", "label": "\x1b[0;38;2;62;67;69m"}
RESET = "\x1b[0m"
STATES = {"ready": "Ready", "pending": "Connecting", "live": "Connected", "stale": "Stale", "account-changed": "Account changed",
          "error": "Read failed", "ended": "Ended", "hidden": "Hidden"}
ACTIVITIES = {"waiting": "Waiting", "working": "Working", "complete": "Complete", "interrupted": "Interrupted"}
TOOLS = {"exec_command", "write_stdin", "apply_patch", "shell", "shell_command"}
OSC = re.compile(r"\x1b\][^\x07\x1b]*(?:\x07|\x1b\\|$)")
CSI = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")


def clean(value):
    """Strip terminal controls, bidi formatting and unsupported value types."""
    if not isinstance(value, str):
        return "—"
    value = CSI.sub("", OSC.sub("", value[:4096]))
    return "".join(c for c in value if not unicodedata.category(c).startswith("C"))


def cell_width(character):
    if unicodedata.combining(character):
        return 0
    return 2 if unicodedata.east_asian_width(character) in "WF" else 1


def line(segments, width, color):
    result, used = [], 0
    for text, tint in segments:
        chunk = []
        for char in clean(text):
            size = cell_width(char)
            if used + size > width:
                break
            if size == 0 and used == 0:
                continue
            chunk.append(char)
            used += size
        if chunk:
            if color:
                result.append(COLORS[tint])
            result.append("".join(chunk))
        if used >= width:
            break
    if color and result:
        result.append(RESET)
    return "".join(result)


def percent(value):
    return value if isinstance(value, (int, float)) and not isinstance(value, bool) and 0 <= value <= 100 else None


def timestamp(value):
    try:
        if isinstance(value, bool):
            return None
        if isinstance(value, (int, float)) and math.isfinite(value):
            return float(value)
        if isinstance(value, str):
            parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
            if parsed.tzinfo is not None:
                return parsed.timestamp()
    except (ValueError, OverflowError, OSError):
        pass
    return None


def duration(value):
    if not isinstance(value, (int, float)) or isinstance(value, bool) or not 0 <= value <= 10**10:
        return "—"
    seconds = int(value)
    return f"{seconds // 3600}:{seconds % 3600 // 60:02}:{seconds % 60:02}" if seconds >= 3600 else f"{seconds // 60}:{seconds % 60:02}"


def token_count(value):
    return value if type(value) is int and 0 <= value <= 10**12 else None


def format_tokens(value):
    if value is None:
        return "n/a"
    return f"{math.floor(value / 1000 + 0.5)}k" if value >= 1000 else str(value)


def used_percent(remaining):
    remaining = percent(remaining)
    return None if remaining is None else 100 - remaining


def slider(used):
    filled = math.floor(used / 10 + 0.5)
    return "▓" * filled + "░" * (10 - filled)


def reset_countdown(value):
    reset_at = timestamp(value)
    if reset_at is None:
        return None
    delta = reset_at - time.time()
    if delta <= 0:
        return "reset pending"
    minutes = math.floor(delta / 60)
    if minutes < 1:
        return "<1m"
    days, remainder = divmod(minutes, 1440)
    hours, minutes = divmod(remainder, 60)
    parts = []
    if days:
        parts.append(f"{days}d")
    if hours:
        parts.append(f"{hours}hr")
    if minutes:
        parts.append(f"{minutes}m")
    return " ".join(parts)


def segments_width(segments):
    return sum(cell_width(char) for text, _ in segments for char in clean(text))


def capacity_row(snapshot, width):
    """Keep all capacity labels before spending columns on bars and timers."""
    context = used_percent(snapshot.get("context_remaining"))
    used = token_count(snapshot.get("context_used"))
    window = token_count(snapshot.get("context_window"))
    quotas = []
    for field, label, tint in (("five_hour", "5H", "blue"), ("weekly", "7D", "yellow")):
        value = snapshot.get(field)
        value = value if isinstance(value, dict) else {}
        quotas.append((label, tint, used_percent(value.get("remaining")), value.get("resets_at")))
    candidates = ((True, True, True), (True, True, False), (False, True, False), (False, False, False))
    for bars, counts, timers in candidates:
        context_label = "Context" if bars or counts else "Ctx"
        context_text = "n/a" if context is None else f"{math.floor(context + 0.5)}%"
        if counts and used is not None and window is not None and window > 0:
            context_text = f"{format_tokens(used)}/{format_tokens(window)} ({context_text})"
        if bars and context is not None:
            context_text = slider(context) + " " + context_text
        parts = [(context_label + " ", "label"), (context_text, "mint")]
        for label, tint, quota_used, resets_at in quotas:
            value = "n/a" if quota_used is None else f"{quota_used:.1f}%"
            if bars and quota_used is not None:
                value = slider(quota_used) + " " + value
            parts.extend([(" │ " + label + " ", "label"), (value, tint)])
            countdown = reset_countdown(resets_at) if timers and quota_used is not None else None
            if countdown:
                parts.extend([(" ↻ ", "label"), (countdown, "dim")])
        if segments_width(parts) <= width:
            return parts
    return parts


def bound_session(snapshot):
    try:
        return isinstance(snapshot.get("session_id"), str) and bool(uuid.UUID(snapshot["session_id"]))
    except ValueError:
        return False


def append_if_fits(parts, addition, width):
    if segments_width(parts) + segments_width(addition) <= width:
        parts.extend(addition)


def activity_row(snapshot, width):
    state_key = clean(snapshot.get("state"))
    state = STATES.get(state_key, "Pending")
    if state_key == "live" and not bound_session(snapshot):
        state = "Connecting"
    activity = ACTIVITIES.get(clean(snapshot.get("activity")), "Unknown")
    if state_key == "ready":
        parts = [("Ready · Send a message to start session tracking", "dim")]
    else:
        reason = " · Unsupported quota" if snapshot.get("quota_scope") == "unsupported" else ""
        parts = [(f"{state}{reason} · {activity}", "dim")]
    error = snapshot.get("quota_error")
    allowed_errors = {"rate-limited", "timeout", "unavailable", "not-authenticated", "account-changed",
                      "no-credentials", "unsupported", "api-error", "parse-error", "read-failed"}
    if isinstance(error, str) and error:
        message = error if error in allowed_errors else "unavailable"
        append_if_fits(parts, [(" │ Quota " + message, "dim")], width)
    if state_key != "ready" and snapshot.get("elapsed") is not None:
        append_if_fits(parts, [(" │ Elapsed ", "label"), (duration(snapshot.get("elapsed")), "dim")], width)
    cached = token_count(snapshot.get("cached_tokens"))
    inputs = token_count(snapshot.get("input_tokens"))
    if cached is not None and inputs is not None and cached <= inputs:
        cache_parts = [(" │ Cached ", "label"), (format_tokens(cached), "dim")]
        if inputs > 0:
            cache_parts.extend([(" Hit rate ", "label"), (f"{cached / inputs * 100:.1f}%", "dim")])
        append_if_fits(parts, cache_parts, width)
    if isinstance(snapshot.get("tool"), str) and snapshot["tool"] in TOOLS:
        append_if_fits(parts, [(" │ " + snapshot["tool"], "blue")], width)
    observed = timestamp(snapshot.get("quota_updated_at"))
    if observed is not None:
        try:
            stamp = dt.datetime.fromtimestamp(observed).strftime("%m/%d %H:%M:%S")
            addition = [(f" │ Quota observed {stamp} ({duration(max(0, time.time() - observed))} ago)", "dim")]
            append_if_fits(parts, addition, width)
        except (ValueError, OverflowError, OSError):
            pass
    return parts


def render(snapshot, width, color=True):
    """Return three cell-bounded lines; render remaining inputs as used %."""
    width = max(1, min(width if type(width) is int else 80, 500))
    snapshot = snapshot if isinstance(snapshot, dict) else {}
    model = clean(snapshot.get("model"))
    model = model if model not in ("", "—") else "Codex"
    effort = clean(snapshot.get("effort"))
    cwd = clean(snapshot.get("cwd"))
    branch = clean(snapshot.get("branch"))
    first = [(model, "model")]
    if effort not in ("", "—"):
        first.extend([(" Effort ", "label"), (effort, "mint")])
    if width >= 65 and cwd not in ("", "—"):
        location = "/".join(cwd.rstrip("/").split("/")[-2:]) or "/"
        first.extend([(" │ Directory ", "label"), (location, "dim")])
    if width >= 50 and branch not in ("", "—"):
        first.extend([(" Branch ", "label"), (line([(branch, "mint")], 26, False), "mint")])
    return [line(parts, width, color) for parts in (first, capacity_row(snapshot, width), activity_row(snapshot, width))]


def run_renderer(runtime_path, run_id):
    """Block on snapshots; only a terminal resize sends an extra request."""
    import collector

    run_id = str(uuid.UUID(run_id))
    runtime = collector.validate_runtime(runtime_path)
    deadline = time.monotonic() + 12
    while not (runtime / "runs" / (run_id + ".json")).is_file():
        if time.monotonic() >= deadline:
            return 2
        time.sleep(0.05)
    connection = collector.ensure_collector(runtime)
    terminal = sys.stdout.isatty()
    width = lambda: max(1, shutil.get_terminal_size((80, 4)).columns - 1)
    old_resize = signal.getsignal(signal.SIGWINCH)

    def resize(_signal, _frame):
        try:
            connection.sendall((json.dumps({"width": width()}) + "\n").encode())
        except OSError:
            pass

    try:
        signal.signal(signal.SIGWINCH, resize)
        connection.sendall((json.dumps({"v": 1, "run": run_id, "width": width()}) + "\n").encode())
        if terminal:
            sys.stdout.write("\x1b[?25l\x1b[2J")
        with connection.makefile("rb") as stream:
            while True:
                record = stream.readline(65537)
                if not record:
                    return 0
                if len(record) > 65536 or not record.endswith(b"\n"):
                    return 2
                try:
                    snapshot = json.loads(record)
                except (ValueError, UnicodeDecodeError):
                    return 2
                if not isinstance(snapshot, dict) or type(snapshot.get("v")) is not int or snapshot["v"] != 1:
                    return 2
                lines = render(snapshot, width(), color=terminal)
                if terminal:
                    sys.stdout.write("\x1b[H" + "\r\n".join(row + "\x1b[K" for row in lines))
                else:
                    sys.stdout.write("\n".join(lines) + "\n")
                sys.stdout.flush()
    except (OSError, KeyboardInterrupt):
        return 0
    finally:
        signal.signal(signal.SIGWINCH, old_resize)
        connection.close()
        if terminal:
            sys.stdout.write("\x1b[0m\x1b[?25h")
            sys.stdout.flush()
