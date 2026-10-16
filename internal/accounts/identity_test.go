package accounts

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func testIdentity() Identity {
	return Identity{"https://service.example", "oauth", "principal", "organization"}
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := NewRegistry([]byte("manager-private-secret"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func bind(t *testing.T, r *Registry, connection string, identity *Identity, generation string, now time.Time) string {
	t.Helper()
	key, changed, err := r.Rebind(connection, identity, generation, now)
	if err != nil || !changed {
		t.Fatalf("bind: changed=%v err=%v", changed, err)
	}
	return key
}

func TestKeyCanonicalAndScoped(t *testing.T) {
	secret := []byte("secret")
	base := testIdentity()
	key, err := Key(secret, base)
	if err != nil || len(key) != 64 {
		t.Fatal("invalid opaque key")
	}
	variants := []Identity{
		{"https://other.example", base.AuthClass, base.Principal, base.Realm},
		{base.Service, "api-key", base.Principal, base.Realm},
		{base.Service, base.AuthClass, "another", base.Realm},
		{base.Service, base.AuthClass, base.Principal, "other-org"},
	}
	for _, identity := range variants {
		got, _ := Key(secret, identity)
		if got == key {
			t.Fatal("distinct identity merged")
		}
	}
	a, _ := Key(secret, Identity{"ab", "c", "d", "e"})
	b, _ := Key(secret, Identity{"a", "bc", "d", "e"})
	if a == b {
		t.Fatal("ambiguous field encoding")
	}
	c, _ := Key([]byte("other-secret"), base)
	if c == key {
		t.Fatal("secret did not separate scopes")
	}
}

func TestKeyRejectsInvalidFields(t *testing.T) {
	for _, bad := range []string{"", "line\nfeed", "\x00", "\u0085", string([]byte{0xff}), strings.Repeat("x", 4097)} {
		for field := 0; field < 4; field++ {
			identity := testIdentity()
			fields := []*string{&identity.Service, &identity.AuthClass, &identity.Principal, &identity.Realm}
			*fields[field] = bad
			if _, err := Key([]byte("secret"), identity); err == nil {
				t.Fatal("accepted invalid field")
			}
		}
	}
	if _, err := Key(nil, testIdentity()); err == nil {
		t.Fatal("accepted empty secret")
	}
	if _, err := NewRegistry(nil); err == nil {
		t.Fatal("accepted empty registry secret")
	}
}

func TestConcurrentIdentityConvergence(t *testing.T) {
	r := newTestRegistry(t)
	identity := testIdentity()
	const count = 100
	keys := make(chan string, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			connection := fmt.Sprintf("profile/%d", i)
			key, _, err := r.Rebind(connection, &identity, "g1", time.Now())
			if err != nil {
				t.Error(err)
				return
			}
			if !r.Accept(connection, key, "g1") {
				t.Error("current ticket rejected")
			}
			keys <- key
		}(i)
	}
	wg.Wait()
	close(keys)
	var first string
	for key := range keys {
		if first == "" {
			first = key
		}
		if key != first {
			t.Fatal("same identity failed to converge")
		}
	}
	if len(r.scopes) != 1 || r.scopes[first].connections != count {
		t.Fatal("incorrect shared scope count")
	}
}

func TestUnknownIsolationSwitchAndRefresh(t *testing.T) {
	r := newTestRegistry(t)
	now := time.Now()
	a := bind(t, r, "a", nil, "g1", now)
	b := bind(t, r, "b", nil, "g1", now)
	if a == b {
		t.Fatal("unresolved connections merged")
	}
	identity := testIdentity()
	key := bind(t, r, "a", &identity, "g2", now)
	if key == a || r.Accept("a", a, "g1") {
		t.Fatal("old account ticket retained")
	}
	if got := bind(t, r, "a", &identity, "g3", now); got != key {
		t.Fatal("refresh changed scope")
	}
	if r.Accept("a", key, "g2") || !r.Accept("a", key, "g3") {
		t.Fatal("generation validation failed")
	}
	if _, changed, err := r.Rebind("a", &identity, "g3", now); err != nil || changed {
		t.Fatal("unchanged binding changed")
	}
	identity.Realm = "new-org"
	newKey := bind(t, r, "a", &identity, "g4", now)
	if newKey == key || r.Accept("a", key, "g3") {
		t.Fatal("realm switch retained old ticket")
	}
	identity.Principal = ""
	if _, _, err := r.Rebind("a", &identity, "g5", now); err == nil {
		t.Fatal("invalid identity accepted")
	}
	if !r.Accept("a", newKey, "g4") {
		t.Fatal("failed bind changed ticket")
	}
	r.Remove("a", now)
	if r.Accept("a", newKey, "g4") {
		t.Fatal("removed ticket accepted")
	}
}

func TestIdleGraceAndRejoin(t *testing.T) {
	r := newTestRegistry(t)
	now := time.Unix(100, 0)
	identity := testIdentity()
	key := bind(t, r, "a", &identity, "g1", now)
	bind(t, r, "b", &identity, "g1", now)
	r.Remove("a", now)
	if len(r.Sweep(now.Add(time.Hour), 30*time.Second)) != 0 {
		t.Fatal("live scope swept")
	}
	r.Remove("b", now)
	if len(r.Sweep(now.Add(29*time.Second), 30*time.Second)) != 0 {
		t.Fatal("grace ended early")
	}
	bind(t, r, "c", &identity, "g1", now.Add(29*time.Second))
	if len(r.Sweep(now.Add(time.Hour), 30*time.Second)) != 0 {
		t.Fatal("rejoined scope swept")
	}
	r.Remove("c", now.Add(time.Minute))
	removed := r.Sweep(now.Add(90*time.Second), 30*time.Second)
	if len(removed) != 1 || removed[0] != key || len(r.scopes) != 0 {
		t.Fatal("idle scope not released")
	}
	bind(t, r, "d", nil, "g1", now)
	r.Remove("d", now)
	if len(r.Sweep(now, 0)) != 1 {
		t.Fatal("zero grace did not release immediately")
	}
}
