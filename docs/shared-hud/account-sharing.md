# Account sharing and session isolation

One persistent per-user manager and one owned tmux server serve all HUD sessions. Each session retains a lightweight runner, renderer and native process; central management does not remove those per-window responsibilities.

The registry can share collection only when service, authentication class, principal and billing realm are verified. Its canonical scoped keys are HMAC-derived in memory. Concurrent registry fixtures verify one worker for 100 matching scopes, isolation across accounts/services/realms and rejection after credential generation changes. Session context is always isolated.

Native verified-account merging is unavailable in this alpha. Current profile-wide authentication observations cannot establish which credentials each live process cached. Unresolved native connections stay isolated inside the same manager. Multiple profiles are not guessed to be the same account.

Codex startup quota queries are shared once per exact profile/authentication generation. Later quota observations come from sessions. API credentials, custom endpoints, configuration/profile overrides and local providers disable that default startup helper. Environment forwarding preserves each launch profile without persisting its environment.

The default optional idle retention is 30 seconds, configurable to 0–60. Native process lifetime, rather than the first panel's lifetime, controls registration. Closing one session preserves other sessions; the manager exits when no registered sessions or clients remain.

Release expansion needs an authoritative per-session identity surface before enabling cross-profile native account merging. This is an explicit incomplete portion of the intended account-level optimization, not a claim of verified native deduplication.

## 2026-10-20 checks (runtime-status)

Only the runtime-status manager is counted; a manager left under the former name serves its own earlier sessions until they exit.

- Same account, several windows: two Claude Code 2.1.304 windows in one root, and 1, 5 and 10 Codex fixture windows sharing one account home, each ran under exactly one runtime-status manager and one owned tmux server.
- Different roots: two Codex fixture windows started in two working directories with one account home joined one manager; each bound its own rollout session ID and kept its own working directory.
- Isolated data: the two concurrent Claude windows showed their own cache hit rates (51.2% and 59.7%) and context; a killed window's run was removed while the other kept updating.
- Multiple accounts and credential changes: registry fixtures `TestKeyCanonicalAndScoped`, `TestConcurrentIdentityConvergence` and `TestUnknownIsolationSwitchAndRefresh` in `internal/accounts` keep different services, principals and billing realms apart, converge concurrent same-scope launches on one worker, and isolate a session after its credential generation changes.
- Manager restart: a killed manager was restarted by the HUD view, and sessions re-registered from their run metadata without duplicate managers.
- No account polling: collection stays on session data and callbacks; no periodic account query runs.

Limit kept: account-level native merging stays unavailable, because no runtime reports which credentials a live session holds. Unverified sessions are never merged.

## Account switching

- One home per runtime: `~/.codex` and `~/.claude` keep one login each; `runtime-status account use` swaps that login, unlike Orca's separate account homes.
- Vault: each stored login is a copy kept in the macOS Keychain, service `runtime-status accounts`, on this machine only; a login longer than one `security` line is split into chunk items. `security` prints an item with a non-printable byte as hex, so reads go through `find-generic-password -g`, which marks the form.
- HUD label: the manager reads only `~/.local/state/runtime-status/accounts.json` and shows the selected account's alias, else email, for windows on `~/.codex` or `~/.claude`. The label is the selected account, not a verified per-process identity.
- Usage: each 5H/7D reading of such a window is recorded against the selected account, when it changes and at least every five minutes, and skipped while an account command holds the lock.
- Relaunch: a switch first checks that no process of that runtime outside runtime-status uses the home, then replaces each runtime-status window on it in place: a new runner and HUD under a new run ID, the old process ended, the native CLI started only once the new login is written.

### 2026-10-20 checks with Orca running

- Codex window on `~/.codex`: `account use codex yes@holyground.world --yes` rewrote `~/.codex/auth.json` to that account, relaunched the window, named a window on Orca's account home as skipped, and the relaunched HUD showed `yes@holyground.world` with 5H/7D matching `account list --json`.
- HUD click: a click on the label opened the account menu; picking the other account and confirming switched back and relaunched the window.
- Claude from an Orca terminal: `account use claude yes@holyground.world --yes` relaunched both windows on `~/.claude`, named six windows on Orca's profile home as skipped, and the relaunched HUDs showed `yes@holyground.world` with 5H/7D matching `account list --json`; `claude auth status` reported the same account.
- Five minutes later `account list` current still matched the live logins of both runtimes.
- Claude HUD click: the menu switched back to `seong9115@gmail.com`, relaunched both windows, and their 5H/7D matched that account's recorded reading.
- Windows on Orca's homes showed no label.
- Found and fixed on the way: Keychain items with a newline read back as hex; a menu whose first item was the disabled current account was read as a flag; a stale login of the account in use is now renewed by signing in to it again.

### 2026-10-20 checks of Orca windows (System default selected)

- With Orca's environment and an empty profile pointer, `runtime-status wrap claude` ran on `~/.claude` and `wrap codex` on `~/.codex` with `--no-daemon`; both HUDs showed the account and the Codex window never stopped at the background-server prompt.
- HUD clicks switched Codex to `yes@holyground.world` (5H/7D used 100%/16% against 0/84 remaining in `account list --json`) and Claude to `yes@holyground.world` (7%/15% against 93/85); `account use` switched both back.
- A Codex background server left by an earlier window held `~/.codex` and refused a switch; windows whose tmux pane is gone now have their processes ended by the switch and are named as ended.

### 2026-10-20 checks of relaunch with resume

- A Codex window with a one-line conversation, switched from the HUD menu, restarted as `codex --no-daemon resume <id>` on the new account and showed the same conversation.
- A Claude switch from the HUD menu restarted a `~/.claude` window with `--resume <id>` of its stored conversation; windows without a stored conversation, including one started from inside another Claude Code session, whose transcript saving is off, restarted without one.
- Picking an account in the HUD menu switches at once, with no second menu; the menu stays open while the mouse moves off it.
