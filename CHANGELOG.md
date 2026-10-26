# Changelog

## Unreleased

- Resume each Claude Code window's conversation after an account switch from the session file Claude Code keeps per process, so windows without HUD rows and windows moved to another conversation with `/resume` or `/clear` no longer restart empty; the manager's snapshot and the launch arguments remain the fallbacks.
- Keep a switch log, `switch.log` in the runtime directory, naming the windows a switch relaunched, where each conversation was read and how the switch ended, with no e-mail address; a switch started from inside a window it relaunches writes its output there instead of the caller's pipes.
- Leave the HUD rows out of Claude Code windows with `RUNTIME_STATUS_CLAUDE_HUD=off`, for a Claude Code statusline that shows the session itself: the window keeps its tmux session, run metadata, account switch relaunch and session cleanup, gets no statusLine override or activity hooks, and relaunches after a switch in the form it was opened with. Codex and Antigravity windows are unchanged.
- End a HUD tmux session when its last client goes away again: the HUD pane's resize hook had replaced the hook that turns this on, so closed windows left detached sessions running.

## 0.3.0-alpha.1

- Keep several Codex and Claude Code accounts with `runtime-status account add|list|use|rename|remove`; logins are stored in the macOS Keychain of this machine only, and `account list --json` shows each account's usage and which one is current.
- Switch accounts from the HUD: a click on the account name opens a menu of that runtime's accounts, and a pick switches at once.
- Restart the runtime's open windows after a switch, each resuming the conversation it was running.
- Support Orca terminal windows on the default `~/.codex` and `~/.claude` homes.
- End a HUD tmux session when its last client goes away, and skip such windowless sessions when an account switch relaunches windows.
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
