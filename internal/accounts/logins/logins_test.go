package logins

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeSecrets struct {
	items  map[string][]byte
	denyAt string
	writes int
}

func (f *fakeSecrets) Get(service, account string) ([]byte, error) {
	if f.denyAt == service+"|"+account {
		return nil, errors.New("prompt denied")
	}
	v, ok := f.items[service+"|"+account]
	if !ok {
		return nil, ErrNotFound
	}
	return v, nil
}
func (f *fakeSecrets) Set(service, account string, data []byte) error {
	f.writes++
	f.items[service+"|"+account] = append([]byte(nil), data...)
	return nil
}
func (f *fakeSecrets) Delete(service, account string) error {
	delete(f.items, service+"|"+account)
	return nil
}

func codexAuth(id, email, refresh string) []byte {
	claims, _ := json.Marshal(map[string]string{"email": email})
	token := "h." + base64.RawURLEncoding.EncodeToString(claims) + ".s"
	raw, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "tokens": map[string]string{"account_id": id, "id_token": token, "refresh_token": refresh}})
	return raw
}

func claudeItem(refresh string) []byte {
	raw, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]string{"refreshToken": refresh}, "mcpOAuth": map[string]string{"server": "keep"}})
	return raw
}

func claudeProfile(uuid, email string) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"accountUuid": uuid, "organizationUuid": "org", "emailAddress": email})
	return raw
}

type fixture struct {
	m       *Manager
	secrets *fakeSecrets
	procs   []Process
	signIn  func(runtime, dir string) error
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{secrets: &fakeSecrets{items: map[string][]byte{}}}
	home := t.TempDir()
	f.m = &Manager{Home: home, User: "owner", Env: func(string) string { return "" }, Secrets: f.secrets,
		Processes: func() ([]Process, error) { return f.procs, nil },
		SignIn:    func(r, d string) error { return f.signIn(r, d) },
		Now:       func() time.Time { return time.Unix(1700000000, 0) }}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) liveCodex(t *testing.T, raw []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.m.Home, ".codex", "auth.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) readCodex(t *testing.T) Login {
	t.Helper()
	login, err := codex{home: f.m.Home}.live()
	if err != nil {
		t.Fatal(err)
	}
	return login
}

// addCodex signs a second account in through the temporary home.
func (f *fixture) addCodex(t *testing.T, id, email string) {
	t.Helper()
	f.signIn = func(r, dir string) error {
		if r != "codex" || !strings.HasPrefix(filepath.Base(dir), "runtime-status-login-") {
			t.Fatal("sign-in outside a temporary home", r, dir)
		}
		return os.WriteFile(filepath.Join(dir, "auth.json"), codexAuth(id, email, "r-"+id), 0600)
	}
	if _, err := f.m.Add("codex", false); err != nil {
		t.Fatal(err)
	}
}

func TestAddListRenameRemove(t *testing.T) {
	f := newFixture(t)
	f.liveCodex(t, codexAuth("acct-a", "a@example.com", "r1"))
	if _, err := f.m.Add("codex", true); err != nil {
		t.Fatal(err)
	}
	f.addCodex(t, "acct-b", "b@example.com")
	f.addCodex(t, "acct-c", "c@example.com")
	if f.readCodex(t).ID != "acct-a" {
		t.Fatal("adding through a temporary home changed the live login")
	}
	entries, err := f.m.List()
	if err != nil || len(entries) != 3 || !entries[0].Current || entries[1].Current || entries[2].Order != 3 {
		t.Fatalf("%+v %v", entries, err)
	}
	if err = f.m.Rename("codex", "b@example.com", "work"); err != nil {
		t.Fatal(err)
	}
	if err = f.m.Rename("codex", "c@example.com", "work"); err == nil {
		t.Fatal("duplicate alias accepted")
	}
	if err = f.m.Rename("codex", "c@example.com", "a@example.com"); err == nil {
		t.Fatal("alias equal to another email accepted")
	}
	if err = f.m.Remove("codex", "a@example.com"); err == nil {
		t.Fatal("account in use removed")
	}
	if err = f.m.Remove("codex", "c@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.secrets.items[VaultService+"|codex:acct-c"]; ok {
		t.Fatal("removed account kept its vault item")
	}
	entries, _ = f.m.List()
	if len(entries) != 2 || entries[1].Label() != "work" {
		t.Fatalf("%+v", entries)
	}
}

