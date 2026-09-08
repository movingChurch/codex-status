package engine

import (
	"testing"
	"time"
)

type fakeSession struct {
	reads, resets int
	bytes         int64
	invalid       bool
}

func (r *fakeSession) read() map[string]any {
	r.reads++
	r.bytes += 17
	if r.resets > 0 {
		return map[string]any{"five_hour": nil, "quota_updated_at": nil}
	}
	return map[string]any{"model": "fixture", "five_hour": map[string]any{"remaining": 88}, "quota_updated_at": float64(1)}
}
func (r *fakeSession) reset()            { r.resets++ }
func (r *fakeSession) invalidated() bool { return r.invalid }
func (r *fakeSession) bytesRead() int64  { return r.bytes }
func fixtureCollector() (*collector, *fakeSession, *bool, *string) {
	reader := &fakeSession{}
	visible := true
	epoch := "old"
	c := newCollector(dependencies{epoch: func(string) string { return epoch }, bind: func(map[string]any) string { return "/tmp/rollout-12345678-1234-1234-1234-123456789abc.jsonl" }, reader: func(string, string) session { return reader }, visible: func(string) map[string]bool { return map[string]bool{"%1": visible} }})
	c.runs["r"] = &run{meta: map[string]any{"home": "/fixture", "cwd": "/fixture", "tmux_socket": "/fixture.sock", "hud_pane": "%1", "launched_at": float64(0)}, epoch: "old"}
	return c, reader, &visible, &epoch
}
func TestOneReadPerRunAndHiddenPause(t *testing.T) {
	c, r, visible, _ := fixtureCollector()
	now := time.Unix(1000, 0)
	got := c.tick(now)
	if r.reads != 1 || got["r"]["model"] != "fixture" {
		t.Fatalf("visible run must read once and publish fixture: reads=%d snapshots=%v", r.reads, got)
	}
	*visible = false
	got = c.tick(now.Add(5 * time.Second))
	if r.reads != 1 || got["r"]["state"] != "hidden" {
		t.Fatalf("hidden run must stop reads: reads=%d snapshot=%v", r.reads, got)
	}
	if c.metrics["bytes_read"] != 17 {
		t.Fatalf("byte metrics = %v", c.metrics)
	}
}
func TestAccountEpochDropsOldQuota(t *testing.T) {
	c, r, _, epoch := fixtureCollector()
	now := time.Unix(1000, 0)
	c.tick(now)
	*epoch = "new"
	got := c.tick(now.Add(time.Second))["r"]
	if r.resets != 1 || got["five_hour"] != nil || got["state"] != "account-changed" {
		t.Fatalf("old quota survived epoch: reset=%d snapshot=%v", r.resets, got)
	}
}
func TestInvalidatedReaderClearsRun(t *testing.T) {
	c, r, _, _ := fixtureCollector()
	r.invalid = true
	got := c.tick(time.Unix(1000, 0))["r"]
	if c.runs["r"].reader != nil || got["model"] != nil || got["state"] != "ready" {
		t.Fatalf("invalidated reader retained: %+v %v", c.runs["r"], got)
	}
}
