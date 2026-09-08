"""Real Unix socket lifecycle; no credentials, Codex execution or API calls."""
import asyncio
import json
from pathlib import Path
import os
import socket
import subprocess
import sys
import tempfile
import time
import unittest
import uuid
from collector import validate_runtime, write_run_metadata


class SocketLifecycle(unittest.TestCase):
    def test_shared_collector_last_disconnect_exits(self):
        with tempfile.TemporaryDirectory(dir='/tmp') as td:
            root = validate_runtime(Path(td)/'runtime')
            run = str(uuid.uuid4())
            write_run_metadata(root, {'v':1, 'id':run, 'pid':os.getpid(), 'started':'fixture',
                                     'home':td, 'cwd':td, 'tmux_socket':str(Path(td)/'absent.sock'),
                                     'hud_pane':'%1', 'launched_at':time.time()})
            proc = subprocess.Popen([sys.executable, 'collector.py', '--runtime', str(root)], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            clients = []
            try:
                deadline = time.monotonic()+5
                while not (root/'collector.sock').exists() and time.monotonic()<deadline:
                    time.sleep(0.02)
                for _ in range(36):
                    client = socket.socket(socket.AF_UNIX)
                    client.settimeout(5)
                    client.connect(str(root/'collector.sock'))
                    client.sendall(json.dumps({'v':1,'run':run,'width':120}).encode()+b'\n')
                    stream = client.makefile('rb')
                    message = json.loads(stream.readline())
                    self.assertEqual(message['state'], 'pending')
                    self.assertIsNone(message['session_id'])
                    stream.close()
                    clients.append(client)
                self.assertIsNone(proc.poll())
                for client in clients:
                    client.close()
                proc.wait(timeout=5)
                self.assertEqual(proc.returncode, 0, proc.stderr.read().decode())
                self.assertFalse((root/'collector.sock').exists())
            finally:
                for client in clients:
                    client.close()
                if proc.poll() is None:
                    proc.terminate(); proc.wait(timeout=5)
                proc.stderr.close()


if __name__ == '__main__':
    unittest.main()


class CollectorRecovery(unittest.TestCase):
    def test_abrupt_collector_exit_allows_clean_restart(self):
        from collector import ensure_collector
        with tempfile.TemporaryDirectory(dir='/tmp') as td:
            root = validate_runtime(Path(td)/'runtime')
            run = str(uuid.uuid4())
            write_run_metadata(root, {'v':1,'id':run,'pid':os.getpid(),'started':'fixture','home':td,'cwd':td,
                                     'tmux_socket':str(Path(td)/'absent.sock'),'hud_pane':'%1','launched_at':time.time()})
            proc = subprocess.Popen([sys.executable,'collector.py','--runtime',str(root)],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            first = None; replacement = None; restarted_pid = None
            try:
                deadline = time.monotonic()+5
                while not (root/'collector.sock').exists() and time.monotonic()<deadline: time.sleep(0.02)
                first = socket.socket(socket.AF_UNIX); first.settimeout(3); first.connect(str(root/'collector.sock'))
                first.sendall(json.dumps({'v':1,'run':run}).encode()+b'\n')
                self.assertIn(b'pending',first.recv(16384))
                proc.kill();proc.wait(timeout=3)
                while first.recv(16384): pass
                first.close();first=None
                self.assertTrue((root/'collector.sock').exists())
                replacement=ensure_collector(root); replacement.settimeout(3)
                replacement.sendall(json.dumps({'v':1,'run':run}).encode()+b'\n')
                self.assertIn(b'pending',replacement.recv(16384))
                with socket.socket(socket.AF_UNIX) as query:
                    query.settimeout(3);query.connect(str(root/'collector.sock'));query.sendall(b'{"v":1,"op":"status"}\n')
                    restarted_pid=json.loads(query.recv(16384))['pid']
                self.assertNotEqual(restarted_pid,proc.pid)
                replacement.close();replacement=None
                deadline=time.monotonic()+5
                while (root/'collector.sock').exists() and time.monotonic()<deadline: time.sleep(0.02)
                self.assertFalse((root/'collector.sock').exists())
                if restarted_pid: os.waitpid(restarted_pid,0)
            finally:
                if first:first.close()
                if replacement:replacement.close()
                if proc.poll() is None:proc.kill();proc.wait()
