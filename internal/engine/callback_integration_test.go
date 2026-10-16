package engine

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/movingChurch/codex-status/internal/telemetry"
)

func TestCallbackIPCAndNativeRegistrationLifetime(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "hud-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	id := "12345678-1234-1234-1234-123456789abc"
	meta := map[string]any{"v": 1, "id": id, "runtime": "claude", "home": root, "cwd": root, "pid": os.Getpid(), "started": telemetry.ProcessIdentity(os.Getpid()), "launched_at": 1, "tmux_socket": "/fake.sock", "hud_pane": "%1", "account_quota": false}
	path, err := WriteMetadata(root, meta)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(root, "collector.sock"))
	if err != nil {
		t.Fatal(err)
	}
	c, _, _, _ := fixtureCollector()
	delete(c.runs, "r")
	c.deps.visible = func(string) map[string]bool { return map[string]bool{"%1": true} }
	done := make(chan error, 1)
	go func() { done <- runServer(listener, root, c, renderSnapshot, 10*time.Millisecond, 20*time.Millisecond) }()
	defer func() { _ = os.Remove(path); _ = listener.Close() }()
	if err = Handshake(root); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err = conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(conn).Encode(map[string]any{"v": 1, "run": id, "format": "json"}); err != nil {
		t.Fatal(err)
	}
	scan := bufio.NewScanner(conn)
	if !scan.Scan() {
		t.Fatal("missing initial frame")
	}
	ticket, err := RPC(root, map[string]any{"v": 1, "op": "ticket", "run": id}, time.Now().Add(CallbackBudget))
	if err != nil {
		t.Fatal(err)
	}
	_, err = RPC(root, map[string]any{"v": 1, "op": "publish", "run": id, "ticket": ticket["ticket"], "data": map[string]any{"runtime": "claude", "session_id": "native-session", "context_remaining": 75}}, time.Now().Add(CallbackBudget))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for scan.Scan() {
		var frame map[string]any
		if err = json.Unmarshal(scan.Bytes(), &frame); err != nil {
			t.Fatal(err)
		}
		if frame["session_id"] == "native-session" {
			if frame["context_remaining"] != float64(75) {
				t.Fatal(frame)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("callback did not reach shared view")
	}
	_ = conn.Close()
	time.Sleep(50 * time.Millisecond)
	if _, err = RPC(root, map[string]any{"v": 1, "op": "status"}, time.Now().Add(time.Second)); err != nil {
		t.Fatal("manager exited with live native registration", err)
	}
	_ = os.Remove(path)
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("manager failed to exit after unregister")
	}
}
