package telemetry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccountNormalizeWindows(t *testing.T) {
	var input map[string]any
	json.Unmarshal([]byte(`{"rateLimits":{"primary":{"usedPercent":87,"windowDurationMins":10080},"secondary":{"usedPercent":24,"windowDurationMins":300}}}`), &input)
	got := accountNormalize(input)
	if got["weekly"].(map[string]any)["remaining"] != float64(13) || got["five_hour"].(map[string]any)["remaining"] != float64(76) {
		t.Fatal(got)
	}
	delete(input["rateLimits"].(map[string]any), "secondary")
	if accountNormalize(input)["five_hour"] != nil {
		t.Fatal("missing window became usage")
	}
	input["rateLimitsByLimitId"] = map[string]any{"codex": map[string]any{"primary": map[string]any{"usedPercent": float64(10), "windowDurationMins": float64(300)}}}
	if accountNormalize(input)["five_hour"].(map[string]any)["remaining"] != float64(90) {
		t.Fatal("wrong bucket")
	}
}

func TestAccountRejectInvalidWindows(t *testing.T) {
	for _, used := range []any{true, "10", float64(-1), float64(101), nil} {
		got := accountNormalize(map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": used, "windowDurationMins": float64(300)}}})
		if got["five_hour"] != nil || got["quota_updated_at"] != nil {
			t.Fatal(got)
		}
	}
}

func TestAccountFetchProtocol(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-other-session")
	helper := filepath.Join(t.TempDir(), "codex")
	script := `#!/bin/sh
[ "$1" = app-server ] || exit 1
[ "$CODEX_HOME" = /test/account ] || exit 1
[ -z "$OPENAI_API_KEY" ] || exit 1
read init
case "$init" in *initialize*) ;; *) exit 1;; esac
printf '%s\n' '{"method":"notice"}' '{"id":1,"result":{}}'
read initialized
read quota
case "$quota" in *account/rateLimits/read*) ;; *) exit 1;; esac
printf '%s\n' '{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":20,"windowDurationMins":300}}}}'
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	got := FetchAccount(context.Background(), "/test/account", helper)
	if got["five_hour"] == nil || got["five_hour"].(map[string]any)["remaining"] != float64(80) {
		t.Fatal(got)
	}
}

func TestAccountFetchFailureAndCancellation(t *testing.T) {
	if FetchAccount(context.Background(), "", "/does/not/exist")["quota_error"] == nil {
		t.Fatal("missing error")
	}
	helper := filepath.Join(t.TempDir(), "codex")
	os.WriteFile(helper, []byte("#!/bin/sh\nwhile :; do :; done\n"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if FetchAccount(ctx, "", helper)["quota_error"] == nil {
		t.Fatal("missing cancellation error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("cleanup not bounded")
	}
}

func TestAccountResetIntegerBoundaries(t *testing.T) {
	for _, reset := range []any{json.Number("-1"), json.Number("9223372036854775808"), json.Number("1.0"), json.Number("1e0"), float64(1), true} {
		got := accountNormalize(map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 10, "windowDurationMins": 300, "resetsAt": reset}}})
		if got["five_hour"] != nil {
			t.Errorf("invalid reset %v accepted", reset)
		}
	}
	for _, reset := range []any{json.Number("0"), json.Number("9223372036854775807"), int64(12)} {
		got := accountNormalize(map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 10, "windowDurationMins": 300, "resetsAt": reset}}})
		if got["five_hour"] == nil {
			t.Errorf("valid reset %v rejected", reset)
		}
	}
}

func TestAccountFetchPreservesResetNumberType(t *testing.T) {
	for _, tc := range []struct {
		reset string
		valid bool
	}{{"1.0", false}, {"-1", false}, {"9223372036854775808", false}, {"9223372036854775807", true}} {
		t.Run(tc.reset, func(t *testing.T) {
			helper := filepath.Join(t.TempDir(), "codex")
			script := "#!/bin/sh\nread init\nprintf '%s\\n' '{\"id\":1,\"result\":{}}'\nread initialized\nread quota\nprintf '%s\\n' '{\"id\":2,\"result\":{\"rateLimits\":{\"primary\":{\"usedPercent\":20,\"windowDurationMins\":300,\"resetsAt\":" + tc.reset + "}}}}'\n"
			if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			got := FetchAccount(context.Background(), "", helper)
			if (got["five_hour"] != nil) != tc.valid {
				t.Fatal(got)
			}
			if got["quota_error"] != nil {
				t.Fatal(got)
			}
		})
	}
}

func TestAccountNormalizeMonthlyWindow(t *testing.T) {
	out := accountNormalize(map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 12.0, "windowDurationMins": 43200.0}}})
	monthly, _ := out["monthly"].(map[string]any)
	if monthly == nil || monthly["remaining"] != 88.0 || out["quota_updated_at"] == nil {
		t.Fatalf("a 30-day window must become monthly quota: %v", out)
	}
	line := Render(map[string]any{"monthly": monthly}, 120, false)[1]
	if !strings.Contains(line, "30D") || !strings.Contains(line, "12.0%") {
		t.Fatalf("capacity row %q lacks the 30D window", line)
	}
}
