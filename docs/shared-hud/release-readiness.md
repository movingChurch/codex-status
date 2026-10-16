# Release readiness: 0.3.0-alpha.1

Candidate prepared 2026-10-16. Local artifacts are reviewable; no release has been published. Native verified-account merging remains incomplete; see [account sharing](account-sharing.md).

## Verification

- 148 tests across six packages passed with the race detector; `go vet ./...` passed.
- Changed-line golangci-lint against imported source `f20e9f01171c64b9fe3446981266eada7eb0c620` reported zero issues. Unrelated legacy lint findings remain outside this change.
- Independent review's provider/profile startup-query finding was corrected and independently rechecked. Native adapter evidence is in [compatibility](compatibility.md).
- Both architecture archive contents/checksums passed; arm64 isolated install/update/version/removal and unrelated-file/symlink refusal passed. Shellcheck, project-style shfmt and redacted secret scanning passed.
- One-use environment relay checks preserve distinct homes and model markers across 2/5 windows, with one manager/server. Closing one preserves remaining native sessions.
- Callback synthetic benchmark: p95 6.68 ms, maximum 6.88 ms; peak RSS 7.09 MiB. A stalled stdin invocation exited around 255 ms. These measure short callbacks, not native model workload performance.

## Matched synthetic resource benchmark

macOS arm64, Go 1.27.1, tmux 3.7c, 140×30 PTY; four-second settling and five-second sample. Same compiled fixture, working directory and account home across no-HUD, imported old source and candidate. Active fixtures append one token-count event per second. RSS totals include the native fixture.

| Workload | Sessions | No HUD RSS MiB | Old RSS MiB / processes | Candidate RSS MiB / processes |
| --- | ---: | ---: | ---: | ---: |
| Idle | 1 | 4.61 | 45.20 / 7 | 38.62 / 6 |
| Idle | 5 | 22.75 | 188.34 / 31 | 134.58 / 22 |
| Idle | 10 | 46.20 | 362.75 / 61 | 254.61 / 42 |
| Active | 1 | 4.80 | 45.03 / 7 | 37.95 / 6 |
| Active | 5 | 23.89 | 190.31 / 31 | 136.31 / 22 |
| Active | 10 | 47.48 | 359.72 / 61 | 256.33 / 42 |

Ten idle sessions save 108.14 MiB (29.8%) and 19 persistent processes in this sample. Latest environment-relay candidate recheck measured 253.61 MiB and 42 processes. Both multi-window samples retained exactly one manager and one tmux server. Owned test processes were cleaned up.

CPU seconds over five seconds (old/candidate): idle 1: .01/.01, 5: .01/.03, 10: .05/.02; active 1: .01/.01, 5: .09/.06, 10: .11/.11. No CPU improvement is claimed. Short samples are noisy; process RSS double-counts shared pages and excludes exited helpers. One sample per matrix cell is not a statistical performance guarantee. Production LLM activity was not benchmarked.

Read/redraw behavior is checked through bounded incremental-reader and hidden-view fixtures; syscall counts and redraw frequency were not instrumented in the workload matrix. Live compaction, billing accuracy and all-runtime long-running failure recovery remain unaccepted.

## Distribution

`scripts/build-release.sh` produces `dist/codex-status_0.3.0-alpha.1_darwin_{arm64,amd64}.tar.gz` and `dist/checksums_0.3.0-alpha.1.txt`. Archives include the executable, installer, README, license/notice material, relevant Darwin source and adapter/compatibility records. arm64 is executed locally; amd64 is cross-built.

Finish existing v0.2 HUD sessions normally before launching v0.3. The incompatible old manager is preserved rather than duplicated. Existing installed executable, running sessions and production account metadata were not replaced during preparation.

Owner terminal-layout acceptance and actual public publication remain pending. Account-level native deduplication needs authoritative per-session identity before it can satisfy the full intended optimization.
