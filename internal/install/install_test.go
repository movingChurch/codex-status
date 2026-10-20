package install

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (Options, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "runtime-status")
	if err := os.WriteFile(source, []byte("binary v1"), 0755); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	return Options{Home: home, Source: source, Busy: func(string) bool { return false }, Out: out}, out
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallIsIdempotentAndKeepsUserLines(t *testing.T) {
	o, _ := fixture(t)
	if err := os.WriteFile(o.zshrc(), []byte("export A=1"), 0640); err != nil {
		t.Fatal(err)
	}
	calls := 0
	o.Antigravity = func(binary string) error {
		calls++
		if binary != o.bin() {
			t.Fatal("callback not pointed at the installed binary", binary)
		}
		return nil
	}
	for range 2 {
		if err := Install(o); err != nil {
			t.Fatal(err)
		}
	}
	text := read(t, o.zshrc())
	if strings.Count(text, blockBegin) != 1 || !strings.HasPrefix(text, "export A=1\n\n") || !strings.Contains(text, "agy() { __runtime_status_wrap antigravity agy") {
		t.Fatal(text)
	}
	if info, _ := os.Stat(o.zshrc()); info.Mode().Perm() != 0640 {
		t.Fatal("zshrc mode changed", info.Mode())
	}
	if read(t, o.bin()) != "binary v1" || calls != 2 {
		t.Fatal("binary or antigravity step missing")
	}
}

func TestInterruptedInstallCompletesOnRetry(t *testing.T) {
	o, _ := fixture(t)
	failing := errors.New("interrupted")
	o.Antigravity = func(string) error { return failing }
	if err := Install(o); !errors.Is(err, failing) {
		t.Fatal(err)
	}
	if _, err := os.Stat(o.zshrc()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("shell block written before earlier steps finished")
	}
	o.Antigravity = func(string) error { return nil }
	if err := Install(o); err != nil || !strings.Contains(read(t, o.zshrc()), blockBegin) {
		t.Fatal(err)
	}
}

func TestSymlinkedZshrcRefused(t *testing.T) {
	o, _ := fixture(t)
	target := filepath.Join(o.Home, "dotfiles-zshrc")
	if err := os.WriteFile(target, []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, o.zshrc()); err != nil {
		t.Fatal(err)
	}
	if err := Install(o); err == nil || !strings.Contains(err.Error(), "symlink") || read(t, target) != "x\n" {
		t.Fatal("symlinked zshrc changed", err)
	}
}

func TestMissingAntigravityIsReported(t *testing.T) {
	o, out := fixture(t)
	if err := Install(o); err != nil || !strings.Contains(out.String(), "agy not on PATH") {
		t.Fatal(err, out.String())
	}
}

func TestUninstallAfterMigration(t *testing.T) {
	o, _ := fixture(t)
	if err := os.WriteFile(o.zshrc(), []byte("export A=1\n\n"+legacyBlocks(o.Home)[0]), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	restored := false
	if err := Uninstall(o, func() error { restored = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if text := read(t, o.zshrc()); text != "export A=1\n" {
		t.Fatalf("%q", text)
	}
	if _, err := os.Stat(o.bin()); !errors.Is(err, os.ErrNotExist) || !restored {
		t.Fatal("binary kept or antigravity not restored")
	}
	if backups, _ := filepath.Glob(filepath.Join(o.state(), "backup-*", "*")); len(backups) != 1 {
		t.Fatal("backups removed", backups)
	}
}
