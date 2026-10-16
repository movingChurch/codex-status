# Claude Code adapter verification

Verified 2026-10-16 against Claude Code 2.1.301. Vendor versions label evidence; compatible field contracts determine admission.

## Implemented

- `codex-status run claude -- [native arguments]` shares the tmux host and renderer.
- Per-launch `--settings` overrides only statusLine. One explicit user settings source is merged into a private temporary file; unrelated values and the original file remain intact.
- Callback input maps model, explicit context percentages, nullable token usage and independently optional five-hour/seven-day limits.
- A private one-use environment relay preserves each window's selected profile and literal environment values, rather than inheriting the first tmux server environment. Environment contents remain in memory/transport, not files or logs.
- Single-use ordered callback tickets and a pinned native session prevent old callbacks from changing an established session. Unsupported native conversation transitions require relaunch.

## Acceptance

- Actual native startup with per-launch settings, empty setting sources and strict empty MCP produced one accepted candidate frame with zero rejections. No model prompt was submitted in that Claude probe; context and quotas were absent and remained pending.
- Synthetic fixtures cover nonzero and zero percentages, null usage after compaction, absent/changing quotas, required identity failures, future additive fields and terminal sanitization.
- Multi-window relay tests and an isolated native-fixture benchmark verified distinct profiles and model markers for 2/5 windows, one manager/server, and preservation of other sessions after one closes.
- Codex compatibility investigation during this stage additionally found the ChatGPT bundle launcher re-execs a different kernel binary. New launch receipts record the actual kernel executable without weakening subsequent binding. A read-only in-memory diagnostic receipt recovered context and weekly quota from all four currently inspected live Codex roots; installed metadata/binary were unchanged.

## Limits

Live Claude model-turn quota delivery, compaction and native conversation switching are not accepted by the startup probe. The account identity resolver remains unresolved because profile-wide authentication status cannot prove which credentials a live session cached. Context stays per session and native connections remain isolated inside one manager.