func TestSwitchSavesRotatedLoginFirst(t *testing.T) {
	f := newFixture(t)
	f.liveCodex(t, codexAuth("acct-a", "a@example.com", "r1"))
	if _, err := f.m.Add("codex", true); err != nil {
		t.Fatal(err)
	}
	f.addCodex(t, "acct-b", "b@example.com")
	// The open window refreshes acct-a's token after it was stored.
	f.liveCodex(t, codexAuth("acct-a", "a@example.com", "r2"))
	stopped := false
	if _, err := f.m.Use("codex", "b@example.com", &Windows{Stop: func(string) error { stopped = true; return nil }}); err != nil {
		t.Fatal(err)
	}
	if !stopped || f.readCodex(t).ID != "acct-b" {
		t.Fatal("switch did not stop windows or write the chosen login")
	}
	login, _ := codex{}.parse(f.secrets.items[VaultService+"|codex:acct-a"])
	if !strings.Contains(string(login.Data), `"r2"`) {
		t.Fatal("rotated login of the previous account was not saved")
	}
	if _, err := f.m.Use("codex", "a@example.com", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(f.readCodex(t).Data), `"r2"`) {
		t.Fatal("switching back lost the rotated token")
	}
}

func TestSwitchSavesUnderLiveIdentity(t *testing.T) {
	f := newFixture(t)
	f.liveCodex(t, codexAuth("acct-a", "a@example.com", "r1"))
	if _, err := f.m.Add("codex", true); err != nil {
		t.Fatal(err)
	}
	f.addCodex(t, "acct-b", "b@example.com")
	// A native login to a third account happened outside runtime-status.
	f.liveCodex(t, codexAuth("acct-z", "z@example.com", "rz"))
	if _, err := f.m.Use("codex", "b@example.com", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(f.secrets.items[VaultService+"|codex:acct-a"]), "rz") {
		t.Fatal("another account's login saved under acct-a")
	}
	if _, ok := f.secrets.items[VaultService+"|codex:acct-z"]; !ok {
		t.Fatal("unknown live login was not kept")
	}
}

func TestRefusalsWriteNothing(t *testing.T) {
	f := newFixture(t)
	f.liveCodex(t, codexAuth("acct-a", "a@example.com", "r1"))
	if _, err := f.m.Add("codex", true); err != nil {
		t.Fatal(err)
	}
	f.addCodex(t, "acct-b", "b@example.com")
	for _, p := range []Process{{PID: 11, Args: "/opt/homebrew/bin/codex resume", Env: map[string]string{}}, {PID: 12, Args: "/Applications/Codex.app/Contents/MacOS/Codex", Env: map[string]string{}}} {
		f.procs = []Process{p}
		if _, err := f.m.Use("codex", "b@example.com", nil); err == nil || !strings.Contains(err.Error(), "pid") {
			t.Fatalf("switch allowed beside %s: %v", p.Args, err)
		}
	}
	f.procs = nil
	f.secrets.denyAt = VaultService + "|codex:acct-b"
	writes := f.secrets.writes
	if _, err := f.m.Use("codex", "b@example.com", nil); err == nil || f.secrets.writes != writes {
		t.Fatal("denied Keychain prompt still wrote", err)
	}
	if f.readCodex(t).ID != "acct-a" {
		t.Fatal("refused switch changed the live login")
	}
	f.m.Env = func(k string) string {
		if k == "CODEX_HOME" {
			return "/elsewhere"
		}
		return ""
	}
	if _, err := f.m.Add("codex", true); err == nil || !strings.Contains(err.Error(), "CODEX_HOME") {
		t.Fatal("--current accepted under a foreign CODEX_HOME", err)
	}
	f.secrets.denyAt = ""
	if _, err := f.m.Use("codex", "b@example.com", nil); err != nil {
		t.Fatal("caller's CODEX_HOME refused a switch of ~/.codex", err)
	}
}

