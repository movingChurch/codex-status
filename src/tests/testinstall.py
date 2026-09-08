import contextlib
import importlib.util
import io
import os
from pathlib import Path
import tempfile
import types
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location("checkout_installer", Path(__file__).resolve().parents[2] / "install.py")
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class CheckoutInstallTests(unittest.TestCase):
    def setUp(self):
        self.fixture = tempfile.TemporaryDirectory(dir="/tmp")
        self.addCleanup(self.fixture.cleanup)
        self.root = Path(self.fixture.name)
        self.repository = self.root / "source checkout"
        self.entry = self.repository / "src" / "codex-status.py"
        self.entry.parent.mkdir(parents=True)
        self.entry.write_text("#!/usr/bin/env python3\n")
        self.entry.chmod(0o755)
        self.bin_dir = self.root / "bin"
        self.link = self.bin_dir / "codex-status"
        self.preflight = mock.patch.object(installer, "preflight")
        self.preflight.start()
        self.addCleanup(self.preflight.stop)

    def test_install_and_repeat_create_exact_owned_link(self):
        with mock.patch.object(installer, "build_native", return_value=True):
            installer.install(self.repository, self.bin_dir)
            inode = self.link.lstat().st_ino
            installer.install(self.repository, self.bin_dir)
        self.assertTrue(self.link.is_symlink())
        self.assertEqual(os.readlink(self.link), str(self.entry.resolve()))
        self.assertEqual(self.link.lstat().st_ino, inode)

    def test_unrelated_file_is_preserved(self):
        self.bin_dir.mkdir()
        self.link.write_text("existing command")
        with self.assertRaises(ValueError):
            installer.install(self.repository, self.bin_dir, native=False)
        self.assertEqual(self.link.read_text(), "existing command")

    def test_unrelated_and_broken_symlinks_are_preserved(self):
        self.bin_dir.mkdir()
        for target in (self.root / "unrelated", self.entry.parent / "missing.py"):
            with self.subTest(target=target.name):
                self.link.symlink_to(target)
                with self.assertRaises(ValueError):
                    installer.install(self.repository, self.bin_dir, native=False)
                self.assertEqual(os.readlink(self.link), str(target))
                self.link.unlink()

    def test_uninstall_removes_only_exact_owned_link(self):
        installer.install(self.repository, self.bin_dir, native=False)
        default_codex = self.bin_dir / "codex"
        default_codex.write_text("default Codex")
        self.assertTrue(installer.uninstall(self.repository, self.bin_dir))
        self.assertFalse(self.link.is_symlink())

        self.assertFalse(installer.uninstall(self.repository, self.bin_dir))
        self.assertEqual(default_codex.read_text(), "default Codex")
        self.assertTrue(self.entry.is_file())

    def test_uninstall_preserves_other_checkouts_and_regular_files(self):
        self.bin_dir.mkdir()
        self.link.symlink_to(self.root / "other-checkout" / "src" / "codex-status.py")
        with self.assertRaises(ValueError):
            installer.uninstall(self.repository, self.bin_dir)
        self.assertTrue(self.link.is_symlink())
        self.link.unlink()
        self.link.write_text("not installer owned")
        with self.assertRaises(ValueError):
            installer.uninstall(self.repository, self.bin_dir)
        self.assertEqual(self.link.read_text(), "not installer owned")

    def test_uninstall_owned_broken_link_without_runtime_preflight(self):
        installer.install(self.repository, self.bin_dir, native=False)
        self.entry.unlink()
        with mock.patch.object(installer, "preflight", side_effect=ValueError("unavailable runtime")):
            self.assertTrue(installer.uninstall(self.repository, self.bin_dir))

    def test_failed_optional_build_keeps_python_entry(self):
        with mock.patch.object(installer, "build_native", return_value=False):
            link, native = installer.install(self.repository, self.bin_dir)
        self.assertEqual(link, self.link)
        self.assertFalse(native)
        self.assertTrue(self.link.is_symlink())

    def test_preflight_failure_has_no_install_side_effect(self):
        with mock.patch.object(installer, "preflight", side_effect=ValueError("tmux unavailable")):
            with self.assertRaises(ValueError):
                installer.install(self.repository, self.bin_dir, native=False)
        self.assertFalse(self.bin_dir.exists())

    def test_cli_install_uninstall_honor_custom_bin_directory(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            self.assertEqual(installer.main(["--bin-dir", str(self.bin_dir), "--skip-native"], repository=self.repository), 0)
            self.assertTrue(self.link.is_symlink())
            self.assertEqual(installer.main(["--bin-dir", str(self.bin_dir), "--uninstall"], repository=self.repository), 0)
        self.assertFalse(self.link.is_symlink())


class InstallerPreflightTests(unittest.TestCase):
    def check(self, *, platform="darwin", version=(3, 14), missing=None, path_version="3.14"):
        with mock.patch.object(installer.sys, "platform", platform), mock.patch.object(installer.sys, "version_info", version), mock.patch.object(installer.shutil, "which", side_effect=lambda name:None if name == missing else "/bin/" + name), mock.patch.object(installer.subprocess, "run", return_value=types.SimpleNamespace(stdout=path_version)):
            installer.preflight()

    def test_supported_runtime_passes(self):
        self.check()

    def test_unsupported_platform_or_python_is_rejected(self):
        for options in ({"platform":"linux"}, {"version":(3, 13)}, {"path_version":"3.13"}):
            with self.subTest(options=options), self.assertRaises(ValueError):
                self.check(**options)

    def test_missing_required_commands_are_rejected(self):
        for command in ("python3", "tmux", "codex"):
            with self.subTest(command=command), self.assertRaises(ValueError):
                self.check(missing=command)


if __name__ == "__main__":
    unittest.main()
