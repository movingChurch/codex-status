#!/bin/sh
# Install one self-contained executable. Never change the official codex command.
set -eu
source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bin_dir="${HOME}/.local/bin"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --bin-dir) [ "$#" -ge 2 ] || { echo 'Missing --bin-dir value' >&2; exit 2; }; bin_dir=$2; shift 2 ;;
    -h|--help) echo 'Usage: ./install.sh [--bin-dir DIRECTORY]'; exit 0 ;;
    *) echo 'Unknown installer option' >&2; exit 2 ;;
  esac
done
case "$(uname -s)" in Darwin) ;; *) echo 'This release supports macOS only.' >&2; exit 2;; esac
case "$bin_dir" in /*) ;; *) echo 'Use an absolute install directory.' >&2; exit 2;; esac
if [ -f "$source_dir/codex-status" ]; then
  binary="$source_dir/codex-status"
else
  command -v go >/dev/null 2>&1 || { echo 'Install a release archive, or install Go to build from source.' >&2; exit 2; }
  mkdir -p "$source_dir/build"
  (cd "$source_dir" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(cat VERSION)" -o build/codex-status ./cmd/codex-status)
  binary="$source_dir/build/codex-status"
fi
"$binary" --version
mkdir -p "$bin_dir"
target="$bin_dir/codex-status"
if [ -L "$target" ]; then
  # Preserve legacy checkouts and unrelated symlinks; local migration handles its known link.
  echo 'The target is a symlink. Preserve or move it before installing.' >&2; exit 2
fi
if [ -e "$target" ]; then
  case "$("$target" --version 2>/dev/null || true)" in
    'codex-status '*) ;;
    *) echo 'Refusing to replace an unrelated file.' >&2; exit 2 ;;
  esac
fi
staged=$(mktemp "$bin_dir/.codex-status.XXXXXX")
trap 'rm -f "$staged"' EXIT HUP INT TERM
cp "$binary" "$staged"
chmod 755 "$staged"
mv -f "$staged" "$target"
printf 'Installed %s\n' "$target"
