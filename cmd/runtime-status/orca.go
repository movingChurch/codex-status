package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Orca injects its selected Claude profile into CLAUDE_CONFIG_DIR when a
// terminal starts, and its own `claude` shell function re-reads the selection
// at each launch. The runtime-status shell function runs in its place, so it
// applies the same rule before anything else.

// orcaClaudeHome returns the CLAUDE_CONFIG_DIR a launch inside Orca uses and
// whether it is set at all; outside Orca it keeps the environment as it is.
func orcaClaudeHome(getenv func(string) string) (string, bool, error) {
	current := getenv("CLAUDE_CONFIG_DIR")
	pointer := getenv("ORCA_CLAUDE_PROFILE_POINTER")
	injected, user := getenv("ORCA_CLAUDE_INJECTED_CONFIG_DIR"), getenv("ORCA_CLAUDE_USER_CONFIG_DIR")
	if pointer == "" {
		return current, current != "", nil
	}
	// A directory the owner set, rather than Orca, is kept.
	if current != "" && current != injected && strings.TrimSuffix(current, "/") != strings.TrimSuffix(user, "/") {
		return current, true, nil
	}
	if rest, ok := strings.CutPrefix(pointer, "~/"); ok {
		pointer = filepath.Join(getenv("HOME"), rest)
	}
	raw, _ := os.ReadFile(pointer)
	home := strings.TrimSpace(string(raw))
	switch {
	case home == "" && user != "":
		return user, true, nil
	case home == "":
		return "", false, nil
	case strings.HasPrefix(home, "~/"):
		return filepath.Join(getenv("HOME"), home[2:]), true, nil
	case filepath.IsAbs(home):
		return home, true, nil
	}
	return "", false, errors.New("the Claude profile Orca selected is not an absolute path")
}

// applyOrcaClaudeHome sets this process's CLAUDE_CONFIG_DIR by Orca's rule.
func applyOrcaClaudeHome() error {
	home, set, err := orcaClaudeHome(os.Getenv)
	if err != nil {
		return err
	}
	if !set {
		_ = os.Unsetenv("ORCA_CLAUDE_INJECTED_CONFIG_DIR")
		return os.Unsetenv("CLAUDE_CONFIG_DIR")
	}
	return os.Setenv("CLAUDE_CONFIG_DIR", home)
}

// orcaCodexOptOut lists the arguments after which Orca's `codex` function
// leaves the background server alone.
var orcaCodexOptOut = []string{"--no-daemon", "--remote", "agents", "queue"}

// orcaCodexArgs runs Orca's Codex launch preflight and adds --no-daemon, as
// Orca's own `codex` function does, so a window inside Orca never stops at
// Codex's background-server prompt; outside Orca it returns args unchanged.
func orcaCodexArgs(args []string, getenv func(string) string, help func() string) []string {
	if getenv("ORCA_WORKSPACE_ID") == "" && getenv("ORCA_CODEX_HOME") == "" || getenv("ORCA_CODEX_ISOLATE") == "0" {
		return args
	}
	if preflight := getenv("ORCA_CODEX_LAUNCH_PREFLIGHT"); preflight != "" {
		if info, err := os.Stat(preflight); err == nil && info.Mode()&0111 != 0 {
			_ = exec.Command(preflight, "agent", "hooks", "prepare-codex").Run()
		}
	}
	for _, arg := range args {
		if slices.Contains(orcaCodexOptOut, arg) || strings.HasPrefix(arg, "--remote=") {
			return args
		}
	}
	if !strings.Contains(help(), "--no-daemon") {
		return args
	}
	return append([]string{"--no-daemon"}, args...)
}

func codexHelp(native string) func() string {
	return func() string {
		out, _ := exec.Command(native, "--help").Output()
		return string(out)
	}
}
