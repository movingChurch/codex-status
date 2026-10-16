# Shared HUD core implementation

Implementation record for `flow:shared-cli-hud#REQ-01`, `REQ-02`, `REQ-05`, `REQ-06` and `REQ-07`, verified 2026-10-16.

## Behavior

- Codex binding and incremental reader admit bounded compatible root contracts independently of vendor version strings; process start, executable, inode, unique root and exact session checks remain enforced.
- Known binding errors have explicit reasons; startup waiting and a measured zero remain distinct.
- Capacity rows include only observed quota windows or pools, with one aggregate pending state when no quota is observed.
- One per-user manager endpoint serves all supported runtime views. Required IPC capabilities and kernel peer PID/start identity are verified before native launch.
- One owned tmux socket hosts independent sessions. The launcher replaces itself with the tmux client after setup; runner and view transports retain their independent terminal responsibilities.
- Callback tickets precede stdin parsing, expire after 250 ms, are single-use and ordered, and bind to run/auth generation/account scope. Ordinary callbacks preserve established session identity across auth refreshes.
- Complete native callback snapshots replace session observations. Shared quota stores are separate from per-session context.
- Codex startup quota is queried once per exact profile/auth generation; subsequent updates come from native session observations. Unresolved profiles do not start periodic polling loops or merge across roots.

## Identity limits

The account registry uses canonical HMAC keys over service, auth class, principal and billing realm. Synthetic tests cover convergence, isolation, refresh, switches, late replies and idle release. Current native contracts do not tie that complete identity to each live process; the native resolver stays unresolved rather than guessing from email or directories. Central process sharing works independently of verified account merging. Native verified-account merging remains unavailable in this candidate.

## Verification

- Race tests include compatible old/current/unknown versions, malformed identity, dynamic quota windows, valid zero, callback order/reuse/expiry/restart, account switches and native-registration lifetime.
- IPC integration proves callback data reaches a shared view, last-view departure retains a live native registration, and final unregister permits manager shutdown.
- Independent targeted recheck resolved eight initial Major findings; it accepted the documented narrow external-editor race inherent in portable rename.
- Synthetic callback measurement: 30 processes, p95 6.68 ms, peak RSS 7,438,336 bytes; stalled stdin exits in 254.99 ms including process overhead.
- Workspace evidence: `flow/shared-cli-hud/evidence/20261016183001-shared-hud-core-results.md`.

Native adapter acceptance and matched multi-window measurements belong to the subsequent stage records. This record does not authorize public compatibility claims.
