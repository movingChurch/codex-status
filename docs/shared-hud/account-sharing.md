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

Limit kept: account-level native merging stays unavailable, because no runtime reports which credentials a live session holds. Unverified sessions are never merged. Account switching by separate account homes, as Orca does, would give an identity source and is tracked as a separate request.
