# Compatibility evidence

Evidence dates: 2026-10-16 and 2026-10-20. Versions identify observations, never admission rules. Unknown additive fields are tolerated; required identity and supported metric contracts remain checked.

| Runtime | Native evidence | Synthetic coverage | Remaining limits |
| --- | --- | --- | --- |
| Codex | Compatible live rollout context and weekly quota recovered from four roots using read-only in-memory diagnostic receipts | Launch/resume, process identity, executable re-exec, root ambiguity, quota windows, bounded reads | Installed old metadata was unchanged; restart with candidate required. Native account identity unresolved |
| Claude Code 2.1.301–2.1.304 | Startup callback accepted with no rejections; 2.1.304 model turns deliver context, quota, effort and cache (live acceptance below) | Percentages including zero, nullable usage, quota omission, future fields, ordering and session pinning | Live compaction unverified; a user interrupt fires no hook |
| Antigravity CLI 1.4.2 | Pending-to-session callback with context and quota accepted; restoration on the owner settings passed (live acceptance below) | Empty startup IDs, quota pools, malformed input, settings recovery and session replacement | Billing accuracy, live compaction and built-in fallback presentation unverified |

All runtimes share the tmux interface and one manager. A native conversation replacement without an authoritative transition signal requires wrapper relaunch. Unsupported metrics remain pending/unavailable or are omitted; absent five-hour quota is never fabricated.

## 2026-10-20 live acceptance (runtime-status)

Installed `runtime-status` 0.3.0-alpha.1 (commit 40fc900) launched through the zsh functions of `runtime-status install`, macOS arm64, Claude Code 2.1.304 and Antigravity CLI 1.4.2.

| Check | Claude Code 2.1.304 | Antigravity CLI 1.4.2 |
| --- | --- | --- |
| Payload mapping after a model turn | Model, effort, context, 5H and 7D quota, cache and hit rate shown | Model, context, weekly quota pools, cache shown |
| Installation and restoration | Per-launch settings only; nothing persistent to restore | `uninstall antigravity` removed only `statusLine` from the owner settings, keeping every other key; `install` re-applied it |
| Session identity | Two concurrent windows showed their own hit rates (51.2% and 59.7%) through one manager | Pending-to-session callback accepted after the first prompt |
| Cancellation | Esc during a streamed turn: Claude fires no hook on a user interrupt, so the row keeps Working until the next event (stated limit) | Esc during a streamed turn moved the row from Working to Waiting |
| Native exit | `/exit` closed the HUD pane and removed the run | Ctrl-C twice closed the HUD pane and removed the run |
| Manager killed during a session | HUD reconnected to a restarted manager within 6 seconds; data returned on the next 15-second refresh | HUD reconnected within 6 seconds; data returned on the next prompt |
| One native killed (`kill -9`) | Its HUD session closed and its run was removed; the other window kept updating | Shares the manager path checked with Claude |

The manager-restart check found that the HUD pane closed for good when its manager died; commit 40fc900 makes the view restart the manager and reconnect with backoff while the native process lives, and stop on a signal or once the run is gone.

macOS arm64 is executed in verification; macOS amd64 is cross-built. Linux and Windows releases are outside this alpha. Exact evidence and limits: [Claude](claude-adapter.md), [Antigravity](antigravity-adapter.md), [account sharing](account-sharing.md), [release readiness](release-readiness.md).
