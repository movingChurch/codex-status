package engine

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type accountEntry struct {
	epoch   string
	data    map[string]any
	next    time.Time
	pending bool
	queried bool
	cancel  context.CancelFunc
}
type accountCache struct {
	mu      sync.Mutex
	entries map[[2]string]*accountEntry
	fetch   func(context.Context, string, string) map[string]any
	queries int64
	wg      sync.WaitGroup
	closed  bool
	once    bool
}

func newAccountCache(fetch func(context.Context, string, string) map[string]any) *accountCache {
	return &accountCache{entries: map[[2]string]*accountEntry{}, fetch: fetch}
}
func (c *accountCache) snapshot(home, executable, epoch string, now time.Time) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.fetch == nil {
		return nil
	}
	key := [2]string{home, executable}
	entry := c.entries[key]
	if entry == nil || entry.epoch != epoch {
		if entry != nil && entry.cancel != nil {
			entry.cancel()
		}
		entry = &accountEntry{epoch: epoch}
		c.entries[key] = entry
	}
	if !entry.pending && (!c.once || !entry.queried) && !now.Before(entry.next) {
		ctx, cancel := context.WithTimeout(context.Background(), 17*time.Second)
		entry.cancel = cancel
		entry.pending = true
		entry.queried = true
		entry.next = now.Add(180 * time.Second)
		c.queries++
		c.wg.Add(1)
		go func(item *accountEntry) {
			defer c.wg.Done()
			defer cancel()
			data := c.fetch(ctx, home, executable)
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.entries[key] == item && !c.closed {
				item.data = data
				item.pending = false
			}
		}(entry)
	}
	out := map[string]any{}
	merge(out, entry.data)
	return out
}
func (c *accountCache) queryCount() int64 { c.mu.Lock(); defer c.mu.Unlock(); return c.queries }
func (c *accountCache) close() {
	c.mu.Lock()
	c.closed = true
	for _, entry := range c.entries {
		if entry.cancel != nil {
			entry.cancel()
		}
	}
	c.mu.Unlock()
	c.wg.Wait()
}

type linkEntry struct {
	data    map[string]any
	next    time.Time
	pending bool
}
type linkCache struct {
	mu      sync.Mutex
	entries map[[2]string]*linkEntry
	fetch   func(string, string, string) map[string]any
	queries int64
	closed  bool
	wg      sync.WaitGroup
}

func newLinkCache(fetch func(string, string, string) map[string]any) *linkCache {
	return &linkCache{entries: map[[2]string]*linkEntry{}, fetch: fetch}
}
func (c *linkCache) snapshot(cwd, branch, path string, now time.Time) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.fetch == nil {
		return nil
	}
	key := [2]string{cwd, branch}
	entry := c.entries[key]
	if entry == nil {
		entry = &linkEntry{}
		c.entries[key] = entry
	}
	if !entry.pending && !now.Before(entry.next) {
		entry.pending = true
		entry.next = now.Add(60 * time.Second)
		c.queries++
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			data := c.fetch(cwd, branch, "")
			c.mu.Lock()
			defer c.mu.Unlock()
			entry.data = data
			entry.pending = false
		}()
	}
	out := map[string]any{}
	merge(out, entry.data)
	out["session_url"] = nil
	if path != "" {
		if absolute, err := filepath.Abs(path); err == nil {
			out["session_url"] = (&url.URL{Scheme: "file", Path: absolute}).String()
		}
	}
	return out
}
func (c *linkCache) queryCount() int64 { c.mu.Lock(); defer c.mu.Unlock(); return c.queries }
func (c *linkCache) close()            { c.mu.Lock(); c.closed = true; c.mu.Unlock(); c.wg.Wait() }

type headEntry struct {
	info  os.FileInfo
	value string
}
type branchCache struct {
	roots map[string]string
	heads map[string]headEntry
	reads int64
}

func newBranchCache() *branchCache {
	return &branchCache{roots: map[string]string{}, heads: map[string]headEntry{}}
}
func (c *branchCache) branch(cwd string) string {
	root, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	head, known := c.roots[root]
	if !known {
		for dir := root; ; dir = filepath.Dir(dir) {
			git := filepath.Join(dir, ".git")
			info, err := os.Stat(git)
			if err == nil {
				if info.IsDir() {
					head = filepath.Join(git, "HEAD")
				} else if info.Mode().IsRegular() {
					line := readFirstLine(git)
					if strings.HasPrefix(line, "gitdir: ") {
						target := strings.TrimPrefix(line, "gitdir: ")
						if !filepath.IsAbs(target) {
							target = filepath.Join(dir, target)
						}
						head = filepath.Join(target, "HEAD")
					}
				}
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
		c.roots[root] = head
	}
	if head == "" {
		return ""
	}
	info, err := os.Stat(head)
	if err != nil {
		return ""
	}
	old, ok := c.heads[head]
	if ok && os.SameFile(info, old.info) && info.ModTime() == old.info.ModTime() && info.Size() == old.info.Size() {
		return old.value
	}
	value := readFirstLine(head)
	c.reads++
	if strings.HasPrefix(value, "ref: refs/heads/") {
		value = strings.TrimPrefix(value, "ref: refs/heads/")
	} else if len(value) > 8 {
		value = value[:8]
	}
	c.heads[head] = headEntry{info, value}
	return value
}
func readFirstLine(path string) string {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return ""
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
}
