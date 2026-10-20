# Runtime Status

**Your model, context, and quota. Right below your CLI session.**

A shared three-row HUD for Codex, Claude Code, and Antigravity CLI terminals, distributed as a single Go executable. No Python, C compiler, or Go installation is needed to run a release binary.

![Three runtime HUD previews](assets/shared-hud-preview.png)

*Candidate Codex, Claude Code and Antigravity previews captured from the terminal with synthetic data. This image is not live account data.*

**Early alpha:** macOS (Apple Silicon and Intel) and tmux. Compatible payloads are validated by their fields and session identity, rather than a vendor version pin. See the [compatibility matrix](docs/shared-hud/compatibility.md) for tested features and remaining limits. This is an independent project.

## Install

Download the macOS archive for your architecture from a release that includes Go binaries: `darwin_arm64` for Apple Silicon or `darwin_amd64` for Intel. Extract it, then run:

```sh
./install.sh
```

Or build from this checkout with Go 1.25 or later and run the same script. `./install.sh` finds or builds the executable and runs `runtime-status install`, which:

1. copies the executable to `~/.local/bin/runtime-status`,
2. installs the Antigravity statusLine callback when `agy` is on PATH, keeping the prior setting for restoration,
3. adds one marked block to `~/.zshrc` with `codex`, `claude` and `agy` functions.

Open a new zsh shell afterwards. Repeating the install changes nothing; an interrupted install is completed by the next run. Shell integration supports zsh only, and a symlinked `~/.zshrc` is refused. The official CLIs and tmux must be on PATH; their executables are never changed.

## Everyday use

After installation, run the native commands as usual. Interactive launches open inside the HUD; non-interactive calls, informational flags and management subcommands, such as `claude -p` or `agy models`, pass through to the official CLI unchanged.

```sh
codex
claude
agy
```

The executable can also be called directly:

```sh
runtime-status wrap claude [native arguments]
runtime-status --resume SESSION-UUID
runtime-status -- --model MODEL
runtime-status run antigravity --
runtime-status doctor claude
runtime-status preview antigravity
```

- **Scroll:** use the mouse wheel over the Codex pane. Scrolling enters tmux history, rather than recalling earlier input. Press `q` to return to the live pane.
- **Detach:** press `Ctrl-b`, then `d`.
- **Close only the HUD:** select the lower pane with `Ctrl-b`, then Down; press `Ctrl-b`, then `x`.
- **Finish:** exit the native CLI normally. Its paired HUD closes. The manager exits when no registered native sessions or views remain.

The launcher's dedicated tmux server enables mouse handling and hides the default tmux status bar. It does not read or change your personal tmux configuration. Terminal text selection may require holding Shift while dragging.

For Codex, explicit UUID resumes open with the HUD; informational commands and unsupported entry modes pass through to the official CLI. `RUNTIME_STATUS_CODEX`, `RUNTIME_STATUS_CLAUDE` and `RUNTIME_STATUS_ANTIGRAVITY` can select each native executable path.

All launches share one owned tmux server with separate sessions. List or reattach using your user ID and the run's UUID:

```sh
tmux -S /tmp/runtime-status-UID/tmux.sock list-sessions
tmux -S /tmp/runtime-status-UID/tmux.sock attach -t hud-RUN-UUID
```

A HUD session ends when its last terminal client goes away: closing the window, or detaching manually, ends the session and its runtime.

## Accounts

Codex and Claude Code can each keep several accounts and switch between them; Antigravity stays on one account.

```sh
runtime-status account add codex|claude [--current]
runtime-status account list [--json]
runtime-status account use codex|claude EMAIL|ALIAS [--yes]
runtime-status account rename codex|claude EMAIL|ALIAS NEW-ALIAS
runtime-status account remove codex|claude EMAIL|ALIAS
```

- **Add:** `add` opens the native sign-in in a temporary home, so the login in use stays unchanged; `--current` stores the login in use. Each login is copied into a `runtime-status accounts` item of the macOS Keychain on this machine only.
- **Switch:** `use`, or a click on the account name at the start of the HUD's first row, underlined and marked `▾` like a link, which opens a menu of that runtime's accounts. `~/.codex` and `~/.claude` keep one login each, so settings, skills and history stay shared and only the login changes.
- **Open windows:** runtime-status windows on `~/.codex` or `~/.claude` restart on the new account in their pane and directory with their launch options and resume the conversation they were running; a window whose conversation is not stored yet restarts without one and is named so. `use` names them and asks once; `--yes` skips the question. Windows on another home, such as Orca's account homes, are left alone and named as skipped. A process of that runtime outside runtime-status that uses the same home refuses the switch.
- **Usage:** `account list --json` reports the last 5H/7D reading the HUD observed for each account, as remaining percent.

- **Orca:** in Orca's account settings, keep **System default** selected for Claude Code and for Codex (real home), so Orca terminals run on `~/.claude` and `~/.codex` and their windows show the account and switch. runtime-status follows Orca's selected Claude profile the same way Orca's own `claude` function does, and starts Codex inside Orca with `--no-daemon` as Orca does. Windows on an Orca account profile show no account label and are skipped by a switch.

Rotating accounts to get around usage limits can break provider terms; runtime-status never switches on its own.

