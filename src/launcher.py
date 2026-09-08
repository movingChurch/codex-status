"""Opt-in launcher for a dedicated Codex and HUD tmux pair."""

import argparse
import os
from pathlib import Path
import re
import shlex
import shutil
import signal
import subprocess
import sys
import time
import uuid


def parse_launch(argv):
    argv = list(argv)
    resume = None
    if argv[:1] == ["--resume"]:
        if len(argv) < 2:
            raise ValueError("resume requires an explicit UUID")
        try:
            resume = str(uuid.UUID(argv[1]))
        except (ValueError, AttributeError):
            raise ValueError("resume requires an explicit UUID") from None
        argv = argv[2:]
    if argv and argv[0] != "--":
        raise ValueError("Codex arguments must follow --")
    args = argv[1:] if argv else []
    # These entry paths change the selected root or do not create a CLI session.
    commands = {"resume", "fork", "exec", "e", "review", "login", "logout", "mcp", "mcp-server", "app-server", "completion", "sandbox", "debug", "apply", "cloud", "features"}
    if args and args[0] in commands:
        raise ValueError("use a fresh session or --resume UUID")
    if any(arg in {"--last", "--all", "--cd", "-C"} or arg.startswith("--cd=") for arg in args):
        raise ValueError("indirect session and working directory selection are unsupported")
    return resume, args


def cleanup_pair(meta, tmux="tmux"):
    try:
        run = str(uuid.UUID(meta["id"]))
        socket_path = Path(meta["tmux_socket"])
        hud = meta["hud_pane"]
        codex = meta["codex_pane"]
        if socket_path.name != f"tmux-{run}.sock" or hud == codex:
            return
        if not re.fullmatch(r"%\d+", hud) or not re.fullmatch(r"%\d+", codex):
            return
    except (KeyError, TypeError, ValueError, AttributeError):
        return
    try:
        subprocess.run([tmux, "-S", str(socket_path), "kill-pane", "-t", hud],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=3, check=False)
    except (OSError, subprocess.SubprocessError):
        pass


def build_runner_command(entry, runtime, run_id, socket_path, codex, resume, args):
    words = [sys.executable, str(entry), "__runner", "--runtime", str(runtime), "--run", run_id,
             "--socket", str(socket_path), "--codex", str(codex)]
    if resume:
        words += ["--resume", resume]
    return shlex.join(words + ["--"] + list(args))


class QuietParser(argparse.ArgumentParser):
    def error(self, _message):
        raise ValueError("invalid internal arguments")


