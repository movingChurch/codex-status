"""One read-only collector, with no background service installation."""
import argparse
import atexit
import asyncio
from dataclasses import dataclass, field
import fcntl
import json
import math
import os
import resource
from pathlib import Path
import socket
import stat
import subprocess
import sys
import tempfile
import time
import uuid

INTERVAL = 5.0
MAX_MESSAGE = 16384
_started_children = []


def reap_started_children():
    _started_children[:] = [child for child in _started_children if child.poll() is None]


atexit.register(reap_started_children)


def validate_runtime(path=None):
    path = Path(path or Path('/tmp') / f'codex-status-{os.getuid()}').absolute()
    try:
        path.mkdir(mode=0o700)
    except FileExistsError:
        pass
    st = path.lstat()
    if not stat.S_ISDIR(st.st_mode) or st.st_uid != os.getuid() or st.st_mode & 0o077:
        raise ValueError('unsafe runtime directory')
    return path


def private_open(path):
    fd = os.open(path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    st = os.fstat(fd)
    if not stat.S_ISREG(st.st_mode) or st.st_uid != os.getuid() or st.st_mode & 0o077:
        os.close(fd)
        raise ValueError('unsafe runtime file')
    return fd


def run_path(runtime, run_id):
    if str(uuid.UUID(str(run_id))) != run_id:
        raise ValueError('invalid run identifier')
    return validate_runtime(runtime / 'runs') / (run_id + '.json')


def write_run_metadata(runtime, meta):
    runtime = validate_runtime(runtime)
    path = run_path(runtime, meta.get('id'))
    encoded = json.dumps(meta, allow_nan=False).encode()
    if len(encoded) > MAX_MESSAGE:
        raise ValueError('metadata too large')
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix='.write-')
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(encoded)
        os.replace(tmp, path)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)
    return path


def read_run_metadata(runtime, run_id):
    path = run_path(runtime, run_id)
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, 'rb') as stream:
        st = os.fstat(stream.fileno())
        if not stat.S_ISREG(st.st_mode) or st.st_uid != os.getuid() or st.st_mode & 0o077:
            raise ValueError('unsafe metadata')
        raw = stream.read(MAX_MESSAGE + 1)
    if len(raw) > MAX_MESSAGE:
        raise ValueError('metadata too large')
    meta = json.loads(raw)
    if meta.get('v') != 1 or meta.get('id') != run_id:
        raise ValueError('unsupported metadata')
    for key in ('home', 'cwd', 'tmux_socket'):
        if not isinstance(meta.get(key), str) or not os.path.isabs(meta[key]):
            raise ValueError('invalid path field')
    if type(meta.get('pid')) is not int or meta['pid'] < 1 or not isinstance(meta.get('started'), str):
        raise ValueError('invalid process identity')
    if type(meta.get('launched_at')) not in (int, float) or not math.isfinite(meta['launched_at']):
        raise ValueError('invalid launch time')
    if not isinstance(meta.get('hud_pane'), str) or not meta['hud_pane'].startswith('%'):
        raise ValueError('invalid pane identity')
    return meta


def ensure_collector(runtime):
    runtime = validate_runtime(runtime)
    reap_started_children()
    lock = private_open(runtime / 'startup.lock')
    try:
        fcntl.flock(lock, fcntl.LOCK_EX)
        def connect():
            sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            try:
                sock.connect(str(runtime / 'collector.sock'))
            except OSError:
                sock.close()
                return None
            return sock
        sock = connect()
        if sock:
            return sock
        child = subprocess.Popen([sys.executable, str(Path(__file__).resolve()), '--runtime', str(runtime)],
                                 stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                 stderr=subprocess.DEVNULL, start_new_session=True)
        _started_children.append(child)
        for _ in range(100):
            sock = connect()
            if sock:
                return sock
            if child.poll() is not None and child.returncode != 0:
                break
            time.sleep(0.05)
        raise RuntimeError('collector unavailable')
    finally:
        os.close(lock)


class BranchCache:
    """Read HEAD only; never execute Git or scan submodules."""
    def __init__(self):
        self.roots = {}
        self.heads = {}
        self.head_reads = 0

    def branch(self, cwd):
        cwd = Path(cwd).resolve()
        if cwd not in self.roots:
            self.roots[cwd] = None
            for parent in (cwd, *cwd.parents):
                gd = parent / '.git'
                if gd.is_dir():
                    self.roots[cwd] = gd / 'HEAD'
                    break
                if gd.is_file():
                    try:
                        with gd.open() as stream:
                            line = stream.readline(4096).strip()
                        if line.startswith('gitdir: '):
                            self.roots[cwd] = (parent / line[8:]).resolve() / 'HEAD'
                    except (OSError, UnicodeError):
                        pass
                    break
        head = self.roots[cwd]
        if head is None:
            return None
        try:
            st = head.stat()
            stamp = (st.st_dev, st.st_ino, st.st_mtime_ns, st.st_size)
            if head not in self.heads or self.heads[head][0] != stamp:
                with head.open() as stream:
                    text = stream.readline(4096).strip()
                self.head_reads += 1
                value = text[16:] if text.startswith('ref: refs/heads/') else text[:8]
                self.heads[head] = (stamp, value or None)
            return self.heads[head][1]
        except (OSError, UnicodeError):
            return None


