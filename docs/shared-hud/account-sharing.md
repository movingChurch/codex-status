# Account sharing and session isolation

One persistent per-user manager and one owned tmux server serve all HUD sessions. Each session retains a lightweight runner, renderer and native process; central management does not remove those per-window responsibilities.

The registry can share collection only when service, authentication class, principal and billing realm are verified. Its canonical scoped keys are HMAC-derived in memory. Concurrent registry fixtures verify one worker for 100 matching scopes, isolation across accounts/services/realms and rejection after credential generation changes. Session context is always isolated.

Native verified-account merging is unavailable in this alpha. Current profile-wide authentication observations cannot establish which credentials each live process cached. Unresolved native connections stay isolated inside the same manager. Multiple profiles are not guessed to be the same account.

Codex startup quota queries are shared once per exact profile/authentication generation. Later quota observations come from sessions. API credentials, custom endpoints, configuration/profile overrides and local providers disable that default startup helper. Environment forwarding preserves each launch profile without persisting its environment.

The default optional idle retention is 30 seconds, configurable to 0–60. Native process lifetime, rather than the first panel's lifetime, controls registration. Closing one session preserves other sessions; the manager exits when no registered sessions or clients remain.

Release expansion needs an authoritative per-session identity surface before enabling cross-profile native account merging. This is an explicit incomplete portion of the intended account-level optimization, not a claim of verified native deduplication.
