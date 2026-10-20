package logins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Windows are the runtime-status windows a switch relaunches: processes of
// those runs pass the first busy check, then Stop replaces the windows and the
// switch waits for their old processes to end. Nil means no window may be open.
type Windows struct {
	Runs []string
	Stop func(runtime string) error
}

// Use switches runtime to the named account: the live login is saved under its
// own identity first, since tokens rotate while in use, then the chosen login
// is written and read back.
func (m *Manager) Use(name, account string, windows *Windows) (Account, error) {
	rt, err := m.runtime(name)
	if err != nil {
		return Account{}, err
	}
	unlock, err := m.lock()
	if err != nil {
		return Account{}, err
	}
	defer unlock()
	if err = m.recover(); err != nil {
		return Account{}, err
	}
	if err = rt.check(); err != nil {
		return Account{}, err
	}
	meta, err := m.load()
	if err != nil {
		return Account{}, err
	}
	i, err := meta.find(name, account)
	if err != nil {
		return Account{}, err
	}
	target := meta.Accounts[i]
	// Every read comes first, so a refused Keychain prompt writes nothing.
	next, err := m.Secrets.Get(VaultService, vaultKey(name, target.ID))
	if err != nil {
		return Account{}, fmt.Errorf("stored login of %s: %w", target.Label(), err)
	}
	live, err := rt.live()
	if err != nil && !errors.Is(err, errNoLogin) {
		return Account{}, err
	}
	if live.ID == target.ID {
		meta.Current[name] = target.ID
		return target, m.save(meta)
	}
	if err = m.writeJournal(journal{Runtime: name, From: live.ID, To: target.ID, Phase: "stopping"}); err != nil {
		return Account{}, err
	}
	if err = m.clear(name, windows); err != nil {
		_ = os.Remove(m.journalPath())
		return Account{}, err
	}
	// Re-read after the windows stopped: their last refresh may have rotated it.
	if live, err = rt.live(); err != nil && !errors.Is(err, errNoLogin) {
		_ = os.Remove(m.journalPath())
		return Account{}, err
	}
	if live.ID != "" {
		if err = m.Secrets.Set(VaultService, vaultKey(name, live.ID), live.Data); err != nil {
			_ = os.Remove(m.journalPath())
			return Account{}, err
		}
		meta.remember(name, live, m.Now())
	}
	if err = m.writeJournal(journal{Runtime: name, From: live.ID, To: target.ID, Phase: "writing"}); err != nil {
		return Account{}, err
	}
	if err = rt.write(next); err == nil {
		var check Login
		if check, err = rt.live(); err == nil && check.ID != target.ID {
			err = errors.New("the written login reads back as another account")
		}
	}
	if err != nil {
		if live.ID != "" {
			_ = rt.write(live.Data)
		}
		_ = os.Remove(m.journalPath())
		return Account{}, fmt.Errorf("switch undone: %w", err)
	}
	meta.Current[name] = target.ID
	if err = m.save(meta); err != nil {
		return Account{}, err
	}
	return target, os.Remove(m.journalPath())
}

func (m *Manager) clear(name string, windows *Windows) error {
	if windows != nil {
		if err := m.idle(name, windows.Runs...); err != nil {
			return err
		}
		if err := windows.Stop(name); err != nil {
			return err
		}
		m.waitGone(windows.Runs, 10*time.Second)
	}
	return m.idle(name)
}

func (m *Manager) idle(name string, runs ...string) error {
	busy, err := m.Busy(name, runs...)
	if err != nil {
		return err
	}
	if len(busy) > 0 {
		return fmt.Errorf("close these first: %s", strings.Join(busy, ", "))
	}
	return nil
}

// waitGone waits for the processes of runs to end, then terminates and at
// last kills those still running, so no old window refreshes during the swap.
func (m *Manager) waitGone(runs []string, limit time.Duration) {
	if len(runs) == 0 {
		return
	}
	deadline := time.Now().Add(limit)
	for signal := syscall.Signal(0); ; {
		left := m.runProcesses(runs)
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			switch signal {
			case 0:
				signal, deadline = syscall.SIGTERM, time.Now().Add(3*time.Second)
			case syscall.SIGTERM:
				signal, deadline = syscall.SIGKILL, time.Now().Add(2*time.Second)
			default:
				return
			}
			for _, pid := range left {
				_ = syscall.Kill(pid, signal)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (m *Manager) runProcesses(runs []string) []int {
	processes, err := m.Processes()
	if err != nil {
		return nil
	}
	pids := []int{}
	for _, p := range processes {
		if p.Env != nil && slices.Contains(runs, p.Env[runVar]) {
			pids = append(pids, p.PID)
		}
	}
	return pids
}

func (m *Manager) writeJournal(j journal) error {
	raw, _ := json.Marshal(j)
	return atomicWrite(m.journalPath(), raw, 0600)
}

// recover finishes or undoes a switch a crash interrupted.
func (m *Manager) recover() error {
	m.sweep()
	raw, err := os.ReadFile(m.journalPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var j journal
	if err = json.Unmarshal(raw, &j); err != nil {
		return os.Remove(m.journalPath())
	}
	rt, err := m.runtime(j.Runtime)
	if err != nil {
		return os.Remove(m.journalPath())
	}
	if j.Phase == "writing" {
		live, err := rt.live()
		meta, metaErr := m.load()
		if metaErr != nil {
			return metaErr
		}
		if err == nil && live.ID == j.To {
			meta.Current[j.Runtime] = j.To
		} else if j.From != "" {
			saved, err := m.Secrets.Get(VaultService, vaultKey(j.Runtime, j.From))
			if err != nil {
				return fmt.Errorf("interrupted switch cannot be undone: %w", err)
			}
			if err = rt.write(saved); err != nil {
				return err
			}
			meta.Current[j.Runtime] = j.From
		}
		if err = m.save(meta); err != nil {
			return err
		}
	}
	return os.Remove(m.journalPath())
}

// WaitSwitch holds a new launch of runtime, or of any runtime when it is
// empty, while a switch runs, so no window starts on the old login mid-swap;
// it gives up after limit.
func WaitSwitch(home, runtime string, limit time.Duration) {
	path := (&Manager{Home: home}).journalPath()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return
		}
		var j journal
		if json.Unmarshal(raw, &j) != nil || runtime != "" && j.Runtime != runtime {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
