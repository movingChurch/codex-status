package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/movingChurch/codex-status/internal/accounts"
)

type identityResult struct {
	profile, generation string
	identity            *accounts.Identity
}
type identityProfile struct {
	generation string
	pending    bool
	identity   *accounts.Identity
}

// AuthGeneration observes metadata only. It never reads provider credentials.
func AuthGeneration(kind, home string) string {
	var names []string
	switch kind {
	case "claude":
		names = []string{".credentials.json", ".claude.json"}
	case "antigravity":
		names = []string{"oauth_creds.json", "accounts.json"}
	default:
		names = []string{"auth.json"}
	}
	var out strings.Builder
	for _, name := range names {
		i, e := os.Lstat(filepath.Join(home, name))
		if e != nil {
			out.WriteString("missing;")
			continue
		}
		out.WriteString(i.ModTime().UTC().Format(time.RFC3339Nano))
		_, _ = fmt.Fprintf(&out, ":%d:%v", i.Size(), i.Mode())
		if st, ok := i.Sys().(*syscall.Stat_t); ok {
			_, _ = fmt.Fprintf(&out, ":%d:%d", st.Dev, st.Ino)
		}
		out.WriteString(";")
	}
	return out.String()
}

// resolveIdentity stays fail-closed until a native contract ties a scoped
// principal and billing realm to this exact live session. Profile-wide auth
// status alone cannot establish which cached credentials a running CLI uses.
func resolveIdentity(meta map[string]any) *accounts.Identity {
	return nil
}
