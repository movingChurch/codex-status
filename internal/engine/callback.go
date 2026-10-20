package engine

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"

	"github.com/movingChurch/codex-status/internal/accounts"
	"github.com/movingChurch/codex-status/internal/telemetry"
)

// CallbackBudget bounds the entire short-lived callback, including stdin.
const CallbackBudget = 250 * time.Millisecond

type callbackTicket struct {
	run, epoch string
	scope      string
	sequence   uint64
	issued     time.Time
}
type callbackState struct {
	session, epoch     string
	sequence, accepted uint64
	updates            uint64
	data               map[string]any
	updated            time.Time
	diagnostic         string
	diagnostics        map[string]any
	// Hook activity is kept apart from statusline data, which each payload replaces.
	activity, activitySession string
}
type callbackStore struct {
	tickets  map[string]callbackTicket
	runs     map[string]*callbackState
	registry *accounts.Registry
	scopes   map[string]string
	quota    map[string]map[string]any
}

func newCallbackStore() *callbackStore {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic("secure randomness unavailable")
	}
	registry, _ := accounts.NewRegistry(secret)
	return &callbackStore{tickets: map[string]callbackTicket{}, runs: map[string]*callbackState{}, registry: registry, scopes: map[string]string{}, quota: map[string]map[string]any{}}
}
func (s *callbackStore) bind(id, generation string, identity *accounts.Identity, now time.Time) {
	key, changed, err := s.registry.Rebind(id, identity, generation, now)
	if err != nil {
		return
	}
	s.scopes[id] = key
	if changed {
		if r := s.runs[id]; r != nil {
			r.data = nil
		}
	}
}
func randomToken() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
func (s *callbackStore) ticket(id, epoch string, now time.Time) map[string]any {
	for key, t := range s.tickets {
		if now.Sub(t.issued) > CallbackBudget {
			delete(s.tickets, key)
		}
	}
	r := s.runs[id]
	if r == nil || r.epoch != epoch {
		session := ""
		if r != nil {
			session = r.session
		}
		r = &callbackState{epoch: epoch, session: session}
		s.runs[id] = r
	}
	r.sequence++
	token := randomToken()
	if token == "" {
		return map[string]any{"error": "random-unavailable"}
	}
	s.tickets[token] = callbackTicket{run: id, epoch: epoch, scope: s.scopes[id], sequence: r.sequence, issued: now}
	return map[string]any{"v": 1, "ticket": token}
}
func (s *callbackStore) update(id, epoch, token string, data map[string]any, now time.Time) bool {
	t, ok := s.tickets[token]
	delete(s.tickets, token)
	r := s.runs[id]
	if !ok || r == nil || t.run != id || t.epoch != epoch || r.epoch != epoch || t.scope != s.scopes[id] || now.Sub(t.issued) > CallbackBudget || t.sequence <= r.accepted {
		return false
	}
	session, ok := data["session_id"].(string)
	if !ok || session == "" || len(session) > 512 {
		return false
	}
	if r.session != "" && r.session != session {
		r.data = nil
		r.diagnostic = "session-rebind-required"
		return false
	}
	r.session = session
	r.accepted = t.sequence
	r.updates++
	r.data = data
	r.diagnostic = ""
	r.updated = now
	if key := s.scopes[id]; key != "" && s.registry.Accept(id, key, epoch) {
		q := map[string]any{}
		for _, field := range []string{"five_hour", "weekly", "quota_pools"} {
			q[field] = data[field]
		}
		s.quota[key] = q
	}
	return true
}

