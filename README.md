# Codex Status

**Your model, context, and quota. Right below your Codex session.**

A three-row HUD for people running multiple Codex terminals.

![Codex Status HUD with model, context, quota, and task state](assets/hud-preview.png)

*Actual HUD renderer, captured in a terminal emulator with synthetic example data. These are not live account values or a live Codex conversation.*

- See your model, context, and last observed quota without leaving the terminal.
- Share one collector across panels, with record collection paused for hidden panels.
- Add a HUD to the sessions you choose. Keep your usual `codex` command.

**Early alpha:** macOS, Python 3.14, stock Codex CLI 0.153.4. Other Codex versions remain pending. This is an independent project, not an OpenAI product.


## Quick start

You need macOS, Python 3.14 available as `python3`, `tmux` on PATH, and a working official Codex CLI 0.153.4.

```sh
git clone --branch v0.1.0-alpha.2 https://github.com/movingChurch/codex-status.git
cd codex-status
python3 install.py
```

Keep the checkout and its build directory: the installed command points into them. Add `~/.local/bin` to PATH if needed. From your project directory, with your intended Codex account environment:

```sh
codex-status
```

Apple's Command Line Tools supply `xcrun` and clang for the small native display client. If that optional build is unavailable, the Python display works; the native performance measurements below do not apply to that fallback.

<details>
<summary>Custom install directory and direct execution</summary>

```sh
python3 install.py --bin-dir /your/bin
python3 install.py --skip-native
python3 src/codex-status.py
```

The installer preserves unrelated files and symlinks. It does not modify the default `codex` command or add panels to already running sessions.

</details>


## At a glance

| Row | Information |
| --- | --- |
| Session | Model, reasoning effort, working directory, Git branch |
| Capacity | Context remaining, last observed general Codex quota |
| Activity | Observed task state, elapsed time, data freshness |

Bars show **used** capacity; numbers show **remaining** capacity. Missing values stay `—`. Quota comes from that exact session's last observation, so it records what the session last reported. A new session may show pending until its first prompt creates a rollout file.


## Everyday use

Resume a specific session or pass Codex options after `--`:

```sh
codex-status --resume SESSION-UUID
codex-status -- --model MODEL
```

- **Detach:** press `Ctrl-b`, then `d`.
- **Close just the HUD:** select the lower pane with `Ctrl-b`, then Down; press `Ctrl-b`, then `x`.
- **Finish your session:** exit Codex normally. Its paired HUD closes. The collector stops after the last HUD disconnects.

Each launch uses a dedicated tmux socket. To reattach, substitute your user ID and that run's UUID:

```sh
tmux -S /tmp/codex-status-UID/tmux-RUN-UUID.sock attach
```


## Shared collection

This project grew from investigating repeated collection work across multiple terminal HUDs. Codex Status shares the collector, reuses repository HEAD reads, and pauses record collection for hidden panels. The display clients wait for frames on a local socket.

<details>
<summary>Collection and session binding</summary>

The collector binds to an exact Codex PID, process start identity, account home, root session UUID, and supported CLI version. Ambiguous or unsupported bindings stay pending.

On macOS it uses libproc instead of repeatedly launching `ps` or `lsof`. It checks pane visibility and session binding every five seconds, reads new session records with a 256 KiB per-call budget, and shares repository HEAD reads without running Git commands. It renders frames when data or terminal width changes.

</details>


## Current limits

This alpha supports macOS. Linux and Windows are outside its support scope. The HUD is a separate tmux pane below the native Codex interface.

This alpha omits model-specific quota buckets, billing estimates, custom widgets, aggregate agent progress, indirect resume selectors, and fork entry commands.

The collector reads selected local rollout metadata and makes no independent API calls. It does not read credential contents. Codex itself keeps its normal network and authentication behavior. Quota is a session observation, not a fresh account-wide API lookup.

<details>
<summary>Local data and account changes</summary>

Run metadata excludes prompts, tool arguments, and environment contents. Runtime files and sockets stay in a user-private directory. Processes running as the same local user share that trust boundary.

Account-file stat changes invalidate quota observations. A file change does not establish account identity, and this check cannot detect every keychain-only change.

</details>


## Performance and tests

During **v0.1.0-alpha.1 development**, the native implementation used **1.89–2.48% of one CPU core** across three visible samples with 36 synthetic sessions, and 0.31% while hidden.

<details>
<summary>Measurement scope and reproduction</summary>

Those samples used fixed records with real tmux panels, display clients, and a collector. They did not run 36 real model requests. These are development measurements, not competitor comparisons or a guarantee for every machine.

Measured RSS was 98,976–99,008 KiB for the display clients, collector, tmux, and fixture waiting pane. **These RSS figures exclude Codex and launcher waiting processes.** The measurements apply to the native client, not the Python fallback.

```sh
python3 src/build-client.py
cd src
python3 -m unittest discover -s tests -v
python3 benchmark.py --sessions 36 --interval 5 --output ../benchmark-result.json
```

Tests cover binding, record parsing, terminal sanitization, lifecycle, IPC, native display, and installation. The benchmark uses owned temporary fixtures and makes no model requests. The repository contains no personal session data or internal development logs.

</details>


## Update or uninstall

Close your HUD panels before updating or removing the checkout. Keep sockets belonging to running sessions intact. For an update, fetch the desired release tag, check out that tag, and rerun the installer.

To uninstall, run this from the same checkout; add `--bin-dir` if you used a custom directory:

```sh
python3 install.py --uninstall
```

The uninstaller removes only the exact symlink owned by that checkout.


## Feedback

See [CONTRIBUTING.md](CONTRIBUTING.md). Bug reports are most useful with your OS, Python, tmux, and Codex versions. Please leave out raw session logs and authentication files.

If this fits your workflow, a star or a link shared with another Codex user helps people find it.


## License

Project code uses **Apache-2.0**, except `src/darwin_abi.py`, which retains **APSL-2.0** for adapted Apple SDK declarations. The source includes both license texts and modification notices. See [NOTICE](NOTICE) and [LICENSES](LICENSES/).

---

Built by **Captain Dev · HolyGround**

[YouTube](https://www.youtube.com/channel/UCOg67wZ-HgA4I73caIE-ByA) · [Working with AI — course](https://www.holyground.world/ko/garden/captaindev/seeds/commanding-ai) · [More from Captain Dev](https://www.holyground.world/ko/garden/captaindev)

The videos and Working with AI course are primarily in Korean.
