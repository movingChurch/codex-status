package engine

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStatusAndLastDisconnectShutdown(t *testing.T) {
	runtime, err := os.MkdirTemp("/tmp", "engine-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(runtime)
	listener, err := net.Listen("unix", filepath.Join(runtime, "collector.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	id := "12345678-1234-1234-1234-123456789abc"
	if err := os.Mkdir(filepath.Join(runtime, "runs"), 0700); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{"v": 1, "id": id, "home": runtime, "cwd": runtime, "pid": 1, "started": "fake", "launched_at": 1, "tmux_socket": "/fake.sock", "hud_pane": "%1", "auth_epoch": "old"}
	raw, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(runtime, "runs", id+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	c, _, _, _ := fixtureCollector()
	delete(c.runs, "r")
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	c.deps.visible = func(string) map[string]bool {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return map[string]bool{"%1": true}
	}
	done := make(chan error, 1)
	go func() {
		done <- runServer(listener, runtime, c, func(map[string]any, int, bool) []string { return []string{"one", "two", "three"} }, 10*time.Millisecond, 30*time.Millisecond)
	}()
	conn, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	json.NewEncoder(conn).Encode(map[string]any{"v": 1, "run": id, "width": 80})
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatalf("no pending snapshot: %v", scanner.Err())
	}
	var snapshot map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["state"] != "pending" {
		t.Fatalf("first frame=%v", snapshot)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("tick never queried visibility")
	}
	status, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	status.SetDeadline(time.Now().Add(time.Second))
	json.NewEncoder(status).Encode(map[string]any{"v": 1, "op": "status"})
	var state map[string]any
	status.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	decodeErr := json.NewDecoder(status).Decode(&state)
	close(release)
	if decodeErr != nil {
		t.Fatalf("status blocked behind visibility helper: %v", decodeErr)
	}
	status.Close()
	if state["clients"] != float64(1) || state["runs"] != float64(1) {
		t.Fatalf("status=%v", state)
	}
	conn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("collector remained after last disconnect")
	}
}

func TestFrameDeduplicationAndResize(t *testing.T) {
	client := &subscriber{format: "ansi", width: 80, frames: make(chan []byte, 4)}
	widths := []int{}
	render := func(snapshot map[string]any, width int, color bool) []string {
		widths = append(widths, width)
		return []string{"one", "two", "three"}
	}
	snapshot := map[string]any{"state": "pending"}
	client.send(snapshot, render)
	frame := string(<-client.frames)
	if !strings.HasPrefix(frame, "\x1b[?25l\x1b[H") || strings.Count(frame, "\r\n") != 2 {
		t.Fatalf("invalid three row frame %q", frame)
	}
	client.send(snapshot, render)
	if len(client.frames) != 0 || len(widths) != 1 {
		t.Fatal("unchanged frame rendered twice")
	}
	client.width = 40
	client.send(snapshot, render)
	if len(client.frames) != 1 || widths[1] != 40 {
		t.Fatal("resize did not rerender")
	}
}
func TestVisibilityRequiresAttachedActiveLivePane(t *testing.T) {
	got := parseVisibility("%1\t1\t2\t0\t0\t0\n%2\t1\t0\t0\t1\t0\n%3\t1\t1\t1\t0\t0\n%4\t1\t1\t1\t1\t0\n%5\t1\t1\t0\t1\t1\n%6\t0\t1\t0\t1\t0\n")
	if !got["%1"] || got["%2"] || got["%3"] || !got["%4"] || got["%5"] || got["%6"] {
		t.Fatalf("visibility=%v", got)
	}
}
func TestServeRejectsUnsafeSocketAndHonorsCollectorLock(t *testing.T) {
	runtime, err := os.MkdirTemp("/tmp", "engine-lock-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(runtime)
	lock, err := privateOpen(filepath.Join(runtime, "collector.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if err := Serve(runtime); err != nil {
		t.Fatalf("second collector should leave owner alone: %v", err)
	}
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	path := filepath.Join(runtime, "collector.sock")
	os.WriteFile(path, []byte("sentinel"), 0600)
	if err := Serve(runtime); err == nil {
		t.Fatal("ordinary file accepted as stale socket")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "sentinel" {
		t.Fatal("unsafe socket path overwritten")
	}
}
