package telemetry

import (
	"context"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const linkUsageURL = "https://chatgpt.com/codex/settings/usage"

// UsageURL returns the runtime's own usage page, or nil when it publishes none.
func UsageURL(runtime any) any {
	switch runtime {
	case "claude":
		return "https://claude.ai/settings/usage"
	case "antigravity":
		return nil
	}
	return linkUsageURL
}

var linkSCP = regexp.MustCompile(`^(?:[^/@:]+@)?([A-Za-z0-9.-]+):(.+)$`)
var linkHost = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

func linkControl(s string) bool {
	for _, r := range s {
		if renderInRanges(r, renderControlRanges) {
			return true
		}
	}
	return false
}
func linkQuote(s, safe string) string {
	const digits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~"+safe, rune(c)) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(digits[c>>4])
			b.WriteByte(digits[c&15])
		}
	}
	return b.String()
}
func linkRepository(remote string) string {
	if len([]rune(remote)) > 4096 || linkControl(remote) {
		return ""
	}
	remote = strings.TrimSpace(remote)
	if !strings.Contains(remote, "://") {
		m := linkSCP.FindStringSubmatch(remote)
		if m == nil {
			return ""
		}
		remote = "ssh://" + m[1] + "/" + m[2]
	}
	u, err := url.Parse(remote)
	if err != nil || !(u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "ssh") || !linkHost.MatchString(u.Hostname()) {
		return ""
	}
	if u.Port() != "" {
		p, e := strconv.Atoi(u.Port())
		if e != nil || p < 0 || p > 65535 {
			return ""
		}
	}
	authority := strings.ToLower(u.Hostname())
	if u.Scheme != "ssh" && u.Port() != "" && u.Port() != "0" {
		authority += ":" + u.Port()
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	path = strings.TrimSuffix(path, ".git")
	if path == "" || path == "/" {
		return ""
	}
	return "https://" + authority + linkQuote(path, "/%")
}
func linkBranch(repository, branch string) string {
	if repository == "" || branch == "" || linkControl(branch) {
		return ""
	}
	u, err := url.Parse(repository)
	if err != nil {
		return ""
	}
	route := "/tree/"
	host := u.Hostname()
	if strings.Contains(host, "gitlab") {
		route = "/-/tree/"
	} else if host == "bitbucket.org" {
		route = "/src/"
	} else if host == "codeberg.org" {
		route = "/src/branch/"
	}
	return repository + route + linkQuote(branch, "")
}
func linkFile(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return "file://" + linkQuote(absolute, "/")
}

// LinkSnapshot performs one bounded lookup; the engine owns shared lookup caching.
func LinkSnapshot(cwd, branch, path string) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", cwd, "config", "--get", "remote.origin.url").Output()
	var branchURL, sessionURL any
	if err == nil {
		if target := linkBranch(linkRepository(strings.TrimRight(string(out), "\n")), branch); target != "" {
			branchURL = target
		}
	}
	if path != "" {
		sessionURL = linkFile(path)
	}
	return map[string]any{"directory_url": linkFile(cwd), "usage_url": linkUsageURL, "branch_url": branchURL, "session_url": sessionURL}
}
