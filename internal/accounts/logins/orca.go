package logins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// orcaClaudeService is the Keychain item Orca keeps per Claude account.
const orcaClaudeService = "Orca Claude Code Managed Credentials"

func (m *Manager) orcaDir() string {
	return filepath.Join(m.Home, "Library", "Application Support", "orca")
}

// Imported is one Orca account and what the import did with it.
type Imported struct {
	Account
	Skipped bool `json:"skipped"`
}

// ImportOrca copies each Codex and Claude login Orca stores into the vault,
// keyed by account id; an account already stored is skipped unless replace,
// so a second import changes nothing. Orca's own files and items stay.
func (m *Manager) ImportOrca(replace bool) ([]Imported, error) {
	unlock, err := m.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err = m.recover(); err != nil {
		return nil, err
	}
	running, err := m.orcaRunning()
	if err != nil {
		return nil, err
	}
	if running {
		return nil, errors.New("quit Orca first; it refreshes these logins while it runs")
	}
	if _, err = os.Stat(m.orcaDir()); err != nil {
		return nil, fmt.Errorf("no Orca data at %s", m.orcaDir())
	}
	found := []struct {
		runtime string
		login   Login
	}{}
	for _, name := range Runtimes {
		logins, err := m.orcaLogins(name)
		if err != nil {
			return nil, err
		}
		for _, l := range logins {
			found = append(found, struct {
				runtime string
				login   Login
			}{name, l})
		}
	}
	if len(found) == 0 {
		return nil, errors.New("no Codex or Claude login found in Orca")
	}
	meta, err := m.load()
	if err != nil {
		return nil, err
	}
	out := []Imported{}
	for _, f := range found {
		_, findErr := meta.find(f.runtime, f.login.ID)
		skip := findErr == nil && !replace
		if !skip {
			if err = m.Secrets.Set(VaultService, vaultKey(f.runtime, f.login.ID), f.login.Data); err != nil {
				return nil, err
			}
			meta.remember(f.runtime, f.login, m.Now())
		}
		i, _ := meta.find(f.runtime, f.login.ID)
		out = append(out, Imported{Account: meta.Accounts[i], Skipped: skip})
	}
	for _, name := range Runtimes {
		if meta.Current[name] == "" {
			if live, err := m.liveID(name); err == nil {
				if _, err := meta.find(name, live); err == nil {
					meta.Current[name] = live
				}
			}
		}
	}
	meta.OrcaImported = true
	return out, m.save(meta)
}

// orcaLogins reads the logins Orca keeps for one runtime, in a stable order.
func (m *Manager) orcaLogins(name string) ([]Login, error) {
	rt, err := m.runtime(name)
	if err != nil {
		return nil, err
	}
	sub := map[string]string{"codex": "codex-accounts", "claude": "claude-accounts"}[name]
	dirs, err := filepath.Glob(filepath.Join(m.orcaDir(), sub, "*"))
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	logins := []Login{}
	for _, dir := range dirs {
		var login Login
		if name == "codex" {
			raw, err := os.ReadFile(filepath.Join(dir, "home", "auth.json"))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if login, err = rt.parse(raw); err != nil {
				return nil, fmt.Errorf("codex account %s in Orca: %w", filepath.Base(dir), err)
			}
		} else {
			profile, err := os.ReadFile(filepath.Join(dir, "auth", "oauth-account.json"))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			item, err := m.Secrets.Get(orcaClaudeService, filepath.Base(dir))
			if err != nil {
				return nil, fmt.Errorf("claude account %s in Orca: %w", filepath.Base(dir), err)
			}
			var creds map[string]json.RawMessage
			if json.Unmarshal(item, &creds) != nil || creds["claudeAiOauth"] == nil {
				return nil, fmt.Errorf("claude account %s in Orca has no OAuth login", filepath.Base(dir))
			}
			data, _ := json.Marshal(claudeLogin{OAuth: creds["claudeAiOauth"], Account: json.RawMessage(profile)})
			if login, err = rt.parse(data); err != nil {
				return nil, fmt.Errorf("claude account %s in Orca: %w", filepath.Base(dir), err)
			}
		}
		logins = append(logins, login)
	}
	return logins, nil
}

// OrcaRunning reports whether Orca runs, which matters after an import:
// its token refresh would sign the imported copies out.
func (m *Manager) OrcaRunning() (bool, error) { return m.orcaRunning() }

func (m *Manager) orcaRunning() (bool, error) {
	processes, err := m.Processes()
	if err != nil {
		return false, errors.New("process list unavailable")
	}
	for _, p := range processes {
		if strings.Contains(strings.Fields(p.Args + " ")[0], "/Orca.app/") {
			return true, nil
		}
	}
	return false, nil
}

// ImportedFromOrca reports whether accounts were ever imported from Orca.
func (m *Manager) ImportedFromOrca() bool {
	meta, err := m.load()
	return err == nil && meta.OrcaImported
}

// FirstUse reports true once, on the first switch, so the terms notice is
// shown then and not on every switch.
func (m *Manager) FirstUse() bool {
	unlock, err := m.lock()
	if err != nil {
		return false
	}
	defer unlock()
	meta, err := m.load()
	if err != nil || meta.Noticed {
		return false
	}
	meta.Noticed = true
	return m.save(meta) == nil
}
