package main

import (
	"slices"
	"testing"
)

func TestWindowArgsKept(t *testing.T) {
	meta := map[string]any{"id": "00000000-0000-0000-0000-000000000000", "args": []any{"--resume", "a b"}}
	if got := windowArgs(meta); !slices.Equal(got, []string{"--resume", "a b"}) {
		t.Fatalf("args %q", got)
	}
}

func TestMenuTextStaysLiteral(t *testing.T) {
	if got := menuText("a#b\"c\\d\x01"); got != "a##bcd" {
		t.Fatalf("%q", got)
	}
	if got := tmuxQuote(`run "x" $HOME`); got != `"run \"x\" \$HOME"` {
		t.Fatalf("%q", got)
	}
}
