package main

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func launchTestRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("TMPDIR", "/tmp")
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLaunchEnvironmentDistinct(t *testing.T) {
	root := launchTestRoot(t)
	for _, value := range []string{"first", "second\n'\"$literal"} {
		expected := []string{"ACCOUNT=" + value, "EMPTY="}
		id := newID()
		relay, err := startLaunchEnvironment(root, id, expected)
		if err != nil {
			t.Fatal(err)
		}
		defer relay.Close()
		got, err := receiveLaunchEnvironment(root, id)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatal("environment changed")
		}
		if err = relay.Wait(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatal("relay did not clean up")
		}
	}
}

func TestLaunchEnvironmentConcurrent(t *testing.T) {
	root := launchTestRoot(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := newID()
			expected := []string{"ACCOUNT=" + id}
			relay, err := startLaunchEnvironment(root, id, expected)
			if err != nil {
				t.Error(err)
				return
			}
			defer relay.Close()
			got, err := receiveLaunchEnvironment(root, id)
			if err != nil {
				t.Error(err)
				return
			}
			if !reflect.DeepEqual(got, expected) {
				t.Error("crossed environments")
			}
			if err = relay.Wait(time.Now().Add(time.Second)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestLaunchEnvironmentRejectsInvalid(t *testing.T) {
	root := launchTestRoot(t)
	for _, env := range [][]string{nil, {"NO_EQUALS"}, {"=empty"}, {"KEY=bad\x00"}, {"BIG=" + strings.Repeat("x", launchEnvironmentLimit)}} {
		if relay, err := startLaunchEnvironment(root, newID(), env); err == nil {
			relay.Close()
			t.Fatal("accepted invalid environment")
		}
	}
	id := newID()
	path := filepath.Join(root, "launch-"+id+".sock")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := startLaunchEnvironment(root, id, []string{"A=b"}); err == nil {
		t.Fatal("accepted symlink")
	}
	if _, err := receiveLaunchEnvironment(root, id); err == nil {
		t.Fatal("received symlink")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatal("removed foreign path")
	}
}

func TestLaunchEnvironmentClose(t *testing.T) {
	root := launchTestRoot(t)
	relay, err := startLaunchEnvironment(root, newID(), []string{"A=b"})
	if err != nil {
		t.Fatal(err)
	}
	relay.Close()
	if err = relay.Wait(time.Now().Add(time.Second)); err == nil {
		t.Fatal("closed relay succeeded")
	}
}

func TestLaunchEnvironmentMalformedInput(t *testing.T) {
	for _, payload := range []string{"null", `["bad"]`, `["A=b"] trailing`, strings.Repeat("x", launchEnvironmentLimit+1)} {
		root := launchTestRoot(t)
		id := newID()
		path := filepath.Join(root, "launch-"+id+".sock")
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(path, 0600); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			conn, e := listener.AcceptUnix()
			if e != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			_, _ = conn.Write([]byte(payload))
			_ = conn.CloseWrite()
		}()
		if _, err = receiveLaunchEnvironment(root, id); err == nil {
			t.Error("accepted malformed payload")
		}
		_ = listener.Close()
		<-done
	}
}

func TestLaunchEnvironmentActive(t *testing.T) {
	root := launchTestRoot(t)
	id := newID()
	relay, err := startLaunchEnvironment(root, id, []string{"A=b"})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: relay.path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	relay.Close()
	if err = relay.Wait(time.Now().Add(time.Second)); err == nil {
		t.Fatal("unacknowledged transfer succeeded")
	}
}

func TestLaunchEnvironmentReplaced(t *testing.T) {
	root := launchTestRoot(t)
	relay, err := startLaunchEnvironment(root, newID(), []string{"A=b"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(relay.path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(relay.path, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	relay.Close()
	if _, err = os.Stat(relay.path); err != nil {
		t.Fatal("removed replacement")
	}
	_ = relay.Wait(time.Now().Add(time.Second))
}
