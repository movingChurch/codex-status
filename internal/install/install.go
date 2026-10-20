// Package install places the executable and its zsh integration, and migrates earlier installs.
package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Product names the installed command, its state directory and its shell block.
const Product = "runtime-status"

const (
	blockBegin = "# BEGIN " + Product + " shell integration"
	blockEnd   = "# END " + Product + " shell integration"
)

// Options carries the machine-specific steps so tests run against an isolated HOME.
type Options struct {
	Home   string
	Source string
	// Antigravity installs the callback for the installed binary; nil when agy is absent.
	Antigravity func(binary string) error
	// Busy reports whether a running process executes from or names a path.
	Busy func(path string) bool
	Out  io.Writer
}

func (o Options) bin() string   { return filepath.Join(o.Home, ".local", "bin", Product) }
func (o Options) state() string { return filepath.Join(o.Home, ".local", "state", Product) }
func (o Options) zshrc() string { return filepath.Join(o.Home, ".zshrc") }

// Block returns the zsh functions; each falls back to the native command outside
// an interactive terminal or once the binary is gone.
func Block(bin string) string {
	q := "'" + strings.ReplaceAll(bin, "'", `'\''`) + "'"
	return blockBegin + `
# Managed by ` + "`" + Product + " install`" + `; remove with ` + "`" + Product + " uninstall`" + `.
__runtime_status_wrap() {
  local runtime=$1 native=$2
  shift 2
  if [[ -o interactive && -t 0 && -t 1 && -x ` + q + ` ]]; then
    ` + q + ` wrap "$runtime" "$@"
  else
    command "$native" "$@"
  fi
}
codex() { __runtime_status_wrap codex codex "$@"; }
claude() { __runtime_status_wrap claude claude "$@"; }
agy() { __runtime_status_wrap antigravity agy "$@"; }
` + blockEnd + "\n"
}

// Install runs each step in order under one lock; repeating it changes nothing,
// and an interrupted run is completed by the next one.
func Install(o Options) error {
	unlock, err := lock(o.state())
	if err != nil {
		return err
	}
	defer unlock()
	if err = copyBinary(o.Source, o.bin()); err != nil {
		return fmt.Errorf("binary: %w", err)
	}
	if o.Antigravity == nil {
		_, _ = fmt.Fprintln(o.Out, "Antigravity: agy not on PATH, callback setup skipped")
	} else if err = o.Antigravity(o.bin()); err != nil {
		return fmt.Errorf("antigravity: %w", err)
	}
	backup := filepath.Join(o.state(), "backup-"+time.Now().Format("20060102150405"))
	if err = editZshrc(o.zshrc(), func(text string) string {
		for _, legacy := range legacyBlocks(o.Home) {
			if strings.Contains(text, legacy) {
				if saveBackup(backup, "zshrc-block-"+fmt.Sprint(len(legacy)), []byte(legacy)) == nil {
					text = strings.Replace(text, legacy, "", 1)
				}
			}
		}
		return withBlock(text, Block(o.bin()))
	}); err != nil {
		return fmt.Errorf("zshrc: %w", err)
	}
	removeLegacy(o, backup)
	_, _ = fmt.Fprintf(o.Out, "Installed %s\n", o.bin())
	return nil
}

// Uninstall removes the block, restores Antigravity through restore and removes
// the binary; backups stay, and migrated old blocks are never restored.
func Uninstall(o Options, restore func() error) error {
	unlock, err := lock(o.state())
	if err != nil {
		return err
	}
	defer unlock()
	if _, statErr := os.Lstat(o.zshrc()); statErr == nil {
		if err = editZshrc(o.zshrc(), func(text string) string { return withBlock(text, "") }); err != nil {
			return fmt.Errorf("zshrc: %w", err)
		}
	}
	if restore != nil {
		if err = restore(); err != nil {
			return fmt.Errorf("antigravity: %w", err)
		}
	}
	if err = os.Remove(o.bin()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, _ = fmt.Fprintf(o.Out, "Uninstalled %s\n", o.bin())
	return nil
}

func lock(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "install.lock"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

func copyBinary(source, target string) error {
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("target is a symlink; preserve or move it first")
	}
	a, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if b, err := os.ReadFile(target); err == nil && bytes.Equal(a, b) {
		return nil
	}
	return atomicWrite(target, a, 0755)
}

// withBlock replaces the marked block, appends it when absent, or removes it when block is empty.
func withBlock(text, block string) string {
	start := strings.Index(text, blockBegin)
	if start >= 0 {
		if end := strings.Index(text[start:], blockEnd); end >= 0 {
			end += start + len(blockEnd)
			if end < len(text) && text[end] == '\n' {
				end++
			}
			head := text[:start]
			if block == "" && strings.HasSuffix(head, "\n\n") {
				head = head[:len(head)-1] // drop the blank separator added on append
			}
			return head + block + text[end:]
		}
	}
	if block == "" {
		return text
	}
	for text != "" && !strings.HasSuffix(text, "\n\n") {
		text += "\n"
	}
	return text + block
}

func editZshrc(path string, edit func(string) string) error {
	mode := os.FileMode(0644)
	info, err := os.Lstat(path)
	switch {
	case err == nil && info.Mode()&os.ModeSymlink != 0:
		return errors.New("~/.zshrc is a symlink; add the block to its target by hand")
	case err == nil && !info.Mode().IsRegular():
		return errors.New("~/.zshrc is not a regular file")
	case err == nil:
		mode = info.Mode().Perm()
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	text := edit(string(old))
	if text == string(old) {
		return nil
	}
	return atomicWrite(path, []byte(text), mode)
}

func atomicWrite(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(b); err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func saveBackup(dir, name string, b []byte) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), b, 0600)
}
