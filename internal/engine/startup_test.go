package engine

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test executable acts only as a synthetic listening collector in children.
func TestMain(m *testing.M) {
	if os.Getenv("ENGINE_TEST_COLLECTOR") == "1" && len(os.Args) == 3 && os.Args[1] == "__collector" {
		runtime := os.Args[2]
		f, err := os.OpenFile(filepath.Join(runtime, "starts"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(2)
		}
		fmt.Fprintln(f, os.Getpid())
		f.Close()
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(runtime, "collector.sock"), Net: "unix"})
		if err != nil {
			os.Exit(2)
		}
		defer listener.Close()
		listener.SetDeadline(time.Now().Add(5 * time.Second))
		for {
			conn, err := listener.Accept()
			if err != nil {
				os.Exit(0)
			}
			conn.SetReadDeadline(time.Now().Add(time.Second))
			var command [1]byte
			n, _ := conn.Read(command[:])
			conn.Close()
			if n == 1 && command[0] == 'q' {
				os.Exit(0)
			}
		}
	}
	os.Exit(m.Run())
}
func TestConcurrentStartupLaunchesOneFakeCollector(t *testing.T) {
	runtime, err := os.MkdirTemp("/tmp", "engine-start-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(runtime)
	t.Setenv("ENGINE_TEST_COLLECTOR", "1")
	defer func() {
		if conn, err := net.DialTimeout("unix", filepath.Join(runtime, "collector.sock"), time.Second); err == nil {
			conn.Write([]byte("q"))
			conn.Close()
		}
	}()
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- EnsureCollector(runtime) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(runtime, "starts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Fields(string(raw))) != 1 {
		t.Fatalf("duplicate collector processes: %q", raw)
	}
}
