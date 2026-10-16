package engine

import (
	"testing"
	"time"

	"github.com/movingChurch/codex-status/internal/accounts"
)

func TestCallbackTicketsAndSessionPin(t *testing.T) {
	s := newCallbackStore()
	now := time.Now()
	s.bind("run", "auth1", nil, now)
	first := s.ticket("run", "auth1", now)["ticket"].(string)
	second := s.ticket("run", "auth1", now)["ticket"].(string)
	data := map[string]any{"session_id": "session-a", "context_used": 12}
	if !s.update("run", "auth1", second, data, now) {
		t.Fatal("new ticket rejected")
	}
	if s.update("run", "auth1", first, data, now) {
		t.Fatal("out of order ticket accepted")
	}
	if s.update("run", "auth1", second, data, now) {
		t.Fatal("reused ticket accepted")
	}
	third := s.ticket("run", "auth1", now)["ticket"].(string)
	if s.update("run", "auth1", third, map[string]any{"session_id": "session-b"}, now) {
		t.Fatal("ordinary payload changed session")
	}
	old := s.ticket("run", "auth1", now)["ticket"].(string)
	s.bind("run", "auth2", nil, now)
	fresh := s.ticket("run", "auth2", now)["ticket"].(string)
	if s.snapshot("run", now) != nil {
		t.Fatal("old auth data retained")
	}
	if s.update("run", "auth2", old, data, now) {
		t.Fatal("old auth ticket accepted")
	}
	if s.update("run", "auth2", fresh, map[string]any{"session_id": "session-b"}, now) {
		t.Fatal("refresh implicitly changed session")
	}
	fresh = s.ticket("run", "auth2", now)["ticket"].(string)
	if !s.update("run", "auth2", fresh, data, now) {
		t.Fatal("same session refresh rejected")
	}
	expired := s.ticket("run", "auth2", now)["ticket"].(string)
	if s.update("run", "auth2", expired, data, now.Add(CallbackBudget+time.Millisecond)) {
		t.Fatal("expired ticket accepted")
	}
	other := newCallbackStore()
	if other.update("run", "auth2", fresh, data, now) {
		t.Fatal("restart retained ticket")
	}
}

func TestCallbackScopedQuotasAndIsolatedContext(t *testing.T) {
	s := newCallbackStore()
	now := time.Now()
	identity := &accounts.Identity{Service: "provider", AuthClass: "oauth", Principal: "opaque-user", Realm: "org-a"}
	for _, id := range []string{"a", "b"} {
		s.bind(id, "auth", identity, now)
	}
	publish := func(id string, data map[string]any) {
		t.Helper()
		token := s.ticket(id, "auth", now)["ticket"].(string)
		if !s.update(id, "auth", token, data, now) {
			t.Fatal("publish failed")
		}
	}
	publish("a", map[string]any{"session_id": "session-a", "context_used": 12, "weekly": map[string]any{"remaining": 80}})
	publish("b", map[string]any{"session_id": "session-b", "context_used": 34, "weekly": map[string]any{"remaining": 70}})
	a := s.snapshot("a", now)
	if a["context_used"] != 12 || a["weekly"].(map[string]any)["remaining"] != 70 {
		t.Fatal(a)
	}
	old := s.ticket("a", "auth", now)["ticket"].(string)
	other := &accounts.Identity{Service: "provider", AuthClass: "oauth", Principal: "opaque-user", Realm: "org-b"}
	s.bind("a", "auth", other, now)
	if s.snapshot("a", now) != nil || s.update("a", "auth", old, map[string]any{"session_id": "session-a"}, now) {
		t.Fatal("account switch retained old observation")
	}
	publish("a", map[string]any{"session_id": "session-a", "context_used": 56})
	if s.snapshot("a", now)["weekly"] != nil {
		t.Fatal("new realm inherited quota")
	}
	publish("b", map[string]any{"session_id": "session-b", "context_used": 34})
	if s.snapshot("b", now)["weekly"] != nil {
		t.Fatal("absent window retained")
	}
}

func TestCallbackPreConversationPending(t *testing.T) {
	s := newCallbackStore()
	now := time.Now()
	s.bind("a", "auth", nil, now)
	token := s.ticket("a", "auth", now)["ticket"].(string)
	if !s.pending("a", "auth", token, now) || s.snapshot("a", now)["context_status"] != "pending" {
		t.Fatal("startup not pending")
	}
	token = s.ticket("a", "auth", now)["ticket"].(string)
	if !s.update("a", "auth", token, map[string]any{"session_id": "created-session"}, now) {
		t.Fatal("pending prevented real session binding")
	}
	token = s.ticket("a", "auth", now)["ticket"].(string)
	if s.pending("a", "auth", token, now) || s.runs["a"].session != "created-session" {
		t.Fatal("pending reset established session")
	}
}

func TestOldPendingCannotEraseNewObservation(t *testing.T) {
	s := newCallbackStore()
	now := time.Now()
	s.bind("a", "auth", nil, now)
	old := s.ticket("a", "auth", now)["ticket"].(string)
	fresh := s.ticket("a", "auth", now)["ticket"].(string)
	if !s.update("a", "auth", fresh, map[string]any{"session_id": "new-session", "context_used": 10}, now) {
		t.Fatal("new observation rejected")
	}
	if s.pending("a", "auth", old, now) || s.snapshot("a", now)["context_used"] != 10 {
		t.Fatal("old pending erased new observation")
	}
}