def pane_visibility(socket_path):
    try:
        result = subprocess.run(['tmux', '-S', socket_path, 'list-panes', '-a', '-F',
                                 '#{pane_id}\t#{window_active}\t#{session_attached}\t#{window_zoomed_flag}\t#{pane_active}\t#{pane_dead}'],
                                capture_output=True, text=True, timeout=2)
        if result.returncode:
            return {}
        visible = {}
        for line in result.stdout.splitlines():
            row = line.split('\t')
            if len(row) == 6:
                pane, active, attached, zoomed, selected, dead = row
                visible[pane] = active == '1' and attached.isdigit() and int(attached) > 0 and dead == '0' and (zoomed != '1' or selected == '1')
        return visible
    except (OSError, subprocess.TimeoutExpired):
        return {}


@dataclass
class Run:
    meta: dict
    epoch: tuple
    reader: object = None
    path: object = None
    last_binding: float = 0
    snapshot: dict = field(default_factory=dict)
    account_changed: bool = False


class Collector:
    def __init__(self, runtime):
        self.runtime = validate_runtime(runtime)
        self.clients = {}
        self.runs = {}
        self.branches = BranchCache()
        self.visible = {}
        self.visibility_at = 0
        self.had_clients = False
        self.started_at = time.monotonic()
        self.last_empty = self.started_at
        self.metrics = {'ticks': 0, 'binding_checks': 0, 'session_reads': 0, 'tmux_queries': 0, 'git_queries': 0, 'bytes_read': 0}

    async def client(self, reader, writer):
        from binding import auth_epoch
        try:
            line = await asyncio.wait_for(reader.readline(), 10)
            request = json.loads(line)
            if request.get('v') != 1:
                raise ValueError('unsupported protocol')
            if request.get('op') == 'status':
                await self.send(writer, {'v': 1, 'pid': os.getpid(), 'clients': len(self.clients), 'runs': len(self.runs), 'metrics': self.metrics, 'head_reads': self.branches.head_reads, 'children_cpu_seconds': resource.getrusage(resource.RUSAGE_CHILDREN).ru_utime + resource.getrusage(resource.RUSAGE_CHILDREN).ru_stime})
                return
            run_id = request.get('run')
            meta = read_run_metadata(self.runtime, run_id)
            if run_id not in self.runs:
                self.runs[run_id] = Run(meta, tuple(meta.get('auth_epoch', auth_epoch(meta['home']))))
            elif self.runs[run_id].meta != meta:
                raise ValueError('changed run metadata')
            output_format = request.get('format', 'json')
            width = request.get('width', 80)
            if output_format not in ('json', 'ansi') or type(width) is not int or not 1 <= width <= 500:
                raise ValueError('invalid display request')
            self.clients[writer] = {'run': run_id, 'last': None, 'format': output_format, 'width': width}
            self.had_clients = True
            self.visibility_at = 0
            await self.send(writer, self.pending(meta))
            while line := await reader.readline():
                update = json.loads(line)
                width = update.get('width')
                if type(width) is not int or not 1 <= width <= 500:
                    raise ValueError('invalid width update')
                self.clients[writer]['width'] = width
                self.clients[writer]['last'] = None
                self.clients[writer]['last_key'] = None
        except (ValueError, TypeError, OSError, asyncio.TimeoutError, KeyError, asyncio.LimitOverrunError):
            pass
        finally:
            self.clients.pop(writer, None)
            used = {client['run'] for client in self.clients.values()}
            for key in list(self.runs):
                if key not in used:
                    del self.runs[key]
            if not self.clients:
                self.last_empty = time.monotonic()
            writer.close()
            try:
                await writer.wait_closed()
            except OSError:
                pass

    def pending(self, meta):
        return {'v': 1, 'state': 'pending', 'model': None, 'effort': None, 'cwd': meta['cwd'],
                'branch': None, 'context_remaining': None, 'five_hour': None, 'weekly': None,
                'activity': 'waiting', 'tool': None, 'elapsed': 0, 'updated_at': None,
                'quota_updated_at': None, 'session_id': None, 'quota_scope': None}

    async def send(self, writer, snapshot):
        client = self.clients.get(writer)
        payload = json.dumps(snapshot, allow_nan=False, separators=(',', ':')).encode()
        key = (client.get('width', 80), payload) if client is not None else None
        if client is not None and client.get('last_key') == key:
            return
        if client is not None and client.get('format') == 'ansi':
            from renderer import render
            lines = render(snapshot, client.get('width', 80))
            encoded = ('\x1b[?25l\x1b[H' + '\r\n'.join('\x1b[2K' + line for line in lines)).encode()
        else:
            encoded = payload + b'\n'
        if client is not None and client['last'] == encoded:
            return
        try:
            writer.write(encoded)
            await asyncio.wait_for(writer.drain(), 1)
            if client is not None:
                client['last'] = encoded
                client['last_key'] = key
        except (OSError, asyncio.TimeoutError):
            writer.close()

    async def tick(self):
        from binding import auth_epoch, bind_run
        from events import SessionReader
        now = time.monotonic()
        self.metrics['ticks'] += 1
        if now - self.visibility_at >= INTERVAL:
            sockets = {run.meta['tmux_socket'] for run in self.runs.values()}
            results = await asyncio.gather(*(asyncio.to_thread(pane_visibility, s) for s in sockets))
            self.visible = dict(zip(sockets, results))
            self.metrics['tmux_queries'] += len(sockets)
            self.visibility_at = now
        for run_id, run in list(self.runs.items()):
            if run_id not in self.runs:
                continue
            meta = run.meta
            is_visible = self.visible.get(meta['tmux_socket'], {}).get(meta['hud_pane'], False)
            snapshot = self.pending(meta)
            if not is_visible:
                snapshot['state'] = 'hidden'
            else:
                epoch = auth_epoch(meta['home'])
                if epoch != run.epoch:
                    run.epoch = epoch
                    run.account_changed = True
                    if run.reader:
                        run.reader.reset_after_account_change()
                    run.snapshot = {}
                if now - run.last_binding >= INTERVAL:
                    self.metrics['binding_checks'] += 1
                    path = await asyncio.to_thread(bind_run, meta)
                    run.last_binding = now
                    if path != run.path:
                        run.path = path
                        run.reader = None
                        run.snapshot = {}
                    if path is not None and run.reader is None:
                        # UUID is validated by bind_run against session metadata and filename.
                        session_id = path.stem[-36:]
                        run.reader = SessionReader(path, session_id)
                        if run.account_changed:
                            run.reader.reset_after_account_change()
                if run.reader:
                    try:
                        before = getattr(run.reader, 'bytes_read', 0)
                        data = run.reader.read()
                        self.metrics['bytes_read'] += max(0, getattr(run.reader, 'bytes_read', 0) - before)
                        self.metrics['session_reads'] += 1
                        if run.reader.invalidated:
                            run.reader = None
                            run.path = None
                            run.last_binding = 0
                            run.snapshot = {}
                        else:
                            run.snapshot = data
                            if data.get('quota_updated_at') is not None:
                                run.account_changed = False
                    except (OSError, ValueError, UnicodeError):
                        run.reader = None
                        run.path = None
                        run.last_binding = 0
                        run.snapshot = {}
                snapshot.update(run.snapshot)
                snapshot['branch'] = self.branches.branch(meta['cwd'])
                snapshot['elapsed'] = max(0, int((time.time() - meta.get('launched_at', time.time())) // INTERVAL) * int(INTERVAL))
                snapshot['state'] = 'live' if run.reader else 'pending'
                stamp = snapshot.get('quota_updated_at')
                if isinstance(stamp, (int, float)) and time.time() - stamp > INTERVAL:
                    snapshot['state'] = 'stale'
                if run.account_changed:
                    snapshot['state'] = 'account-changed'
            await asyncio.gather(*(self.send(writer, snapshot) for writer, client in list(self.clients.items()) if client['run'] == run_id))

    async def run(self):
        server = await asyncio.start_unix_server(self.client, str(self.runtime / 'collector.sock'), limit=MAX_MESSAGE)
        os.chmod(self.runtime / 'collector.sock', 0o600)
        try:
            async with server:
                while True:
                    await self.tick()
                    now = time.monotonic()
                    if not self.clients and ((self.had_clients and now - self.last_empty >= 1) or now - self.started_at > 10):
                        break
                    await asyncio.sleep(1)
        finally:
            for writer in list(self.clients):
                writer.close()
            server.close()
            await server.wait_closed()


async def serve(runtime):
    runtime = validate_runtime(runtime)
    lock = private_open(runtime / 'collector.lock')
    try:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return
        sock = runtime / 'collector.sock'
        if sock.exists():
            st = sock.lstat()
            if not stat.S_ISSOCK(st.st_mode) or st.st_uid != os.getuid():
                raise ValueError('unsafe collector socket')
            sock.unlink()
        try:
            await Collector(runtime).run()
        finally:
            if sock.exists() and stat.S_ISSOCK(sock.lstat().st_mode):
                sock.unlink()
    finally:
        os.close(lock)


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--runtime', required=True)
    args = parser.parse_args()
    try:
        asyncio.run(serve(Path(args.runtime)))
    except (KeyboardInterrupt, OSError, ValueError):
        sys.exit(1)
