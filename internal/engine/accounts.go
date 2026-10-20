package engine

import (
	"encoding/json"
	"os"
	"time"

	"github.com/movingChurch/runtime-status/internal/accounts/logins"
)

// accountBook labels runs on a runtime's live home with the selected account
// from accounts.json and records their quota readings against that account.
// A run's home never changes, so whether it is live is kept by run ID.
type accountBook struct {
	home     string
	record   func(home, runtime string, usage logins.Usage)
	last     map[string]string
	at       map[string]time.Time
	live     map[string]bool
	selected logins.SelectedCache
}

func newAccountBook() *accountBook {
	home, _ := os.UserHomeDir()
	record := func(home, runtime string, usage logins.Usage) { _ = logins.RecordUsage(home, runtime, usage) }
	return &accountBook{home: home, record: record, last: map[string]string{}, at: map[string]time.Time{}, live: map[string]bool{}, selected: logins.SelectedCache{Home: home}}
}

func (b *accountBook) annotate(id string, meta, snapshot map[string]any, now time.Time) {
	kind := stringValue(meta["runtime"])
	if b.home == "" {
		return
	}
	live, known := b.live[id]
	if !known {
		live = logins.OnLiveHome(b.home, kind, stringValue(meta["home"]))
		b.live[id] = live
	}
	if !live {
		return
	}
	label := b.selected.Label(kind)
	if label == "" {
		return
	}
	snapshot["account"] = label
	if snapshot["state"] == "account-changed" || snapshot["five_hour"] == nil && snapshot["weekly"] == nil && snapshot["monthly"] == nil {
		return
	}
	// A reading is kept when it changes, and at least every five minutes.
	reading, _ := json.Marshal([]any{snapshot["five_hour"], snapshot["weekly"], snapshot["monthly"]})
	if string(reading) == b.last[id] && now.Sub(b.at[id]) < 5*time.Minute {
		return
	}
	b.last[id], b.at[id] = string(reading), now
	usage := logins.Usage{FiveHour: snapshot["five_hour"], Weekly: snapshot["weekly"], Monthly: snapshot["monthly"], At: now.UTC().Format(time.RFC3339)}
	go b.record(b.home, kind, usage)
}

func (b *accountBook) forget(id string) {
	delete(b.last, id)
	delete(b.at, id)
	delete(b.live, id)
}
