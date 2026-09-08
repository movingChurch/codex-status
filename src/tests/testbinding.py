import ctypes
import json
import os
import sys
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
import binding

ROOT = "11111111-1111-7111-8111-111111111111"
OTHER = "22222222-2222-7222-8222-222222222222"

class BindingTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = Path(self.tmp.name) / "account"
        self.cwd = Path(self.tmp.name) / "project"
        self.cwd.mkdir()
        self.folder = self.home / "sessions" / "2026" / "09" / "08"
        self.folder.mkdir(parents=True)
        self.meta = dict(pid=345, started="Mon Sep  8 12:00:00 2026", home=str(self.home), cwd=str(self.cwd), expected_session=None)
        self.paths = []
        self.identity = patch.object(binding, "process_identity", return_value=self.meta["started"])
        self.identity.start(); self.addCleanup(self.identity.stop)
        self.run = patch.object(binding, "_open_rollouts", side_effect=lambda pid: [(p, None) for p in self.paths])
        self.run.start(); self.addCleanup(self.run.stop)

    def lsof(self, args, **kwargs):
        return subprocess.CompletedProcess(args, 0, "p345\n" + "".join("n" + str(p) + "\n" for p in self.paths), "")

    def rollout(self, ident=ROOT, **changes):
        p = self.folder / ("rollout-2026-09-08T12-00-00-" + ident + ".jsonl")
        meta = dict(id=ident, session_id=ident, source="cli", parent_thread_id=None, cwd=str(self.cwd), cli_version="0.153.4")
        meta.update(changes)
        p.write_text(json.dumps(dict(type="session_meta", payload=meta)) + "\n")
        self.paths.append(p)
        return p

    def test_binds_unique_root_among_subagents(self):
        root = self.rollout()
        self.rollout(OTHER, source={"subagent": {}}, parent_thread_id=ROOT, session_id=ROOT)
        self.assertEqual(binding.bind_run(self.meta), root.resolve())

    def test_two_roots_are_ambiguous(self):
        self.rollout(); self.rollout(OTHER)
        self.assertIsNone(binding.bind_run(self.meta))

    def test_reused_pid_cannot_bind(self):
        self.rollout(); self.meta["started"] = "different process"
        self.assertIsNone(binding.bind_run(self.meta))

    def test_foreign_account_and_cwd_are_rejected(self):
        self.rollout(cwd="/another-project")
        self.assertIsNone(binding.bind_run(self.meta))
        self.paths.clear(); self.rollout()
        self.meta["home"] = str(Path(self.tmp.name) / "other-account")
        self.assertIsNone(binding.bind_run(self.meta))

    def test_explicit_resume_must_match(self):
        root = self.rollout(); self.meta["expected_session"] = OTHER
        self.assertIsNone(binding.bind_run(self.meta))
        self.meta["expected_session"] = ROOT
        self.assertEqual(binding.bind_run(self.meta), root.resolve())

    def test_version_and_file_name_must_match(self):
        p = self.rollout(cli_version="0.144.1")
        self.assertIsNone(binding.bind_run(self.meta))
        p.unlink(); self.paths.clear(); p = self.rollout()
        target = p.with_name(p.name.replace(ROOT, OTHER)); p.rename(target); self.paths = [target]
        self.assertIsNone(binding.bind_run(self.meta))

    def test_fd_query_failure_is_not_evidence(self):
        self.rollout()
        with patch.object(binding, "_open_rollouts", return_value=None):
            self.assertIsNone(binding.bind_run(self.meta))

    def test_explicit_resume_cannot_hide_another_open_root(self):
        self.rollout(); self.rollout(OTHER)
        self.meta["expected_session"] = ROOT
        self.assertIsNone(binding.bind_run(self.meta))

    def test_symlink_rollout_and_wrong_executable_are_rejected(self):
        root = self.rollout()
        alias = root.with_name("alias-" + root.name); alias.symlink_to(root)
        self.paths = [alias]
        self.assertIsNone(binding.bind_run(self.meta))
        self.paths = [root]; self.meta["executable"] = str(root)
        with patch.object(binding, "_process_executable", return_value=self.cwd):
            self.assertIsNone(binding.bind_run(self.meta))

    def test_fd_inode_must_match_even_when_metadata_is_cached(self):
        root = self.rollout()
        self.assertEqual(binding.bind_run(self.meta), root.resolve())
        current = root.stat()
        with patch.object(binding, "_open_rollouts", return_value=[(root, (current.st_dev, current.st_ino + 1))]):
            self.assertIsNone(binding.bind_run(self.meta))

    def test_metadata_cache_revalidates_and_keeps_only_safe_fields(self):
        root = self.rollout()
        self.assertEqual(binding._read_meta(root)["id"], ROOT)
        with patch.object(os, "open", wraps=os.open) as opened:
            self.assertEqual(binding._read_meta(root)["id"], ROOT)
            opened.assert_not_called()
        payload = dict(id=OTHER, session_id=OTHER, source="cli", parent_thread_id=None,
                       cwd=str(self.cwd), cli_version="0.153.4", base_instructions="sensitive-fixture")
        root.write_text(json.dumps(dict(type="session_meta", payload=payload)) + "\n")
        with patch.object(os, "open", wraps=os.open) as opened:
            result = binding._read_meta(root)
            self.assertEqual(result["id"], OTHER)
            self.assertEqual(opened.call_count, 1)
        self.assertNotIn("sensitive-fixture", repr(binding._META_CACHE))
        self.assertNotIn("base_instructions", result)

    def test_auth_epoch_changes_without_reading_contents(self):
        first = binding.auth_epoch(self.home)
        auth = self.home / "auth.json"; auth.write_text("fixture-secret")
        with patch.object(Path, "read_text", side_effect=AssertionError("auth must never be read")):
            second = binding.auth_epoch(self.home)
        self.assertNotEqual(first, second)
        auth.unlink(); self.assertEqual(binding.auth_epoch(self.home), first)


