package setup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	d := t.TempDir()
	return filepath.Join(d, "settings.json"), filepath.Join(d, "journal")
}
func put(t *testing.T, p, s string) {
	t.Helper()
	if e := os.WriteFile(p, []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
}
func get(t *testing.T, p string) map[string]json.RawMessage {
	t.Helper()
	m, _, e := readSettings(p)
	if e != nil {
		t.Fatal(e)
	}
	return m
}

var managed = map[string]any{"type": "command", "command": "hud"}

func TestPreserveAndRestore(t *testing.T) {
	p, d := fixture(t)
	put(t, p, `{"statusLine":{"command":"old"},"other":{"x":3}}`)
	if e := Install(p, d, managed); e != nil {
		t.Fatal(e)
	}
	m := get(t, p)
	m["later"] = json.RawMessage(`true`)
	b, _ := json.Marshal(m)
	put(t, p, string(b))
	if e := Restore(p, d); e != nil {
		t.Fatal(e)
	}
	m = get(t, p)
	if !equal(selected{true, m["other"]}, selected{true, json.RawMessage(`{"x":3}`)}) || string(m["later"]) != "true" || !equal(pick(m), selected{true, json.RawMessage(`{"command":"old"}`)}) {
		t.Fatalf("restored %s", m)
	}
}
func TestAbsentAndMissing(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "absent"}[exists], func(t *testing.T) {
			p, d := fixture(t)
			if exists {
				put(t, p, `{"other":1}`)
			}
			if e := Install(p, d, managed); e != nil {
				t.Fatal(e)
			}
			if e := Restore(p, d); e != nil {
				t.Fatal(e)
			}
			if _, ok := get(t, p)["statusLine"]; ok {
				t.Fatal("key remained")
			}
		})
	}
}
func TestUserConflict(t *testing.T) {
	p, d := fixture(t)
	put(t, p, `{}`)
	if e := Install(p, d, managed); e != nil {
		t.Fatal(e)
	}
	put(t, p, `{"statusLine":"mine","other":5}`)
	if e := Restore(p, d); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if string(get(t, p)["statusLine"]) != `"mine"` {
		t.Fatal("overwritten")
	}
	tx, e := begin(p, d)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.close()
	if j, e := tx.load(); e != nil || j == nil {
		t.Fatal("journal lost", e)
	}
}
func TestRecoveryBoundaries(t *testing.T) {
	for _, phase := range []string{"prepared", "applied", "restoring"} {
		for _, current := range []string{"before", "installed"} {
			t.Run(phase+"/"+current, func(t *testing.T) {
				p, d := fixture(t)
				put(t, p, `{"unrelated":7}`)
				tx, e := begin(p, d)
				if e != nil {
					t.Fatal(e)
				}
				b, _ := json.Marshal(managed)
				j := journal{1, tx.target, phase, selected{}, selected{true, b}}
				if e = tx.save(j); e != nil {
					t.Fatal(e)
				}
				if current == "installed" {
					if e = tx.replace(j.Before, j.Installed); e != nil {
						t.Fatal(e)
					}
				}
				tx.close()
				if phase == "applied" && current == "before" {
					if e = Restore(p, d); !errors.Is(e, ErrConflict) {
						t.Fatal(e)
					}
					return
				}
				if e = Restore(p, d); e != nil {
					t.Fatal(e)
				}
				if pick(get(t, p)).Present {
					t.Fatal("not restored")
				}
			})
		}
	}
}
func TestPreparedRetry(t *testing.T) {
	p, d := fixture(t)
	put(t, p, `{}`)
	tx, e := begin(p, d)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(managed)
	e = tx.save(journal{1, tx.target, "prepared", selected{}, selected{true, b}})
	tx.close()
	if e != nil {
		t.Fatal(e)
	}
	if e = Install(p, d, managed); e != nil {
		t.Fatal(e)
	}
	if e = Restore(p, d); e != nil {
		t.Fatal(e)
	}
}
func TestCanonicalConcurrency(t *testing.T) {
	p, d := fixture(t)
	alias := filepath.Join(filepath.Dir(p), "alias")
	if e := os.Symlink(filepath.Dir(p), alias); e != nil {
		t.Fatal(e)
	}
	paths := []string{p, filepath.Join(alias, "settings.json")}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs <- Install(paths[i%2], d, managed) }(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if e := Restore(p, d); e != nil {
		t.Fatal(e)
	}
	if pick(get(t, p)).Present {
		t.Fatal("original absent value lost")
	}
}
func TestRejectSymlinksAndNonregular(t *testing.T) {
	p, d := fixture(t)
	real := p + ".real"
	put(t, real, `{}`)
	if e := os.Symlink(real, p); e != nil {
		t.Fatal(e)
	}
	if e := Install(p, d, managed); e == nil {
		t.Fatal("accepted target symlink")
	}
	if e := os.Remove(p); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(p, 0700); e != nil {
		t.Fatal(e)
	}
	if e := Install(p, d, managed); e == nil {
		t.Fatal("accepted directory")
	}
	if e := os.Remove(p); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(d, 0755); e != nil {
		t.Fatal(e)
	}
	if e := Install(p, d, managed); e == nil {
		t.Fatal("accepted public journal")
	}
}