func TestBusyByProcessHome(t *testing.T) {
	f := newFixture(t)
	orca := filepath.Join(f.m.Home, "Library", "Application Support", "orca")
	free := []Process{
		{PID: 10, Args: "/Applications/Orca.app/Contents/MacOS/Orca"},
		{PID: 11, Args: "/opt/homebrew/bin/codex resume", Env: map[string]string{"CODEX_HOME": filepath.Join(orca, "codex-accounts", "u1", "home")}},
		{PID: 12, Args: "/Users/owner/.local/share/claude/versions/2.1.0", Env: map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(orca, "claude-profiles", "p1", "home")}},
		{PID: 13, Args: "/bin/zsh", Env: nil},
	}
	blocking := map[string][]Process{
		"codex": {
			{PID: 20, Args: "/opt/homebrew/bin/codex", Env: map[string]string{}},
			{PID: 21, Args: "/opt/homebrew/bin/codex", Env: map[string]string{"CODEX_HOME": filepath.Join(f.m.Home, ".codex") + "/"}},
			{PID: 22, Args: "/opt/homebrew/bin/codex", Env: map[string]string{"CODEX_HOME": filepath.Join(orca, "codex-runtime-home", "home")}},
			{PID: 23, Args: "/opt/homebrew/bin/codex", Env: nil},
		},
		"claude": {
			{PID: 30, Args: "/opt/homebrew/bin/claude", Env: map[string]string{"HOME": f.m.Home}},
			{PID: 31, Args: "/Users/owner/.local/share/claude/versions/2.1.0", Env: nil},
		},
	}
	for name, procs := range blocking {
		f.procs = free
		if busy, err := f.m.Busy(name); err != nil || len(busy) != 0 {
			t.Fatalf("%s blocked by Orca or its own homes: %v %v", name, busy, err)
		}
		for _, p := range procs {
			f.procs = append(append([]Process{}, free...), p)
			if busy, _ := f.m.Busy(name); len(busy) != 1 {
				t.Fatalf("%s not blocked by %+v: %v", name, p, busy)
			}
		}
	}
}

func TestParseEnvKeepsSpaces(t *testing.T) {
	env := parseEnv(" HOME=/Users/o CODEX_HOME=/Users/o/Library/Application Support/orca/x/home TERM=xterm")
	if env["CODEX_HOME"] != "/Users/o/Library/Application Support/orca/x/home" || env["HOME"] != "/Users/o" || env["TERM"] != "xterm" {
		t.Fatalf("%q", env)
	}
	if parseEnv("  ") != nil {
		t.Fatal("empty environment read as readable")
	}
}

func TestSweepRemovesInterruptedSignIn(t *testing.T) {
	f := newFixture(t)
	t.Setenv("TMPDIR", t.TempDir())
	dir, err := os.MkdirTemp("", signInPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.secrets.Set(scopedService(dir), f.m.User, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err = f.m.List(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("temporary home left after an interrupted sign-in")
	}
	if _, err = f.secrets.Get(scopedService(dir), f.m.User); err == nil {
		t.Fatal("scoped Keychain item left after an interrupted sign-in")
	}
}

func TestKeyringStoreRefused(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.m.Home, ".codex", "config.toml"), []byte("cli_auth_credentials_store = \"keyring\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Add("codex", true); err == nil || !strings.Contains(err.Error(), "file store") {
		t.Fatal("keyring store accepted", err)
	}
}

func TestCrashRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, phase, live, want string
	}{
		{"written before crash", "writing", "acct-b", "acct-b"},
		{"crash before write landed", "writing", "acct-a", "acct-a"},
		{"crash while stopping", "stopping", "acct-a", "acct-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.liveCodex(t, codexAuth("acct-a", "a@example.com", "r1"))
			if _, err := f.m.Add("codex", true); err != nil {
				t.Fatal(err)
			}
			f.addCodex(t, "acct-b", "b@example.com")
			if tc.live == "acct-b" {
				f.liveCodex(t, f.secrets.items[VaultService+"|codex:acct-b"])
			} else if tc.phase == "writing" {
				f.liveCodex(t, []byte("{}"))
			}
			if err := f.m.writeJournal(journal{Runtime: "codex", From: "acct-a", To: "acct-b", Phase: tc.phase}); err != nil {
				t.Fatal(err)
			}
			entries, err := f.m.List()
			if err != nil {
				t.Fatal(err)
			}
			if f.readCodex(t).ID != tc.want {
				t.Fatal("live login after recovery", f.readCodex(t).ID)
			}
			for _, e := range entries {
				if e.Current != (e.ID == tc.want) {
					t.Fatalf("current after recovery %+v", entries)
				}
			}
			if _, err := os.Stat(f.m.journalPath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("journal left behind")
			}
		})
	}
}

