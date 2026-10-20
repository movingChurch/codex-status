package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestOrcaClaudeHomeFollowsOrcaRule(t *testing.T) {
	dir := t.TempDir()
	pointer := filepath.Join(dir, "selected-host")
	injected := "/orca/claude-profiles/p1/home"
	cases := []struct {
		name, pointerText, current, user, want string
		set, fails                             bool
	}{
		{"System default", "", injected, "", "", false, false},
		{"System default with the owner's own dir", "", injected, "/owner/claude", "/owner/claude", true, false},
		{"profile selected", "/orca/claude-profiles/p2/home\n", injected, "", "/orca/claude-profiles/p2/home", true, false},
		{"profile under home", "~/profiles/p3", injected, "", filepath.Join(dir, "profiles/p3"), true, false},
		{"owner's dir kept", "/orca/p2", "/elsewhere", "", "/elsewhere", true, false},
		{"relative pointer refused", "profiles/p4", injected, "", "", false, true},
	}
	for _, c := range cases {
		if err := os.WriteFile(pointer, []byte(c.pointerText), 0600); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{"HOME": dir, "ORCA_CLAUDE_PROFILE_POINTER": pointer, "CLAUDE_CONFIG_DIR": c.current, "ORCA_CLAUDE_INJECTED_CONFIG_DIR": injected, "ORCA_CLAUDE_USER_CONFIG_DIR": c.user}
		home, set, err := orcaClaudeHome(func(k string) string { return env[k] })
		if (err != nil) != c.fails || home != c.want || set != c.set {
			t.Errorf("%s: got %q set=%v err=%v", c.name, home, set, err)
		}
	}
	home, set, _ := orcaClaudeHome(func(k string) string { return map[string]string{"CLAUDE_CONFIG_DIR": "/x"}[k] })
	if home != "/x" || !set {
		t.Error("outside Orca the environment must stay as it is")
	}
}

func TestOrcaCodexAddsNoDaemon(t *testing.T) {
	inOrca := func(k string) string { return map[string]string{"ORCA_WORKSPACE_ID": "w"}[k] }
	supported := func() string { return "  --no-daemon  Run without the background server" }
	if got := orcaCodexArgs([]string{"resume", "id"}, inOrca, supported); !slices.Equal(got, []string{"--no-daemon", "resume", "id"}) {
		t.Errorf("inside Orca: %q", got)
	}
	for _, args := range [][]string{{"--remote=x"}, {"agents"}, {"queue"}, {"--no-daemon"}} {
		if got := orcaCodexArgs(args, inOrca, supported); !slices.Equal(got, args) {
			t.Errorf("opt-out %q changed to %q", args, got)
		}
	}
	if got := orcaCodexArgs(nil, inOrca, func() string { return "" }); len(got) != 0 {
		t.Error("a Codex without --no-daemon must get no flag")
	}
	if got := orcaCodexArgs(nil, func(string) string { return "" }, supported); len(got) != 0 {
		t.Error("outside Orca no flag is added")
	}
	isolateOff := func(k string) string {
		return map[string]string{"ORCA_WORKSPACE_ID": "w", "ORCA_CODEX_ISOLATE": "0"}[k]
	}
	if got := orcaCodexArgs(nil, isolateOff, supported); len(got) != 0 {
		t.Error("ORCA_CODEX_ISOLATE=0 must add no flag")
	}
}
