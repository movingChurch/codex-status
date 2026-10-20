package engine

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

func collectorConnect(runtime string) (net.Conn, error) {
	return collectorConnectTimeout(runtime, time.Second)
}
func collectorConnectTimeout(runtime string, timeout time.Duration) (net.Conn, error) {
	path := filepath.Join(runtime, "collector.sock")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Getuid()) || info.Mode()&os.ModeSocket == 0 {
		return nil, errors.New("unsafe collector socket")
	}
	return net.DialTimeout("unix", path, timeout)
}
func EnsureCollector(runtime string) error {
	runtime, err := ValidateRuntime(runtime)
	if err != nil {
		return err
	}
	lock, err := privateOpen(filepath.Join(runtime, "startup.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if c, err := collectorConnect(runtime); err == nil {
		c.Close()
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	child := exec.Command(executable, "__collector", runtime)
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = child.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if c, err := collectorConnect(runtime); err == nil {
				c.Close()
				return nil
			}
		case <-exited:
			return errors.New("collector exited before becoming available")
		case <-deadline.C:
			return errors.New("collector unavailable")
		}
	}
}
func terminalWidth() int {
	width := 80
	for _, file := range []*os.File{os.Stdout, os.Stdin} {
		var size [4]uint16
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&size)))
		if errno == 0 && size[1] > 0 {
			width = int(size[1])
			break
		}
	}
	if width > 1 {
		width--
	}
	if width > 500 {
		width = 500
	}
	return width
}

// ErrCollectorLost reports a render connection that ended without a stop signal.
var ErrCollectorLost = errors.New("collector connection lost")

// RenderClient streams one run's frames; a lost manager is restarted and the
// view reconnects for as long as the native session runs.
func RenderClient(runtime, run string) error {
	if !validRunID(run) {
		return errors.New("invalid run identifier")
	}
	runtime, err := ValidateRuntime(runtime)
	if err != nil {
		return err
	}
	notices := make(chan os.Signal, 8)
	signal.Notify(notices, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	defer signal.Stop(notices)
	backoff := 250 * time.Millisecond
	for {
		conn, err := collectorConnect(runtime)
		if err == nil {
			err = renderConnection(conn, run, os.Stdout, terminalWidth, notices)
			if !errors.Is(err, ErrCollectorLost) {
				return err
			}
			backoff = 250 * time.Millisecond
		}
		if !runLive(runtime, run) || !waitForRetry(notices, backoff) {
			return nil
		}
		backoff = min(2*backoff, 5*time.Second)
		_ = EnsureCollector(runtime)
	}
}
func runLive(runtime, run string) bool {
	meta, err := ReadMetadata(runtime, run)
	return err == nil && liveMetadata(meta)
}

// waitForRetry waits out the delay; a stop signal ends the view instead.
func waitForRetry(notices <-chan os.Signal, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return true
		case sig := <-notices:
			if sig != syscall.SIGWINCH {
				return false
			}
		}
	}
}
func renderConnection(conn net.Conn, run string, out io.Writer, width func() int, notices <-chan os.Signal) error {
	defer conn.Close()
	encoder := json.NewEncoder(conn)
	send := func(initial bool) error {
		request := map[string]any{"width": width()}
		if initial {
			request["v"] = 1
			request["run"] = run
			request["format"] = "ansi"
		}
		conn.SetWriteDeadline(time.Now().Add(time.Second))
		return encoder.Encode(request)
	}
	if err := send(true); err != nil {
		return err
	}
	defer io.WriteString(out, "\x1b[0m\x1b[?25h")
	if _, err := io.WriteString(out, "\x1b[?25l\x1b[2J"); err != nil {
		return err
	}
	chunks := make(chan []byte)
	readError := make(chan error, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		buffer := make([]byte, maxMessage)
		for {
			n, err := conn.Read(buffer)
			if n > 0 {
				data := append([]byte(nil), buffer[:n]...)
				select {
				case chunks <- data:
				case <-stop:
					return
				}
			}
			if err != nil {
				readError <- err
				return
			}
		}
	}()
	for {
		select {
		case data := <-chunks:
			if _, err := out.Write(data); err != nil {
				return err
			}
		case <-readError:
			return ErrCollectorLost
		case sig := <-notices:
			if sig == syscall.SIGWINCH {
				if err := send(false); err != nil {
					return err
				}
			} else {
				return nil
			}
		}
	}
}
