package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationRemovesMatchingLegacyAndKeepsBusyOrEdited(t *testing.T) {
	o, out := fixture(t)
	bin := filepath.Join(o.Home, ".local", "bin")
	libexec := filepath.Join(o.Home, ".local", "libexec", "codex-status", "0.3.0-alpha.1")
	for _, dir := range []string{bin, libexec} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	legacy := "#!/bin/sh\necho 'codex-status 0.3.0-alpha.1'\n"
	busy := filepath.Join(libexec, "codex-status")
	for _, path := range []string{filepath.Join(bin, "codex-status"), busy} {
		if err := os.WriteFile(path, []byte(legacy), 0755); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\n# Shared Claude terminal HUD; native Claude configuration is launch-scoped.\nexec " + busy + " run claude -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "claude-hud"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	blocks := legacyBlocks(o.Home)
	edited := strings.Replace(blocks[1], "auth|", "auth|custom|", 1)
	if err := os.WriteFile(o.zshrc(), []byte("export A=1\n\n"+blocks[0]+"\n"+edited), 0644); err != nil {
		t.Fatal(err)
	}
	o.Busy = func(path string) bool { return path == busy }
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	text := read(t, o.zshrc())
	if strings.Contains(text, "codex-status automatic HUD") || !strings.Contains(text, "auth|custom|") || !strings.Contains(text, blockBegin) {
		t.Fatal("legacy block handling", text)
	}
	for _, gone := range []string{filepath.Join(bin, "codex-status"), filepath.Join(bin, "claude-hud")} {
		if _, err := os.Stat(gone); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("legacy file kept", gone)
		}
	}
	if _, err := os.Stat(busy); err != nil || !strings.Contains(out.String(), "Kept "+libexec) {
		t.Fatal("busy legacy executable removed", out.String())
	}
	backups, _ := filepath.Glob(filepath.Join(o.state(), "backup-*", "*"))
	if len(backups) != 3 {
		t.Fatal("backups", backups)
	}
	o.Busy = func(string) bool { return false }
	if err := Install(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(libexec)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("later install kept the freed legacy executable")
	}
}
