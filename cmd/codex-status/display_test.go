package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnedTmuxScrollSettings(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	socket := filepath.Join(t.TempDir(), "t.sock")
	call := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	call("-f", "/dev/null", "new-session", "-d", "-s", "fixture", "sleep 60")
	t.Cleanup(func() { exec.Command("tmux", "-S", socket, "kill-server").Run() })
	for _, args := range displaySettings() {
		call(args...)
	}
	if got := call("show-options", "-gv", "mouse"); got != "on" {
		t.Errorf("wheel input must be captured, got mouse %s", got)
	}
	if got := call("show-options", "-gv", "status"); got != "off" {
		t.Errorf("default status bar must be hidden, got %s", got)
	}
	// Query the whole table: tmux versions differ in named mouse-key filtering.
	binding := ""
	for _, line := range strings.Split(call("list-keys", "-T", "root"), "\n") {
		for _, field := range strings.Fields(line) {
			if field == "WheelUpPane" {
				binding = line
				break
			}
		}
	}
	if binding == "" {
		t.Fatal("WheelUpPane binding is missing from the root table")
	}
	if strings.Contains(binding, "alternate_on") || !strings.Contains(binding, "copy-mode") {
		t.Errorf("wheel must scroll pane history even in alternate screen: %s", binding)
	}
}

func TestDisconnectCleanup(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	socket := filepath.Join(t.TempDir(), "t.sock")
	call := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	call("-f", "/dev/null", "new-session", "-d", "-s", "codex", "sleep 60")
	t.Cleanup(func() { exec.Command("tmux", "-S", socket, "kill-server").Run() })
	hud := call("split-window", "-d", "-P", "-F", "#{pane_id}", "sleep 60")
	call("set-hook", "-t", "codex", "client-attached", attachmentHook("client-attached", hud))
	// Setup must survive before any terminal attaches.
	call("has-session", "-t", "codex")
	client := exec.Command("tmux", "-S", socket, "-C", "attach-session", "-t", "codex")
	input, err := client.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); client.Process.Kill() })
	deadline := time.Now().Add(5 * time.Second)
	for call("show-options", "-v", "-t", "codex", "destroy-unattached") != "on" {
		if time.Now().After(deadline) {
			t.Fatal("attachment did not arm cleanup")
		}
		time.Sleep(20 * time.Millisecond)
	}
	input.Close()
	client.Wait()
	for exec.Command("tmux", "-S", socket, "has-session", "-t", "codex").Run() == nil {
		if time.Now().After(deadline) {
			t.Fatal("detached Codex session survived")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
