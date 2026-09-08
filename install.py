#!/usr/bin/env python3
"""Install one checkout-owned entry without changing the default Codex."""

import argparse
import importlib.util
import os
from pathlib import Path
import shutil
import subprocess
import sys


def preflight():
    if sys.platform != "darwin":
        raise ValueError("This experimental release supports macOS only.")
    if sys.version_info[:2] < (3, 14):
        raise ValueError("Python 3.14 or newer is required.")
    for command in ("python3", "tmux", "codex"):
        if shutil.which(command) is None:
            raise ValueError(f"Required command is unavailable on PATH: {command}")
    # The installed entry has an env-python3 shebang, so check that interpreter
    # separately from the Python used to invoke this installer.
    try:
        result = subprocess.run([shutil.which("python3"), "-c", "import sys; print('%d.%d' % sys.version_info[:2])"],
                                capture_output=True, text=True, timeout=5, check=True)
        version = tuple(int(part) for part in result.stdout.strip().split("."))
    except (ValueError, OSError, subprocess.SubprocessError):
        raise ValueError("Could not verify python3 on PATH.") from None
    if len(version) != 2 or version < (3, 14):
        raise ValueError("python3 on PATH must be Python 3.14 or newer.")


def build_native(repository):
    try:
        path = Path(repository) / "src" / "build-client.py"
        spec = importlib.util.spec_from_file_location("codex_status_native_builder", path)
        if spec is None or spec.loader is None:
            return False
        builder = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(builder)
        return builder.build() is not None
    except (OSError, ImportError):
        return False


def paths(repository, bin_dir):
    repository = Path(repository).expanduser().resolve()
    entry = repository / "src" / "codex-status.py"
    link = Path(bin_dir).expanduser().absolute() / "codex-status"
    return repository, entry, link


def owned_link(link, entry):
    return link.is_symlink() and os.readlink(link) == str(entry)


def install(repository, bin_dir, native=True):
    repository, entry, link = paths(repository, bin_dir)
    if os.path.lexists(link) and not owned_link(link, entry):
        raise ValueError("codex-status already exists and belongs to another installation; it was preserved.")
    preflight()
    if not entry.is_file() or not os.access(entry, os.X_OK):
        raise ValueError("This checkout is missing its executable src/codex-status.py entry.")
    native_ready = build_native(repository) if native else False
    link.parent.mkdir(parents=True, exist_ok=True)
    try:
        link.symlink_to(entry)
    except FileExistsError:
        if not owned_link(link, entry):
            raise ValueError("codex-status changed during installation; the existing entry was preserved.") from None
    return link, native_ready


def uninstall(repository, bin_dir):
    _, entry, link = paths(repository, bin_dir)
    if not os.path.lexists(link):
        return False
    if not owned_link(link, entry):
        raise ValueError("codex-status does not point to this checkout; it was preserved.")
    link.unlink()
    return True


def main(argv=None, repository=None):
    parser = argparse.ArgumentParser(description="Install or remove this checkout's codex-status symlink.")
    parser.add_argument("--bin-dir", type=Path, default=Path.home() / ".local" / "bin",
                        help="entry directory (default: ~/.local/bin)")
    parser.add_argument("--uninstall", action="store_true", help="remove only this checkout's exact symlink")
    parser.add_argument("--skip-native", action="store_true", help="skip the optional native client build")
    args = parser.parse_args(argv)
    repository = Path(__file__).resolve().parent if repository is None else Path(repository)
    try:
        if args.uninstall:
            removed = uninstall(repository, args.bin_dir)
            print("Removed this checkout's codex-status entry." if removed else "No codex-status entry to remove.")
        else:
            link, native_ready = install(repository, args.bin_dir, native=not args.skip_native)
            print(f"Installed: {link}")
            if args.skip_native:
                print("Native build skipped.")
            elif native_ready:
                print("Native display client built.")
            else:
                print("Native build unavailable; Python display remains available.")
            print("Keep this source checkout and its build directory in place.")
        return 0
    except ValueError as error:
        print(f"Installation stopped: {error}", file=sys.stderr)
        return 2
    except OSError:
        print("Installation stopped: could not update the selected entry directory.", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
