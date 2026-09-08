# Changelog

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
