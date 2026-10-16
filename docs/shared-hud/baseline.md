# Shared CLI HUD baseline verification

Verified 2026-10-16 for `flow:shared-cli-hud`, baseline stage. Application source revision: `f20e9f01171c64b9fe3446981266eada7eb0c620`. This report records the existing implementation before shared-core changes.

## Repository and checks

| Check | Result | Scope |
| --- | --- | --- |
| `go test ./...` | 75 tests passed in 3 packages | Existing synthetic tests |
| `go test -race ./...` | 75 tests passed | Existing concurrency coverage |
| `go vet ./...` | Passed | Existing source |
| `sh scripts/build-release.sh` | Passed | Darwin arm64 and amd64, CGO disabled |
| Archive members and SHA-256 | Passed | Executable, installer, README, LICENSE and NOTICE present; computed hashes match checksum file |
| Extracted arm64 install/update/remove | Passed | Temporary directory, version 0.2.0-alpha.1; production executable untouched |

Build output remains ignored under `dist/`; these baseline archives are not new release candidates. Native Intel execution was not tested.

## Codex diagnosis

- Existing `bindingRootCandidate` requires root metadata `cli_version == 0.153.4`.
- Observed installed Codex 0.162.0-alpha.2 root records otherwise match source, parent, ID and cwd checks; they contain context capacity and token counts.
- The imported source rejects these valid newer records. Installed binary instruction parity remains unproven because its reported source revision includes local modifications.
- Observed quota contains the weekly window, with no 5-hour window; this observation does not establish permanent account or provider support limits.
- One contacted collector serves seven registered runs. Its cache uses home/executable keys, which do not establish verified account equality across roots.

## Adapter feasibility

| Runtime | Established surface | Remaining acceptance |
| --- | --- | --- |
| Claude Code | Official command/stdin-JSON statusLine; installed help exposes `--settings <file-or-json>` and setting sources | Actual callback invocation, session binding, payload variants, deadlines and restoration |
| Antigravity CLI | Official statusLine command/stdin-JSON contract; installed `agy --help` confirms native CLI entry points | Installed callback configuration, actual payload, timeout/multiline behavior and restoration |

Help probes and existing synthetic tests sent no model requests. Production settings and credentials were unchanged. Adapter support remains unverified until its implementation stage completes native acceptance.

## Identity limits

Claude status exposes email/org metadata in the observed installation. Generated Codex account schema exposes email, plan and auth type without a required stable principal/workspace ID. Antigravity evidence does not establish a stable billing-realm identifier. Email, model, directory or executable alone cannot establish a universal quota owner.

The approved design resolves sufficiently scoped identity within one manager and isolates unresolved connections without speculative polling. This behavior is a future implementation obligation, not a baseline property.

## Sources

- Workspace evidence `flow/shared-cli-hud/evidence/20261016172854-shared-hud-baseline-diagnostics.md`: bounded metadata observations, test commands and identity limitations.
- Workspace design `flow/shared-cli-hud/design/shared-tmux-hud.md`: approved shared-core and adapter contracts.
- [Claude statusline documentation](https://code.claude.com/docs/en/statusline), retrieved 2026-10-16 in the flow's source record.
- [Antigravity statusline documentation](https://www.antigravity.google/docs/cli/statusline), retrieved 2026-10-16 in the flow's source record.

## Verification limits

This factual report introduces no agent procedure or always-loaded rule; verification checked its claims against recorded observations and build/install output. Behavioral document harness runs were not applicable. Current-version interactive binding, cross-root account deduplication, new adapters, shared tmux hosting and performance targets remain pending.
