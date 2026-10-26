package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/movingChurch/runtime-status/internal/engine"
)

// An account switch leaves its course in switch.log of the runtime directory:
// the windows it found, where each one's conversation was read, and how it
// ended, so a switch that left windows behind can be read afterwards. The log
// names runs, panes and the account argument only; a word with "@" in it is
// written as <account>, so no address reaches the file.

const (
	switchLogName = "switch.log"
	// switchLogLimit bounds the log; a longer one starts over.
	switchLogLimit = 256 << 10
)

type switchLog struct{ file *os.File }

// openSwitchLog opens the log for appending; a runtime directory that is not
// private, or a log that is not a regular file of this user, gives no log.
func openSwitchLog(runtimeDir string) *switchLog {
	file, err := openSwitchLogFile(runtimeDir)
	if err != nil {
		return &switchLog{}
	}
	return &switchLog{file: file}
}

func openSwitchLogFile(runtimeDir string) (*os.File, error) {
	root, err := engine.ValidateRuntime(runtimeDir)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(root, switchLogName), os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || !info.Mode().IsRegular() || st.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0077 != 0 {
		_ = file.Close()
		return nil, errors.New("unsafe switch log")
	}
	if info.Size() > switchLogLimit {
		_ = file.Truncate(0)
	}
	return file, nil
}

func (l *switchLog) Close() {
	if l.file != nil {
		_ = l.file.Close()
	}
}

func (l *switchLog) printf(format string, args ...any) {
	if l.file == nil {
		return
	}
	line := redactAccounts(fmt.Sprintf(format, args...))
	_, _ = fmt.Fprintf(l.file, "%s pid=%d %s\n", time.Now().Format(time.RFC3339), os.Getpid(), line)
}

// windows records the windows a switch found and each relaunched window's
// conversation source: claude-session, manager, launch-args or none.
func (l *switchLog) windows(kind string, w switchWindows) {
	l.printf("switch %s: %d to relaunch, %d orphaned, %d on another home, caller run %q", kind, len(w.relaunch), len(w.orphaned), len(w.skipped), os.Getenv("RUNTIME_STATUS_RUN"))
	for _, meta := range w.relaunch {
		hud := "on"
		if stringOf(meta["hud_pane"]) == "" {
			hud = "off"
		}
		id := stringOf(meta["id"])
		l.printf("  window run=%s pane=%s hud=%s conversation=%s", id, stringOf(meta["codex_pane"]), hud, w.sources[id])
	}
}

func (l *switchLog) result(err error) {
	if err != nil {
		l.printf("switch failed: %v", err)
		return
	}
	l.printf("switch done")
}

// accountWord is a word with an "@", such as an address in an error.
var accountWord = regexp.MustCompile(`[^\s"'<>(),;:]*@[^\s"'<>(),;:]*`)

// redactAccounts writes each word holding an "@" as <account>.
func redactAccounts(s string) string {
	return accountWord.ReplaceAllString(s, "<account>")
}

// startDetachedSwitch starts `__account-switch` in a session of its own, so it
// outlives the window it relaunches and the process that asked for it. Its
// input is /dev/null and its output goes to the switch log, never to the
// caller's pipes, which close when the caller returns.
func startDetachedSwitch(runtimeDir, self, kind, account string) error {
	cmd := exec.Command(self, "__account-switch", kind, account)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if file, err := openSwitchLogFile(runtimeDir); err == nil {
		defer func() { _ = file.Close() }()
		_, _ = fmt.Fprintf(file, "%s pid=%d %s\n", time.Now().Format(time.RFC3339), os.Getpid(), redactAccounts(fmt.Sprintf("switch %s to %s: started outside the caller's window", kind, account)))
		cmd.Stdout, cmd.Stderr = file, file
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
