package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const hudTestRun = "11111111-1111-4111-8111-111111111111"

func TestClaudeHUDSettingTurnsOffOnlyClaude(t *testing.T) {
	env := func(value string) func(string) string {
		return func(name string) string {
			if name == claudeHUDSetting {
				return value
			}
			return ""
		}
	}
	for _, kind := range []string{"codex", "claude", "antigravity"} {
		if !hudShown(kind, env("")) || !hudShown(kind, env("on")) {
			t.Errorf("%s must keep its HUD by default", kind)
		}
	}
	if hudShown("claude", env("off")) {
		t.Error("claude must draw no HUD rows with the setting off")
	}
	if !hudShown("codex", env("off")) || !hudShown("antigravity", env("off")) {
		t.Error("the Claude setting must leave Codex and Antigravity unchanged")
	}
}

func TestRunnerTakesHUDOffOnlyForClaude(t *testing.T) {
	base := []string{"--runtime", "/tmp/owned", "--run", hudTestRun, "--socket", "/tmp/owned/tmux.sock", "--codex", "/bin/true"}
	with := func(extra ...string) []string { return append(append([]string{}, base...), extra...) }
	if o, err := parseRunner(with("--kind", "claude", "--hud", "off")); err != nil || o.HUD != "off" {
		t.Fatal(o, err)
	}
	for _, args := range [][]string{with("--kind", "codex", "--hud", "off"), with("--kind", "antigravity", "--hud", "off"), with("--kind", "claude", "--hud", "on")} {
		if _, err := parseRunner(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

// TestClaudeWithoutHUDKeepsItsSettings: a Claude Code window without HUD rows
// gets no statusLine override and no hooks, inline or merged into a file.
func TestClaudeWithoutHUDKeepsItsSettings(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "original.json")
	if err := os.WriteFile(source, []byte(`{"statusLine":{"type":"command","command":"own"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	o := runnerOptions{Kind: "claude", Runtime: root, Run: hudTestRun, HUD: "off"}
	for _, args := range [][]string{{"--model", "example"}, {"--settings", source, "--model", "example"}} {
		got, err := prepareNative(o, args)
		if err != nil || strings.Join(got, "\x00") != strings.Join(args, "\x00") {
			t.Errorf("prepareNative(%v) = %v, %v; want the arguments unchanged", args, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "launch-"+hudTestRun+".json")); !os.IsNotExist(err) {
		t.Error("no merged settings file may be written without the HUD")
	}
}

func hudScript(t *testing.T) string {
	t.Helper()
	self := filepath.Join(t.TempDir(), "runtime-status")
	if err := os.WriteFile(self, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return self
}

// TestLaunchWindowWithoutClaudeHUD follows the launcher's tmux setup: with the
// setting off a Claude Code window has one pane and no HUD, and keeps the
// hook that ends its session after the last client; Codex still splits.
func TestLaunchWindowWithoutClaudeHUD(t *testing.T) {
	socket, call := ghostTmux(t)
	getenv := func(string) string { return "off" }
	self := hudScript(t)
	for kind, panes := range map[string]int{"claude": 1, "codex": 2} {
		session := "hud-" + kind
		pane := call("-f", "/dev/null", "new-session", "-d", "-s", session, "-P", "-F", "#{pane_id}", "sleep 60")
		hud, err := prepareWindow(socket, session, pane, self, t.TempDir(), hudTestRun, kind, hudShown(kind, getenv))
		if err != nil {
			t.Fatal(kind, err)
		}
		if got := len(strings.Fields(call("list-panes", "-t", session, "-F", "#{pane_id}"))); got != panes {
			t.Errorf("%s window has %d panes, want %d", kind, got, panes)
		}
		if (hud == "") != (kind == "claude") {
			t.Errorf("%s HUD pane = %q", kind, hud)
		}
		hooks := call("show-hooks", "-t", session, "client-attached")
		if !strings.Contains(hooks, "destroy-unattached on") {
			t.Errorf("%s window lost its cleanup hook: %q", kind, hooks)
		}
	}
}

// TestRelaunchKeepsWindowWithoutHUD: an account switch relaunches a Claude
// Code window that had no HUD pane without one, and one that had a HUD with it.
func TestRelaunchKeepsWindowWithoutHUD(t *testing.T) {
	socket, call := ghostTmux(t)
	self := hudScript(t)
	runtimeDir := t.TempDir()
	for name, shown := range map[string]bool{"hud-off": false, "hud-on": true} {
		pane := call("-f", "/dev/null", "new-session", "-d", "-s", name, "-P", "-F", "#{pane_id}", "sleep 60")
		hud := ""
		if shown {
			var err error
			if hud, err = splitHUD(socket, name, pane, self, runtimeDir, hudTestRun, "claude"); err != nil {
				t.Fatal(err)
			}
		}
		meta := map[string]any{"tmux_socket": socket, "codex_pane": pane, "hud_pane": hud, "runtime": "claude", "launcher_executable": "/bin/true", "cwd": runtimeDir, "args": []any{}}
		if err := relaunchWindow(self, runtimeDir, meta, "", &helpFlags{}); err != nil {
			t.Fatal(name, err)
		}
		panes := len(strings.Fields(call("list-panes", "-t", name, "-F", "#{pane_id}")))
		start := call("display-message", "-p", "-t", pane, "#{pane_start_command}")
		if shown && (panes != 2 || strings.Contains(start, "--hud")) {
			t.Errorf("window with a HUD relaunched with %d panes: %s", panes, start)
		}
		if !shown && (panes != 1 || !strings.Contains(start, "--hud") || !strings.Contains(start, "off")) {
			t.Errorf("window without a HUD relaunched with %d panes: %s", panes, start)
		}
	}
}
