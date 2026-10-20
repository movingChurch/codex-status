package logins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Process is one running process, its first argument the executable.
type Process struct {
	PID  int
	Args string
	// Env is the process environment, nil when it cannot be read.
	Env map[string]string
}

// Manager runs the account commands against one HOME; tests replace Secrets,
// Processes and SignIn so the login Keychain and native CLIs stay untouched.
type Manager struct {
	Home      string
	User      string
	Env       func(string) string
	Secrets   Secrets
	Processes func() ([]Process, error)
	// SignIn runs the native sign-in of runtime with its home in dir.
	SignIn func(runtime, dir string) error
	Now    func() time.Time
}

// Runtimes lists the runtimes whose logins can be switched.
var Runtimes = []string{"codex", "claude"}

// NewManager wires the real Keychain, process list and native sign-in.
func NewManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Manager{Home: home, User: os.Getenv("USER"), Env: os.Getenv, Secrets: Keychain{}, Processes: listProcesses, SignIn: nativeSignIn, Now: time.Now}, nil
}

func (m *Manager) runtime(name string) (runtime, error) {
	switch name {
	case "codex":
		return codex{home: m.Home}, nil
	case "claude":
		return claude{home: m.Home, user: m.User, secrets: m.Secrets}, nil
	}
	return nil, fmt.Errorf("unknown runtime %q; use codex or claude", name)
}

// Entry is one account as `account list` reports it.
type Entry struct {
	Account
	Current bool   `json:"current"`
	Usage   *Usage `json:"usage,omitempty"`
}

// List returns every stored account, finishing an interrupted switch first.
func (m *Manager) List() ([]Entry, error) {
	unlock, err := m.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err = m.recover(); err != nil {
		return nil, err
	}
	meta, err := m.load()
	if err != nil {
		return nil, err
	}
	entries := []Entry{}
	for _, a := range meta.Accounts {
		e := Entry{Account: a, Current: meta.Current[a.Runtime] == a.ID}
		if u, ok := meta.Usage[vaultKey(a.Runtime, a.ID)]; ok {
			e.Usage = &u
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Runtime != entries[j].Runtime {
			return entries[i].Runtime < entries[j].Runtime
		}
		return entries[i].Order < entries[j].Order
	})
	return entries, nil
}

// Add stores a login: the one in use with current, else a new native
// sign-in made in a temporary home that is deleted afterwards.
func (m *Manager) Add(name string, current bool) (Account, error) {
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
	var login Login
	if current {
		if err = rt.check(); err != nil {
			return Account{}, err
		}
		if err = m.checkCaller(name); err != nil {
			return Account{}, err
		}
		if login, err = rt.live(); err != nil {
			return Account{}, err
		}
	} else if login, err = m.signInFresh(name, rt); err != nil {
		return Account{}, err
	}
	return m.store(name, login, current)
}

// homeVars names the variable that moves each runtime's home.
var homeVars = map[string]string{"codex": "CODEX_HOME", "claude": "CLAUDE_CONFIG_DIR"}

// checkCaller refuses `add --current` from a terminal whose runtime runs on
// another home, since the login in use there is not the one stored here.
func (m *Manager) checkCaller(name string) error {
	v := m.Env(homeVars[name])
	if v != "" && filepath.Clean(v) != m.defaultHome(name) {
		return fmt.Errorf("%s is set to %s; --current covers only %s", homeVars[name], v, m.defaultHome(name))
	}
	return nil
}

func (m *Manager) defaultHome(name string) string {
	if name == "codex" {
		return filepath.Join(m.Home, ".codex")
	}
	return filepath.Join(m.Home, ".claude")
}

