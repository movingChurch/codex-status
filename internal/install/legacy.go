package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Earlier installs under the codex-status name, matched exactly so that edited
// copies are kept.
const legacyCodexBlock = `# BEGIN codex-status automatic HUD
codex() {
  # codex-status ends its session when the terminal disconnects, including Orca.
  if [[ -o interactive && -t 0 && -t 1 ]]; then
    HOME/.local/bin/codex-status --wrap "$@"
  else
    command codex "$@"
  fi
}
# END codex-status automatic HUD
`

const legacyClaudeBlock = `# BEGIN claude-hud automatic HUD
claude() {
  if [[ ! -o interactive || ! -t 0 || ! -t 1 ]]; then
    command claude "$@"
    return $?
  fi
  local arg
  for arg in "$@"; do
    case "$arg" in
      -p|--print|--print=*|-p?*|-h|--help|-v|--version|--output-format|--output-format=*|--input-format|--input-format=*)
        command claude "$@"
        return $?
        ;;
    esac
  done
  case "${1-}" in
    auth|mcp|plugin|plugins|install|update|upgrade|doctor|setup-token|agents|help|remote-control)
      command claude "$@"
      ;;
    *)
      HOME/.local/bin/claude-hud "$@"
      ;;
  esac
}
# END claude-hud automatic HUD
`

var legacyClaudeScript = regexp.MustCompile(`\A#!/bin/sh\n# Shared Claude terminal HUD; native Claude configuration is launch-scoped\.\nexec (\S+/\.local/libexec/codex-status/[0-9A-Za-z.+-]+/codex-status) run claude -- "\$@"\n\z`)

func legacyBlocks(home string) []string {
	return []string{strings.ReplaceAll(legacyCodexBlock, "HOME", home), strings.ReplaceAll(legacyClaudeBlock, "HOME", home)}
}

// LegacyJournal is the settings journal directory of the codex-status name.
func LegacyJournal(home string) string {
	return filepath.Join(home, ".local", "state", "codex-status", "settings")
}

// removeLegacy removes old files after a backup; a file a running process still
// uses is kept and reported, so a later install removes it.
func removeLegacy(o Options, backup string) {
	bin := filepath.Join(o.Home, ".local", "bin")
	script := filepath.Join(bin, "claude-hud")
	if b, err := os.ReadFile(script); err == nil && legacyClaudeScript.Match(b) {
		removeFile(o, backup, script, b)
	}
	old := filepath.Join(bin, "codex-status")
	if isLegacyBinary(old) {
		if b, err := os.ReadFile(old); err == nil {
			removeFile(o, backup, old, b)
		}
	}
	libexec := filepath.Join(o.Home, ".local", "libexec", "codex-status")
	versions, _ := os.ReadDir(libexec)
	for _, v := range versions {
		dir := filepath.Join(libexec, v.Name())
		binary := filepath.Join(dir, "codex-status")
		if !v.IsDir() || !isLegacyBinary(binary) {
			continue
		}
		if o.Busy(binary) {
			_, _ = fmt.Fprintf(o.Out, "Kept %s: a running session uses it; install again after it exits\n", dir)
			continue
		}
		if err := os.RemoveAll(dir); err == nil {
			_, _ = fmt.Fprintf(o.Out, "Removed %s\n", dir)
		}
	}
	_ = os.Remove(libexec)
}

func removeFile(o Options, backup, path string, b []byte) {
	if o.Busy(path) {
		_, _ = fmt.Fprintf(o.Out, "Kept %s: a running session uses it; install again after it exits\n", path)
		return
	}
	if saveBackup(backup, filepath.Base(path), b) != nil {
		return
	}
	if os.Remove(path) == nil {
		_, _ = fmt.Fprintf(o.Out, "Removed %s (backup in %s)\n", path, backup)
	}
}

func isLegacyBinary(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	// A loaded machine can delay the first exec of a binary; a timeout keeps the file.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	return err == nil && strings.HasPrefix(string(out), "codex-status ")
}

// Busy lists running processes whose executable or arguments name path; Claude
// sessions name the callback executable in their per-launch settings.
func Busy(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-axww", "-o", "args=").Output()
	if err != nil {
		return true
	}
	return strings.Contains(string(out), path)
}