// activity records a Claude hook event in arrival order; hooks run synchronously,
// so the manager receives one session's events in firing order.
func (s *callbackStore) activity(id, epoch, session, activity string) bool {
	r := s.runs[id]
	if r == nil || r.epoch != epoch || (r.session != "" && r.session != session) {
		return false
	}
	r.activity, r.activitySession = activity, session
	return true
}
func (s *callbackStore) snapshot(id string, now time.Time) map[string]any {
	r := s.runs[id]
	if r != nil && r.diagnostic == "session-pending" {
		return map[string]any{"state": "ready", "context_status": "pending"}
	}
	if r != nil && r.diagnostic != "" {
		return map[string]any{"state": "error", "context_status": "error", "callback_error": r.diagnostic}
	}
	if r == nil || r.data == nil {
		return nil
	}
	out := map[string]any{}
	merge(out, r.data)
	if r.activity != "" && r.activitySession == r.session {
		out["activity"] = r.activity
	}
	if key := s.scopes[id]; key != "" {
		merge(out, s.quota[key])
	}
	out["state"] = "live"
	if now.Sub(r.updated) > 180*time.Second {
		out["state"] = "stale"
		out["context_status"] = "stale"
	}
	return out
}

func (s *callbackStore) reject(id, epoch, token string, now time.Time) bool {
	t, ok := s.tickets[token]
	delete(s.tickets, token)
	r := s.runs[id]
	if !ok || r == nil || t.run != id || t.epoch != epoch || t.scope != s.scopes[id] || t.sequence <= r.accepted || now.Sub(t.issued) > CallbackBudget {
		return false
	}
	r.accepted = t.sequence
	r.data = nil
	r.diagnostic = "incompatible-payload"
	return true
}

func (s *callbackStore) pending(id, epoch, token string, now time.Time) bool {
	if !s.reject(id, epoch, token, now) {
		return false
	}
	r := s.runs[id]
	if r.session != "" {
		r.diagnostic = "session-rebind-required"
		return false
	}
	r.diagnostic = "session-pending"
	return true
}

// RPC performs one bounded exchange with the existing manager; it never starts one.
func RPC(runtime string, value map[string]any, deadline time.Time) (map[string]any, error) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return nil, errors.New("request deadline exceeded")
	}
	conn, err := collectorConnectTimeout(runtime, remaining)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if err = conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	peer, peerErr := peerPID(conn)
	if peerErr != nil || peer < 1 {
		return nil, errors.New("manager peer identity unavailable")
	}
	if err = json.NewEncoder(conn).Encode(value); err != nil {
		return nil, err
	}
	var response map[string]any
	err = json.NewDecoder(&boundedReader{conn: conn, left: maxMessage}).Decode(&response)
	if err == nil && value["op"] == "status" {
		if int(number(response["pid"])) != peer || stringValue(response["started"]) == "" || telemetry.ProcessIdentity(peer) != response["started"] {
			err = errors.New("manager process identity mismatch")
		}
	}
	if err == nil && response["error"] != nil {
		err = errors.New("manager rejected request")
	}
	return response, err
}

// Handshake validates native socket peer identity and the product's required IPC
// capabilities independently of the vendor CLI release.
func Handshake(root string) error {
	state, err := RPC(root, map[string]any{"v": 1, "op": "status"}, time.Now().Add(time.Second))
	if err != nil {
		return err
	}
	if state["protocol"] != float64(2) {
		return errors.New("incompatible manager protocol")
	}
	caps, _ := state["capabilities"].([]any)
	set := map[string]bool{}
	for _, capability := range caps {
		if name, ok := capability.(string); ok {
			set[name] = true
		}
	}
	for _, required := range []string{"callback-ticket", "adaptive-metrics", "shared-tmux"} {
		if !set[required] {
			return errors.New("incompatible manager capabilities")
		}
	}
	return nil
}

type boundedReader struct {
	conn net.Conn
	left int
}

func (r *boundedReader) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, errors.New("response too large")
	}
	if len(p) > r.left {
		p = p[:r.left]
	}
	n, e := r.conn.Read(p)
	r.left -= n
	return n, e
}

func liveMetadata(meta map[string]any) bool {
	pid := int(number(meta["pid"]))
	started := stringValue(meta["started"])
	return pid > 0 && started != "" && telemetry.ProcessIdentity(pid) == started
}
func processIdentity() string { return telemetry.ProcessIdentity(os.Getpid()) }
