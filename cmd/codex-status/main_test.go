package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseLaunchPreservesArguments(t *testing.T) {
	id := "12345678-1234-4234-8234-123456789abc"
	args := []string{"--model", "example", "a prompt with 'quotes' $(echo BAD)"}
	resume, got, err := parseLaunch(append([]string{"--resume", id, "--"}, args...))
	if err != nil || resume != id || !reflect.DeepEqual(got, args) {
		t.Fatalf("resume arguments changed: %q %q %v", resume, got, err)
	}
}
func TestRejectUnbindableEntry(t *testing.T) {
	for _, args := range [][]string{{"--resume", "bad"}, {"--model", "x"}, {"--", "resume", "--last"}, {"--", "-C/tmp"}, {"--", "--cd=/tmp"}, {"--", "--last"}, {"--", "exec"}} {
		if _, _, err := parseLaunch(args); err == nil {
			t.Errorf("accepted unsupported arguments %v", args)
		}
	}
}
func TestRunnerRejectsForeignSocketAndDuplicateOptions(t *testing.T) {
	id := "12345678-1234-4234-8234-123456789abc"
	args := []string{"--runtime", "/tmp/owned", "--run", id, "--socket", "/tmp/foreign.sock", "--codex", "/bin/true"}
	if _, err := parseRunner(args); err == nil {
		t.Fatal("accepted foreign socket")
	}
	args[5] = "/tmp/owned/tmux-" + id + ".sock"
	if _, err := parseRunner(args); err != nil {
		t.Fatalf("valid runner: %v", err)
	}
	if _, err := parseRunner(append(args, "--run", id)); err == nil {
		t.Fatal("accepted duplicate identity")
	}
}
func TestShellCommandKeepsUntrustedArgumentsLiteral(t *testing.T) {
	words := []string{"printf", "%s\n", "$(touch SHOULD-NOT-EXIST)", "a'b", "hello\nworld", "`echo no`"}
	cmd := exec.Command("/bin/sh", "-c", shellCommand(words))
	cmd.Dir = t.TempDir()
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "$(touch SHOULD-NOT-EXIST)\na'b\nhello\nworld\n`echo no`\n"
	if string(out) != want {
		t.Fatalf("arguments were evaluated: %q", out)
	}
	if _, err := os.Stat(filepath.Join(cmd.Dir, "SHOULD-NOT-EXIST")); !os.IsNotExist(err) {
		t.Fatal("shell injection")
	}
}
func TestUniqueRunID(t *testing.T) {
	first := newID()
	if !uuidPattern.MatchString(first) || first == newID() {
		t.Fatal("invalid run identity")
	}
}