func TestInstallFinalizesPreparedAfterSettingsRename(t *testing.T) {
	p, d := fixture(t)
	put(t, p, `{"statusLine":"old"}`)
	tx, e := begin(p, d)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(managed)
	before := pick(get(t, p))
	j := journal{1, tx.target, "prepared", before, selected{true, b}}
	if e = tx.save(j); e != nil {
		t.Fatal(e)
	}
	if e = tx.replace(j.Before, j.Installed); e != nil {
		t.Fatal(e)
	}
	tx.close()
	if e = Install(p, d, managed); e != nil {
		t.Fatal(e)
	}
	tx, e = begin(p, d)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := tx.load()
	tx.close()
	if e != nil || saved.Phase != "applied" {
		t.Fatal("not finalized", e)
	}
	if e = Restore(p, d); e != nil {
		t.Fatal(e)
	}
	if !equal(pick(get(t, p)), before) {
		t.Fatal("original lost")
	}
}

func TestReadOnlyHelpers(t *testing.T) {
	p, d := fixture(t)
	put(t, p, `{"statusLine":{"command":"old"}}`)
	if ok, e := Installed(p, d, managed); e != nil || ok {
		t.Fatal(ok, e)
	}
	if _, ok, e := Previous(p, d); e != nil || ok {
		t.Fatal(ok, e)
	}
	if e := Install(p, d, managed); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	if ok, e := Installed(p, d, managed); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if ok, e := Installed(p, d, map[string]any{"command": "different"}); e != nil || ok {
		t.Fatal(ok, e)
	}
	prev, ok, e := Previous(p, d)
	if e != nil || !ok || prev["command"] != "old" {
		t.Fatal(prev, ok, e)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("helper mutated settings")
	}
	put(t, p, `{"statusLine":{"command":"user"}}`)
	if ok, e := Installed(p, d, managed); ok || !errors.Is(e, ErrConflict) {
		t.Fatal(ok, e)
	}
	if _, ok, e := Previous(p, d); ok || !errors.Is(e, ErrConflict) {
		t.Fatal(ok, e)
	}
}
func TestHelperRecoveryStates(t *testing.T) {
	for _, phase := range []string{"prepared", "applied", "restoring"} {
		t.Run(phase, func(t *testing.T) {
			p, d := fixture(t)
			put(t, p, `{}`)
			tx, e := begin(p, d)
			if e != nil {
				t.Fatal(e)
			}
			b, _ := json.Marshal(managed)
			j := journal{1, tx.target, phase, selected{}, selected{true, b}}
			if e = tx.save(j); e != nil {
				t.Fatal(e)
			}
			if e = tx.replace(j.Before, j.Installed); e != nil {
				t.Fatal(e)
			}
			tx.close()
			ok, e := Installed(p, d, managed)
			if e != nil || ok != (phase != "restoring") {
				t.Fatal(ok, e)
			}
			if _, present, e := Previous(p, d); e != nil || present {
				t.Fatal(present, e)
			}
		})
	}
}

