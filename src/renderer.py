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


COLORS = {"mint": "\x1b[38;5;115m", "blue": "\x1b[38;5;153m", "yellow": "\x1b[38;5;222m", "dim": "\x1b[38;5;250m"}
RESET = "\x1b[0m"
STATES = {"pending": "연결 대기", "live": "연결됨", "stale": "갱신 지연", "account-changed": "계정 변경",
          "error": "확인 실패", "ended": "작업 종료", "hidden": "숨김"}
ACTIVITIES = {"waiting": "대기", "working": "작업 중", "complete": "완료", "interrupted": "중단"}
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


def quota(label, value, width, reset=None):
    value = percent(value)
    number = "—" if value is None else f"{value:g}%"
    if width < 65:
        return f"{label} {number}"
    result = f"{label} 잔여 {number}"
    if width >= 110:
        filled = 0 if value is None else int((100 - value) * 6 / 100 + 0.5)
        bar = "—" * 6 if value is None else "━" * filled + "·" * (6 - filled)
        result = f"{label} 사용 [{bar}] 잔여 {number}"
    reset_at = timestamp(reset)
    if width >= 180 and reset_at is not None:
        if reset_at <= time.time():
            result += " 리셋 대기"
        else:
            try:
                result += " ↻" + dt.datetime.fromtimestamp(reset_at).strftime("%m/%d %H:%M")
            except (ValueError, OverflowError, OSError):
                pass
    return result


def render(snapshot, width, color=True):
    """Return exactly three terminal-cell-bounded lines; values are remaining %."""
    width = max(1, min(width if type(width) is int else 80, 500))
    snapshot = snapshot if isinstance(snapshot, dict) else {}
    model = clean(snapshot.get("model"))
    effort = clean(snapshot.get("effort"))
    cwd = clean(snapshot.get("cwd"))
    branch = clean(snapshot.get("branch"))
    first = [(model, "mint")]
    if width >= 65:
        first += [(f" · {effort}", "blue")]
    if width >= 50:
        # Long model names must not hide quota or state, which have their own rows.
        first += [(f"  {cwd}", "dim"), (f"  {branch}", "yellow")]
    five = snapshot.get("five_hour")
    five = five if isinstance(five, dict) else {}
    week = snapshot.get("weekly")
    week = week if isinstance(week, dict) else {}
    second = [(quota("문맥", snapshot.get("context_remaining"), width), "mint"),
              (" · " + quota("5h", five.get("remaining"), width, five.get("resets_at")), "blue"),
              (" · " + quota("주", week.get("remaining"), width, week.get("resets_at")), "yellow")]
    state = STATES.get(clean(snapshot.get("state")), "확인 대기")
    activity = ACTIVITIES.get(clean(snapshot.get("activity")), "상태 미확인")
    quota_reason = " · 한도 범위 미지원" if snapshot.get("quota_scope") == "unsupported" else ""
    third = [(f"{state}{quota_reason} · {activity}", "dim")]
    if width >= 55:
        third.append((" · " + duration(snapshot.get("elapsed")), "dim"))
    if width >= 75 and isinstance(snapshot.get("tool"), str) and snapshot["tool"] in TOOLS:
        third.append((" · " + snapshot["tool"], "blue"))
    observed = timestamp(snapshot.get("quota_updated_at"))
    if width >= 85 and observed is not None:
        try:
            stamp = dt.datetime.fromtimestamp(observed).strftime("%m/%d %H:%M:%S")
            third.append((f" · 한도 관측 {stamp} ({duration(max(0, time.time() - observed))} 전)", "dim"))
        except (ValueError, OverflowError, OSError):
            pass
    return [line(parts, width, color) for parts in (first, second, third)]


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