## Display

| Row | Information |
| --- | --- |
| Session | Selected account, model, reasoning effort, working directory, Git branch |
| Capacity | Context tokens and usage, 5H/7D usage and reset countdowns, and 30D for accounts whose plan has only a 30-day window |
| Activity | Observed task state, elapsed time, cache tokens and hit rate |

The account is the alias, else the email, of the account selected in `~/.local/state/runtime-status/accounts.json`, shown only for windows on `~/.codex` or `~/.claude`; the HUD never reads credentials, and a native `/login` outside runtime-status is not reflected until the next `account` command. Bars and percentages show **used** capacity. Codex context follows its validated reserved-token calculation; other adapters prefer provider percentages. Cache hit rate is cached input divided by input tokens. Only observed quota windows or pools appear. Loading, unavailable data, known binding errors, and measured zero remain distinct.

Before the first prompt, Codex account quota can already appear. Until a native session identity exists, the HUD displays a ready state; Antigravity's empty startup IDs are pending. Billing estimates, aggregate agent progress, and indirect Codex resume selectors are outside the current scope.

Claude receives a per-launch statusLine override with a 15-second refresh and activity hooks; an explicit settings file is merged privately without modifying the original. Antigravity's documented callback is persistent, so `runtime-status install` sets it up; `runtime-status uninstall` (or `runtime-status uninstall antigravity` alone) restores the prior selected setting while preserving unrelated changes. Unwrapped sessions dispatch the prior custom command when available. A native conversation change without an authoritative transition signal requires relaunching its HUD wrapper.

Colored output includes terminal hyperlinks: directory opens the local folder, branch opens its Git remote, quota opens the runtime's usage page (Codex or Claude), and elapsed opens the exact session log. In Orca on macOS, use Cmd-click. The terminal controls the required modifier. Embedded Git credentials are stripped from links.

## Collection and privacy

One manager serves multiple panels over a private local socket. It binds Codex to an exact process start identity, kernel executable, account home, and unique root rollout. Ambiguous or incompatible bindings show a reason. Darwin process inspection uses libproc directly without starting Python, ps, or lsof.

Hidden panels pause rollout reads. Codex views share one startup quota observation per profile/auth generation through official app-server `account/rateLimits/read`; later changes come from session observations. API-key/custom-endpoint sessions do not use that startup helper. No speculative periodic account polling is started. Account-file metadata changes invalidate observations; credential contents are not read.

The scoped-account registry merges only verified service/principal/billing-realm identities and keeps context separate per session. Current native surfaces do not prove that scope for each live process, so **native verified-account merging is unavailable in this alpha**. Those connections remain isolated within the same manager. See [account sharing](docs/shared-hud/account-sharing.md).

Each launcher forwards its environment to its runner over a private one-use socket before replacing itself with the tmux client. Environment contents are never stored in runtime files or logs. Optional idle worker retention defaults to 30 seconds; `RUNTIME_STATUS_IDLE_GRACE_SECONDS` accepts 0–60 seconds when starting a new manager.

Only selected telemetry fields leave the rollout reader. Read budgets, record size limits, and terminal sanitization bound processing. Runtime metadata does not contain prompts, tool arguments, or environment contents. Local processes running as your user share the runtime trust boundary.

## Build and verify

```sh
go test -race ./...
go vet ./...
./scripts/build-release.sh
```

The build produces macOS arm64 and amd64 archives and SHA-256 checksums under `dist/`, with `CGO_ENABLED=0`. Release consumers need neither Go nor an SDK. The test suite uses synthetic session fixtures and owned tmux sockets; it does not send model requests.

Previous Python/native-client CPU measurements do not characterize this Go implementation. Linux and Windows releases are not supported in this alpha.

## Update and uninstall

Run `./install.sh` from the new archive. Running sessions continue using their existing process; new sessions use the new version. If an incompatible manager is still running, the launcher reports the upgrade requirement instead of starting a duplicate.

`runtime-status install` also migrates installs made under the project's former name (see the [changelog](CHANGELOG.md)): it backs up and removes the old shell blocks in `~/.zshrc` when they match the original text, moves the Antigravity callback to the new executable, and removes the old executables. Files a running session still uses are kept and reported; run the install again after those sessions exit. Backups are kept in `~/.local/state/runtime-status/`.

`runtime-status uninstall` removes the shell block, restores the prior Antigravity setting and removes the executable; backups stay. Leave runtime sockets in place until their sessions finish.

## Feedback and license

See [CONTRIBUTING.md](CONTRIBUTING.md). Include OS, architecture, tmux, Codex, and Runtime Status versions. Do not attach raw session logs or authentication files.

Project code uses Apache-2.0, with Apple-derived Darwin ABI material under APSL-2.0 and purego under Apache-2.0. See [NOTICE](NOTICE) and [LICENSES](LICENSES/).

Built by **Captain Dev · HolyGround**

[YouTube](https://www.youtube.com/channel/UCOg67wZ-HgA4I73caIE-ByA) · [Working with AI — course](https://www.holyground.world/ko/garden/captaindev/seeds/commanding-ai) · [More from Captain Dev](https://www.holyground.world/ko/garden/captaindev)
