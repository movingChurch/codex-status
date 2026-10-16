# Antigravity adapter verification

Verified 2026-10-16. Admission follows callback fields and session identity rather than a vendor version.

- Explicit `setup antigravity` installs the managed statusLine callback; ordinary launch requires that setup.
- Empty startup conversation/session IDs remain pending. The first authoritative ID pins the connection; subsequent unsupported conversation changes require relaunch.
- Context uses provider percentages. Quota pools map remaining fractions into used percentages and preserve independent reset times.
- Actual native probing observed pending startup followed by six tickets, one accepted frame, zero remaining pending/rejected runs, and one context/quota observation after one minimal prompt launch in plan mode. Model response and billing cost were not instrumented.
- Setup/uninstall restored the original absent selected key. Sparse native serialization of padding/stack defaults is recognized only for the exact managed command; unrelated fields survive restoration.
- Synthetic tests cover malformed input, missing identity, callback ordering, session replacement, optional data, bounded processing and settings conflict/crash recovery.

Global callback setup is the supported vendor surface. Unwrapped sessions forward a previous custom callback when present. Native built-in fallback presentation, live compaction and billing accuracy remain unverified. Account identity limits are recorded in [account-sharing.md](account-sharing.md).
