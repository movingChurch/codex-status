package logins

import (
	"os"
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

// SelectedCache answers Selected for one home and reads accounts.json again
// only after the file changed, for callers that ask every second.
type SelectedCache struct {
	Home   string
	file   os.FileInfo
	labels map[string]string
}

// Label is Selected(c.Home, runtime), from the cache while the file is the same.
func (c *SelectedCache) Label(runtime string) string {
	info, err := os.Stat((&Manager{Home: c.Home}).metaPath())
	if err != nil {
		c.file, c.labels = nil, nil
		return ""
	}
	if c.file == nil || !os.SameFile(c.file, info) || !c.file.ModTime().Equal(info.ModTime()) || c.file.Size() != info.Size() {
		c.file, c.labels = info, map[string]string{}
		for _, name := range []string{"codex", "claude"} {
			c.labels[name] = Selected(c.Home, name)
		}
	}
	return c.labels[runtime]
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
	mirror := filepath.Join(orcaCodexMirror(home), "home")
	return window == resolve((&Manager{Home: home}).defaultHome(runtime)) || runtime == "codex" && window == resolve(mirror)
}

// orcaCodexMirror is where Orca keeps its copy of ~/.codex.
func orcaCodexMirror(home string) string {
	return filepath.Join(home, "Library", "Application Support", "orca", "codex-runtime-home")
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
