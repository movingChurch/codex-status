# Codex Status

A shared terminal HUD for people running multiple Codex sessions.

**Experimental: macOS, Python 3.14, and stock Codex CLI 0.153.4.** Other Codex versions remain pending rather than displaying unverified data. HUD labels are currently Korean; commands and this README are English. This is an independent project, not an OpenAI product.

## What it does

Codex Status opens Codex in a dedicated tmux session with a three-row panel underneath. A single local collector serves the panels. The original `codex` command stays unchanged.

- Model, reasoning effort, working directory, and Git branch
- Context remaining and the last observed general Codex quota
- Observed task state, elapsed time, and freshness
- Shared repository HEAD reads without running Git commands
- Incremental session reads; hidden panels stop collecting records
- Collector shutdown when the last panel disconnects

Bars show **used** capacity; numbers show **remaining** capacity. Missing values stay `—`. Quota is the last observation from that exact session, not a fresh account-wide API lookup.

## Install

Requirements: macOS, Python 3.14 available as `python3`, tmux on PATH, and a working official Codex CLI 0.153.4. Apple's Command Line Tools provide `xcrun` and clang for the small native display client. Without that build, the Python fallback works but the native performance measurements do not apply.

```sh
git clone --branch v0.1.0-alpha.1 https://github.com/movingChurch/codex-status.git
cd codex-status
python3 install.py
```

Keep the checkout: the installed command points into it. Ensure `~/.local/bin` is on PATH, then run from your intended working directory and account environment:

```sh
codex-status
codex-status --resume SESSION-UUID
codex-status -- --model MODEL
```

You can also run `python3 src/codex-status.py` from the checkout. Existing Codex sessions do not automatically gain a HUD.

## Close and resume

- Detach: tmux prefix `Ctrl-b`, then `d`
- Close only the HUD: select the lower pane with `Ctrl-b`, then Down; use `Ctrl-b`, then `x`
- Exit Codex normally: its paired HUD closes; the final HUD disconnect stops the collector

Each launch has a separate tmux socket under `/tmp/codex-status-UID/`. To reattach a detached run, replace the placeholders with that run's socket:

```sh
tmux -S /tmp/codex-status-UID/tmux-RUN-UUID.sock attach
```

## Uninstall and update

From the same checkout:

```sh
python3 install.py --uninstall
```

Close your HUD panels before removing the checkout. Do not delete sockets belonging to running sessions. For an update, close the HUDs, fetch the desired release tag, check out that tag, and rerun the installer. The installer must not overwrite unrelated commands.

## Design

```text
Exact Codex PID + start identity + account home + root UUID
                         |
                  Shared collector  <--- repository HEAD
                         |
                    Unix socket
                         |
                Small C display clients
                         |
                  Three-row tmux HUDs
```

On macOS, the collector uses libproc instead of repeatedly spawning ps or lsof. It checks pane visibility and session binding every five seconds. Incremental record reads have a 256 KiB per-call budget. The collector renders frames only when the data or terminal width changes. The optional C client waits for those frames on a private Unix socket.

## Limits and privacy

The collector reads selected metadata from local Codex rollout files. It makes no independent API calls and does not read credential contents. The Codex process you launch still uses its normal network and authentication behavior.

Run metadata excludes prompts, tool arguments, and environment contents. Files and sockets live in a user-private runtime directory. Local processes running as the same user remain within the same trust boundary.

Session binding checks the exact process, process start identity, home, root UUID, and supported CLI version. Ambiguous or unsupported bindings remain pending. A fresh session may not create its rollout until the first prompt. Account-file stat changes invalidate quota observations; this does not prove account identity or detect every keychain-only change.

This alpha omits model-specific quota buckets, billing estimates, custom widgets, aggregate agent progress, indirect resume selectors, and fork entry commands. The panel is separate from the native Codex footer. This alpha targets macOS; Linux and Windows remain outside its support scope.

## Tests and benchmark

```sh
python3 src/build-client.py
cd src
python3 -m unittest discover -s tests -v
python3 benchmark.py --sessions 36 --interval 5 --output ../benchmark-result.json
```

The original native implementation measured **1.89–2.48% of one CPU core** across three visible samples with 36 synthetic sessions, and 0.31% while hidden. This used fixed records and real tmux panels, clients, and a collector. It did not run 36 real model requests. These are development measurements, not a comparison against competing products or a guarantee for every machine.

The measured RSS was 98,976–99,008 KiB for the display clients, collector, tmux, and fixture waiting pane. It excludes Codex and launcher waiting processes. No raw personal session data or internal development logs ship with this repository.

The automated tests cover binding, record parsing, terminal sanitization, lifecycle, IPC, native display, and installation. Benchmark execution uses only owned temporary fixtures; it makes no model requests.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Include your OS, Python, tmux, and Codex versions when reporting a problem. Do not attach raw session logs or authentication files. See [NOTICE](NOTICE) for upstream references and licensing details.

## License

Project code uses Apache-2.0 except `src/darwin_abi.py`, which retains APSL-2.0 for the adapted Apple SDK declarations. The source distribution includes both license texts and modification notices. See [NOTICE](NOTICE) and [LICENSES](LICENSES/).
