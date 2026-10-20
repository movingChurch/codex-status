package logins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Account is one stored login's metadata; the login itself is a vault item.
type Account struct {
	Runtime string `json:"runtime"`
	ID      string `json:"id"`
	Email   string `json:"email"`
	Alias   string `json:"alias,omitempty"`
	Order   int    `json:"order"`
	Added   string `json:"added"`
}

// Label is what the HUD and listings show: the alias, else the email.
func (a Account) Label() string {
	if a.Alias != "" {
		return a.Alias
	}
	if a.Email != "" {
		return a.Email
	}
	return a.ID
}

// Usage is the last quota reading observed for an account.
type Usage struct {
	FiveHour any    `json:"five_hour,omitempty"`
	Weekly   any    `json:"weekly,omitempty"`
	At       string `json:"at"`
}

type metadata struct {
	Version  int               `json:"version"`
	Accounts []Account         `json:"accounts"`
	Current  map[string]string `json:"current"`
	Usage    map[string]Usage  `json:"usage,omitempty"`
	// Noticed marks the terms notice as shown on a first switch.
	Noticed bool `json:"noticed,omitempty"`
}

// journal records a switch in progress so a crash is finished or undone.
type journal struct {
	Runtime string `json:"runtime"`
	From    string `json:"from"`
	To      string `json:"to"`
	Phase   string `json:"phase"`
}

func sha8(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])[:8]
}

func vaultKey(runtime, id string) string { return runtime + ":" + id }

func (m *Manager) state() string    { return filepath.Join(m.Home, ".local", "state", "runtime-status") }
func (m *Manager) metaPath() string { return filepath.Join(m.state(), "accounts.json") }
func (m *Manager) journalPath() string {
	return filepath.Join(m.state(), "switch.json")
}

func (m *Manager) lock() (func(), error) {
	if err := os.MkdirAll(m.state(), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(m.state(), "accounts.lock"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, errors.New("another account command is running")
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

func (m *Manager) load() (*metadata, error) {
	meta := &metadata{Version: 1, Current: map[string]string{}}
	raw, err := os.ReadFile(m.metaPath())
	if errors.Is(err, os.ErrNotExist) {
		return meta, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, meta); err != nil {
		return nil, fmt.Errorf("%s is unreadable", m.metaPath())
	}
	if meta.Current == nil {
		meta.Current = map[string]string{}
	}
	return meta, nil
}

func (m *Manager) save(meta *metadata) error {
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(m.metaPath(), append(raw, '\n'), 0600)
}

func (meta *metadata) find(runtime, name string) (int, error) {
	for i, a := range meta.Accounts {
		if a.Runtime == runtime && (a.ID == name || strings.EqualFold(a.Email, name) || (a.Alias != "" && a.Alias == name)) {
			return i, nil
		}
	}
	return -1, fmt.Errorf("no %s account named %s", runtime, name)
}

// remember adds or refreshes the metadata of a login just stored.
func (meta *metadata) remember(runtime string, login Login, now time.Time) {
	for i, a := range meta.Accounts {
		if a.Runtime == runtime && a.ID == login.ID {
			if login.Email != "" {
				meta.Accounts[i].Email = login.Email
			}
			return
		}
	}
	order := 1
	for _, a := range meta.Accounts {
		if a.Runtime == runtime && a.Order >= order {
			order = a.Order + 1
		}
	}
	meta.Accounts = append(meta.Accounts, Account{Runtime: runtime, ID: login.ID, Email: login.Email, Order: order, Added: now.UTC().Format(time.RFC3339)})
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(data); err == nil {
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
