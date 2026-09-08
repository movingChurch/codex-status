package telemetry

import "testing"

func TestLinkRepository(t *testing.T) {
	for _, remote := range []string{"git@github.com:owner/repo.git", "https://github.com/owner/repo.git", "ssh://git@github.com/owner/repo.git", "https://user:secret@github.com/owner/repo.git?token=secret#secret"} {
		if got := linkRepository(remote); got != "https://github.com/owner/repo" {
			t.Fatal(remote, got)
		}
	}
	for _, remote := range []string{"file:///repo", "/local/repo", "https://github.com/repo\n"} {
		if got := linkRepository(remote); got != "" {
			t.Fatal(got)
		}
	}
}
func TestLinkBranchAndFile(t *testing.T) {
	if got := linkBranch("https://github.com/o/r", "feat/a#b"); got != "https://github.com/o/r/tree/feat%2Fa%23b" {
		t.Fatal(got)
	}
	if got := linkBranch("https://gitlab.com/o/r", "main"); got != "https://gitlab.com/o/r/-/tree/main" {
		t.Fatal(got)
	}
	if got := linkFile("/tmp/a b#c"); got != "file:///tmp/a%20b%23c" {
		t.Fatal(got)
	}
}
