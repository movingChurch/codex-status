package engine

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/movingChurch/runtime-status/internal/accounts/logins"
	"github.com/movingChurch/runtime-status/internal/telemetry"
)

func writeAccounts(t *testing.T, home, alias string) {
	t.Helper()
	state := filepath.Join(home, ".local", "state", "runtime-status")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	raw := `{"version":1,"accounts":[{"runtime":"codex","id":"acct-a","email":"a@example.com","alias":"` + alias + `","order":1}],"current":{"codex":"acct-a"}}`
	if err := os.WriteFile(filepath.Join(state, "accounts.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestHUDFirstRowShowsSelectedAccount(t *testing.T) {
	home := t.TempDir()
	live := filepath.Join(home, ".codex")
	if err := os.MkdirAll(live, 0700); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	recorded := []logins.Usage{}
	book := &accountBook{home: home, last: map[string]string{}, at: map[string]time.Time{}, record: func(_, _ string, u logins.Usage) {
		mu.Lock()
		recorded = append(recorded, u)
		mu.Unlock()
	}}
	firstRow := func(meta map[string]any) string {
		snapshot := map[string]any{"runtime": "codex", "model": "gpt-5", "five_hour": map[string]any{"remaining": 40.0}}
		book.annotate("run", meta, snapshot, time.Now())
		return telemetry.Render(snapshot, 120, false)[0]
	}
	onLive := map[string]any{"runtime": "codex", "home": live}
	for _, c := range []struct{ alias, want string }{{"", "a@example.com ▾ │ gpt-5"}, {"work", "work ▾ │ gpt-5"}} {
		writeAccounts(t, home, c.alias)
		if row := firstRow(onLive); !strings.HasPrefix(row, c.want) {
			t.Errorf("first row %q, want it to start with %q", row, c.want)
		}
	}
	if row := firstRow(map[string]any{"runtime": "codex", "home": filepath.Join(home, "orca")}); !strings.HasPrefix(row, "gpt-5") {
		t.Errorf("a window on another home must show no account: %q", row)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(recorded) != 1 {
		t.Errorf("one unchanged reading must be recorded once, got %d", len(recorded))
	}
}

func TestHUDAccountLabelLooksLikeALink(t *testing.T) {
	row := telemetry.Render(map[string]any{"runtime": "codex", "account": "a@example.com"}, 120, true)[0]
	if !strings.Contains(row, "\x1b[0;4;") || !strings.Contains(row, "a@example.com ▾") {
		t.Fatalf("label must be underlined with a menu mark: %q", row)
	}
}
