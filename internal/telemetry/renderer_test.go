package telemetry

import (
	"math"
	"strings"
	"testing"
)

func renderFixture() map[string]any {
	return map[string]any{"state": "live", "activity": "working", "session_id": "11111111-2222-4333-8444-555555555555", "model": "gpt-test", "effort": "high", "cwd": "/work/한글 작업", "branch": "main", "context_remaining": 70, "context_used": 60000, "context_window": 200000, "input_tokens": 10000, "cached_tokens": 4000, "elapsed": 85, "five_hour": map[string]any{"remaining": 55}, "weekly": map[string]any{"remaining": 20}}
}
func TestRenderCapacityAndState(t *testing.T) {
	s := renderFixture()
	rows := Render(s, 40, false)
	if rows[1] != "Ctx 30% │ 5H 45.0% │ 7D 80.0%" {
		t.Fatal(rows)
	}
	if !strings.Contains(rows[2], "Connected · Working") {
		t.Fatal(rows)
	}
	rows = Render(s, 220, false)
	for _, want := range []string{"60k/200k (30%)", "Cached 4k", "Hit rate 40.0%", "Elapsed 1:25"} {
		if !strings.Contains(strings.Join(rows, "\n"), want) {
			t.Fatal(want, rows)
		}
	}
	s["session_id"] = nil
	if strings.Contains(strings.Join(Render(s, 220, false), ""), "Connected") {
		t.Fatal("unbound connection")
	}
}
func TestRenderInjectionAndWidth(t *testing.T) {
	s := renderFixture()
	s["model"] = "evil\x1b[2J\x1b]52;c;secret\x07model\n\r\u202e"
	if text := strings.Join(Render(s, 180, false), "\n"); strings.ContainsAny(text, "\x1b\u202e") || strings.Contains(text, "secret") {
		t.Fatal(text)
	}
	s["model"] = strings.Repeat("한글🙂e\u0301", 80)
	for _, width := range []int{1, 12, 25, 40, 80, 120} {
		for _, color := range []bool{false, true} {
			for _, row := range Render(s, width, color) {
				if renderTextWidth(renderClean(row)) > width {
					t.Fatal(width, row)
				}
			}
		}
	}
}
func TestRenderLinksAndInvalidInputs(t *testing.T) {
	s := renderFixture()
	for _, bad := range []any{nil, true, math.NaN(), math.Inf(1), -1, 101, "50"} {
		s["context_remaining"] = bad
		if !strings.Contains(Render(s, 180, false)[1], "n/a") {
			t.Fatal(bad)
		}
	}
	for _, bad := range []string{"http://example.com", "https://user:password@example.com", "file://localhost:80/path", "file://remote/path", "https://example.com:bad/path", "https://example.com/\u202epath", "https://example.com\\@evil.test"} {
		s["directory_url"] = bad
		if strings.Contains(Render(s, 220, true)[0], "\x1b]") {
			t.Fatal(bad)
		}
	}
	s["directory_url"] = "file:///work/a%20b"
	if !strings.Contains(Render(s, 220, true)[0], "\x1b]8;;file:///work/a%20b\x1b\\") {
		t.Fatal("missing link")
	}
	if strings.Contains(Render(s, 220, false)[0], "\x1b") {
		t.Fatal("plain escape")
	}
}
func TestRenderReadyAndErrors(t *testing.T) {
	s := map[string]any{"state": "ready", "quota_error": "private error", "weekly": map[string]any{"remaining": 13}}
	r := Render(s, 220, false)
	if !strings.HasPrefix(r[0], "Codex") || !strings.Contains(r[1], "87.0%") || !strings.Contains(r[2], "Ready · Send a message") || strings.Contains(r[2], "private") {
		t.Fatal(r)
	}
}

func TestRenderCountdownAndBalancedTruncation(t *testing.T) {
	for _, tc := range []struct {
		at   float64
		want string
	}{{13600, "1hr"}, {100000, "1d 1hr"}, {10001, "<1m"}, {1, "reset pending"}} {
		if got := renderCountdown(tc.at, 10000); got != tc.want {
			t.Fatal(got, tc.want)
		}
	}
	for width, want := range []string{"", "!", "한", "한!", "한글", "한글!"} {
		got := renderLine([]renderSegment{{"한글", "mint", "https://example.com/tree"}, {"!", "dim", nil}}, width, true)
		if renderClean(got) != want {
			t.Fatal(width, got, want)
		}
		if strings.Count(got, "\x1b]8;;")%2 != 0 {
			t.Fatal("unclosed link", got)
		}
	}
	for _, tc := range []struct {
		r    rune
		want int
	}{{'한', 2}, {'🙂', 2}, {'\u0301', 0}, {'\u0903', 1}, {'a', 1}} {
		if got := renderCell(tc.r); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
