package engine

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const testRun = "12345678-1234-1234-1234-123456789abc"

func testMeta() map[string]any {
	return map[string]any{"v": 1, "id": testRun, "home": "/synthetic", "cwd": "/synthetic", "tmux_socket": "/synthetic/socket", "pid": 123, "started": "identity", "launched_at": 1, "hud_pane": "%1"}
}
func TestMetadataRoundTripAndBoundaries(t *testing.T) {
	runtime := filepath.Join(t.TempDir(), "private")
	path, err := WriteMetadata(runtime, testMeta())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ReadMetadata(runtime, testRun); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadMetadata(runtime, testRun); err == nil {
		t.Fatal("public metadata accepted")
	}
	if _, err = ReadMetadata(runtime, "../bad"); err == nil {
		t.Fatal("invalid identifier accepted")
	}
	meta := testMeta()
	meta["padding"] = strings.Repeat("x", 16384)
	if _, err = WriteMetadata(runtime, meta); err == nil {
		t.Fatal("oversized metadata accepted")
	}
}
func TestRejectSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	os.Mkdir(target, 0700)
	link := filepath.Join(root, "link")
	os.Symlink(target, link)
	if _, err := ValidateRuntime(link); err == nil {
		t.Fatal("runtime symlink accepted")
	}
	path, err := WriteMetadata(target, testMeta())
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	os.Symlink("/dev/null", path)
	if _, err := ReadMetadata(target, testRun); err == nil {
		t.Fatal("metadata symlink accepted")
	}
}

func TestMetadataRejectsInvalidFields(t *testing.T) {
	for key, value := range map[string]any{"v": 2, "home": "relative", "cwd": false, "tmux_socket": "relative", "pid": 1.5, "started": 123, "launched_at": "now", "hud_pane": "1"} {
		t.Run(key, func(t *testing.T) {
			meta := testMeta()
			meta[key] = value
			runtime := filepath.Join(t.TempDir(), "r")
			if _, err := WriteMetadata(runtime, meta); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadMetadata(runtime, testRun); err == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
}
func TestPrivateFileRejectsPublicLock(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "lock")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if f, err := privateOpen(path); err == nil {
		f.Close()
		t.Fatal("public lock accepted")
	}
}

func TestPrivateFileRejectsFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if f, err := openPrivate(path, syscall.O_RDONLY); err == nil {
		f.Close()
		t.Fatal("FIFO accepted")
	}
}

// A window drawn without HUD rows keeps hud_pane "" and stays listed, so an
// account switch still finds it.
func TestMetadataAcceptsWindowWithoutHUD(t *testing.T) {
	meta := testMeta()
	meta["hud_pane"] = ""
	runtime := filepath.Join(t.TempDir(), "r")
	if _, err := WriteMetadata(runtime, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMetadata(runtime, testRun); err != nil {
		t.Fatal(err)
	}
}
