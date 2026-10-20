package main

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestWindowArgsKept(t *testing.T) {
	meta := map[string]any{"id": "00000000-0000-0000-0000-000000000000", "args": []any{"--resume", "a b"}}
	if got := windowArgs(meta); !slices.Equal(got, []string{"--resume", "a b"}) {
		t.Fatalf("args %q", got)
	}
}

func TestMenuTextStaysLiteral(t *testing.T) {
	if got := menuText("a#b\"c\\d\x01"); got != "a##bcd" {
		t.Fatalf("%q", got)
	}
	if got := tmuxQuote(`run "x" $HOME`); got != `"run \"x\" \$HOME"` {
		t.Fatalf("%q", got)
	}
}

func TestAccountItemRunsSwitch(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	socket := filepath.Join(t.TempDir(), "t.sock")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	if out, err := exec.Command("tmux", "-S", socket, "-f", "/dev/null", "new-session", "-d", "sleep 30").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	command := switchCommand("'/bin/echo' 'x'")
	// Binding the command makes tmux parse it exactly as a menu item does.
	if out, err := exec.Command("tmux", "-S", socket, "bind-key", "-T", "root", "F12", command).CombinedOutput(); err != nil {
		t.Fatalf("menu command does not parse: %v %s", err, out)
	}
	out, _ := exec.Command("tmux", "-S", socket, "list-keys", "-T", "root").Output()
	listed := ""
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, " F12 ") {
			listed = line
		}
	}
	if !strings.Contains(listed, "run-shell") || strings.Contains(listed, "display-menu") {
		t.Fatalf("account item does not run the switch directly: %s", listed)
	}
}
