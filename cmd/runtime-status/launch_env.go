package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/movingChurch/runtime-status/internal/engine"
)

const launchEnvironmentLimit = 1 << 20
const launchEnvironmentTimeout = 5 * time.Second

type launchEnvironment struct {
	listener *net.UnixListener
	path     string
	info     os.FileInfo
	mu       sync.Mutex
	conn     *net.UnixConn
	closed   bool
	done     chan struct{}
	err      error
}

func launchEnvironmentPath(root, id string) (string, error) {
	if !uuidPattern.MatchString(id) {
		return "", errors.New("invalid launch identifier")
	}
	root, err := engine.ValidateRuntime(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "launch-"+id+".sock"), nil
}

func validateLaunchEnvironment(environment []string) error {
	if environment == nil {
		return errors.New("missing launch environment")
	}
	for _, entry := range environment {
		if strings.IndexByte(entry, '=') < 1 || strings.IndexByte(entry, 0) >= 0 {
			return errors.New("invalid launch environment entry")
		}
	}
	return nil
}

func startLaunchEnvironment(root, id string, environment []string) (*launchEnvironment, error) {
	if err := validateLaunchEnvironment(environment); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(environment)
	if err != nil {
		return nil, err
	}
	if len(payload) > launchEnvironmentLimit {
		return nil, errors.New("launch environment too large")
	}
	path, err := launchEnvironmentPath(root, id)
	if err != nil {
		return nil, err
	}
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		return nil, errors.New("launch environment path already exists")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	info, err := os.Lstat(path)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	relay := &launchEnvironment{listener: listener, path: path, info: info, done: make(chan struct{})}
	if err = os.Chmod(path, 0600); err != nil {
		relay.Close()
		return nil, err
	}
	deadline := time.Now().Add(launchEnvironmentTimeout)
	if err = listener.SetDeadline(deadline); err != nil {
		relay.Close()
		return nil, err
	}
	go func() {
		relay.err = relay.send(payload, deadline)
		relay.Close()
		close(relay.done)
	}()
	return relay, nil
}

func (relay *launchEnvironment) send(payload []byte, deadline time.Time) error {
	conn, err := relay.listener.AcceptUnix()
	if err != nil {
		return err
	}
	relay.mu.Lock()
	if relay.closed {
		relay.mu.Unlock()
		_ = conn.Close()
		return net.ErrClosed
	}
	relay.conn = conn
	relay.mu.Unlock()
	defer func() { _ = conn.Close() }()
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	if _, err = io.Copy(conn, strings.NewReader(string(payload))); err != nil {
		return err
	}
	if err = conn.CloseWrite(); err != nil {
		return err
	}
	var ack [1]byte
	if _, err = io.ReadFull(conn, ack[:]); err != nil {
		return err
	}
	if ack[0] != 1 {
		return errors.New("invalid launch environment acknowledgement")
	}
	return nil
}

func (relay *launchEnvironment) Wait(deadline time.Time) error {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-relay.done:
		return relay.err
	case <-timer.C:
		relay.Close()
		<-relay.done
		return errors.New("launch environment timed out")
	}
}

func (relay *launchEnvironment) Close() {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	relay.closed = true
	_ = relay.listener.Close()
	if relay.conn != nil {
		_ = relay.conn.Close()
	}
	if info, err := os.Lstat(relay.path); err == nil && os.SameFile(relay.info, info) {
		_ = os.Remove(relay.path)
	}
}

func receiveLaunchEnvironment(root, id string) ([]string, error) {
	path, err := launchEnvironmentPath(root, id)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		return nil, errors.New("unsafe launch environment socket")
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if err = conn.SetDeadline(time.Now().Add(launchEnvironmentTimeout)); err != nil {
		return nil, err
	}
	payload, err := io.ReadAll(io.LimitReader(conn, launchEnvironmentLimit+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > launchEnvironmentLimit {
		return nil, errors.New("launch environment too large")
	}
	var environment []string
	if err = json.Unmarshal(payload, &environment); err != nil {
		return nil, errors.New("invalid launch environment payload")
	}
	if err = validateLaunchEnvironment(environment); err != nil {
		return nil, err
	}
	if _, err = conn.Write([]byte{1}); err != nil {
		return nil, err
	}
	return environment, nil
}
