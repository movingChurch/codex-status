// Package accounts binds connections to verified, opaque account scopes.
package accounts

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Identity is provider-verified account identity. Callers must not infer it from
// configuration paths or other profile hints. Values remain in memory only.
type Identity struct {
	Service, AuthClass, Principal, Realm string
}

func valid(value string) bool {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func digest(secret []byte, domain string, fields ...string) string {
	mac := hmac.New(sha256.New, secret)
	for _, field := range append([]string{domain}, fields...) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		mac.Write(length[:])
		mac.Write([]byte(field))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// Key returns a domain-separated HMAC of unambiguously length-delimited fields.
// Secret must be nonempty; identity fields must be nonempty UTF-8 strings of at
// most 4096 bytes without control characters. Errors never include field values.
func Key(secret []byte, identity Identity) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("account scope secret is empty")
	}
	fields := []string{identity.Service, identity.AuthClass, identity.Principal, identity.Realm}
	for _, field := range fields {
		if !valid(field) {
			return "", errors.New("invalid account identity field")
		}
	}
	return digest(secret, "verified-account-v1", fields...), nil
}

type binding struct {
	key, generation string
}

type scope struct {
	connections int
	idleSince   time.Time
}

// Registry atomically tracks current connection tickets and idle scope lifetime.
// It stores opaque keys, never raw identities. Its methods are concurrency safe.
type Registry struct {
	mu          sync.Mutex
	secret      []byte
	connections map[string]binding
	scopes      map[string]*scope
}

// NewRegistry copies a nonempty manager-private secret into a new registry.
func NewRegistry(secret []byte) (*Registry, error) {
	if len(secret) == 0 {
		return nil, errors.New("account scope secret is empty")
	}
	return &Registry{secret: append([]byte(nil), secret...), connections: make(map[string]binding), scopes: make(map[string]*scope)}, nil
}

// Rebind replaces a connection's account and generation ticket atomically.
// A nil identity creates a connection-isolated scope. Same-account refreshes
// retain their scope key while invalidating the previous generation ticket.
// Changed reports a new binding or a change to either part of its ticket.
// Invalid inputs leave the current binding intact.
func (r *Registry) Rebind(connection string, identity *Identity, generation string, now time.Time) (key string, changed bool, err error) {
	if !valid(connection) || !valid(generation) {
		return "", false, errors.New("invalid account connection or generation")
	}
	if identity == nil {
		key = digest(r.secret, "unresolved-connection-v1", connection)
	} else {
		key, err = Key(r.secret, *identity)
		if err != nil {
			return "", false, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old, exists := r.connections[connection]
	next := binding{key, generation}
	if exists && old == next {
		return key, false, nil
	}
	if !exists || old.key != key {
		if exists {
			r.detach(old.key, now)
		}
		if r.scopes[key] == nil {
			r.scopes[key] = &scope{}
		}
		r.scopes[key].connections++
		r.scopes[key].idleSince = time.Time{}
	}
	r.connections[connection] = next
	return key, true, nil
}

// Accept reports whether a result belongs to a connection's current ticket.
func (r *Registry) Accept(connection, key, generation string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.connections[connection]
	return ok && current == (binding{key, generation})
}

func (r *Registry) detach(key string, now time.Time) {
	entry := r.scopes[key]
	entry.connections--
	if entry.connections == 0 {
		entry.idleSince = now
	}
}

// Remove invalidates a connection's ticket and starts idle grace for its last scope.
func (r *Registry) Remove(connection string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.connections[connection]; ok {
		delete(r.connections, connection)
		r.detach(old.key, now)
	}
}

// Sweep removes scopes with no connections whose idle grace has elapsed and
// returns their opaque keys. Negative grace behaves as zero. Callers must remove
// departed registrations and coordinate viewer/task lifetime before releasing workers.
func (r *Registry) Sweep(now time.Time, grace time.Duration) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if grace < 0 {
		grace = 0
	}
	var removed []string
	for key, entry := range r.scopes {
		if entry.connections == 0 && !now.Before(entry.idleSince.Add(grace)) {
			delete(r.scopes, key)
			removed = append(removed, key)
		}
	}
	return removed
}
