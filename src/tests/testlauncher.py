from pathlib import Path
import contextlib
import io
import shlex
import tempfile
import types
import unittest
from unittest import mock
import uuid

import launcher


class LauncherTests(unittest.TestCase):
    def test_fresh_launch_preserves_arguments_after_separator(self):
        resume, args = launcher.parse_launch(["--", "--model", "model name", "literal $(no) `no`"])
        self.assertIsNone(resume)
        self.assertEqual(args, ["--model", "model name", "literal $(no) `no`"])

    def test_resume_requires_exact_uuid(self):
        run = str(uuid.uuid4())
        self.assertEqual(launcher.parse_launch(["--resume", run, "--", "--model", "test"]), (run, ["--model", "test"]))
        for args in (["--resume", "latest"], ["--last"], ["--", "resume", "--last"], ["--", "exec", "x"], ["--", "--last"]):
            with self.assertRaises(ValueError):
                launcher.parse_launch(args)

    def test_shell_command_preserves_literal_arguments(self):
        args = ["quotes' spaces", "$(touch /not-real)", "line\nbreak"]
        command = launcher.build_runner_command(Path("/tmp/a b/codex-status.py"), Path("/tmp/private"), str(uuid.uuid4()), Path("/tmp/private/t.sock"), "/opt/Codex App/codex", None, args)
        words = shlex.split(command)
        self.assertEqual(words[-3:], args)
        self.assertIn("/opt/Codex App/codex", words)
        self.assertIn("/tmp/a b/codex-status.py", words)

    def test_cleanup_only_paired_pane_on_dedicated_socket(self):
        run = str(uuid.uuid4())
        meta = {"id":run, "tmux_socket":f"/tmp/codex-status-501/tmux-{run}.sock", "codex_pane":"%0", "hud_pane":"%1"}
        with mock.patch("launcher.subprocess.run") as call:
            launcher.cleanup_pair(meta)
        self.assertEqual(call.call_count, 1)
        cmd = call.call_args.args[0]
        self.assertEqual(cmd, ["tmux", "-S", meta["tmux_socket"], "kill-pane", "-t", "%1"])
        self.assertNotIn("kill-server", cmd)

    def test_cleanup_rejects_foreign_socket_or_codex_pane(self):
        run = str(uuid.uuid4())
        for meta in ({"id":run,"tmux_socket":"/tmp/default","codex_pane":"%0","hud_pane":"%1"},
                     {"id":run,"tmux_socket":f"/tmp/tmux-{run}.sock","codex_pane":"%0","hud_pane":"%0"}):
            with mock.patch("launcher.subprocess.run") as call:
                launcher.cleanup_pair(meta)
            call.assert_not_called()

    def test_runner_waits_for_codex_when_identity_probe_fails(self):
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            run = str(uuid.uuid4())
            opts = types.SimpleNamespace(runtime=root, run=run, socket=str(Path(root)/f"tmux-{run}.sock"), codex="/bin/true", resume=None, args=[])
            child = mock.Mock(pid=1234)
            child.wait.return_value = 7
            binding = types.SimpleNamespace(process_identity=mock.Mock(side_effect=OSError("private detail")), auth_epoch=lambda home:(1,2,3))
            collector = types.SimpleNamespace(validate_runtime=lambda root:Path(root), write_run_metadata=mock.Mock())
            with mock.patch.dict("sys.modules", {"binding":binding,"collector":collector}), mock.patch.dict("os.environ", {"TMUX_PANE":"%0"}), mock.patch("launcher.shutil.which", return_value="tmux"), mock.patch("launcher.subprocess.run",return_value=types.SimpleNamespace(stdout="%1\n")), mock.patch("launcher.subprocess.Popen",return_value=child):
                self.assertEqual(launcher.run_codex(opts), 7)
            child.wait.assert_called_once()
            child.terminate.assert_not_called()
            child.kill.assert_not_called()

    def test_runner_metadata_has_stat_epoch_but_no_arguments(self):
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            run = str(uuid.uuid4())
            opts = types.SimpleNamespace(runtime=root, run=run, socket=str(Path(root)/f"tmux-{run}.sock"), codex="/bin/true", resume=None, args=["private prompt"])
            child = mock.Mock(pid=1234)
            child.wait.return_value=0
            binding = types.SimpleNamespace(process_identity=lambda pid:"identity",auth_epoch=lambda home:(1,2,3))
            writer=mock.Mock(return_value=Path(root)/"metadata.json")
            collector=types.SimpleNamespace(validate_runtime=lambda root:Path(root),write_run_metadata=writer)
            with mock.patch.dict("sys.modules", {"binding":binding,"collector":collector}),mock.patch.dict("os.environ",{"TMUX_PANE":"%0"}),mock.patch("launcher.shutil.which",return_value="tmux"),mock.patch("launcher.subprocess.run",return_value=types.SimpleNamespace(stdout="%1\n")),mock.patch("launcher.subprocess.Popen",return_value=child):
                self.assertEqual(launcher.run_codex(opts),0)
            meta=writer.call_args.args[1]
            self.assertEqual(meta.get("auth_epoch"),(1,2,3))
            self.assertNotIn("private prompt",repr(meta))
            self.assertEqual(meta["pid"],1234)

    def test_main_failure_does_not_echo_arguments_or_subprocess_exception(self):
        secret="private prompt value"
        output=io.StringIO()
        with mock.patch("launcher.launch",side_effect=OSError(secret)),contextlib.redirect_stderr(output):
            self.assertEqual(launcher.main(["--",secret]),2)
        self.assertNotIn(secret,output.getvalue())

    def test_launch_uses_new_socket_and_preserves_account_on_attach(self):
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            collector=types.SimpleNamespace(validate_runtime=lambda:Path(root))
            commands=[]
            def run(command,**kwargs):
                commands.append(command)
                return types.SimpleNamespace(stdout="%0\n" if "new-session" in command else "%1\n" if "split-window" in command else "")
            with mock.patch.dict("sys.modules",{"collector":collector}),mock.patch.dict("os.environ",{"TMUX":"prior-server","CODEX_HOME":"/tmp/selected-account"}),mock.patch("launcher.shutil.which",side_effect=lambda name:"/bin/"+name),mock.patch("launcher.subprocess.run",side_effect=run),mock.patch("launcher.subprocess.call",return_value=0) as attach:
                self.assertEqual(launcher.launch(None,[]),0)
            self.assertIn("/dev/null",commands[0])
            sockets={command[2] for command in commands}
            self.assertEqual(len(sockets),1)
            self.assertTrue(Path(next(iter(sockets))).name.startswith("tmux-"))
            self.assertFalse(any("kill-pane" in command or "kill-server" in command for command in commands))
            env=attach.call_args.kwargs["env"]
            self.assertEqual(env["CODEX_HOME"],"/tmp/selected-account")
            self.assertNotIn("TMUX",env)

    def test_partial_setup_cleans_only_unstarted_owned_pane(self):
        with tempfile.TemporaryDirectory(dir="/tmp") as root:
            collector=types.SimpleNamespace(validate_runtime=lambda:Path(root))
            commands=[]
            def run(command,**kwargs):
                commands.append(command)
                if "split-window" in command:
                    raise OSError("setup failure")
                return types.SimpleNamespace(stdout="%0\n")
            with mock.patch.dict("sys.modules",{"collector":collector}),mock.patch("launcher.shutil.which",side_effect=lambda name:"/bin/"+name),mock.patch("launcher.subprocess.run",side_effect=run):
                with self.assertRaises(OSError):
                    launcher.launch(None,[])
            self.assertEqual(commands[-1][-3:],["kill-pane","-t","%0"])
            self.assertEqual(len({command[2] for command in commands}),1)
            self.assertFalse(any("kill-server" in command for command in commands))


if __name__ == "__main__":
    unittest.main()
