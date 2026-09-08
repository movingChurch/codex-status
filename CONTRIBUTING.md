# Contributing

Use Go 1.25 or later and macOS with tmux for integration tests.

```sh
go test -race ./...
go vet ./...
./scripts/build-release.sh
```

Keep tests synthetic. Never submit authentication files, raw conversations, account identifiers, or private runtime metadata. Report your OS, architecture, tmux, Codex, and Codex Status versions with reproduction steps.

Maintain the existing display, exact-process session binding, bounded record reads, account separation, terminal sanitization, and owned-pane lifecycle. Changes to those contracts need behavioral tests. Run metadata and collector IPC belong to the user's private runtime directory.

The Go executable contains the launcher, collector, and display client. Release archives must run without Python, a C compiler, or a Go toolchain. Do not reintroduce an interpreter fallback. Keep platform-specific process inspection in the corresponding telemetry files.
