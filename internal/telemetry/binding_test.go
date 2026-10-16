package telemetry

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const bindingRoot = "11111111-1111-7111-8111-111111111111"
const bindingOther = "22222222-2222-7222-8222-222222222222"

func bindingFixture(t *testing.T) (map[string]any, string) {
	t.Helper()
	home := bindingResolve(t.TempDir())
	sessions := filepath.Join(home, "sessions")
	if err := os.Mkdir(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"pid": os.Getpid(), "started": ProcessIdentity(os.Getpid()), "home": home, "cwd": home}, sessions
}
func writeBindingRoot(t *testing.T, folder, id string, changes map[string]any) string {
	t.Helper()
	payload := map[string]any{"id": id, "session_id": id, "source": "cli", "parent_thread_id": nil, "cwd": filepath.Dir(folder), "cli_version": "0.153.4"}
	for k, v := range changes {
		payload[k] = v
	}
	data, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": payload})
	path := filepath.Join(folder, "rollout-2026-09-08T12-00-00-"+id+".jsonl")
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestBindingLiveOpenedRoot(t *testing.T) {
	meta, folder := bindingFixture(t)
	path := writeBindingRoot(t, folder, bindingRoot, nil)
	if meta["started"] == "" {
		t.Fatal("missing live identity")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	exe, _ := os.Executable()
	meta["executable"] = exe
	if got := BindRun(meta); got != path {
		t.Fatalf("binding = %q, want %q", got, path)
	}
	meta["expected_session"] = bindingOther
	if got := BindRun(meta); got != "" {
		t.Fatal("wrong resume accepted")
	}
	meta["expected_session"] = bindingRoot
	other := writeBindingRoot(t, folder, bindingOther, nil)
	g, _ := os.Open(other)
	defer g.Close()
	if got := BindRun(meta); got != "" {
		t.Fatal("resume hid ambiguous root")
	}
	g.Close()
	meta["started"] = "reused"
	if got := BindRun(meta); got != "" {
		t.Fatal("reused PID accepted")
	}
}
func TestBindingRejectsUnsafeMetadata(t *testing.T) {
	for name, changes := range map[string]map[string]any{"subagent": {"source": map[string]any{"subagent": true}}, "parent": {"parent_thread_id": bindingOther}, "cwd": {"cwd": "/missing-project"}, "session": {"session_id": bindingOther}, "invalid": {"id": true}} {
		t.Run(name, func(t *testing.T) {
			meta, folder := bindingFixture(t)
			path := writeBindingRoot(t, folder, bindingRoot, changes)
			f, _ := os.Open(path)
			defer f.Close()
			if BindRun(meta) != "" {
				t.Fatal("unsafe metadata accepted")
			}
		})
	}
}
func TestBindingCompatibleVersions(t *testing.T) {
	for _, version := range []any{"0.144.1", "0.162.0-alpha.2", "future-release", nil} {
		meta, folder := bindingFixture(t)
		path := writeBindingRoot(t, folder, bindingRoot, map[string]any{"cli_version": version})
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := BindRun(meta); got != path {
			t.Errorf("compatible version %v rejected", version)
		}
		_ = f.Close()
	}
}
func TestBindingClosedRootNotEvidence(t *testing.T) {
	meta, folder := bindingFixture(t)
	writeBindingRoot(t, folder, bindingRoot, nil)
	if BindRun(meta) != "" {
		t.Fatal("closed root accepted")
	}
}
func TestBindingAuthEpoch(t *testing.T) {
	home := bindingResolve(t.TempDir())
	first := AuthEpoch(home)
	os.WriteFile(filepath.Join(home, "auth.json"), []byte("never-read-this"), 0000)
	second := AuthEpoch(home)
	if first == second {
		t.Fatal("auth change missed")
	}
	os.Remove(filepath.Join(home, "auth.json"))
	if AuthEpoch(home) != first {
		t.Fatal("missing epoch unstable")
	}
}

func TestBindingFileSafety(t *testing.T) {
	_, folder := bindingFixture(t)
	path := writeBindingRoot(t, folder, bindingRoot, map[string]any{"base_instructions": "secret-fixture"})
	info, _ := os.Stat(path)
	identity := bindingFileIdentity(info)
	meta := bindingReadMeta(path, identity)
	if meta == nil {
		t.Fatal("valid metadata missing")
	}
	if _, ok := meta["base_instructions"]; ok {
		t.Fatal("unsafe field retained")
	}
	if bindingReadMeta(path, rolloutIdentity{identity.dev, identity.ino + 1}) != nil {
		t.Fatal("stale fd inode accepted from cache")
	}
	alias := filepath.Join(folder, "alias.jsonl")
	os.Symlink(path, alias)
	if bindingReadMeta(alias, identity) != nil {
		t.Fatal("symlink accepted")
	}
	os.WriteFile(path, []byte(strings.Repeat("x", bindingHeaderLimit+1)+"\n"), 0600)
	info, _ = os.Stat(path)
	if bindingReadMeta(path, bindingFileIdentity(info)) != nil {
		t.Fatal("oversized header accepted")
	}
	os.WriteFile(path, []byte(`{"type":"session_meta","payload":{}}`), 0600)
	info, _ = os.Stat(path)
	if bindingReadMeta(path, bindingFileIdentity(info)) != nil {
		t.Fatal("unfinished header accepted")
	}
}

func TestBindingChildHelper(t *testing.T) {
	if os.Getenv("CODEX_BINDING_CHILD") == "" {
		return
	}
	file, err := os.Open(os.Getenv("CODEX_BINDING_CHILD"))
	if err != nil {
		os.Exit(2)
	}
	defer file.Close()
	fmt.Println("ready")
	var b [1]byte
	os.Stdin.Read(b[:])
	os.Exit(0)
}
func TestBindingLiveChild(t *testing.T) {
	meta, folder := bindingFixture(t)
	path := writeBindingRoot(t, folder, bindingRoot, nil)
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "-test.run=^TestBindingChildHelper$")
	cmd.Env = append(os.Environ(), "CODEX_BINDING_CHILD="+path)
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); cmd.Wait() }()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("child readiness %q %v", line, err)
	}
	meta["pid"] = cmd.Process.Pid
	meta["started"] = ProcessIdentity(cmd.Process.Pid)
	meta["executable"] = exe
	if got := BindRun(meta); got != path {
		t.Fatalf("child bind = %q", got)
	}
	meta["executable"] = path
	if BindRun(meta) != "" {
		t.Fatal("wrong child executable accepted")
	}
}
