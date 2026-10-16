package telemetry

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
)

const bindingHeaderLimit = 262144

var rolloutName = regexp.MustCompile(`^rollout-.*-([0-9a-fA-F-]{36})\.jsonl$`)

type rolloutIdentity struct {
	dev uint64
	ino uint64
}
type openedRollout struct {
	path     string
	identity rolloutIdentity
}

func bindingFileIdentity(info os.FileInfo) rolloutIdentity {
	stat := info.Sys().(*syscall.Stat_t)
	return rolloutIdentity{uint64(stat.Dev), stat.Ino}
}
func bindingFingerprint(info os.FileInfo) string {
	return fmt.Sprintf("%v:%d:%d:%d", bindingFileIdentity(info), info.ModTime().UnixNano(), bindingChangeTime(info), info.Size())
}
func bindingResolve(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return ""
	}
	return resolved
}
func bindingUnder(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
func bindingUUID(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	s = strings.TrimPrefix(s, "urn:uuid:")
	s = strings.Trim(s, "{}")
	s = strings.ReplaceAll(s, "-", "")
	if len(s) != 32 {
		return ""
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return ""
		}
	}
	s = strings.ToLower(s)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

// AuthEpoch fingerprints credential metadata without opening credential contents.
func AuthEpoch(home string) string {
	path, err := filepath.Abs(home)
	if err != nil {
		return "unavailable"
	}
	if resolved := bindingResolve(path); resolved != "" {
		path = resolved
	}
	info, err := os.Stat(filepath.Join(path, "auth.json"))
	if os.IsNotExist(err) {
		return path + ":missing"
	}
	if err != nil {
		return "unavailable"
	}
	return fmt.Sprintf("%s:present:%v:%d:%d", path, bindingFileIdentity(info), info.ModTime().UnixNano(), info.Size())
}

var bindingCache = struct {
	sync.Mutex
	values map[string]map[string]any
	order  []string
}{values: make(map[string]map[string]any)}

func bindingReadMeta(path string, expected rolloutIdentity) map[string]any {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || bindingFileIdentity(info) != expected {
		return nil
	}
	key := path + ":" + bindingFingerprint(info)
	bindingCache.Lock()
	cached := bindingCache.values[key]
	bindingCache.Unlock()
	if cached != nil {
		return cached
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || bindingFileIdentity(opened) != expected {
		return nil
	}
	line, err := bufio.NewReader(io.LimitReader(file, bindingHeaderLimit+1)).ReadBytes('\n')
	if err != nil || len(line) > bindingHeaderLimit {
		return nil
	}
	finished, err := file.Stat()
	if err != nil || bindingFingerprint(opened) != bindingFingerprint(finished) {
		return nil
	}
	var record map[string]any
	if json.Unmarshal(line, &record) != nil || record["type"] != "session_meta" {
		return nil
	}
	payload, ok := record["payload"].(map[string]any)
	if !ok {
		return nil
	}
	safe := make(map[string]any)
	for _, field := range []string{"id", "session_id", "parent_thread_id", "cwd", "cli_version"} {
		value := payload[field]
		if value == nil {
			safe[field] = nil
		} else if s, ok := value.(string); ok && len(s) <= 4096 {
			safe[field] = s
		} else {
			safe[field] = "invalid"
		}
	}
	safe["source"] = "other"
	if source, ok := payload["source"].(string); ok && source == "cli" {
		safe["source"] = "cli"
	}
	bindingCache.Lock()
	if _, exists := bindingCache.values[key]; !exists {
		bindingCache.order = append(bindingCache.order, key)
	}
	bindingCache.values[key] = safe
	for len(bindingCache.order) > 128 {
		delete(bindingCache.values, bindingCache.order[0])
		bindingCache.order = bindingCache.order[1:]
	}
	bindingCache.Unlock()
	return safe
}
func bindingRootCandidate(open openedRollout, sessions, cwd string) string {
	info, err := os.Lstat(open.path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	path := bindingResolve(open.path)
	if path == "" || !bindingUnder(path, sessions) {
		return ""
	}
	match := rolloutName.FindStringSubmatch(filepath.Base(path))
	if match == nil {
		return ""
	}
	meta := bindingReadMeta(path, open.identity)
	// Versions are diagnostic metadata. Admission depends on the bounded root
	// contract and exact process/file/session identity, including unknown releases.
	if meta == nil || meta["source"] != "cli" || meta["parent_thread_id"] != nil {
		return ""
	}
	id := bindingUUID(meta["id"])
	if id == "" || id != bindingUUID(meta["session_id"]) || id != bindingUUID(match[1]) {
		return ""
	}
	metaCwd, ok := meta["cwd"].(string)
	if !ok || bindingResolve(metaCwd) != cwd {
		return ""
	}
	return path
}
func bindingPID(value any) int {
	switch p := value.(type) {
	case int:
		if p > 0 && p <= 2147483647 {
			return p
		}
	case int64:
		if p > 0 && p <= 2147483647 {
			return int(p)
		}
	case float64:
		if p > 0 && p <= 2147483647 && p == float64(int(p)) {
			return int(p)
		}
	case json.Number:
		i, e := p.Int64()
		if e == nil && i > 0 && i <= 2147483647 {
			return int(i)
		}
	}
	return 0
}

// BindRun accepts only a unique root rollout held by the exact launched process.
func BindRun(meta map[string]any) string {
	path, _ := BindRunStatus(meta)
	return path
}

// BindRunStatus distinguishes startup waiting from a known binding failure.
func BindRunStatus(meta map[string]any) (string, string) {
	pid := bindingPID(meta["pid"])
	started, ok := meta["started"].(string)
	if pid == 0 || !ok || started == "" || ProcessIdentity(pid) != started {
		return "", "process-ended"
	}
	expected := ""
	if meta["expected_session"] != nil {
		expected = bindingUUID(meta["expected_session"])
		if expected == "" {
			return "", "invalid-session"
		}
	}
	homeValue, hOK := meta["home"].(string)
	cwdValue, cOK := meta["cwd"].(string)
	if !hOK || !cOK {
		return "", "invalid-path"
	}
	home := bindingResolve(homeValue)
	cwd := bindingResolve(cwdValue)
	sessions := bindingResolve(filepath.Join(home, "sessions"))
	if home == "" || cwd == "" || sessions == "" || !bindingUnder(sessions, home) {
		return "", "pending"
	}
	if meta["executable"] != nil {
		exe, ok := meta["executable"].(string)
		if !ok {
			return "", "process-mismatch"
		}
		resolved := bindingResolve(exe)
		if resolved == "" || bindingProcessExecutable(pid) != resolved {
			return "", "process-mismatch"
		}
	}
	open, ok := bindingOpenRollouts(pid)
	if !ok {
		return "", "inspection-failed"
	}
	candidates := make(map[string]bool)
	for _, file := range open {
		if path := bindingRootCandidate(file, sessions, cwd); path != "" {
			candidates[path] = true
		}
	}
	if ProcessIdentity(pid) != started {
		return "", "process-ended"
	}
	if len(candidates) > 1 {
		return "", "ambiguous-root"
	}
	if len(candidates) == 0 {
		if len(open) > 0 {
			return "", "unsupported-root"
		}
		return "", "pending"
	}
	for path := range candidates {
		if expected != "" && bindingUUID(rolloutName.FindStringSubmatch(filepath.Base(path))[1]) != expected {
			return "", "resume-mismatch"
		}
		return path, ""
	}
	return "", "pending"
}
