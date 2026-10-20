package engine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestClientStreamsAndResizes(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	signals := make(chan os.Signal, 1)
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- renderConnection(client, testRun, &output, func() int { return 42 }, signals) }()
	reader := bufio.NewReader(server)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	json.Unmarshal(line, &request)
	if request["width"] != float64(42) || request["format"] != "ansi" || request["run"] != testRun {
		t.Fatalf("request %s", line)
	}
	signals <- syscall.SIGWINCH
	line, err = reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	request = nil
	json.Unmarshal(line, &request)
	if request["width"] != float64(42) || len(request) != 1 {
		t.Fatalf("resize %s", line)
	}
	server.Write([]byte("fixture ANSI"))
	server.Close()
	if err := <-done; !errors.Is(err, ErrCollectorLost) {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte("fixture ANSI")) || !bytes.HasSuffix(output.Bytes(), []byte("\x1b[0m\x1b[?25h")) {
		t.Fatalf("output %q", output.String())
	}
}
func TestStopSignalEndsRetryWait(t *testing.T) {
	signals := make(chan os.Signal, 2)
	signals <- syscall.SIGWINCH
	signals <- syscall.SIGTERM
	if waitForRetry(signals, time.Minute) {
		t.Fatal("stop signal must end the reconnect wait")
	}
	if !waitForRetry(make(chan os.Signal), time.Millisecond) {
		t.Fatal("elapsed delay must allow a reconnect")
	}
}
func TestEndedRunIsNotReconnected(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "engine-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	if runLive(root, testRun) {
		t.Fatal("run without metadata must not reconnect")
	}
}
func TestEnsureExistingCollector(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "engine-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	runtime, err := ValidateRuntime(filepath.Join(root, "r"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(runtime, "collector.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		c, err := listener.Accept()
		if err == nil {
			c.Close()
		}
	}()
	if err := EnsureCollector(runtime); err != nil {
		t.Fatal(err)
	}
}
