# Claude Code adapter verification

Verified 2026-10-16 against Claude Code 2.1.301. Vendor versions label evidence; compatible field contracts determine admission.

## Implemented

- `codex-status run claude -- [native arguments]` shares the tmux host and renderer.
- Per-launch `--settings` overrides only statusLine. One explicit user settings source is merged into a private temporary file; unrelated values and the original file remain intact.
- Callback input maps model, explicit context percentages, nullable token usage and independently optional five-hour/seven-day limits.
- A private one-use environment relay preserves each window's selected profile and literal environment values, rather than inheriting the first tmux server environment. Environment contents remain in memory/transport, not files or logs.
- Single-use ordered callback tickets and a pinned native session prevent old callbacks from changing an established session. Unsupported native conversation transitions require relaunch.

## 2026-10-20 display fixes

Verified against Claude Code 2.1.304 in an isolated runtime directory with a live wrapped session.

- Effort comes from `effort.level`; the cell is omitted when the field is absent.
- Hit rate is `cache_read_input_tokens` over all input tokens of `current_usage`; both cells are omitted while usage is null or zero.
- Activity comes from launch-scoped synchronous command hooks with a 1-second timeout: `UserPromptSubmit`, `PreToolUse`, `PostToolUse` and `PostToolUseFailure` mean Working, `Stop` Complete, `StopFailure` Interrupted, `Notification` (`permission_prompt`, `idle_prompt`) Waiting. Hook input keeps only the event, notification type and session ID; no transcript is read. A foreign session's hook is rejected.
- An explicit `--settings` source keeps its own hooks; the launcher appends its entries. Hooks of the user's own settings files keep firing beside them (live `SessionStart` user hook observed).
- The statusLine sets `refreshInterval` 15, so idle sessions stay inside the 180-second stale limit. One real callback measured about 17 ms wall and 6 ms CPU; ten idle sessions add about 40 callbacks and 0.24 CPU seconds per minute.
- The directory label uses `workspace.project_dir`, then `current_dir`, then `cwd`, matching the manager's branch and file links. Quota labels link to `https://claude.ai/settings/usage`.
- Live capture: Working during a tool call, then Complete, then Waiting after the idle prompt; `Cached 52k Hit rate 99.7%`, context, 5H and 7D rows present.
- Limit: a user interrupt fires no hook, so Working remains until the next event. A running manager without the `hook-activity` capability ignores hook events until it is replaced.

## Acceptance

- Actual native startup with per-launch settings, empty setting sources and strict empty MCP produced one accepted candidate frame with zero rejections. No model prompt was submitted in that Claude probe; context and quotas were absent and remained pending.
- Synthetic fixtures cover nonzero and zero percentages, null usage after compaction, absent/changing quotas, required identity failures, future additive fields and terminal sanitization.
- Multi-window relay tests and an isolated native-fixture benchmark verified distinct profiles and model markers for 2/5 windows, one manager/server, and preservation of other sessions after one closes.
- Codex compatibility investigation during this stage additionally found the ChatGPT bundle launcher re-execs a different kernel binary. New launch receipts record the actual kernel executable without weakening subsequent binding. A read-only in-memory diagnostic receipt recovered context and weekly quota from all four currently inspected live Codex roots; installed metadata/binary were unchanged.

## Limits

Live Claude model-turn quota delivery, compaction and native conversation switching are not accepted by the startup probe. The account identity resolver remains unresolved because profile-wide authentication status cannot prove which credentials a live session cached. Context stays per session and native connections remain isolated inside one manager.
