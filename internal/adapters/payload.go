// Package adapters translates native statusline metadata into bounded HUD observations.
package adapters

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxPayload = 1 << 20
const maxSafe = 9007199254740991

var errPayload = errors.New("incompatible statusline payload")
var errDuplicateKey = errors.New("duplicate JSON key")
var errDepth = errors.New("JSON nesting limit exceeded")

// Normalize decodes a complete snapshot. Missing quota fields intentionally remain
// absent, so consumers must replace, rather than merge, previous quota windows.
func Normalize(runtime string, raw []byte) (map[string]any, error) {
	if len(raw) > maxPayload || !utf8.Valid(raw) || (runtime != "claude" && runtime != "antigravity") {
		return nil, errPayload
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := decode(d, 0)
	if err != nil {
		return nil, errPayload
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errPayload
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, errPayload
	}
	id, ok := printable(root["session_id"], 512)
	if runtime == "antigravity" {
		if v, exists := root["conversation_id"]; exists {
			var valid bool
			id, valid = printable(v, 512)
			if !valid {
				return nil, errPayload
			}
			if alias, exists := root["session_id"]; exists {
				s, valid := printable(alias, 512)
				if !valid || s != id {
					return nil, errPayload
				}
			}
			ok = true
		}
	}
	if !ok {
		return nil, errPayload
	}
	out := map[string]any{"runtime": runtime, "session_id": id, "context_status": "pending"}
	model := object(root["model"])
	copyText(out, "model", model["display_name"], 256)
	if _, ok := out["model"]; !ok {
		copyText(out, "model", model["id"], 256)
	}
	copyText(out, "cwd", object(root["workspace"])["current_dir"], 4096)
	if _, ok := out["cwd"]; !ok {
		copyText(out, "cwd", root["cwd"], 4096)
	}
	context(out, object(root["context_window"]))
	if runtime == "claude" {
		limits := object(root["rate_limits"])
		for src, dst := range map[string]string{"five_hour": "five_hour", "seven_day": "weekly"} {
			w := object(limits[src])
			if used, ok := percentage(w["used_percentage"]); ok {
				q := map[string]any{"remaining": 100 - used}
				if reset, ok := number(w["resets_at"]); ok {
					q["resets_at"] = reset
				}
				out[dst] = q
			}
		}
	}
	if runtime == "antigravity" {
		switch root["agent_state"] {
		case "idle":
			out["activity"] = "waiting"
		case "thinking", "working", "tool_use", "initializing":
			out["activity"] = "working"
		}
		pools := object(root["quota"])
		keys := make([]string, 0, len(pools))
		for k := range pools {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var normalized []any
		for _, label := range keys {
			if len(normalized) == 32 {
				break
			}
			if _, ok := printable(label, 128); !ok {
				continue
			}
			p := object(pools[label])
			fraction, ok := number(p["remaining_fraction"])
			if !ok || fraction > 1 {
				continue
			}
			q := map[string]any{"label": label, "remaining": fraction * 100}
			if s, ok := p["reset_time"].(string); ok {
				if t, e := time.Parse(time.RFC3339, s); e == nil && t.Unix() >= 0 {
					q["resets_at"] = t.Unix()
				}
			}
			if _, exists := q["resets_at"]; !exists {
				if seconds, ok := number(p["reset_in_seconds"]); ok && seconds <= float64(maxSafe-time.Now().Unix()) {
					q["resets_at"] = float64(time.Now().Unix()) + seconds
				}
			}
			normalized = append(normalized, q)
		}
		if len(normalized) > 0 {
			out["quota_pools"] = normalized
		}
	}
	b, err := json.Marshal(out)
	if err != nil || len(b) > 16384 {
		return nil, errPayload
	}
	return out, nil
}

func context(out map[string]any, c map[string]any) {
	capacity, capOK := number(c["context_window_size"])
	if capOK && capacity > 0 {
		out["context_window"] = capacity
	} else {
		capOK = false
	}
	usage := object(c["current_usage"])
	input, iok := number(usage["input_tokens"])
	creation, cok := number(usage["cache_creation_input_tokens"])
	read, rok := number(usage["cache_read_input_tokens"])
	if iok {
		out["input_tokens"] = input
	} else if total, ok := number(c["total_input_tokens"]); ok {
		out["input_tokens"] = total
	}
	if cok && rok && creation+read <= maxSafe {
		out["cached_tokens"] = creation + read
	}
	used, uok := 0.0, false
	if iok && cok && rok && input+creation+read <= maxSafe {
		used = input + creation + read
		uok = true
		out["context_used"] = used
	}
	if remaining, ok := percentage(c["remaining_percentage"]); ok {
		out["context_remaining"] = remaining
		out["context_status"] = "current"
	} else if percent, ok := percentage(c["used_percentage"]); ok {
		out["context_remaining"] = 100 - percent
		out["context_status"] = "current"
	} else if uok && capOK && used <= capacity {
		out["context_remaining"] = 100 - used/capacity*100
		out["context_status"] = "current"
	}
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func printable(v any, limit int) (string, bool) {
	s, ok := v.(string)
	if !ok || len(s) == 0 || len(s) > limit || !utf8.ValidString(s) {
		return "", false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	return s, true
}
func copyText(out map[string]any, key string, v any, limit int) {
	if s, ok := printable(v, limit); ok {
		out[key] = s
	}
}
func number(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0 && f <= maxSafe
}
func percentage(v any) (float64, bool) { f, ok := number(v); return f, ok && f <= 100 }

// Reject duplicate keys and excessive nesting rather than choosing an ambiguous identity.
func decode(d *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, errDepth
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			s, ok := k.(string)
			if !ok {
				return nil, errPayload
			}
			if _, exists := m[s]; exists {
				return nil, errDuplicateKey
			}
			v, e := decode(d, depth+1)
			if e != nil {
				return nil, e
			}
			m[s] = v
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return nil, errPayload
		}
		return m, nil
	case '[':
		a := []any{}
		for d.More() {
			v, e := decode(d, depth+1)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return nil, errPayload
		}
		return a, nil
	}
	return nil, errPayload
}

// Diagnose describes only allowlisted schema properties. It never returns native
// values, arbitrary key names, parser messages, or quota pool labels.
func Diagnose(raw []byte) map[string]any {
	out := map[string]any{"json_valid": false, "root_type": "unknown", "within_limit": len(raw) <= maxPayload, "duplicate_keys": false, "depth_exceeded": false, "trailing_data": false, "structure_compatible": false}
	if len(raw) > maxPayload || !utf8.Valid(raw) {
		return out
	}
	out["json_valid"] = json.Valid(raw)
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := decode(d, 0)
	out["duplicate_keys"] = errors.Is(err, errDuplicateKey)
	out["depth_exceeded"] = errors.Is(err, errDepth)
	if err != nil {
		return out
	}
	_, endErr := d.Token()
	out["trailing_data"] = endErr != io.EOF
	out["structure_compatible"] = endErr == io.EOF
	out["root_type"] = schemaType(value, true)
	root, ok := value.(map[string]any)
	if !ok {
		return out
	}
	fields := map[string]any{}
	for _, key := range []string{"session_id", "conversation_id", "model", "context_window", "quota", "cwd", "workspace"} {
		v, present := root[key]
		fields[key] = schemaType(v, present)
	}
	for _, spec := range []struct {
		parent string
		keys   []string
	}{
		{"model", []string{"id", "display_name"}},
		{"context_window", []string{"context_window_size", "used_percentage", "remaining_percentage", "current_usage"}},
		{"workspace", []string{"current_dir"}},
	} {
		m := object(root[spec.parent])
		for _, key := range spec.keys {
			v, present := m[key]
			fields[spec.parent+"."+key] = schemaType(v, present)
		}
	}
	out["fields"] = fields
	for _, key := range []string{"session_id", "conversation_id"} {
		if s, ok := root[key].(string); ok {
			printableText := utf8.ValidString(s)
			for _, r := range s {
				if !unicode.IsPrint(r) {
					printableText = false
					break
				}
			}
			out[key+"_properties"] = map[string]any{"nonempty": len(s) > 0, "length": len(s), "printable": printableText}
		}
	}
	if s, ok := root["session_id"].(string); ok {
		if c, ok := root["conversation_id"].(string); ok {
			out["aliases_match"] = s == c
		}
	}
	return out
}
func schemaType(value any, present bool) string {
	if !present {
		return "missing"
	}
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	}
	return "unknown"
}
