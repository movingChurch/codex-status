# 0.3.0-alpha.1 release notes

- One tmux HUD for Codex, Claude Code and Antigravity CLI, with one shared manager and owned tmux server.
- Adaptive context and observed quota windows/pools; missing five-hour data stays absent. Compatible telemetry no longer requires an exact Codex version.
- Codex launcher re-exec receipt handling fixes an additional source of context binding failures on new launches.
- Private per-launch environment forwarding preserves multiple profiles without storing environment values.
- Bounded native callbacks, ordered session-pinned updates and reversible explicit Antigravity setup.
- Renamed to runtime-status; `runtime-status install` sets up `codex`, `claude` and `agy` zsh functions and the Antigravity callback, migrates the former codex-status install, and `uninstall` reverses it.
- Claude rows show effort, cache hit rate and hook-driven Working/Complete/Waiting with a 15-second refresh and the Claude usage link.
- A HUD view restarts a lost manager and reconnects while its native session runs.
- Shared tmux hosting reduces persistent process count and synthetic multi-session RSS; [measurements and limits](release-readiness.md).

Early alpha: macOS only; native verified-account merging is unavailable. Live compaction, billing accuracy and broad native-version coverage remain unverified. Finish v0.2 HUD sessions before launching v0.3. A user interrupt in Claude Code fires no hook, so its row stays Working until the next event. See [compatibility](compatibility.md).
