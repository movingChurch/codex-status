package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestBranchCacheSharesHeadAndFollowsWorktree(t *testing.T) {
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	os.Mkdir(git, 0700)
	head := filepath.Join(git, "HEAD")
	os.WriteFile(head, []byte("ref: refs/heads/main\n"), 0600)
	sub := filepath.Join(root, "sub")
	os.Mkdir(sub, 0700)
	c := newBranchCache()
	if c.branch(root) != "main" || c.branch(sub) != "main" || c.reads != 1 {
		t.Fatalf("shared HEAD cache: reads=%d", c.reads)
	}
	os.WriteFile(head, []byte("ref: refs/heads/feature\n"), 0600)
	if c.branch(root) != "feature" || c.reads != 2 {
		t.Fatal("HEAD change not refreshed")
	}
	worktree := filepath.Join(root, "worktree")
	metadata := filepath.Join(root, "worktree-meta")
	os.Mkdir(worktree, 0700)
	os.Mkdir(metadata, 0700)
	os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: ../worktree-meta\n"), 0600)
	os.WriteFile(filepath.Join(metadata, "HEAD"), []byte("ref: refs/heads/worktree\n"), 0600)
	if c.branch(worktree) != "worktree" {
		t.Fatal("worktree used common HEAD")
	}
}
func TestAccountCacheSharesQueriesAndCancelsOldEpoch(t *testing.T) {
	calls := make(chan context.Context, 4)
	results := make(chan map[string]any, 4)
	c := newAccountCache(func(ctx context.Context, home, executable string) map[string]any { calls <- ctx; return <-results })
	defer c.close()
	now := time.Unix(1000, 0)
	c.snapshot("/fake", "fake", "old", now)
	old := <-calls
	for range 36 {
		c.snapshot("/fake", "fake", "old", now)
	}
	if c.queryCount() != 1 {
		t.Fatalf("duplicate account helpers: %d", c.queryCount())
	}
	got := c.snapshot("/fake", "fake", "new", now)
	if len(got) != 0 {
		t.Fatalf("old data after epoch change: %v", got)
	}
	select {
	case <-old.Done():
	case <-time.After(time.Second):
		t.Fatal("old helper not cancelled")
	}
	<-calls
	results <- map[string]any{"quota_updated_at": float64(10)}
	results <- map[string]any{"quota_updated_at": float64(20)}
	// Both responses may finish in either order; only the current entry may publish.
	c.wg.Wait()
	if c.queryCount() != 2 {
		t.Fatalf("epoch query count=%d", c.queryCount())
	}
	got = c.snapshot("/fake", "fake", "new", now.Add(179*time.Second))
	if got["quota_updated_at"] == nil {
		t.Fatal("current helper result not cached")
	}
	if c.queryCount() != 2 {
		t.Fatal("quota fetched before 180 second TTL")
	}
}
func TestAccountCacheLateOldResultCannotOverwriteNew(t *testing.T) {
	started := make(chan struct{}, 2)
	oldRelease := make(chan struct{})
	newRelease := make(chan struct{})
	var calls atomic.Int32
	c := newAccountCache(func(ctx context.Context, home, executable string) map[string]any {
		first := calls.Add(1) == 1
		started <- struct{}{}
		if first {
			<-ctx.Done()
			<-oldRelease
			return map[string]any{"quota_updated_at": float64(1)}
		}
		<-newRelease
		return map[string]any{"quota_updated_at": float64(2)}
	})
	defer c.close()
	now := time.Unix(1000, 0)
	c.snapshot("/fake", "fake", "old", now)
	<-started
	c.snapshot("/fake", "fake", "new", now)
	<-started
	close(newRelease)
	deadline := time.Now().Add(time.Second)
	published := false
	for time.Now().Before(deadline) {
		if c.snapshot("/fake", "fake", "new", now)["quota_updated_at"] == float64(2) {
			published = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(oldRelease)
	c.wg.Wait()
	if !published {
		t.Fatal("new quota did not publish before old helper finished")
	}
	got := c.snapshot("/fake", "fake", "new", now)
	if got["quota_updated_at"] != float64(2) {
		t.Fatalf("old response published: %v", got)
	}
}
func TestLinkCacheSharesAndRefreshes(t *testing.T) {
	c := newLinkCache(func(cwd, branch, path string) map[string]any {
		return map[string]any{"branch_url": "https://example.test/" + branch}
	})
	defer c.close()
	now := time.Unix(1000, 0)
	c.snapshot("/fake", "main", "/rollout", now)
	c.wg.Wait()
	for range 36 {
		got := c.snapshot("/fake", "main", "/rollout", now)
		if got["branch_url"] != "https://example.test/main" {
			t.Fatal(got)
		}
	}
	if c.queryCount() != 1 {
		t.Fatal("duplicate link lookups")
	}
	c.snapshot("/fake", "main", "/rollout", now.Add(60*time.Second))
	c.wg.Wait()
	if c.queryCount() != 2 {
		t.Fatal("link cache never refreshed")
	}
}

func TestBranchCacheRejectsFIFOs(t *testing.T) {
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	if err := syscall.Mkfifo(git, 0600); err != nil {
		t.Fatal(err)
	}
	c := newBranchCache()
	if got := c.branch(root); got != "" {
		t.Fatalf("FIFO .git produced branch %q", got)
	}
	other := t.TempDir()
	os.Mkdir(filepath.Join(other, ".git"), 0700)
	if err := syscall.Mkfifo(filepath.Join(other, ".git", "HEAD"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := c.branch(other); got != "" {
		t.Fatalf("FIFO HEAD produced branch %q", got)
	}
}
func TestLinkCacheSharesRepositoryAcrossDifferentSessions(t *testing.T) {
	c := newLinkCache(func(cwd, branch, path string) map[string]any {
		return map[string]any{"branch_url": "https://example.test/tree/main"}
	})
	defer c.close()
	now := time.Unix(1000, 0)
	for i := range 36 {
		path := fmt.Sprintf("/fake/session %d.jsonl", i)
		c.snapshot("/fake", "main", path, now)
	}
	c.wg.Wait()
	if c.queryCount() != 1 {
		t.Fatalf("same repository launched %d helpers", c.queryCount())
	}
	got := c.snapshot("/fake", "main", "/fake/session 35.jsonl", now)
	if got["session_url"] != "file:///fake/session%2035.jsonl" {
		t.Fatalf("shared cache leaked another session link: %v", got)
	}
}
