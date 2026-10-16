// Package setup installs and restores the single managed statusLine setting.
package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const maxSettings = 1 << 20

// ErrConflict means the managed setting was changed outside setup.
var ErrConflict = errors.New("statusLine settings conflict")

type selected struct {
	Present bool            `json:"present"`
	Value   json.RawMessage `json:"value,omitempty"`
}
type journal struct {
	Version   int      `json:"version"`
	Target    string   `json:"target"`
	Phase     string   `json:"phase"`
	Before    selected `json:"before"`
	Installed selected `json:"installed"`
}
type transaction struct {
	target, record string
	lock           *os.File
	beforeReplace  func() // deterministic staging boundary for package tests
}

func safeFile(path string) (os.FileInfo, error) {
	i, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !i.Mode().IsRegular() {
		return nil, fmt.Errorf("unsafe nonregular file: %s", path)
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok || int(s.Uid) != os.Getuid() {
		return nil, fmt.Errorf("unsafe file owner: %s", path)
	}
	return i, nil
}
func begin(target, dir string) (*transaction, error) {
	abs, e := filepath.Abs(target)
	if e != nil {
		return nil, e
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(abs))
	if e != nil {
		return nil, e
	}
	abs = filepath.Join(parent, filepath.Base(abs))
	if _, e = safeFile(abs); e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	i, e := os.Lstat(dir)
	if e != nil {
		return nil, e
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0077 != 0 || !ok || int(s.Uid) != os.Getuid() {
		return nil, errors.New("journal directory must be private and owned by current user")
	}
	sum := sha256.Sum256([]byte(abs))
	base := filepath.Join(dir, hex.EncodeToString(sum[:]))
	lp := base + ".lock"
	fd, e := syscall.Open(lp, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), lp)
	info, statErr := f.Stat()
	pathInfo, pathErr := safeFile(lp)
	if statErr != nil || pathErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !os.SameFile(info, pathInfo) {
		e = errors.New("unsafe lock file")
		_ = f.Close()
		return nil, e
	}
	if e = syscall.Flock(fd, syscall.LOCK_EX); e != nil {
		_ = f.Close()
		return nil, e
	}
	// The pathname must still designate the locked inode after waiting.
	pathInfo, e = safeFile(lp)
	if e != nil || !os.SameFile(info, pathInfo) {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = f.Close()
		return nil, errors.New("lock file changed while acquiring lock")
	}
	return &transaction{target: abs, record: base + ".json", lock: f}, nil
}
func (t *transaction) close() {
	_ = syscall.Flock(int(t.lock.Fd()), syscall.LOCK_UN)
	_ = t.lock.Close()
}

// readVersion opens without following symlinks or blocking on a FIFO, and
// binds the bytes and identity to the same validated descriptor.
func readVersion(path string) ([]byte, os.FileInfo, error) {
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	i, e := f.Stat()
	if e != nil {
		return nil, nil, e
	}
	owner, ok := i.Sys().(*syscall.Stat_t)
	if !i.Mode().IsRegular() || !ok || int(owner.Uid) != os.Getuid() {
		return nil, nil, errors.New("unsafe file descriptor")
	}
	b, e := io.ReadAll(io.LimitReader(f, maxSettings+1))
	if e != nil {
		return nil, nil, e
	}
	if len(b) > maxSettings {
		return nil, nil, errors.New("settings or journal exceeds size limit")
	}
	return b, i, nil
}
func readBounded(path string) ([]byte, error) { b, _, e := readVersion(path); return b, e }
func readSettings(path string) (map[string]json.RawMessage, os.FileMode, error) {
	b, e := readBounded(path)
	if os.IsNotExist(e) {
		return map[string]json.RawMessage{}, 0600, nil
	}
	if e != nil {
		return nil, 0, e
	}
	var m map[string]json.RawMessage
	if e = json.Unmarshal(b, &m); e != nil {
		return nil, 0, e
	}
	if m == nil {
		return nil, 0, errors.New("settings must be a JSON object")
	}
	i, e := safeFile(path)
	if e != nil {
		return nil, 0, e
	}
	return m, i.Mode().Perm(), nil
}
func pick(m map[string]json.RawMessage) selected { v, ok := m["statusLine"]; return selected{ok, v} }
func equal(a, b selected) bool {
	if a.Present != b.Present {
		return false
	}
	if !a.Present {
		return true
	}
	var x, y any
	d := json.NewDecoder(bytes.NewReader(a.Value))
	d.UseNumber()
	if d.Decode(&x) != nil {
		return false
	}
	d = json.NewDecoder(bytes.NewReader(b.Value))
	d.UseNumber()
	if d.Decode(&y) != nil {
		return false
	}
	// Verified Antigravity native settings serialization omits padding:0 and
	// stack_with_default:false. Normalize only these defaults for the exact same
	// managed callback command; retain every other field and the journal's raw prior value.
	xm, xok := x.(map[string]any)
	ym, yok := y.(map[string]any)
	if xok && yok && xm["type"] == "command" && ym["type"] == "command" {
		command, cok := xm["command"].(string)
		if cok && command == ym["command"] && (strings.HasSuffix(command, " __callback antigravity") || strings.HasSuffix(command, " '__callback' 'antigravity'")) {
			for _, m := range []map[string]any{xm, ym} {
				if n, ok := m["padding"].(json.Number); ok && n == "0" {
					delete(m, "padding")
				}
				if v, ok := m["stack_with_default"].(bool); ok && !v {
					delete(m, "stack_with_default")
				}
			}
		}
	}
	p, _ := json.Marshal(x)
	q, _ := json.Marshal(y)
	return bytes.Equal(p, q)
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	return atomicWriteChecked(path, b, mode, nil)
}
func atomicWriteChecked(path string, b []byte, mode os.FileMode, check func() error) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".statusline-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	defer func() { _ = f.Close() }()
	if e = f.Chmod(mode); e != nil {
		return e
	}
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if _, e = safeFile(path); e != nil && !os.IsNotExist(e) {
		return e
	}
	if check != nil {
		if e = check(); e != nil {
			return e
		}
	}
	if e = os.Rename(name, path); e != nil {
		return e
	}
	return syncDir(filepath.Dir(path))
}
func (t *transaction) save(j journal) error {
	b, e := json.Marshal(j)
	if e != nil {
		return e
	}
	if len(b) > maxSettings {
		return errors.New("journal exceeds size limit")
	}
	return atomicWrite(t.record, b, 0600)
}
func (t *transaction) load() (*journal, error) {
	b, e := readBounded(t.record)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var j journal
	if e = json.Unmarshal(b, &j); e != nil {
		return nil, e
	}
	if j.Version != 1 || j.Target != t.target || (j.Phase != "prepared" && j.Phase != "applied" && j.Phase != "restoring") {
		return nil, errors.New("invalid settings journal")
	}
	return &j, nil
}
func (t *transaction) remove() error {
	if e := os.Remove(t.record); e != nil && !os.IsNotExist(e) {
		return e
	}
	return syncDir(filepath.Dir(t.record))
}

