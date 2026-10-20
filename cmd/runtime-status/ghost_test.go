package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPaneAttached(t *testing.T) {
	for out, want := range map[string]bool{"%3 1": true, "%3 0": false, "%4 1": false, "%3": false, "": false} {
		if got := paneAttached(out, "%3"); got != want {
			t.Errorf("paneAttached(%q) = %v, want %v", out, got, want)
		}
	}
}

func ghostTmux(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	// A short directory keeps the socket path under the unix limit.
	dir, err := os.MkdirTemp("/tmp", "rsg")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "t.sock")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
		os.RemoveAll(dir)
	})
	return socket, func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
}

func TestDetachedSessionIsNotAliveWindow(t *testing.T) {
	socket, call := ghostTmux(t)
	pane := call("-f", "/dev/null", "new-session", "-d", "-s", "ghost", "-P", "-F", "#{pane_id}", "sleep 30")
	meta := map[string]any{"tmux_socket": socket, "codex_pane": pane}
	if paneAlive(meta) {
		t.Error("a window whose session has no client must count as ended")
	}
}

func TestSessionEndsWhenUnattachedOnlyAfterFirstClient(t *testing.T) {
	_, call := ghostTmux(t)
	call("-f", "/dev/null", "new-session", "-d", "-s", "hud-x", "sleep 60")
	for _, args := range sessionSettings("hud-x") {
		call(args...)
	}
	// Off until a client attaches, so the gap before the launcher's attach is safe.
	if got := call("show-options", "-Av", "-t", "hud-x", "destroy-unattached"); got != "off" {
		t.Errorf("option must wait for the first client, got %s", got)
	}
	hook := call("show-hooks", "-t", "hud-x", "client-attached")
	if !strings.Contains(hook, "destroy-unattached on") || !strings.Contains(hook, "hud-x") {
		t.Errorf("attach hook must set destroy-unattached for the session, got %s", hook)
	}
}
