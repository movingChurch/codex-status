"""Read-only binding of a launched stock Codex process to its root rollout."""

import ctypes
from collections import OrderedDict
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import uuid

SUPPORTED_VERSION = "0.153.4"
MAX_HEADER_BYTES = 262144  # Product read budget, not a Codex protocol limit.
ROLLOUT_NAME = re.compile(r"^rollout-.*-([0-9a-fA-F-]{36})\.jsonl$")


def _uuid(value):
    if not isinstance(value, str):
        return None
    try:
        return str(uuid.UUID(value))
    except ValueError:
        return None


# ABI declarations are distributed separately under APSL-2.0.
# Keep these imported names available for the binding contract and layout tests.
from darwin_abi import (
    _BsdInfo, _FdInfo, _FileInfo, _VInfoStat, _VnodeInfo, _VnodePath,
    _VnodeFdPath, _ctypes_layout, _SDK_LAYOUT,
)


class _DarwinProc:
    def __init__(self, library):
        self.lib = library
        self.lib.proc_pidinfo.argtypes = [ctypes.c_int, ctypes.c_int,
            ctypes.c_uint64, ctypes.c_void_p, ctypes.c_int]
        self.lib.proc_pidinfo.restype = ctypes.c_int
        self.lib.proc_pidfdinfo.argtypes = [ctypes.c_int, ctypes.c_int,
            ctypes.c_int, ctypes.c_void_p, ctypes.c_int]
        self.lib.proc_pidfdinfo.restype = ctypes.c_int
        self.lib.proc_pidpath.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.c_uint32]
        self.lib.proc_pidpath.restype = ctypes.c_int

    def identity(self, pid):
        info = _BsdInfo()
        length = self.lib.proc_pidinfo(pid, 3, 0, ctypes.byref(info), ctypes.sizeof(info))
        if (length != ctypes.sizeof(info) or info.pid != pid or info.flags & 4
                or info.start_sec <= 0 or info.start_usec >= 1000000):
            return None
        return f"darwin:{info.start_sec}:{info.start_usec:06d}"

    def executable(self, pid):
        output = ctypes.create_string_buffer(4096)
        length = self.lib.proc_pidpath(pid, output, len(output))
        if length <= 0 or length >= len(output):
            return None
        try:
            return Path(os.fsdecode(output.value)).resolve(strict=True)
        except (OSError, ValueError):
            return None

    def _fd_list(self, pid):
        needed = self.lib.proc_pidinfo(pid, 1, 0, None, 0)
        if needed <= 0:
            return None
        capacity = needed // ctypes.sizeof(_FdInfo) + 32
        # Product work bound. Buffer exhaustion is unresolved, never a partial list.
        for _ in range(3):
            if capacity > 16384:
                return None
            output = (_FdInfo * capacity)()
            length = self.lib.proc_pidinfo(pid, 1, 0, output, ctypes.sizeof(output))
            if length <= 0 or length % ctypes.sizeof(_FdInfo):
                return None
            if length < ctypes.sizeof(output):
                return [(item.fd, item.kind) for item in output[:length // ctypes.sizeof(_FdInfo)]]
            capacity *= 2
        return None

    def open_rollouts(self, pid):
        descriptors = self._fd_list(pid)
        if descriptors is None:
            return None
        paths = []
        for descriptor, kind in descriptors:
            if kind != 1:  # PROX_FDTYPE_VNODE
                continue
            info = _VnodeFdPath()
            length = self.lib.proc_pidfdinfo(pid, descriptor, 2, ctypes.byref(info), ctypes.sizeof(info))
            if length != ctypes.sizeof(info):
                return None
            raw_path = info.vnode.path
            if not raw_path:
                return None
            if raw_path.endswith(b".jsonl"):
                paths.append((Path(os.fsdecode(raw_path)),
                              (info.vnode.info.stat.dev, info.vnode.info.stat.ino)))
        return paths


def _load_darwin():
    if sys.platform != "darwin":
        return None
    if _ctypes_layout() != _SDK_LAYOUT:
        return False  # ABI mismatch is unsupported; do not hide it with fallback.
    try:
        return _DarwinProc(ctypes.CDLL("/usr/lib/libproc.dylib", use_errno=True))
    except (OSError, AttributeError):
        return None  # Library or entry point genuinely unavailable.


_DARWIN = _load_darwin()


def process_identity(pid):
    """Read PID start seconds and microseconds without spawning on macOS."""
    if type(pid) is not int or not 0 < pid <= 2**31 - 1:
        return None
    if sys.platform == "darwin":
        if _DARWIN is False:
            return None
        if _DARWIN is not None:
            return _DARWIN.identity(pid)
    try:
        result = subprocess.run(
            ["ps", "-p", str(pid), "-o", "lstart="], capture_output=True,
            text=True, timeout=2, check=False,
        )
        value = result.stdout.strip()
        return value if result.returncode == 0 and value and "\n" not in value else None
    except (OSError, subprocess.SubprocessError):
        return None


def _process_executable(pid):
    if sys.platform == "darwin":
        if _DARWIN is False:
            return None
        if _DARWIN is not None:
            return _DARWIN.executable(pid)
    try:
        if sys.platform == "darwin":
            # A missing libproc cannot supply a reliable executable identity.
            return None
        return Path(os.readlink(f"/proc/{pid}/exe")).resolve(strict=True)
    except (OSError, ValueError):
        return None


def _open_rollouts(pid):
    if sys.platform == "darwin":
        if _DARWIN is False:
            return None
        if _DARWIN is not None:
            return _DARWIN.open_rollouts(pid)
    try:
        result = subprocess.run(
            ["lsof", "-n", "-p", str(pid), "-Fn"], capture_output=True,
            text=True, timeout=2, check=False,
        )
        if result.returncode != 0:
            return None
        return [(Path(line[1:]), None) for line in result.stdout.splitlines()
                if line.startswith("n") and line.endswith(".jsonl")]
    except (OSError, subprocess.SubprocessError):
        return None


def auth_epoch(home):
    """Metadata fingerprint only; credential contents are never opened."""
    try:
        path = Path(home).resolve() / "auth.json"
        info = path.stat()
        return (str(path.parent), "present", info.st_dev, info.st_ino,
                info.st_mtime_ns, info.st_size)
    except FileNotFoundError:
        return (str(Path(home).resolve()), "missing")
    except (OSError, TypeError, ValueError):
        return ("unavailable",)


_META_CACHE = OrderedDict()


def _read_meta(path, expected_identity=None):
    try:
        current = path.stat(follow_symlinks=False)
        identity = (current.st_dev, current.st_ino)
        if not stat.S_ISREG(current.st_mode) or (expected_identity is not None and identity != expected_identity):
            return None
        key = (str(path), current.st_dev, current.st_ino, current.st_mtime_ns,
               current.st_ctime_ns, current.st_size)
        cached = _META_CACHE.get(key)
        if cached is not None:
            _META_CACHE.move_to_end(key)
            return dict(cached)
        descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        with os.fdopen(descriptor, "rb") as stream:
            opened = os.fstat(stream.fileno())
            if not stat.S_ISREG(opened.st_mode) or (opened.st_dev, opened.st_ino) != identity:
                return None
            raw = stream.readline(MAX_HEADER_BYTES + 1)
            finished = os.fstat(stream.fileno())
        if (len(raw) > MAX_HEADER_BYTES or not raw.endswith(b"\n")
                or (opened.st_mtime_ns, opened.st_ctime_ns, opened.st_size)
                   != (finished.st_mtime_ns, finished.st_ctime_ns, finished.st_size)):
            return None
        record = json.loads(raw)
        payload = record.get("payload") if isinstance(record, dict) else None
        if not isinstance(payload, dict) or record.get("type") != "session_meta":
            return None
        safe = {}
        for field in ("id", "session_id", "parent_thread_id", "cwd", "cli_version"):
            value = payload.get(field)
            safe[field] = value if value is None or isinstance(value, str) and len(value) <= 4096 else "invalid"
        safe["source"] = "cli" if payload.get("source") == "cli" else "other"
        _META_CACHE[key] = safe
        while len(_META_CACHE) > 128:
            _META_CACHE.popitem(last=False)
        return dict(safe)
    except (OSError, ValueError, RecursionError, AttributeError):
        return None


def _root_candidate(path, sessions, cwd, expected_session, expected_identity=None):
    try:
        if path.is_symlink():
            return None
        resolved = path.resolve(strict=True)
        if not resolved.is_relative_to(sessions) or not resolved.is_file():
            return None
        name_match = ROLLOUT_NAME.fullmatch(resolved.name)
        if not name_match:
            return None
        meta = _read_meta(resolved, expected_identity)
        if not meta or meta.get("source") != "cli" or meta.get("parent_thread_id") is not None:
            return None
        ident = _uuid(meta.get("id"))
        if not ident or ident != _uuid(meta.get("session_id")):
            return None
        if ident != _uuid(name_match.group(1)):
            return None
        if expected_session is not None and ident != expected_session:
            return None
        if meta.get("cli_version") != SUPPORTED_VERSION:
            return None
        meta_cwd = meta.get("cwd")
        if not isinstance(meta_cwd, str) or Path(meta_cwd).resolve(strict=True) != cwd:
            return None
        return resolved
    except (OSError, ValueError, TypeError):
        return None


def bind_run(meta):
    """Bind only a unique, matching root file held by the exact launched PID."""
    if not isinstance(meta, dict):
        return None
    pid, started = meta.get("pid"), meta.get("started")
    if type(pid) is not int or pid <= 0 or not isinstance(started, str) or not started:
        return None
    if process_identity(pid) != started:
        return None
    expected = meta.get("expected_session")
    if expected is not None:
        expected = _uuid(expected)
        if expected is None:
            return None
    try:
        if not isinstance(meta.get("home"), str) or not isinstance(meta.get("cwd"), str):
            return None
        home = Path(meta["home"]).resolve(strict=True)
        sessions = (home / "sessions").resolve(strict=True)
        if not sessions.is_relative_to(home):
            return None
        cwd = Path(meta["cwd"]).resolve(strict=True)
        executable = meta.get("executable")
        if executable is not None and (
            not isinstance(executable, str)
            or _process_executable(pid) != Path(executable).resolve(strict=True)
        ):
            return None
        open_files = _open_rollouts(pid)
        if open_files is None:
            return None
        candidates = set()
        for path, identity in open_files:
            candidate = _root_candidate(path, sessions, cwd, None, identity)
            if candidate is not None:
                candidates.add(candidate)
        if len(candidates) != 1 or process_identity(pid) != started:
            return None
        chosen = candidates.pop()
        if expected is not None and _uuid(ROLLOUT_NAME.fullmatch(chosen.name).group(1)) != expected:
            return None
        return chosen
    except (OSError, ValueError, TypeError, subprocess.SubprocessError):
        return None
