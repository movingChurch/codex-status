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

func TestRestoreLaunchEnvironmentKeepsFreshPane(t *testing.T) {
	original := os.Environ()
	defer func() {
		os.Clearenv()
		for _, entry := range original {
			name, value, _ := strings.Cut(entry, "=")
			_ = os.Setenv(name, value)
		}
	}()
	_ = os.Setenv("TMUX", "fresh-server")
	_ = os.Setenv("TMUX_PANE", "%42")
	_ = os.Setenv("OLD_ACCOUNT_MARKER", "first-window")
	restoreLaunchEnvironment([]string{"CODEX_HOME=/synthetic/account-b", "CLAUDE_CONFIG_DIR=/synthetic/claude-b", "TMUX=stale-server", "TMUX_PANE=%1", "LITERAL=$HOME\n'quoted'"})
	if os.Getenv("CODEX_HOME") != "/synthetic/account-b" || os.Getenv("CLAUDE_CONFIG_DIR") != "/synthetic/claude-b" || os.Getenv("OLD_ACCOUNT_MARKER") != "" || os.Getenv("TMUX") != "fresh-server" || os.Getenv("TMUX_PANE") != "%42" || os.Getenv("LITERAL") != "$HOME\n'quoted'" {
		t.Fatal("launch environment was not restored independently")
	}
}

func TestStartupQuotaUsesExactProvider(t *testing.T) {
	for _, name := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_BASE_URL", "OPENAI_ORGANIZATION", "OPENAI_PROJECT"} {
		t.Setenv(name, "")
	}
	if !nativeStartupQuota("codex", []string{"--model", "example"}) {
		t.Fatal("default account startup disabled")
	}
	for _, args := range [][]string{{"-c", "model_provider=custom"}, {"-cmodel_provider=custom"}, {"--config=model_provider=custom"}, {"--profile", "other"}, {"-pother"}, {"--oss"}, {"--local-provider=ollama"}} {
		if nativeStartupQuota("codex", args) {
			t.Fatal("override queried default account", args)
		}
	}
	t.Setenv("OPENAI_API_KEY", "synthetic")
	if nativeStartupQuota("codex", nil) {
		t.Fatal("API key queried default account")
	}
	if nativeStartupQuota("claude", nil) {
		t.Fatal("wrong runtime helper enabled")
	}
}