var errVersionChanged = errors.New("settings version changed")

// A noncooperating editor can still change the file between the final version
// check and rename: portable filesystems provide no atomic compare-and-rename.
func (t *transaction) replace(expected, value selected) error {
	for attempt := 0; attempt < 8; attempt++ {
		original, identity, e := readVersion(t.target)
		missing := os.IsNotExist(e)
		if e != nil && !missing {
			return e
		}
		mode := os.FileMode(0600)
		m := map[string]json.RawMessage{}
		if !missing {
			mode = identity.Mode().Perm()
			if e = json.Unmarshal(original, &m); e != nil {
				return e
			}
			if m == nil {
				return errors.New("settings must be a JSON object")
			}
		}
		if !equal(pick(m), expected) {
			return ErrConflict
		}
		if value.Present {
			m["statusLine"] = value.Value
		} else {
			delete(m, "statusLine")
		}
		b, e := json.MarshalIndent(m, "", "  ")
		if e != nil {
			return e
		}
		if len(b) > maxSettings {
			return errors.New("settings exceeds size limit")
		}
		e = atomicWriteChecked(t.target, append(b, '\n'), mode, func() error {
			if t.beforeReplace != nil {
				t.beforeReplace()
			}
			current, currentIdentity, err := readVersion(t.target)
			currentMissing := os.IsNotExist(err)
			if err != nil && !currentMissing {
				return err
			}
			if missing && currentMissing {
				return nil
			}
			if missing != currentMissing || !os.SameFile(identity, currentIdentity) || !bytes.Equal(original, current) {
				return errVersionChanged
			}
			return nil
		})
		if errors.Is(e, errVersionChanged) {
			continue
		}
		return e
	}
	return fmt.Errorf("%w: settings repeatedly changed during replacement", ErrConflict)
}
func (t *transaction) restore(j *journal) error {
	m, _, e := readSettings(t.target)
	if e != nil {
		return e
	}
	cur := pick(m)
	if j.Phase == "restoring" && equal(cur, j.Before) {
		return t.remove()
	}
	if !equal(cur, j.Installed) {
		if j.Phase == "prepared" && equal(cur, j.Before) {
			return t.remove()
		}
		return ErrConflict
	}
	j.Phase = "restoring"
	if e = t.save(*j); e != nil {
		return e
	}
	if e = t.replace(j.Installed, j.Before); e != nil {
		return e
	}
	return t.remove()
}

