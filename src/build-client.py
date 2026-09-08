#!/usr/bin/env python3
"""Build the optional POSIX display client using the installed Apple SDK."""

import os
from pathlib import Path
import subprocess
import tempfile


def build(output_dir=None):
    source = Path(__file__).resolve().with_name("client.c")
    destination = Path(output_dir) if output_dir is not None else source.parent.parent / "build"
    temporary = None
    try:
        compiler = subprocess.run(["xcrun", "--find", "clang"], capture_output=True, text=True,
                                  timeout=10, check=True).stdout.strip()
        sdk = subprocess.run(["xcrun", "--show-sdk-path"], capture_output=True, text=True,
                             timeout=10, check=True).stdout.strip()
        if not Path(compiler).is_file() or not Path(sdk).is_dir():
            return None
        destination.mkdir(parents=True, exist_ok=True)
        descriptor, temporary = tempfile.mkstemp(prefix=".codex-status-client-", dir=destination)
        os.close(descriptor)
        subprocess.run([compiler, "-std=c11", "-O2", "-Wall", "-Wextra", "-Werror", "-isysroot", sdk,
                        str(source), "-o", temporary], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                       timeout=30, check=True)
        os.chmod(temporary, 0o755)
        binary = destination / "codex-status-client"
        os.replace(temporary, binary)
        temporary = None
        return binary
    except (OSError, subprocess.SubprocessError):
        return None
    finally:
        if temporary is not None:
            try:
                os.unlink(temporary)
            except OSError:
                pass


if __name__ == "__main__":
    result = build()
    print("Native display client built." if result else "Native build unavailable; Python display remains available.")
    raise SystemExit(0 if result else 1)