func TestReadBackMismatchRestores(t *testing.T) {
	f := newFixture(t)
	f.liveCodex(t, codexAuth("acct-a", "a@example.com", "r1"))
	if _, err := f.m.Add("codex", true); err != nil {
		t.Fatal(err)
	}
	f.addCodex(t, "acct-b", "b@example.com")
	// The vault item was corrupted to another account's login.
	f.secrets.items[VaultService+"|codex:acct-b"] = codexAuth("acct-x", "x@example.com", "rx")
	if _, err := f.m.Use("codex", "b@example.com", nil); err == nil || !strings.Contains(err.Error(), "undone") {
		t.Fatal("mismatched read-back accepted", err)
	}
	if f.readCodex(t).ID != "acct-a" {
		t.Fatal("previous login not restored")
	}
}

func TestLockAndLaunchHold(t *testing.T) {
	f := newFixture(t)
	unlock, err := f.m.lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.m.List(); err == nil {
		t.Fatal("second account command ran beside a held lock")
	}
	unlock()
	if err = f.m.writeJournal(journal{Runtime: "codex", Phase: "writing"}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	WaitSwitch(f.m.Home, "claude", time.Second)
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("a switch of codex held a claude launch")
	}
	go func() { time.Sleep(300 * time.Millisecond); _ = os.Remove(f.m.journalPath()) }()
	start = time.Now()
	WaitSwitch(f.m.Home, "codex", 5*time.Second)
	if d := time.Since(start); d < 250*time.Millisecond || d > 3*time.Second {
		t.Fatal("codex launch not held until the switch ended", d)
	}
}

