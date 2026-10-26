package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// claudeWindow writes a Claude Code home for cwd holding the transcripts of
// ids, and returns the run metadata of a window on it; no collector runs, as
// for a window drawn without HUD rows, whose statusLine never reports.
func claudeWindow(t *testing.T, pid int, args []any, ids ...string) (string, map[string]any) {
	t.Helper()
	home, cwd := t.TempDir(), "/Users/me/my.app"
	project := filepath.Join(home, "projects", "-Users-me-my-app")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := os.WriteFile(filepath.Join(project, id+".jsonl"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	meta := map[string]any{"id": hudTestRun, "runtime": "claude", "home": home, "cwd": cwd, "pid": float64(pid), "hud_pane": "", "args": args}
	return home, meta
}

func writeSessionFile(t *testing.T, home string, pid int, body string) {
	t.Helper()
	dir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)+".json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

// TestClaudeWindowWithoutHUDResumesItsConversation reproduces the switch of
// 2026-10-26: a Claude Code window without HUD rows reports no conversation
// to the collector, so its relaunch restarted without one.
func TestClaudeWindowWithoutHUDResumesItsConversation(t *testing.T) {
	current := "11111111-2222-4333-8444-555555555555"
	pid := 4242
	home, meta := claudeWindow(t, pid, []any{"--dangerously-skip-permissions"}, current)
	writeSessionFile(t, home, pid, `{"pid":4242,"sessionId":"`+current+`","cwd":"/Users/me/my.app","kind":"interactive"}`)
	got, source := conversationOf(t.TempDir(), meta, time.Now().Add(100*time.Millisecond))
	if got != current || source != "claude-session" {
		t.Fatalf("conversation %q from %q, want %q from Claude Code's session file", got, source, current)
	}
}

// TestClaudeWindowResumesTheConversationItMovedTo: after /resume or /clear the
// window runs another conversation than the one it was launched on, and the
// collector drops the session it no longer binds; the relaunch resumes the
// conversation the window runs now.
func TestClaudeWindowResumesTheConversationItMovedTo(t *testing.T) {
	launched, current := "11111111-2222-4333-8444-555555555555", "22222222-2222-4333-8444-555555555555"
	pid := 4343
	home, meta := claudeWindow(t, pid, []any{"--resume", launched}, launched, current)
	writeSessionFile(t, home, pid, `{"pid":4343,"sessionId":"`+current+`"}`)
	if got, _ := conversationOf(t.TempDir(), meta, time.Now().Add(100*time.Millisecond)); got != current {
		t.Fatalf("conversation %q, want the current %q", got, current)
	}
}

// TestClaudeSessionFileOfAnotherProcessIgnored: a file of another pid, an
// unreadable one, or a conversation not stored under the window's directory
// falls back to the launch arguments.
func TestClaudeSessionFileOfAnotherProcessIgnored(t *testing.T) {
	launched, other := "11111111-2222-4333-8444-555555555555", "33333333-2222-4333-8444-555555555555"
	for name, body := range map[string]string{
		"other pid":  `{"pid":1,"sessionId":"` + other + `"}`,
		"not json":   `{`,
		"not a uuid": `{"pid":4444,"sessionId":"../x"}`,
		"not stored": `{"pid":4444,"sessionId":"` + other + `"}`,
	} {
		home, meta := claudeWindow(t, 4444, []any{"--resume", launched}, launched)
		writeSessionFile(t, home, 4444, body)
		if got, source := conversationOf(t.TempDir(), meta, time.Now().Add(100*time.Millisecond)); got != launched || source != "launch-args" {
			t.Errorf("%s: conversation %q from %q, want %q from the launch arguments", name, got, source, launched)
		}
	}
}

// fakeSwitch is a stand-in for the runtime-status executable that, run as
// `__account-switch`, waits until its caller has gone, then writes to both
// outputs and names its process group.
func fakeSwitch(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime-status")
	script := "#!/bin/sh\nsleep 0.5\necho \"out $1 $2 $3 pgid=$(ps -o pgid= -p $$ | tr -d ' ') pid=$$\"\necho \"err after caller\" >&2\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDetachedSwitchOutlivesCallerAndItsPipes starts the switch from a child
// process whose stdout is a pipe the test closes as soon as the child exits,
// as the mod's process runner does once `account use` returns: the switch
// still runs to its end in a session of its own and writes to the switch log.
func TestDetachedSwitchOutlivesCallerAndItsPipes(t *testing.T) {
	if dir := os.Getenv("RS_TEST_DETACHED_CALLER"); dir != "" {
		if err := startDetachedSwitch(dir, os.Getenv("RS_TEST_FAKE_SWITCH"), "claude", "work"); err != nil {
			os.Exit(3)
		}
		fmt.Println("caller returns")
		os.Exit(0)
	}
	runtimeDir := privateDir(t)
	caller := exec.Command(os.Args[0], "-test.run=^TestDetachedSwitchOutlivesCallerAndItsPipes$")
	caller.Env = append(os.Environ(), "RS_TEST_DETACHED_CALLER="+runtimeDir, "RS_TEST_FAKE_SWITCH="+fakeSwitch(t))
	stdout, err := caller.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = caller.Start(); err != nil {
		t.Fatal(err)
	}
	said, _ := io.ReadAll(io.LimitReader(stdout, 1<<10))
	_ = stdout.Close()
	if err = caller.Wait(); err != nil || !strings.Contains(string(said), "caller returns") {
		t.Fatalf("caller: %v %q", err, said)
	}
	log := filepath.Join(runtimeDir, switchLogName)
	var text string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		data, _ := os.ReadFile(log)
		if text = string(data); strings.Contains(text, "err after caller") {
			break
		}
	}
	if !strings.Contains(text, "out __account-switch claude work") || !strings.Contains(text, "err after caller") || !strings.Contains(text, "switch claude to work: started") {
		t.Fatalf("switch output missing from the switch log:\n%s", text)
	}
	var pgid, pid int
	if _, err = fmt.Sscanf(text[strings.Index(text, "pgid="):], "pgid=%d pid=%d", &pgid, &pid); err != nil || pgid != pid || pgid == syscall.Getpgrp() {
		t.Fatalf("switch not in a process group of its own: pgid %d pid %d test %d (%v)", pgid, pid, syscall.Getpgrp(), err)
	}
	if info, err := os.Stat(log); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("switch log mode: %v %v", info, err)
	}
}

// TestSwitchLogNamesWindowsAndNoAddress: the log tells which windows a switch
// relaunched, with or without HUD rows, where each conversation came from and
// why it failed, and writes no e-mail address.
func TestSwitchLogNamesWindowsAndNoAddress(t *testing.T) {
	runtimeDir := privateDir(t)
	w := switchWindows{
		relaunch: []map[string]any{{"id": "run-a", "codex_pane": "%1", "hud_pane": ""}, {"id": "run-b", "codex_pane": "%2", "hud_pane": "%3"}},
		sources:  map[string]string{"run-a": "claude-session", "run-b": "none"},
	}
	logged := openSwitchLog(runtimeDir)
	logged.windows("claude", w)
	logged.result(fmt.Errorf("stored login of someone@example.com: %w", errors.New("denied")))
	logged.Close()
	data, err := os.ReadFile(filepath.Join(runtimeDir, switchLogName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"switch claude: 2 to relaunch", "run=run-a pane=%1 hud=off conversation=claude-session", "run=run-b pane=%2 hud=on conversation=none", "switch failed: stored login of <account>: denied"} {
		if !strings.Contains(text, want) {
			t.Errorf("log lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "@") {
		t.Errorf("log holds an address:\n%s", text)
	}
}

// privateDir is a runtime directory as the launcher makes it, closed to others.
func privateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSwitchRelaunchesWindowWithoutHUDOnItsConversation follows a switch
// through tmux: a Claude Code window without HUD rows, which no statusLine
// reports for, is respawned in its pane resuming the conversation its process
// runs, read before any process stops.
func TestSwitchRelaunchesWindowWithoutHUDOnItsConversation(t *testing.T) {
	socket, call := ghostTmux(t)
	self, runtimeDir := hudScript(t), privateDir(t)
	current := "11111111-2222-4333-8444-555555555555"
	// A pid no process holds, so no launch environment is relayed.
	pid := 999_999
	if syscall.Kill(pid, 0) == nil {
		t.Skip("pid in use")
	}
	home, meta := claudeWindow(t, pid, []any{"--dangerously-skip-permissions"}, current)
	writeSessionFile(t, home, pid, `{"pid":999999,"sessionId":"`+current+`"}`)
	pane := call("-f", "/dev/null", "new-session", "-d", "-s", "hud-off", "-P", "-F", "#{pane_id}", "sleep 60")
	for key, value := range map[string]any{"tmux_socket": socket, "codex_pane": pane, "launcher_executable": "/bin/true", "cwd": runtimeDir} {
		meta[key] = value
	}
	// The window's directory is the runtime directory, so its transcripts move there.
	project := filepath.Join(home, "projects", projectDirChars.ReplaceAllString(runtimeDir, "-"))
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, current+".jsonl"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	w := switchWindows{runtimeDir: runtimeDir, relaunch: []map[string]any{meta}}
	w.readConversations()
	if len(w.fresh()) != 0 {
		t.Fatalf("window would restart without its conversation; sources %v", w.sources)
	}
	if err := relaunchWindow(self, runtimeDir, meta, w.conversations[hudTestRun], &helpFlags{}); err != nil {
		t.Fatal(err)
	}
	start := call("display-message", "-p", "-t", pane, "#{pane_start_command}")
	if !strings.Contains(start, "--resume") || !strings.Contains(start, current) || !strings.Contains(start, "--hud") {
		t.Fatalf("window relaunched without its conversation: %s", start)
	}
}
