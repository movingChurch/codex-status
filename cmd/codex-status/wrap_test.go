package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/movingChurch/codex-status/internal/install"
	"github.com/movingChurch/codex-status/internal/setup"
)

func TestPassthroughPerRuntime(t *testing.T) {
	tests := []struct {
		kind string
		args []string
		want bool
	}{
		{"claude", nil, false},
		{"claude", []string{"--model", "opus"}, false},
		{"claude", []string{"-p", "hi"}, true},
		{"claude", []string{"-pquery"}, true},
		{"claude", []string{"--output-format=json"}, true},
		{"claude", []string{"mcp", "list"}, true},
		{"claude", []string{"--resume", "x"}, false},
		{"antigravity", nil, false},
		{"antigravity", []string{"--model", "m", "--effort", "high"}, false},
		{"antigravity", []string{"--prompt", "hi"}, true},
		{"antigravity", []string{"--version"}, true},
		{"antigravity", []string{"models"}, true},
		{"antigravity", []string{"mic-serve"}, true},
		{"antigravity", []string{"-c"}, false},
	}
	for _, tt := range tests {
		if got := passthrough(tt.kind, tt.args); got != tt.want {
			t.Errorf("%s %v = %v", tt.kind, tt.args, got)
		}
	}
}

func TestWrapMissingNative(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, kind := range []string{"codex", "claude", "antigravity"} {
		if code := wrapRuntime(kind, nil); code != 127 {
			t.Fatal(kind, code)
		}
	}
}

func TestInstallMigratesLegacyAntigravityJournal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	path, _ := settingsPath("antigravity")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"dark"}`), 0600); err != nil {
		t.Fatal(err)
	}
	old := statusLineFor("/old/bin", "antigravity")
	if err := setup.Install(path, install.LegacyJournal(home), old); err != nil {
		t.Fatal(err)
	}
	if code := installAll(); code != 0 {
		t.Fatal(code)
	}
	var settings map[string]any
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &settings); err != nil || settings["theme"] != "dark" {
		t.Fatal(string(b))
	}
	installed := filepath.Join(home, ".local", "bin", install.Product)
	command := settings["statusLine"].(map[string]any)["command"].(string)
	if !strings.Contains(command, installed) {
		t.Fatal("callback not moved to the installed binary", command)
	}
	if code := uninstallAll(); code != 0 {
		t.Fatal(code)
	}
	b, _ = os.ReadFile(path)
	settings = nil
	if err := json.Unmarshal(b, &settings); err != nil || settings["statusLine"] != nil || settings["theme"] != "dark" {
		t.Fatal("uninstall did not restore the pre-legacy setting", string(b))
	}
}