@unittest.skipUnless(sys.platform == "darwin", "Darwin libproc contract")
class NativeBindingTests(unittest.TestCase):
    def test_process_identity_does_not_fork_and_has_microseconds(self):
        with patch.object(subprocess, "run", wraps=subprocess.run) as child:
            identity = binding.process_identity(os.getpid())
        self.assertIsNotNone(identity)
        child.assert_not_called()
        self.assertRegex(identity, r"^darwin:[0-9]+:[0-9]{6}$")

    def test_native_failure_does_not_fall_back_to_subprocess(self):
        from unittest.mock import Mock
        failed = Mock(); failed.identity.return_value = None
        with patch.object(binding, "_DARWIN", failed, create=True):
            with patch.object(subprocess, "run", wraps=subprocess.run) as child:
                identity = binding.process_identity(os.getpid())
        self.assertIsNone(identity)
        child.assert_not_called()

    def test_layout_matches_sdk_sizes_and_offsets(self):
        self.assertEqual(binding._ctypes_layout(), dict(proc_bsdinfo=136,start_sec=120,start_usec=128,
            proc_fdinfo=8,proc_fileinfo=24,vinfo_stat=136,vnode_info=152,vnode_info_path=1176,
            vnode_fdinfowithpath=1200,vnode_path_offset=176,vnode_dev_offset=24,vnode_ino_offset=32,
            max_path=1024,max_comm=16))

    def test_abi_mismatch_is_fail_closed(self):
        with patch.object(binding, "_ctypes_layout", return_value={}):
            self.assertIs(binding._load_darwin(), False)
        with patch.object(binding, "_DARWIN", False):
            with patch.object(subprocess, "run", wraps=subprocess.run) as child:
                self.assertIsNone(binding.process_identity(os.getpid()))
                self.assertIsNone(binding._open_rollouts(os.getpid()))
                self.assertIsNone(binding._process_executable(os.getpid()))
            child.assert_not_called()

    def test_partial_ffi_results_never_become_partial_evidence(self):
        from unittest.mock import Mock
        lib = Mock(); native = binding._DarwinProc(lib)
        lib.proc_pidinfo.return_value = 135
        self.assertIsNone(native.identity(os.getpid()))
        lib.proc_pidinfo.side_effect = lambda pid, flavor, arg, buffer, size: 8 if buffer is None else size
        self.assertIsNone(native._fd_list(os.getpid()))
        with patch.object(native, "_fd_list", return_value=[(5, 1)]):
            lib.proc_pidfdinfo.return_value = 1199
            self.assertIsNone(native.open_rollouts(os.getpid()))

    def test_same_second_pid_start_microseconds_remain_distinct(self):
        from unittest.mock import Mock
        lib = Mock(); native = binding._DarwinProc(lib)
        def fill(pid, flavor, arg, buffer, size):
            info = ctypes.cast(buffer, ctypes.POINTER(binding._BsdInfo)).contents
            info.pid = pid; info.start_sec = 1800000000; info.start_usec = fill.usec
            return ctypes.sizeof(binding._BsdInfo)
        fill.usec = 1; lib.proc_pidinfo.side_effect = fill
        first = native.identity(123); fill.usec = 2
        self.assertNotEqual(first, native.identity(123))

    def test_missing_ffi_can_use_explicit_fallback(self):
        with patch.object(binding, "_DARWIN", None):
            with patch.object(subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "safe start\n", "")) as child:
                self.assertEqual(binding.process_identity(123), "safe start")
                self.assertEqual(child.call_args.args[0], ["ps", "-p", "123", "-o", "lstart="])

    def test_live_open_root_binds_without_subprocess(self):
        with tempfile.TemporaryDirectory() as folder:
            home = Path(folder).resolve(); sessions = home / "sessions"; sessions.mkdir()
            file = sessions / ("rollout-2026-09-08T12-00-00-" + ROOT + ".jsonl")
            payload = dict(id=ROOT, session_id=ROOT, source="cli", parent_thread_id=None, cwd=str(home), cli_version="0.153.4")
            file.write_text(json.dumps(dict(type="session_meta", payload=payload)) + "\n")
            with file.open("rb"):
                meta = dict(pid=os.getpid(), started=binding.process_identity(os.getpid()), home=str(home), cwd=str(home), expected_session=ROOT)
                with patch.object(subprocess, "run", wraps=subprocess.run) as child:
                    result = binding.bind_run(meta)
                self.assertEqual(result, file)
                child.assert_not_called()

if __name__ == "__main__": unittest.main()
