package engine

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/movingChurch/runtime-status/internal/telemetry"
)

type session interface {
	read() map[string]any
	reset()
	invalidated() bool
	bytesRead() int64
}
type dependencies struct {
	epoch       func(string) string
	bind        func(map[string]any) string
	bindStatus  func(map[string]any) (string, string)
	reader      func(string, string) session
	visible     func(string) map[string]bool
	account     func(context.Context, string, string) map[string]any
	accountOnce bool
	links       func(string, string, string) map[string]any
}
type run struct {
	meta           map[string]any
	epoch          string
	reader         session
	path           string
	lastBinding    time.Time
	snapshot       map[string]any
	accountChanged bool
	bindingIssue   string
}
type collector struct {
	deps         dependencies
	runs         map[string]*run
	visible      map[string]map[string]bool
	visibilityAt time.Time
	metrics      map[string]int64
	accounts     *accountCache
	links        *linkCache
	branches     *branchCache
}

func newCollector(d dependencies) *collector {
	accounts := newAccountCache(d.account)
	accounts.once = d.accountOnce
	return &collector{deps: d, runs: map[string]*run{}, visible: map[string]map[string]bool{}, metrics: map[string]int64{}, accounts: accounts, links: newLinkCache(d.links), branches: newBranchCache()}
}
func (c *collector) tick(now time.Time) map[string]map[string]any {
	c.metrics["ticks"]++
	if c.visibilityAt.IsZero() || now.Sub(c.visibilityAt) >= 5*time.Second {
		sockets := map[string]bool{}
		for _, r := range c.runs {
			sockets[stringValue(r.meta["tmux_socket"])] = true
		}
		c.visible = c.queryVisibility(sockets)
		c.metrics["tmux_queries"] += int64(len(sockets))
		c.visibilityAt = now
	}
	snapshots := map[string]map[string]any{}
	for id, r := range c.runs {
		snapshot := pending(r.meta)
		if !c.visible[stringValue(r.meta["tmux_socket"])][stringValue(r.meta["hud_pane"])] {
			snapshot["state"] = "hidden"
			snapshots[id] = snapshot
			continue
		}
		epoch := c.deps.epoch(stringValue(r.meta["home"]))
		if epoch != r.epoch {
			r.epoch = epoch
			r.accountChanged = true
			r.snapshot = nil
			if r.reader != nil {
				r.reader.reset()
			}
		}
		if r.lastBinding.IsZero() || now.Sub(r.lastBinding) >= 5*time.Second {
			c.metrics["binding_checks"]++
			path := ""
			if kind := stringValue(r.meta["runtime"]); kind == "" || kind == "codex" {
				if c.deps.bindStatus != nil {
					path, r.bindingIssue = c.deps.bindStatus(r.meta)
				} else {
					path = c.deps.bind(r.meta)
				}
			}
			r.lastBinding = now
			if path != r.path {
				r.path = path
				r.reader = nil
				r.snapshot = nil
			}
			if path != "" && r.reader == nil {
				name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
				if len(name) >= 36 {
					r.reader = c.deps.reader(path, name[len(name)-36:])
					if r.accountChanged {
						r.reader.reset()
					}
				}
			}
		}
		if r.reader != nil {
			before := r.reader.bytesRead()
			data := r.reader.read()
			delta := r.reader.bytesRead() - before
			if delta > 0 {
				c.metrics["bytes_read"] += delta
			}
			c.metrics["session_reads"]++
			if r.reader.invalidated() {
				r.reader = nil
				r.path = ""
				r.lastBinding = time.Time{}
				r.snapshot = nil
			} else {
				r.snapshot = data
				if data["quota_updated_at"] != nil {
					r.accountChanged = false
				}
			}
		}
		merge(snapshot, r.snapshot)
		if snapshot["context_remaining"] != nil {
			snapshot["context_status"] = "current"
		}
		if executable := stringValue(r.meta["executable"]); executable != "" && r.meta["account_quota"] != false {
			account := c.accounts.snapshot(stringValue(r.meta["home"]), executable, epoch, now)
			if number(account["quota_updated_at"]) > number(snapshot["quota_updated_at"]) {
				merge(snapshot, account)
			} else if account["quota_error"] != nil {
				snapshot["quota_error"] = account["quota_error"]
			}
		}
		branch := c.branches.branch(stringValue(r.meta["cwd"]))
		if branch != "" {
			snapshot["branch"] = branch
		}
		merge(snapshot, c.links.snapshot(stringValue(r.meta["cwd"]), branch, r.path, now))
		snapshot["usage_url"] = telemetry.UsageURL(r.meta["runtime"])
		elapsed := int64(now.Unix() - int64(number(r.meta["launched_at"])))
		if elapsed < 0 {
			elapsed = 0
		}
		snapshot["elapsed"] = elapsed / 5 * 5
		snapshot["state"] = "ready"
		if r.reader != nil {
			snapshot["state"] = "live"
			if stamp := number(snapshot["quota_updated_at"]); stamp > 0 && float64(now.Unix())-stamp > 180 {
				snapshot["state"] = "stale"
			}
		}
		if r.accountChanged {
			snapshot["state"] = "account-changed"
		}
		if r.bindingIssue != "" && r.bindingIssue != "pending" {
			snapshot["context_status"] = "error"
			snapshot["binding_reason"] = r.bindingIssue
			snapshot["state"] = "error"
		}
		snapshots[id] = snapshot
	}
	c.metrics["account_queries"] = c.accounts.queryCount()
	c.metrics["git_queries"] = c.links.queryCount()
	return snapshots
}
func pending(meta map[string]any) map[string]any {
	return map[string]any{"v": 1, "runtime": meta["runtime"], "context_status": "pending", "identity_status": "unresolved", "state": "pending", "model": nil, "effort": nil, "cwd": meta["cwd"], "branch": nil, "context_remaining": nil, "five_hour": nil, "weekly": nil, "activity": "waiting", "tool": nil, "elapsed": 0, "updated_at": nil, "quota_updated_at": nil, "session_id": nil, "quota_scope": nil}
}
func stringValue(value any) string { v, _ := value.(string); return v }
func number(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}
func merge(target, source map[string]any) {
	for k, v := range source {
		target[k] = v
	}
}
func (c *collector) close() { c.accounts.close(); c.links.close() }

// Limit helper concurrency while allowing unrelated sockets to finish independently.
func (c *collector) queryVisibility(sockets map[string]bool) map[string]map[string]bool {
	type result struct {
		socket string
		panes  map[string]bool
	}
	jobs := make(chan string, len(sockets))
	results := make(chan result, len(sockets))
	for socket := range sockets {
		jobs <- socket
	}
	close(jobs)
	count := len(sockets)
	if count > 8 {
		count = 8
	}
	for range count {
		go func() {
			for socket := range jobs {
				results <- result{socket, c.deps.visible(socket)}
			}
		}()
	}
	out := map[string]map[string]bool{}
	for range len(sockets) {
		r := <-results
		out[r.socket] = r.panes
	}
	return out
}
func (c *collector) fork() *collector {
	worker := *c
	worker.runs = map[string]*run{}
	for id, r := range c.runs {
		copy := *r
		worker.runs[id] = &copy
	}
	worker.metrics = map[string]int64{}
	for k, v := range c.metrics {
		worker.metrics[k] = v
	}
	return &worker
}
