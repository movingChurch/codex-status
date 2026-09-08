import fcntl
import importlib.util
import json
import os
from pathlib import Path
import pty
import signal
import shutil
import socket
import struct
import subprocess
import tempfile
import termios
import types
import unittest
import uuid
from unittest import mock

import launcher


PREFIX = b"\x1b[?25l\x1b[2J"
RESTORE = b"\x1b[0m\x1b[?25h"


class NativeClientTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if shutil.which("xcrun") is None:
            raise unittest.SkipTest("installed Apple compiler and SDK unavailable")
        spec = importlib.util.spec_from_file_location("build_client", Path(__file__).resolve().parents[1] / "build-client.py")
        builder = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(builder)
        cls.build_dir = tempfile.TemporaryDirectory(dir="/tmp")
        cls.binary = builder.build(cls.build_dir.name)
        if cls.binary is None:
            cls.build_dir.cleanup()
            raise RuntimeError("Native client build failed with installed xcrun")

    @classmethod
    def tearDownClass(cls):
        cls.build_dir.cleanup()

    def setUp(self):
        self.fixture = tempfile.TemporaryDirectory(dir="/tmp")
        self.addCleanup(self.fixture.cleanup)
        self.path = str(Path(self.fixture.name) / "client.sock")
        self.server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.addCleanup(self.server.close)
        self.server.bind(self.path)
        self.server.listen(1)
        self.server.settimeout(1)
        self.run_id = str(uuid.uuid4())

    def start(self, stdin=subprocess.DEVNULL):
        child = subprocess.Popen([str(self.binary), self.path, self.run_id], stdin=stdin,
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.addCleanup(self.stop_child, child)
        try:
            connection, _ = self.server.accept()
        except TimeoutError:
            self.fail("display client did not connect to its requested Unix socket")
        connection.settimeout(2)
        self.addCleanup(connection.close)
        stream = connection.makefile("rb", buffering=0)
        self.addCleanup(stream.close)
        first = json.loads(stream.readline())
        self.assertEqual(first["v"], 1)
        self.assertEqual(first["run"], self.run_id)
        self.assertEqual(first["format"], "ansi")
        self.assertEqual(set(first), {"v", "run", "width", "format"})
        return child, connection, stream, first

    @staticmethod
    def stop_child(child):
        if child.poll() is None:
            child.kill()
        child.communicate(timeout=3)

    def test_ansi_frame_bytes_are_forwarded_and_eof_restores_cursor(self):
        child, connection, stream, _ = self.start()
        frame = "\x1b[H\x1b[38;5;115m모델\x1b[0m\r\n문맥\r\n작업 중".encode()
        connection.sendall(frame[:10])
        connection.sendall(frame[10:])
        connection.shutdown(socket.SHUT_WR)
        output, error = child.communicate(timeout=3)
        self.assertEqual(child.returncode, 0)
        self.assertEqual(output, PREFIX + frame + RESTORE)
        self.assertEqual(error, b"")

    def test_terminal_signals_restore_cursor_without_waiting_for_server(self):
        for sig in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
            with self.subTest(signal=sig):
                child, _, _, _ = self.start()
                child.send_signal(sig)
                output, error = child.communicate(timeout=3)
                self.assertEqual(child.returncode, 0)
                self.assertTrue(output.endswith(RESTORE))
                self.assertEqual(error, b"")

    def test_resize_sends_only_width(self):
        master, slave = pty.openpty()
        self.addCleanup(os.close, master)
        self.addCleanup(os.close, slave)
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 4, 101, 0, 0))
        child, connection, stream, first = self.start(stdin=slave)
        self.assertEqual(first["width"], 100)
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 4, 61, 0, 0))
        child.send_signal(signal.SIGWINCH)
        self.assertEqual(json.loads(stream.readline()), {"width": 60})
        connection.shutdown(socket.SHUT_WR)
        child.communicate(timeout=3)

    def test_idle_client_sends_no_periodic_requests(self):
        child, connection, _, _ = self.start()
        connection.settimeout(0.15)
        with self.assertRaises(TimeoutError):
            connection.recv(1)
        connection.shutdown(socket.SHUT_WR)
        child.communicate(timeout=3)

    def test_invalid_uuid_exits_without_echoing_input(self):
        invalid = "invalid-private-input"
        result = subprocess.run([str(self.binary), self.path, invalid], capture_output=True, timeout=3)
        self.assertEqual(result.returncode, 2)
        self.assertNotIn(invalid.encode(), result.stdout + result.stderr)

    def test_dispatch_closes_bootstrap_connection_before_native_exec(self):
        class Replaced(Exception):
            pass
        connection = mock.Mock()
        runtime = Path(self.fixture.name)
        collector = types.SimpleNamespace(validate_runtime=lambda path:runtime, ensure_collector=lambda path:connection)
        renderer = types.SimpleNamespace(run_renderer=mock.Mock(return_value=17))
        def replace(path, args):
            connection.close.assert_called_once()
            self.assertEqual(Path(path).name, "codex-status-client")
            self.assertEqual(args[1:], [str(runtime / "collector.sock"), self.run_id])
            raise Replaced()
        with mock.patch.dict("sys.modules", {"collector":collector, "renderer":renderer}), mock.patch("launcher.Path.is_file", return_value=True), mock.patch("launcher.os.access", return_value=True), mock.patch("launcher.os.execv", side_effect=replace):
            with self.assertRaises(Replaced):
                launcher.main(["__render", str(runtime), self.run_id])
        renderer.run_renderer.assert_not_called()

    def test_dispatch_falls_back_when_native_exec_is_unavailable(self):
        connection = mock.Mock()
        runtime = Path(self.fixture.name)
        collector = types.SimpleNamespace(validate_runtime=lambda path:runtime, ensure_collector=lambda path:connection)
        renderer = types.SimpleNamespace(run_renderer=mock.Mock(return_value=17))
        with mock.patch.dict("sys.modules", {"collector":collector, "renderer":renderer}), mock.patch("launcher.Path.is_file", return_value=True), mock.patch("launcher.os.access", return_value=True), mock.patch("launcher.os.execv", side_effect=OSError("unsupported binary")):
            self.assertEqual(launcher.main(["__render", str(runtime), self.run_id]), 17)
        connection.close.assert_called_once()
        renderer.run_renderer.assert_called_once_with(runtime, self.run_id)


if __name__ == "__main__":
    unittest.main()
