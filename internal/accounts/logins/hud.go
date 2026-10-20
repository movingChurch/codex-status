package logins

import (
	"path/filepath"
	"time"
)

// Selected is the label of runtime's selected account in accounts.json under
// home, read without the lock so the HUD never waits or reads credentials.
func Selected(home, runtime string) string {
	meta, err := (&Manager{Home: home}).load()
	if err != nil {
		return ""
	}
	for _, a := range meta.Accounts {
		if a.Runtime == runtime && a.ID == meta.Current[runtime] {
			return a.Label()
		}
	}
	return ""
}

// OnLiveHome reports whether a window's home is the one switches rewrite.
func OnLiveHome(home, runtime, windowHome string) bool {
	if runtime != "codex" && runtime != "claude" || windowHome == "" {
		return false
	}
	resolve := func(path string) string {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			return real
		}
		return filepath.Clean(path)
	}
	window := resolve(windowHome)
	// Orca's managed Codex System default runs on a mirror of ~/.codex.
	mirror := filepath.Join(home, "Library", "Application Support", "orca", "codex-runtime-home", "home")
	return window == resolve((&Manager{Home: home}).defaultHome(runtime)) || runtime == "codex" && window == resolve(mirror)
}

// RecordUsage keeps a quota reading as the last one of runtime's selected
// account; it waits up to two seconds for the lock, such as one `account
// list` holds, and skips the reading past that, as during a switch.
func RecordUsage(home, runtime string, usage Usage) error {
	m := &Manager{Home: home}
	unlock, err := m.lock()
	for wait := 0; err != nil && wait < 20; wait++ {
		time.Sleep(100 * time.Millisecond)
		unlock, err = m.lock()
	}
	if err != nil {
		return err
	}
	defer unlock()
	meta, err := m.load()
	if err != nil || meta.Current[runtime] == "" {
		return err
	}
	if meta.Usage == nil {
		meta.Usage = map[string]Usage{}
	}
	if usage.At == "" {
		usage.At = time.Now().UTC().Format(time.RFC3339)
	}
	meta.Usage[vaultKey(runtime, meta.Current[runtime])] = usage
	return m.save(meta)
}
