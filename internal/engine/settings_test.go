package engine

import (
	"testing"
	"time"
)

func TestIdleWorkerGraceBounds(t *testing.T) {
	for _, tc := range []struct {
		value   string
		seconds int
		valid   bool
	}{{"", 30, true}, {"0", 0, true}, {"60", 60, true}, {"61", 0, false}, {"-1", 0, false}, {"bad", 0, false}} {
		t.Setenv("RUNTIME_STATUS_IDLE_GRACE_SECONDS", tc.value)
		got, err := idleWorkerGrace()
		if (err == nil) != tc.valid || (tc.valid && got != time.Duration(tc.seconds)*time.Second) {
			t.Fatal(tc.value, got, err)
		}
	}
}