func (m *Manager) store(name string, login Login, current bool) (Account, error) {
	meta, err := m.load()
	if err != nil {
		return Account{}, err
	}
	if err = m.Secrets.Set(VaultService, vaultKey(name, login.ID), login.Data); err != nil {
		return Account{}, err
	}
	meta.remember(name, login, m.Now())
	if current || meta.Current[name] == "" {
		if live, err := m.liveID(name); err == nil && live == login.ID {
			meta.Current[name] = login.ID
		}
	}
	if err = m.save(meta); err != nil {
		return Account{}, err
	}
	i, _ := meta.find(name, login.ID)
	return meta.Accounts[i], nil
}

func (m *Manager) liveID(name string) (string, error) {
	rt, err := m.runtime(name)
	if err != nil {
		return "", err
	}
	login, err := rt.live()
	return login.ID, err
}

const signInPrefix = "runtime-status-login-"

// sweep removes the temporary homes and Claude Keychain items an interrupted
// sign-in left behind; callers hold the accounts lock, so none is in use.
func (m *Manager) sweep() {
	dirs, _ := filepath.Glob(filepath.Join(os.TempDir(), signInPrefix+"*"))
	for _, dir := range dirs {
		_ = m.Secrets.Delete(scopedService(dir), m.User)
		_ = os.RemoveAll(dir)
	}
}

