# Changelog

## Unreleased (runtime-status)

- Rename the project from codex-status to runtime-status: command, Go module, `RUNTIME_STATUS_` environment variables, `/tmp/runtime-status-<uid>` runtime directory and `~/.local/state/runtime-status` state directory.
- Add `runtime-status install` and `uninstall` with zsh functions for `codex`, `claude` and `agy`, and `runtime-status wrap <runtime>`.
- Migrate codex-status installs: matching old `codex-status` and `claude-hud` zsh blocks and executables are backed up and removed, the Antigravity callback moves to the new executable, and files in use by running sessions are kept until a later install.
- Show Claude effort, cache hit rate, hook-driven activity and the Claude usage link.

## 0.2.0-alpha.1

- Replace the Python launcher, collector, and display stack with one Go executable.
- Build macOS arm64 and amd64 archives with checksums; runtime installation needs no language toolchain.
- Capture mouse-wheel input as tmux scroll history and hide the default tmux status bar.
- Preserve session binding, bounded telemetry, startup quota, display links, and owned-pane cleanup.
- Use a separate Go collector runtime so existing Python sessions can finish normally.


## Unreleased

- Add clickable folders, Git branches, quota reset times, and session logs

- Read startup quota through the official Codex account API with shared caching
- Distinguish first-message readiness from a connected session
- Match Claude-style usage sliders, reset countdowns, and observed cache metrics
- Keep the HUD compact when a terminal attaches or resizes


## v0.1.0-alpha.2 — 2026-09-08

- English-only HUD labels, help, and startup errors
- Product preview from the actual renderer with synthetic data
- Reader-focused README with quick start and creator resources

Session collection, quota sources, and supported Codex version remain unchanged.

## v0.1.0-alpha.1 — 2026-09-08

Initial experimental source release for macOS and Codex CLI 0.153.4.

- Shared collector with incremental reads and exact root-session binding
- Three-row Korean HUD in dedicated tmux sessions
- Optional native socket display client and Python fallback
- Isolated installer and synthetic lifecycle tests

Known limits: narrow version support, last-observed quota, no custom widgets, no Linux/Windows support, and no automatic update mechanism.
