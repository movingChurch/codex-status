package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const maxMessage = 16384

func ValidateRuntime(path string) (string, error) {
	if path == "" {
		path = filepath.Join("/tmp", fmt.Sprintf("runtime-status-%d", os.Getuid()))
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err = os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || !ownedPrivate(info) {
		return "", errors.New("unsafe runtime directory")
	}
	return path, nil
}
func ownedPrivate(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Getuid()) && info.Mode().Perm()&0077 == 0
}
func privateOpen(path string) (*os.File, error) {
	return openPrivate(path, syscall.O_CREAT|syscall.O_RDWR)
}
func openPrivate(path string, flags int) (*os.File, error) {
	fd, err := syscall.Open(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || !ownedPrivate(info) {
		file.Close()
		return nil, errors.New("unsafe runtime file")
	}
	return file, nil
}
func validRunID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func metadataPath(runtime, run string) (string, error) {
	if !validRunID(run) {
		return "", errors.New("invalid run identifier")
	}
	root, err := ValidateRuntime(runtime)
	if err != nil {
		return "", err
	}
	runs, err := ValidateRuntime(filepath.Join(root, "runs"))
	if err != nil {
		return "", err
	}
	return filepath.Join(runs, run+".json"), nil
}
func WriteMetadata(runtime string, meta map[string]any) (string, error) {
	id, _ := meta["id"].(string)
	path, err := metadataPath(runtime, id)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	if len(raw) > maxMessage {
		return "", errors.New("metadata too large")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
func ReadMetadata(runtime, run string) (map[string]any, error) {
	path, err := metadataPath(runtime, run)
	if err != nil {
		return nil, err
	}
	f, err := openPrivate(path, syscall.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxMessage+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxMessage {
		return nil, errors.New("metadata too large")
	}
	var meta map[string]any
	if err = json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	if meta["v"] != float64(1) || meta["id"] != run {
		return nil, errors.New("unsupported metadata")
	}
	for _, key := range []string{"home", "cwd", "tmux_socket"} {
		value, ok := meta[key].(string)
		if !ok || !filepath.IsAbs(value) {
			return nil, errors.New("invalid path field")
		}
	}
	pid, ok := meta["pid"].(float64)
	if !ok || pid < 1 || math.Trunc(pid) != pid || pid > float64(math.MaxInt32) {
		return nil, errors.New("invalid process identity")
	}
	if _, ok := meta["started"].(string); !ok {
		return nil, errors.New("invalid process identity")
	}
	if stamp, ok := meta["launched_at"].(float64); !ok || math.IsNaN(stamp) || math.IsInf(stamp, 0) {
		return nil, errors.New("invalid launch time")
	}
	pane, ok := meta["hud_pane"].(string)
	if !ok || len(pane) == 0 || pane[0] != '%' {
		return nil, errors.New("invalid pane identity")
	}
	return meta, nil
}

// ListMetadata returns the metadata of every run whose process still lives.
func ListMetadata(runtime string) []map[string]any {
	root, err := ValidateRuntime(runtime)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(root, "runs"))
	if err != nil {
		return nil
	}
	metas := []map[string]any{}
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !validRunID(id) {
			continue
		}
		if meta, err := ReadMetadata(runtime, id); err == nil && liveMetadata(meta) {
			metas = append(metas, meta)
		}
	}
	return metas
}