// signInFresh signs in inside a temporary home, then reads and deletes it,
// so the login in use is untouched and no copy is left behind.
func (m *Manager) signInFresh(name string, rt runtime) (Login, error) {
	dir, err := os.MkdirTemp("", signInPrefix)
	if err != nil {
		return Login{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	switch name {
	case "codex":
		if err = os.WriteFile(filepath.Join(dir, "config.toml"), []byte("cli_auth_credentials_store = \"file\"\n"), 0600); err != nil {
			return Login{}, err
		}
		if err = m.SignIn(name, dir); err != nil {
			return Login{}, err
		}
		raw, err := os.ReadFile(filepath.Join(dir, "auth.json"))
		if err != nil {
			return Login{}, errors.New("codex sign-in left no login")
		}
		return rt.parse(raw)
	default:
		service := scopedService(dir)
		defer func() { _ = m.Secrets.Delete(service, m.User) }()
		if err = m.SignIn(name, dir); err != nil {
			return Login{}, err
		}
		item, err := m.Secrets.Get(service, m.User)
		if err != nil {
			return Login{}, errors.New("claude sign-in left no login")
		}
		var creds map[string]json.RawMessage
		_ = json.Unmarshal(item, &creds)
		profile, err := readObject(filepath.Join(dir, ".claude.json"))
		if err != nil {
			return Login{}, err
		}
		data, _ := json.Marshal(claudeLogin{OAuth: creds["claudeAiOauth"], Account: profile["oauthAccount"]})
		return rt.parse(data)
	}
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

// Rename sets the alias shown for an account; an empty alias clears it.
func (m *Manager) Rename(name, account, alias string) error {
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	meta, err := m.load()
	if err != nil {
		return err
	}
	i, err := meta.find(name, account)
	if err != nil {
		return err
	}
	for j, a := range meta.Accounts {
		if j != i && a.Runtime == name && alias != "" && (strings.EqualFold(a.Email, alias) || a.Alias == alias || a.ID == alias) {
			return fmt.Errorf("alias %s already names another %s account", alias, name)
		}
	}
	meta.Accounts[i].Alias = alias
	return m.save(meta)
}

// Remove deletes a stored account other than the one in use.
func (m *Manager) Remove(name, account string) error {
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err = m.recover(); err != nil {
		return err
	}
	meta, err := m.load()
	if err != nil {
		return err
	}
	i, err := meta.find(name, account)
	if err != nil {
		return err
	}
	if meta.Current[name] == meta.Accounts[i].ID {
		return errors.New("the account in use cannot be removed; switch to another account first")
	}
	if err = m.Secrets.Delete(VaultService, vaultKey(name, meta.Accounts[i].ID)); err != nil {
		return err
	}
	delete(meta.Usage, vaultKey(name, meta.Accounts[i].ID))
	meta.Accounts = append(meta.Accounts[:i], meta.Accounts[i+1:]...)
	return m.save(meta)
}

// Busy names the processes that block a switch of runtime: those of that
// runtime whose home is the live one, Orca's Codex mirror of it, or unknown.
// Orca itself and windows on Orca's own account homes are left alone.
func (m *Manager) Busy(name string) ([]string, error) {
	processes, err := m.Processes()
	if err != nil {
		return nil, errors.New("process list unavailable")
	}
	busy := []string{}
	for _, p := range processes {
		if p.PID == os.Getpid() || !isRuntime(name, strings.Fields(p.Args + " ")[0]) {
			continue
		}
		switch {
		case p.Env == nil:
			busy = append(busy, fmt.Sprintf("%s (pid %d, home unknown)", name, p.PID))
		case m.usesLiveHome(name, p.Env[homeVars[name]]):
			busy = append(busy, fmt.Sprintf("%s (pid %d)", name, p.PID))
		}
	}
	return dedupe(busy), nil
}

func isRuntime(name, argv0 string) bool {
	if name == "codex" {
		return filepath.Base(argv0) == "codex" || strings.Contains(argv0, "/Codex.app/")
	}
	return filepath.Base(argv0) == "claude" || strings.Contains(argv0, "/claude/versions/")
}

// usesLiveHome reports whether a process home is the one a switch rewrites.
// Orca copies ~/.codex into its runtime home and writes refreshed logins back,
// so a window there counts as one on ~/.codex.
func (m *Manager) usesLiveHome(name, home string) bool {
	if home == "" || filepath.Clean(home) == m.defaultHome(name) {
		return true
	}
	mirror := filepath.Join(m.Home, "Library", "Application Support", "orca", "codex-runtime-home")
	return name == "codex" && strings.HasPrefix(filepath.Clean(home)+"/", mirror+"/")
}

func dedupe(items []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

// listProcesses reads every process with its environment: `ps -E` prints the
// environment after the arguments, so the arguments of a plain `ps` mark where
// it starts; an empty remainder means the environment was not readable.
func listProcesses() ([]Process, error) {
	plain, err := runPS("-axww", "-o", "pid=,args=")
	if err != nil {
		return nil, err
	}
	withEnv, err := runPS("-E", "-axww", "-o", "pid=,args=")
	if err != nil {
		return nil, err
	}
	processes := []Process{}
	for pid, args := range plain {
		p := Process{PID: pid, Args: args}
		if full, ok := withEnv[pid]; ok && strings.HasPrefix(full, args) {
			p.Env = parseEnv(strings.TrimPrefix(full, args))
		}
		processes = append(processes, p)
	}
	return processes, nil
}

func runPS(args ...string) (map[int]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", args...).Output()
	if err != nil {
		return nil, err
	}
	lines := map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimLeft(line, " ")
		pidText, rest, ok := strings.Cut(line, " ")
		pid, err := strconv.Atoi(pidText)
		if !ok || err != nil {
			continue
		}
		lines[pid] = strings.TrimLeft(rest, " ")
	}
	return lines, nil
}

var envStart = regexp.MustCompile(` [A-Za-z_][A-Za-z0-9_]*=`)

// parseEnv splits `ps -E` output at each " NAME=", so values keep their spaces.
func parseEnv(text string) map[string]string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	text = " " + strings.TrimLeft(text, " ")
	env := map[string]string{}
	starts := envStart.FindAllStringIndex(text, -1)
	for i, at := range starts {
		end := len(text)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		key, value, _ := strings.Cut(text[at[0]+1:end], "=")
		env[key] = value
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

// nativeSignIn runs `codex login` or `claude auth login` on the terminal with
// its home pointed at dir.
func nativeSignIn(name, dir string) error {
	var cmd *exec.Cmd
	switch name {
	case "codex":
		cmd = exec.Command("codex", "login")
		cmd.Env = append(os.Environ(), "CODEX_HOME="+dir)
	default:
		cmd = exec.Command("claude", "auth", "login")
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