func TestClaudeSwapKeepsOtherTokens(t *testing.T) {
	f := newFixture(t)
	f.secrets.items[claudeService+"|owner"] = claudeItem("ra")
	profile := map[string]json.RawMessage{"oauthAccount": claudeProfile("u-a", "a@example.com"), "theme": json.RawMessage(`"dark"`)}
	raw, _ := json.Marshal(profile)
	if err := os.WriteFile(filepath.Join(f.m.Home, ".claude.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Add("claude", true); err != nil {
		t.Fatal(err)
	}
	f.signIn = func(r, dir string) error {
		f.secrets.items[scopedService(dir)+"|owner"] = claudeItem("rb")
		p, _ := json.Marshal(map[string]json.RawMessage{"oauthAccount": claudeProfile("u-b", "b@example.com")})
		return os.WriteFile(filepath.Join(dir, ".claude.json"), p, 0600)
	}
	if _, err := f.m.Add("claude", false); err != nil {
		t.Fatal(err)
	}
	for key := range f.secrets.items {
		if strings.HasPrefix(key, claudeService+"-") {
			t.Fatal("temporary sign-in item left in the Keychain", key)
		}
	}
	if _, err := f.m.Use("claude", "b@example.com", nil); err != nil {
		t.Fatal(err)
	}
	item := string(f.secrets.items[claudeService+"|owner"])
	if !strings.Contains(item, `"rb"`) || !strings.Contains(item, `"keep"`) {
		t.Fatal("claude item not swapped in place or MCP tokens lost", item)
	}
	after, _ := readObject(filepath.Join(f.m.Home, ".claude.json"))
	if !strings.Contains(string(after["oauthAccount"]), "b@example.com") || string(after["theme"]) != `"dark"` {
		t.Fatal("profile swap lost settings", after)
	}
}

func TestClaudeCredentialsFileKeptInSync(t *testing.T) {
	f := newFixture(t)
	f.secrets.items[claudeService+"|owner"] = claudeItem("ra")
	raw, _ := json.Marshal(map[string]json.RawMessage{"oauthAccount": claudeProfile("u-a", "a@example.com")})
	if err := os.WriteFile(filepath.Join(f.m.Home, ".claude.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(f.m.Home, ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, claudeItem("ra"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Add("claude", true); err != nil {
		t.Fatal(err)
	}
	f.signIn = func(r, dir string) error {
		f.secrets.items[scopedService(dir)+"|owner"] = claudeItem("rb")
		p, _ := json.Marshal(map[string]json.RawMessage{"oauthAccount": claudeProfile("u-b", "b@example.com")})
		return os.WriteFile(filepath.Join(dir, ".claude.json"), p, 0600)
	}
	if _, err := f.m.Add("claude", false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Use("claude", "b@example.com", nil); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(file)
	if !strings.Contains(string(after), `"rb"`) || !strings.Contains(string(after), `"keep"`) {
		t.Fatal("credentials file not swapped with the Keychain item", string(after))
	}
}

func TestRealKeychainGuardedInTests(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("login Keychain reachable from tests")
		}
	}()
	_, _ = Keychain{}.Get(VaultService, "probe")
}

func TestChunkIndex(t *testing.T) {
	gen, count, ok := parseIndex([]byte(chunkPrefix + "a1b2c3d4:3"))
	if !ok || gen != "a1b2c3d4" || count != 3 || chunkName("codex:x", gen, 2) != "codex:x#a1b2c3d4.2" {
		t.Fatal("chunk index not read back", gen, count, ok)
	}
	for _, bad := range []string{chunkPrefix + "a1:0", chunkPrefix + "a1", chunkPrefix + "a1:x"} {
		if _, _, ok := parseIndex([]byte(bad)); ok {
			t.Fatal("damaged index accepted", bad)
		}
	}
}

func TestSwitchRelaunchesItsWindows(t *testing.T) {
	f := newFixture(t)
	f.liveCodex(t, codexAuth("acct-a", "a@example.com", "r1"))
	if _, err := f.m.Add("codex", true); err != nil {
		t.Fatal(err)
	}
	f.addCodex(t, "acct-b", "b@example.com")
	window := Process{PID: 21, Args: "/opt/homebrew/bin/codex", Env: map[string]string{runVar: "run-1"}}
	outside := Process{PID: 22, Args: "/opt/homebrew/bin/codex", Env: map[string]string{}}
	f.procs = []Process{window, outside}
	stopped := false
	windows := &Windows{Runs: []string{"run-1"}, Stop: func(string) error { stopped = true; return nil }}
	if _, err := f.m.Use("codex", "b@example.com", windows); err == nil || stopped || !strings.Contains(err.Error(), "pid 22") || strings.Contains(err.Error(), "pid 21") {
		t.Fatal("an outside process must refuse the switch before any window stops", err)
	}
	f.procs = []Process{window}
	windows.Stop = func(string) error { stopped = true; f.procs = nil; return nil }
	if _, err := f.m.Use("codex", "b@example.com", windows); err != nil || !stopped || f.readCodex(t).ID != "acct-b" {
		t.Fatal("a switch must stop its own windows and go ahead", err)
	}
}

func TestSelectedLabelAndUsage(t *testing.T) {
	f := newFixture(t)
	f.liveCodex(t, codexAuth("acct-a", "a@example.com", "r1"))
	if _, err := f.m.Add("codex", true); err != nil {
		t.Fatal(err)
	}
	if got := Selected(f.m.Home, "codex"); got != "a@example.com" {
		t.Fatalf("label %q, want the email", got)
	}
	if err := f.m.Rename("codex", "a@example.com", "work"); err != nil {
		t.Fatal(err)
	}
	if got := Selected(f.m.Home, "codex"); got != "work" || Selected(f.m.Home, "claude") != "" {
		t.Fatalf("label %q, want the alias", got)
	}
	if !OnLiveHome(f.m.Home, "codex", filepath.Join(f.m.Home, ".codex")) || OnLiveHome(f.m.Home, "codex", filepath.Join(f.m.Home, "orca", "home")) {
		t.Fatal("only ~/.codex is the live home")
	}
	five := map[string]any{"remaining": 42.0}
	if err := RecordUsage(f.m.Home, "codex", Usage{FiveHour: five}); err != nil {
		t.Fatal(err)
	}
	entries, _ := f.m.List()
	if entries[0].Usage == nil || entries[0].Usage.FiveHour.(map[string]any)["remaining"] != 42.0 || entries[0].Usage.At == "" {
		t.Fatalf("usage not kept for the selected account: %+v", entries[0].Usage)
	}
}

func TestParsePasswordForms(t *testing.T) {
	for output, want := range map[string]string{
		"keychain: \"x\"\n    \"acct\"<blob>=\"p\"\npassword: \"{\"a\":\"b c\"}\"\n": `{"a":"b c"}`,
		"password: 0x7B0A2261223A317D  \"{\\012\"a\":1}\"\n":                         "{\n\"a\":1}",
	} {
		if got, ok := parsePassword(output); !ok || string(got) != want {
			t.Errorf("parsePassword(%q) = %q, %v", output, got, ok)
		}
	}
	if _, ok := parsePassword("no password line"); ok {
		t.Error("output without a password line accepted")
	}
}