// Install durably records the prior value before installing statusLine. Repeating
// the same installation is idempotent. journalDir must remain private and stable.
func Install(settingsPath, journalDir string, statusLine map[string]any) error {
	b, e := json.Marshal(statusLine)
	if e != nil {
		return e
	}
	wanted := selected{true, b}
	t, e := begin(settingsPath, journalDir)
	if e != nil {
		return e
	}
	defer t.close()
	j, e := t.load()
	if e != nil {
		return e
	}
	if j != nil && j.Phase == "restoring" {
		if e = t.restore(j); e != nil {
			return e
		}
		j = nil
	}
	m, _, e := readSettings(t.target)
	if e != nil {
		return e
	}
	cur := pick(m)
	if j != nil {
		if !equal(wanted, j.Installed) {
			return ErrConflict
		}
		if equal(cur, j.Installed) {
			j.Phase = "applied"
			return t.save(*j)
		}
		if j.Phase != "prepared" || !equal(cur, j.Before) {
			return ErrConflict
		}
	} else {
		j = &journal{1, t.target, "prepared", cur, wanted}
		if e = t.save(*j); e != nil {
			return e
		}
	}
	if e = t.replace(j.Before, j.Installed); e != nil {
		return e
	}
	j.Phase = "applied"
	return t.save(*j)
}

// Restore restores only an unchanged managed value, preserving unrelated keys.
func Restore(settingsPath, journalDir string) error {
	t, e := begin(settingsPath, journalDir)
	if e != nil {
		return e
	}
	defer t.close()
	j, e := t.load()
	if e != nil || j == nil {
		return e
	}
	return t.restore(j)
}

// Previous returns the original statusLine object from a valid transaction.
// It does not modify settings or the journal. A missing original returns false;
// a non-object original cannot be represented by this API and returns an error.
func Previous(settingsPath, journalDir string) (map[string]any, bool, error) {
	t, e := begin(settingsPath, journalDir)
	if e != nil {
		return nil, false, e
	}
	defer t.close()
	j, e := t.load()
	if e != nil || j == nil {
		return nil, false, e
	}
	m, _, e := readSettings(t.target)
	if e != nil {
		return nil, false, e
	}
	cur := pick(m)
	if !equal(cur, j.Installed) && ((j.Phase != "prepared" && j.Phase != "restoring") || !equal(cur, j.Before)) {
		return nil, false, ErrConflict
	}
	if !j.Before.Present {
		return nil, false, nil
	}
	var previous map[string]any
	if e = json.Unmarshal(j.Before.Value, &previous); e != nil {
		return nil, false, fmt.Errorf("previous statusLine is not an object: %w", e)
	}
	if previous == nil {
		return nil, false, errors.New("previous statusLine is not an object")
	}
	return previous, true, nil
}

// Installed reports whether a valid, active journal and the current settings
// both match statusLine. Prepared journals recover the settings-renamed state
// by observation; a restore intent never authorizes a managed launch.
// This helper does not modify settings or the journal.
func Installed(settingsPath, journalDir string, statusLine map[string]any) (bool, error) {
	b, e := json.Marshal(statusLine)
	if e != nil {
		return false, e
	}
	wanted := selected{true, b}
	t, e := begin(settingsPath, journalDir)
	if e != nil {
		return false, e
	}
	defer t.close()
	j, e := t.load()
	if e != nil || j == nil {
		return false, e
	}
	m, _, e := readSettings(t.target)
	if e != nil {
		return false, e
	}
	cur := pick(m)
	if !equal(cur, j.Installed) {
		if (j.Phase == "prepared" || j.Phase == "restoring") && equal(cur, j.Before) {
			return false, nil
		}
		return false, ErrConflict
	}
	return j.Phase != "restoring" && equal(j.Installed, wanted), nil
}
