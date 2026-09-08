package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const eventID = "11111111-1111-7111-8111-111111111111"

func eventFixture(t *testing.T) (string, func(string, map[string]any)) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rollout.jsonl")
	appendRecord := func(kind string, payload map[string]any) {
		b, _ := json.Marshal(map[string]any{"type": kind, "timestamp": "2026-09-08T01:00:00Z", "payload": payload})
		f, e := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		f.Write(append(b, '\n'))
	}
	appendRecord("session_meta", map[string]any{"id": eventID, "session_id": eventID, "source": "cli", "cli_version": "0.153.4"})
	return p, appendRecord
}
func TestEventsContextAndCorrelation(t *testing.T) {
	p, add := eventFixture(t)
	add("event_msg", map[string]any{"type": "token_count", "info": map[string]any{"model_context_window": 12200, "last_token_usage": map[string]any{"total_tokens": 12003}}, "rate_limits": map[string]any{"primary": map[string]any{"window_minutes": 300, "used_percent": 25}}})
	r := NewSessionReader(p, eventID)
	s := r.Read()
	if s["context_remaining"] != 99 {
		t.Fatal(s)
	}
	add("event_msg", map[string]any{"type": "task_started", "turn_id": "a"})
	add("event_msg", map[string]any{"type": "exec_command_begin", "turn_id": "a", "call_id": "c", "command": "SECRET"})
	add("event_msg", map[string]any{"type": "task_complete", "turn_id": "b"})
	s = r.Read()
	b, _ := json.Marshal(s)
	if s["tool"] != "exec_command" || strings.Contains(string(b), "SECRET") {
		t.Fatal(s)
	}
	add("event_msg", map[string]any{"type": "task_complete", "turn_id": "a"})
	if r.Read()["activity"] != "complete" {
		t.Fatal("turn not completed")
	}
}
func TestEventsBoundsAndIdentity(t *testing.T) {
	p, add := eventFixture(t)
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(strings.Repeat("x", 800000) + "\n")
	f.Close()
	add("turn_context", map[string]any{"model": "gpt-tail", "effort": "high"})
	r := NewSessionReader(p, eventID)
	for i := 0; i < 3; i++ {
		before := r.BytesRead
		r.Read()
		if r.BytesRead-before > 262144 {
			t.Fatal("budget exceeded")
		}
	}
	if r.Read()["model"] != "gpt-tail" {
		t.Fatal("tail lost")
	}
	os.Truncate(p, 0)
	if len(r.Read()) != 0 || !r.Invalidated {
		t.Fatal("truncation accepted")
	}
}
func TestEventsPartialAndAccountReset(t *testing.T) {
	p, add := eventFixture(t)
	r := NewSessionReader(p, eventID)
	r.Read()
	raw := `{"type":"turn_context","timestamp":"2026-09-08T01:00:00Z","payload":{"model":"old","effort":"low"}}`
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(raw[:20])
	f.Close()
	r.Read()
	r.ResetAfterAccountChange()
	f, _ = os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(raw[20:] + "\n")
	f.Close()
	if r.Read()["model"] != nil {
		t.Fatal("old partial crossed account")
	}
	add("turn_context", map[string]any{"model": "new", "effort": "high"})
	if r.Read()["model"] != "new" {
		t.Fatal("new event missing")
	}
	before := r.BytesRead
	r.Read()
	if before != r.BytesRead {
		t.Fatal("idle read repeated")
	}
}

func TestEventsRejectHeadersAndSymlinks(t *testing.T) {
	for _, change := range []string{"version", "owner", "symlink", "replacement"} {
		t.Run(change, func(t *testing.T) {
			p, _ := eventFixture(t)
			r := NewSessionReader(p, eventID)
			switch change {
			case "version":
				b, _ := os.ReadFile(p)
				os.WriteFile(p, []byte(strings.ReplaceAll(string(b), "0.153.4", "0.1.0")), 0600)
			case "owner":
				r = NewSessionReader(p, "22222222-2222-7222-8222-222222222222")
			case "symlink":
				os.Rename(p, p+".real")
				os.Symlink(p+".real", p)
			case "replacement":
				r.Read()
				b, _ := os.ReadFile(p)
				os.WriteFile(p+".new", b, 0600)
				os.Rename(p+".new", p)
			}
			if len(r.Read()) != 0 || !r.Invalidated {
				t.Fatal("unsafe header accepted")
			}
		})
	}
}
func TestEventsQuotaScopeAndSanitization(t *testing.T) {
	p, add := eventFixture(t)
	add("turn_context", map[string]any{"model": "\x1b[31mgpt-test\x1b[0m", "effort": "high"})
	add("event_msg", map[string]any{"type": "token_count", "rate_limits": map[string]any{"limit_id": "codex", "secondary": map[string]any{"window_minutes": 300, "used_percent": 25}}})
	r := NewSessionReader(p, eventID)
	s := r.Read()
	if s["model"] != "gpt-test" || s["five_hour"].(map[string]any)["remaining"] != float64(75) {
		t.Fatal(s)
	}
	observed := s["quota_updated_at"]
	s["five_hour"].(map[string]any)["remaining"] = 0
	add("event_msg", map[string]any{"type": "token_count", "rate_limits": map[string]any{"limit_id": "other"}})
	s = r.Read()
	if s["quota_scope"] != "codex" || s["quota_updated_at"] != observed || s["five_hour"].(map[string]any)["remaining"] != float64(75) {
		t.Fatal(s)
	}
	add("event_msg", map[string]any{"type": "thread_settings_applied", "thread_id": "other", "thread_settings": map[string]any{"model": "wrong"}})
	if r.Read()["model"] != "gpt-test" {
		t.Fatal("foreign settings accepted")
	}
	add("event_msg", map[string]any{"type": "token_count", "info": map[string]any{"model_context_window": true, "last_token_usage": map[string]any{"total_tokens": 0}}})
	if r.Read()["context_remaining"] != nil {
		t.Fatal("bool accepted as integer")
	}
}