func TestStagedExternalEdits(t *testing.T) {
	for _, selectedEdit := range []bool{false, true} {
		t.Run(map[bool]string{false: "merge-unrelated", true: "reject-statusLine"}[selectedEdit], func(t *testing.T) {
			p, d := fixture(t)
			put(t, p, `{"statusLine":"old","other":1}`)
			tx, e := begin(p, d)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.close()
			tx.beforeReplace = func() {
				tx.beforeReplace = nil
				if selectedEdit {
					put(t, p, `{"statusLine":"user","other":2}`)
				} else {
					put(t, p, `{"statusLine":"old","other":2}`)
				}
			}
			e = tx.replace(selected{true, json.RawMessage(`"old"`)}, selected{true, json.RawMessage(`"managed"`)})
			if selectedEdit {
				if !errors.Is(e, ErrConflict) {
					t.Fatal(e)
				}
				if string(get(t, p)["statusLine"]) != `"user"` {
					t.Fatal("user value overwritten")
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				if string(get(t, p)["other"]) != "2" {
					t.Fatal("unrelated edit lost")
				}
			}
		})
	}
}
func TestStagedReplacementIdentity(t *testing.T) {
	p, d := fixture(t)
	put(t, p, `{"other":1}`)
	tx, e := begin(p, d)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.close()
	calls := 0
	tx.beforeReplace = func() {
		calls++
		if calls == 1 {
			next := p + ".next"
			put(t, next, `{"other":1}`)
			if e := os.Rename(next, p); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = tx.replace(selected{}, selected{true, json.RawMessage(`"managed"`)}); e != nil {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatalf("identity change not retried: %d", calls)
	}
}
func TestRejectUnsafeLock(t *testing.T) {
	p, d := fixture(t)
	tx, e := begin(p, d)
	if e != nil {
		t.Fatal(e)
	}
	lockPath := tx.lock.Name()
	tx.close()
	if e = os.Chmod(lockPath, 0666); e != nil {
		t.Fatal(e)
	}
	if _, e = begin(p, d); e == nil {
		t.Fatal("accepted public lock")
	}
}

func TestRejectFIFOLock(t *testing.T) {
	p, d := fixture(t)
	tx, e := begin(p, d)
	if e != nil {
		t.Fatal(e)
	}
	name := tx.lock.Name()
	tx.close()
	if e = os.Remove(name); e != nil {
		t.Fatal(e)
	}
	if e = syscall.Mkfifo(name, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = begin(p, d); e == nil {
		t.Fatal("accepted FIFO lock")
	}
}

func TestAntigravitySparseNativeDefaults(t *testing.T) {
	command := "'/tmp/runtime-status' '__callback' 'antigravity'"
	installed := map[string]any{"type": "command", "command": command, "enabled": true, "padding": 0, "stack_with_default": false}
	for _, edit := range []string{"defaults-omitted", "padding-nonzero", "stack-true", "unknown-field", "changed-command"} {
		t.Run(edit, func(t *testing.T) {
			p, d := fixture(t)
			put(t, p, `{"native":1}`)
			if e := Install(p, d, installed); e != nil {
				t.Fatal(e)
			}
			native := map[string]any{"type": "command", "command": command, "enabled": true}
			switch edit {
			case "padding-nonzero":
				native["padding"] = 1
			case "stack-true":
				native["stack_with_default"] = true
			case "unknown-field":
				native["unknown"] = true
			case "changed-command":
				native["command"] = "other __callback antigravity"
			}
			b, _ := json.Marshal(map[string]any{"statusLine": native, "native": 2})
			put(t, p, string(b))
			ok, e := Installed(p, d, installed)
			if edit != "defaults-omitted" {
				if ok || !errors.Is(e, ErrConflict) {
					t.Fatal(ok, e)
				}
				if e = Restore(p, d); !errors.Is(e, ErrConflict) {
					t.Fatal(e)
				}
				return
			}
			if e != nil || !ok {
				t.Fatal(ok, e)
			}
			if _, present, e := Previous(p, d); e != nil || present {
				t.Fatal(present, e)
			}
			if e = Restore(p, d); e != nil {
				t.Fatal(e)
			}
			m := get(t, p)
			if pick(m).Present || string(m["native"]) != "2" {
				t.Fatal("native settings not preserved", m)
			}
		})
	}
}
func TestDefaultsNotNormalizedForOtherCommands(t *testing.T) {
	a := selected{true, json.RawMessage(`{"type":"command","command":"user","padding":0,"stack_with_default":false}`)}
	b := selected{true, json.RawMessage(`{"type":"command","command":"user"}`)}
	if equal(a, b) {
		t.Fatal("normalized unmanaged command")
	}
}
