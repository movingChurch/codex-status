//go:build linux

package telemetry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func ProcessIdentity(pid int) string {
	if pid <= 0 || pid > 2147483647 {
		return ""
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return ""
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return ""
	}
	return "linux:" + fields[19]
}
func bindingProcessExecutable(pid int) string {
	return bindingResolve(fmt.Sprintf("/proc/%d/exe", pid))
}
func bindingOpenRollouts(pid int) ([]openedRollout, bool) {
	folder := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil, false
	}
	if len(entries) > 16384 {
		return nil, false
	}
	paths := []openedRollout{}
	for _, entry := range entries {
		fd := filepath.Join(folder, entry.Name())
		path, err := os.Readlink(fd)
		if err != nil {
			continue
		}
		if !strings.HasSuffix(path, ".jsonl") {
			continue
		}
		info, err := os.Stat(fd)
		if err != nil {
			return nil, false
		}
		paths = append(paths, openedRollout{path, bindingFileIdentity(info)})
	}
	return paths, true
}

func bindingChangeTime(info os.FileInfo) int64 { return info.Sys().(*syscall.Stat_t).Ctim.Nano() }