def internal_options(argv):
    parser = QuietParser(add_help=False)
    for name in ("runtime", "run", "socket", "codex"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--resume")
    split = argv.index("--") if "--" in argv else len(argv)
    options = parser.parse_args(argv[:split])
    options.args = argv[split + 1:] if split < len(argv) else []
    options.run = str(uuid.UUID(options.run))
    if options.resume:
        options.resume = str(uuid.UUID(options.resume))
    return options


def run_codex(options):
    """Own the launched child; its ordinary exit closes only the paired HUD."""
    import binding
    import collector

    runtime = collector.validate_runtime(options.runtime)
    socket_path = runtime / f"tmux-{options.run}.sock"
    if Path(options.socket) != socket_path:
        raise ValueError("dedicated socket mismatch")
    pane = os.environ.get("TMUX_PANE", "")
    if not re.fullmatch(r"%\d+", pane):
        raise ValueError("runner requires its owned tmux pane")
    tmux = shutil.which("tmux")
    if not tmux:
        raise ValueError("tmux unavailable")
    base = [tmux, "-S", str(socket_path)]
    subprocess.run(base + ["wait-for", "ready-" + options.run], timeout=15, check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    paired = subprocess.run(base + ["show-options", "-wqv", "-t", pane, "@codex_status_hud"],
                            capture_output=True, text=True, check=True, timeout=3).stdout.strip()
    if not re.fullmatch(r"%\d+", paired) or pane == paired:
        raise ValueError("paired HUD identity unavailable")
    meta = {"v": 1, "id": options.run, "home": str(Path(os.environ.get("CODEX_HOME") or Path.home() / ".codex").resolve()),
            "cwd": str(Path.cwd().resolve()), "tmux_socket": str(socket_path), "codex_pane": pane,
            "hud_pane": paired, "expected_session": options.resume, "executable": str(Path(options.codex).resolve()),
            "launched_at": time.time()}
    command = [meta["executable"]]
    if options.resume:
        command += ["resume", options.resume]
    command += options.args
    meta["auth_epoch"] = binding.auth_epoch(Path(meta["home"]))
    child = subprocess.Popen(command)
    # Terminal signals reach the foreground process group, including Codex.
    # Keeping the runner alive prevents its exit from destroying that pane.
    previous = {}
    for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        previous[sig] = signal.signal(sig, lambda _sig, _frame: None)
    metadata_path = None
    try:
        try:
            started = binding.process_identity(child.pid)
            if started is not None:
                meta.update(pid=child.pid, started=started)
                metadata_path = collector.write_run_metadata(runtime, meta)
            else:
                cleanup_pair(meta, tmux)
        except Exception:
            # Even a metadata or identity bug must not end the live Codex pane.
            # The child stays attached and the runner waits for its own exit.
            cleanup_pair(meta, tmux)
        return child.wait()
    finally:
        for sig, handler in previous.items():
            signal.signal(sig, handler)
        try:
            cleanup_pair(meta, tmux)
        except (OSError, subprocess.SubprocessError):
            pass
        if metadata_path is not None:
            try:
                metadata_path.unlink()
            except OSError:
                pass


def launch(resume, args):
    import collector

    runtime = collector.validate_runtime()
    codex = shutil.which("codex")
    tmux = shutil.which("tmux")
    if not codex or not tmux:
        raise ValueError("Codex and tmux must be installed")
    codex = str(Path(codex).resolve())
    entry = Path(__file__).resolve().with_name("codex-status.py")
    if Path(codex) == entry:
        raise ValueError("Codex entry cannot point at codex-status")
    run = str(uuid.uuid4())
    socket_path = runtime / f"tmux-{run}.sock"
    if len(os.fsencode(socket_path)) >= 104:
        raise ValueError("runtime socket path is too long")
    base = [tmux, "-S", str(socket_path)]
    cwd = str(Path.cwd())
    command = build_runner_command(entry, runtime, run, socket_path, codex, resume, args)
    # A new server inherits this invocation's complete account environment.
    created = subprocess.run(base + ["-f", "/dev/null", "new-session", "-d", "-s", "codex", "-c", cwd,
                                     "-P", "-F", "#{pane_id}", command], capture_output=True, text=True, timeout=10, check=True)
    codex_pane = created.stdout.strip()
    hud_pane = None
    signaled = False
    try:
        if not re.fullmatch(r"%\d+", codex_pane):
            raise ValueError("Codex pane identity unavailable")
        renderer_command = shlex.join([sys.executable, str(entry), "__render", str(runtime), run])
        result = subprocess.run(base + ["split-window", "-v", "-d", "-l", "4", "-t", codex_pane,
                                       "-P", "-F", "#{pane_id}", renderer_command], capture_output=True, text=True, timeout=5, check=True)
        hud_pane = result.stdout.strip()
        if not re.fullmatch(r"%\d+", hud_pane) or hud_pane == codex_pane:
            raise ValueError("HUD pane identity unavailable")
        subprocess.run(base + ["set-option", "-w", "-t", codex_pane, "@codex_status_hud", hud_pane],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=3, check=True)
        subprocess.run(base + ["select-pane", "-t", codex_pane], stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL, timeout=3, check=True)
        subprocess.run(base + ["wait-for", "-S", "ready-" + run], stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL, timeout=3, check=True)
        signaled = True
        attach_environment = dict(os.environ)
        attach_environment.pop("TMUX", None)
        return subprocess.call(base + ["attach-session", "-t", "codex"], env=attach_environment)
    finally:
        if not signaled:
            # Before the ready signal the runner has not launched Codex.
            if hud_pane:
                cleanup_pair({"id": run, "tmux_socket": str(socket_path), "codex_pane": codex_pane, "hud_pane": hud_pane}, tmux)
            if re.fullmatch(r"%\d+", codex_pane):
                subprocess.run(base + ["kill-pane", "-t", codex_pane], stdout=subprocess.DEVNULL,
                               stderr=subprocess.DEVNULL, timeout=3, check=False)


def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    try:
        if argv[:1] == ["__runner"]:
            return run_codex(internal_options(argv[1:]))
        if argv[:1] == ["__render"]:
            if len(argv) != 3:
                raise ValueError("invalid render arguments")
            import collector
            runtime = collector.validate_runtime(argv[1])
            run_id = str(uuid.UUID(argv[2]))
            deadline = time.monotonic() + 12
            while not (runtime / "runs" / (run_id + ".json")).is_file():
                if time.monotonic() >= deadline:
                    return 2
                time.sleep(0.05)
            bootstrap = collector.ensure_collector(runtime)
            bootstrap.close()
            binary = Path(__file__).resolve().parent.parent / "build" / "codex-status-client"
            if binary.is_file() and os.access(binary, os.X_OK):
                try:
                    os.execv(str(binary), [str(binary), str(runtime / "collector.sock"), run_id])
                except OSError:
                    pass
            from renderer import run_renderer
            return run_renderer(runtime, run_id)
        if argv in (["--help"], ["-h"]):
            print("Usage: codex-status [--resume UUID] [-- Codex arguments]\nLaunch this session in a dedicated tmux window with a HUD.")
            return 0
        return launch(*parse_launch(argv))
    except KeyboardInterrupt:
        return 130
    except (ValueError, OSError, RuntimeError, subprocess.SubprocessError):
        # Never echo argv, environment values or subprocess exception payloads.
        print("codex-status: could not start. Check the launch requirements and dedicated tmux window.", file=sys.stderr)
        return 2
