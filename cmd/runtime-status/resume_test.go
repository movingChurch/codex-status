package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestResumeArgsReplaceConversationAndPrompt(t *testing.T) {
	values := valueFlags("Options:\n  --model <model>  Model\n  -r, --resume [value]  Resume\n  --session-id <uuid>  ID\n  --dangerously-skip-permissions  Skip\n")
	if !values["--model"] || !values["--session-id"] || values["--resume"] {
		t.Fatalf("value flags %v", values)
	}
	cases := map[string][2][]string{
		"resume with id":    {{"--dangerously-skip-permissions", "--resume", "8583a7de", "--model", "opus"}, {"--dangerously-skip-permissions", "--model", "opus"}},
		"short resume":      {{"-r", "abc", "-c"}, {}},
		"inline forms":      {{"--resume=abc", "--session-id=x", "--model=opus"}, {"--model=opus"}},
		"prompt dropped":    {{"--model", "opus", "fix the bug"}, {"--model", "opus"}},
		"continue and fork": {{"--continue", "--fork-session", "--from-pr", "12", "--teleport", "t1"}, {}},
	}
	for name, c := range cases {
		if got := resumeArgs(c[0], values); !slices.Equal(got, c[1]) {
			t.Errorf("%s: %q, want %q", name, got, c[1])
		}
	}
}

func TestConversationStoredByRuntime(t *testing.T) {
	home := t.TempDir()
	id := "11111111-2222-4333-8444-555555555555"
	project := filepath.Join(home, "projects", "-Users-me-my-app")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	if conversationStored("claude", home, "/Users/me/my.app", id) {
		t.Fatal("a conversation that is not stored must not be resumed")
	}
	if err := os.WriteFile(filepath.Join(project, id+".jsonl"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if !conversationStored("claude", home, "/Users/me/my.app", id) {
		t.Fatal("stored Claude conversation not found")
	}
	day := filepath.Join(home, "sessions", "2026", "10", "20")
	if err := os.MkdirAll(day, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(day, "rollout-2026-10-20T12-00-00-"+id+".jsonl"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if !conversationStored("codex", home, "/x", id) || conversationStored("codex", home, "/x", "22222222-2222-4333-8444-555555555555") {
		t.Fatal("Codex rollout lookup is wrong")
	}
}

func TestLaunchedConversationFallback(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555555"
	if got := launchedConversation(map[string]any{"expected_session": id}); got != id {
		t.Fatalf("codex resume target lost: %q", got)
	}
	if got := launchedConversation(map[string]any{"args": []any{"--dangerously-skip-permissions", "--resume", id}}); got != id {
		t.Fatalf("claude resume target lost: %q", got)
	}
	if got := launchedConversation(map[string]any{"args": []any{}}); got != "" {
		t.Fatalf("no conversation expected, got %q", got)
	}
}
