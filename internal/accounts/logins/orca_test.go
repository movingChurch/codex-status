package logins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// orcaFixture lays out two Codex and two Claude accounts the way Orca keeps them.
func orcaFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	orca := f.m.orcaDir()
	for i, id := range []string{"acct-1", "acct-2"} {
		home := filepath.Join(orca, "codex-accounts", "uuid-c"+id, "home")
		if err := os.MkdirAll(home, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "auth.json"), codexAuth(id, id+"@example.com", "r"), 0600); err != nil {
			t.Fatal(err)
		}
		uuid := "uuid-" + string(rune('a'+i))
		auth := filepath.Join(orca, "claude-accounts", uuid, "auth")
		if err := os.MkdirAll(auth, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(auth, "oauth-account.json"), claudeProfile("u-"+id, id+"@claude.test"), 0600); err != nil {
			t.Fatal(err)
		}
		f.secrets.items[orcaClaudeService+"|"+uuid] = claudeItem("rc-" + id)
	}
	return f
}

func TestImportOrcaTwiceChangesNothing(t *testing.T) {
	f := orcaFixture(t)
	f.liveCodex(t, codexAuth("acct-1", "acct-1@example.com", "r"))
	imported, err := f.m.ImportOrca(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported) != 4 {
		t.Fatal("want 2 codex and 2 claude accounts", imported)
	}
	entries, _ := f.m.List()
	if len(entries) != 4 || !entries[2].Current || entries[2].ID != "acct-1" {
		t.Fatal("imported accounts or current codex wrong", entries)
	}
	vault := string(f.secrets.items[VaultService+"|claude:u-acct-2/org"])
	if !strings.Contains(vault, "rc-acct-2") || !strings.Contains(vault, "acct-2@claude.test") {
		t.Fatal("claude login not copied from Orca's item and profile", vault)
	}
	writes := f.secrets.writes
	before, _ := os.ReadFile(f.m.metaPath())
	again, err := f.m.ImportOrca(false)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(f.m.metaPath())
	for _, a := range again {
		if !a.Skipped {
			t.Fatal("second import stored again", a)
		}
	}
	if f.secrets.writes != writes || string(before) != string(after) {
		t.Fatal("second import changed the vault or metadata")
	}
	if _, err := f.m.ImportOrca(true); err != nil || f.secrets.writes != writes+4 {
		t.Fatal("--replace did not store again", err)
	}
	if _, ok := f.secrets.items[orcaClaudeService+"|uuid-a"]; !ok {
		t.Fatal("Orca's own item touched")
	}
}

func TestImportOrcaRefusedWhileOrcaRuns(t *testing.T) {
	f := orcaFixture(t)
	f.procs = []Process{{PID: 9, Args: "/Applications/Orca.app/Contents/MacOS/Orca"}}
	if _, err := f.m.ImportOrca(false); err == nil || f.secrets.writes != 0 {
		t.Fatal("import ran while Orca runs")
	}
}

func TestImportOrcaDeniedPromptWritesNothing(t *testing.T) {
	f := orcaFixture(t)
	f.secrets.denyAt = orcaClaudeService + "|uuid-b"
	if _, err := f.m.ImportOrca(false); err == nil || f.secrets.writes != 0 {
		t.Fatal("denied Keychain prompt still stored logins")
	}
	if _, err := os.Stat(f.m.metaPath()); err == nil {
		t.Fatal("metadata written after a denied prompt")
	}
}
