package adapters

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestContext(t *testing.T) {
	tests := []struct {
		name, payload string
		remaining     any
		used          any
	}{
		{"measured zero", `{"remaining_percentage":0}`, float64(0), nil},
		{"null percentage falls back", `{"remaining_percentage":null,"used_percentage":0}`, float64(100), nil},
		{"current input excludes output", `{"context_window_size":100,"current_usage":{"input_tokens":10,"output_tokens":90,"cache_creation_input_tokens":20,"cache_read_input_tokens":30}}`, float64(40), float64(60)},
		{"explicit percentage wins", `{"remaining_percentage":70,"context_window_size":100,"current_usage":{"input_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":30}}`, float64(70), float64(60)},
		{"null usage", `{"current_usage":null}`, nil, nil},
		{"invalid metrics", `{"remaining_percentage":101,"used_percentage":-1,"context_window_size":1e999,"current_usage":{"input_tokens":9007199254740992}}`, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Normalize("claude", []byte(`{"session_id":"opaque id","context_window":`+tt.payload+`}`))
			if err != nil {
				t.Fatal(err)
			}
			if out["context_remaining"] != tt.remaining || out["context_used"] != tt.used {
				t.Fatalf("got %#v", out)
			}
		})
	}
}

func TestAntigravityBeforeConversationIsPending(t *testing.T) {
	_, err := Normalize("antigravity", []byte(`{"conversation_id":"","session_id":"","model":null,"context_window":{"used_percentage":0}}`))
	if !errors.Is(err, ErrSessionPending) {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"conversation_id":"","session_id":"different"}`, `{"conversation_id":{},"session_id":""}`} {
		if _, err = Normalize("antigravity", []byte(raw)); err == nil || errors.Is(err, ErrSessionPending) {
			t.Fatal("invalid identity admitted as pending", raw)
		}
	}
}
func TestWindowsAndPrivacy(t *testing.T) {
	out, err := Normalize("claude", []byte(`{"session_id":"s","cli_version":"future","email":"secret","prompt":"private","tool_args":{"key":"secret"},"model":{"display_name":"Friendly","id":"id"},"context_window":{"remaining_percentage":0},"rate_limits":{"seven_day":{"used_percentage":0,"resets_at":123}},"new_field":[1,2]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["five_hour"]; ok {
		t.Fatal("invented five hour")
	}
	if out["weekly"].(map[string]any)["remaining"] != float64(100) {
		t.Fatal(out)
	}
	if out["model"] != "Friendly" {
		t.Fatal(out)
	}
	b, _ := json.Marshal(out)
	for _, secret := range []string{"secret", "private", "tool_args", "email", "cli_version"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("private field %s", secret)
		}
	}
	next, err := Normalize("claude", []byte(`{"session_id":"s","rate_limits":{"five_hour":{"used_percentage":30}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := next["weekly"]; ok {
		t.Fatal("retained prior window")
	}
}
func TestAntigravity(t *testing.T) {
	out, err := Normalize("antigravity", []byte(`{"conversation_id":"arbitrary/session","session_id":"arbitrary/session","agent_state":"tool_use","quota":{"z-model":{"remaining_fraction":0,"reset_time":"2026-10-16T00:00:00Z"},"a-model":{"remaining_fraction":0.5,"reset_in_seconds":0},"bad":{"remaining_fraction":2}}}`))
	if err != nil {
		t.Fatal(err)
	}
	pools := out["quota_pools"].([]any)
	if len(pools) != 2 || pools[0].(map[string]any)["label"] != "a-model" || pools[1].(map[string]any)["remaining"] != float64(0) || out["activity"] != "working" {
		t.Fatal(out)
	}
	if _, err := Normalize("antigravity", []byte(`{"session_id":"alias"}`)); err != nil {
		t.Fatal(err)
	}
	out, err = Normalize("antigravity", []byte(`{"conversation_id":"s","agent_state":{"future":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["activity"]; ok {
		t.Fatal(out)
	}
}
func TestRejectedPayloads(t *testing.T) {
	for _, payload := range []string{`null`, `[]`, `{}`, `{"session_id":null}`, `{"session_id":1}`, `{"session_id":""}`, `{"session_id":"a\nb"}`, `{"session_id":"a","session_id":"b"}`, `{"session_id":"a"} {}`, `{"session_id":"a","x":NaN}`, `{"session_id":"a","conversation_id":"b"}`, `{"session_id":"a","conversation_id":null}`, strings.Repeat(" ", maxPayload+1)} {
		if _, err := Normalize("antigravity", []byte(payload)); err == nil {
			t.Fatalf("accepted %.80s", payload)
		}
	}
	if _, err := Normalize("unknown", []byte(`{"session_id":"a"}`)); err == nil {
		t.Fatal("unknown runtime accepted")
	}
}
func TestBoundsAndOptionalDegradation(t *testing.T) {
	root := map[string]any{"conversation_id": "s", "model": map[string]any{"id": "valid"}, "quota": map[string]any{}}
	for i := 0; i < 40; i++ {
		root["quota"].(map[string]any)[strings.Repeat("a", i+1)] = map[string]any{"remaining_fraction": 0}
	}
	raw, _ := json.Marshal(root)
	out, err := Normalize("antigravity", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(out["quota_pools"].([]any)) != 32 {
		t.Fatal(out)
	}
	out, err = Normalize("claude", []byte(`{"session_id":"s","model":{"id":"ok"},"context_window":{"remaining_percentage":"0","used_percentage":1e999},"rate_limits":{"five_hour":{"used_percentage":50,"resets_at":-1},"seven_day":{"used_percentage":{}}}}`))
	if err != nil || out["model"] != "ok" {
		t.Fatal(out, err)
	}
	if _, ok := out["weekly"]; ok {
		t.Fatal(out)
	}
	if _, ok := out["five_hour"].(map[string]any)["resets_at"]; ok {
		t.Fatal(out)
	}
}

func TestDiagnosePrivateSchema(t *testing.T) {
	raw := []byte(`{"session_id":"private-session","conversation_id":"private-other","model":{"id":"secret-model","display_name":"secret-name","api_key":"secret-key"},"context_window":{"current_usage":{"input_tokens":999,"prompt":"secret-prompt"},"remaining_percentage":null},"quota":{"secret-pool":{"remaining_fraction":0.2}},"workspace":{"current_dir":"/secret/path","email":"secret-email"},"secret-root":{"nested":"secret-value"}}`)
	out := Diagnose(raw)
	if out["json_valid"] != true || out["root_type"] != "object" || out["aliases_match"] != false || out["structure_compatible"] != true {
		t.Fatal(out)
	}
	fields := out["fields"].(map[string]any)
	if fields["context_window.remaining_percentage"] != "null" || fields["context_window.current_usage"] != "object" || fields["cwd"] != "missing" {
		t.Fatal(fields)
	}
	props := out["session_id_properties"].(map[string]any)
	if props["length"] != 15 || props["nonempty"] != true || props["printable"] != true {
		t.Fatal(props)
	}
	encoded, _ := json.Marshal(out)
	for _, private := range []string{"private-session", "private-other", "secret-", "api_key", "input_tokens", "remaining_fraction", "nested", "prompt", "email"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("diagnostic leaked %q: %s", private, encoded)
		}
	}
}
func TestDiagnoseRejections(t *testing.T) {
	tests := []struct {
		raw, key string
		want     any
	}{
		{`{"session_id":"a","session_id":"b"}`, "duplicate_keys", true},
		{strings.Repeat("[", 66) + strings.Repeat("]", 66), "depth_exceeded", true},
		{`{} {}`, "trailing_data", true},
		{`{"session_id":`, "json_valid", false},
		{strings.Repeat(" ", maxPayload+1), "within_limit", false},
		{`[]`, "root_type", "array"},
		{`null`, "root_type", "null"},
		{`{"session_id":"a\nb"}`, "json_valid", true},
	}
	for _, tt := range tests {
		out := Diagnose([]byte(tt.raw))
		if out[tt.key] != tt.want {
			t.Fatalf("%s: %#v", tt.key, out)
		}
	}
	props := Diagnose([]byte(`{"session_id":"a\nb"}`))["session_id_properties"].(map[string]any)
	if props["printable"] != false {
		t.Fatal(props)
	}
}

func TestClaudeEffortCacheAndDirectory(t *testing.T) {
	tests := []struct {
		name, payload           string
		effort, cwd             any
		cached, input, hitInput any
	}{
		{"effort and cache reads", `{"session_id":"s","effort":{"level":"high"},"workspace":{"project_dir":"/launch","current_dir":"/launch/sub"},"context_window":{"current_usage":{"input_tokens":2,"cache_creation_input_tokens":8,"cache_read_input_tokens":90}}}`, "high", "/launch", float64(90), float64(100), nil},
		{"absent effort and null usage", `{"session_id":"s","workspace":{"current_dir":"/current"},"context_window":{"current_usage":null}}`, nil, "/current", nil, nil, nil},
		{"zero usage", `{"session_id":"s","cwd":"/cwd","context_window":{"current_usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`, nil, "/cwd", nil, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Normalize("claude", []byte(tt.payload))
			if err != nil {
				t.Fatal(err)
			}
			if out["effort"] != tt.effort || out["cwd"] != tt.cwd || out["cached_tokens"] != tt.cached || out["input_tokens"] != tt.input {
				t.Fatal(out)
			}
		})
	}
}

func TestNormalizeHook(t *testing.T) {
	for event, activity := range HookEvents {
		payload := `{"session_id":"s","hook_event_name":"` + event + `","prompt":"private","tool_input":{"k":"secret"}`
		if event == "Notification" {
			payload += `,"notification_type":"permission_prompt"`
		}
		out, err := NormalizeHook([]byte(payload + `}`))
		if err != nil || out["activity"] != activity || out["session_id"] != "s" || len(out) != 2 {
			t.Fatal(event, out, err)
		}
	}
	for _, payload := range []string{
		`{"session_id":"s","hook_event_name":"Notification","notification_type":"auth_success"}`,
		`{"session_id":"s","hook_event_name":"SessionStart"}`,
		`{"session_id":"","hook_event_name":"Stop"}`,
		`{"hook_event_name":"Stop"}`,
		`not json`,
	} {
		if out, err := NormalizeHook([]byte(payload)); err == nil {
			t.Fatal(payload, out)
		}
	}
}
