#!/bin/sh
# Find or build the executable, then let it install itself. Never change the official CLIs.
set -eu
source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
case "${1-}" in
  '') ;;
  -h|--help) echo 'Usage: ./install.sh'; echo 'Installs ~/.local/bin/runtime-status, its zsh integration and the Antigravity callback.'; exit 0 ;;
  *) echo 'Unknown installer option' >&2; exit 2 ;;
esac
case "$(uname -s)" in Darwin) ;; *) echo 'This release supports macOS only.' >&2; exit 2;; esac
if [ -f "$source_dir/runtime-status" ]; then
  binary="$source_dir/runtime-status"
else
  command -v go >/dev/null 2>&1 || { echo 'Install a release archive, or install Go to build from source.' >&2; exit 2; }
  mkdir -p "$source_dir/build"
  (cd "$source_dir" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(cat VERSION)" -o build/runtime-status ./cmd/runtime-status)
  binary="$source_dir/build/runtime-status"
fi
"$binary" --version
exec "$binary" install
