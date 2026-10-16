package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeExplicitSettingsPreserved(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "original.json")
	original := `{"permissions":{"defaultMode":"plan"},"statusLine":{"type":"command","command":"old"},"custom":true}`
	if err := os.WriteFile(source, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	o := runnerOptions{Kind: "claude", Runtime: root, Run: "11111111-1111-4111-8111-111111111111"}
	args, err := prepareNative(o, []string{"--settings", source, "--model", "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 4 || args[0] != "--settings" || args[2] != "--model" {
		t.Fatal(args)
	}
	b, err := os.ReadFile(args[1])
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	if result["custom"] != true || result["permissions"].(map[string]any)["defaultMode"] != "plan" || !strings.Contains(result["statusLine"].(map[string]any)["command"].(string), "__callback") {
		t.Fatal(result)
	}
	b, err = os.ReadFile(source)
	if err != nil || string(b) != original {
		t.Fatal("original changed", err)
	}
	if info, e := os.Stat(args[1]); e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("temporary settings not private")
	}
}

func TestRunnerSharedSocketAndRuntime(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	args := []string{"--runtime", "/tmp/owned", "--run", id, "--socket", "/tmp/owned/tmux.sock", "--codex", "/bin/true", "--kind", "claude", "--", "--model", "example"}
	o, err := parseRunner(args)
	if err != nil || o.Kind != "claude" || len(o.Args) != 2 {
		t.Fatal(o, err)
	}
	args[5] = "/tmp/foreign.sock"
	if _, err = parseRunner(args); err == nil {
		t.Fatal("foreign socket accepted")
	}
}
