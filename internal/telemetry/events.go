// Package telemetry reads only explicit telemetry fields from bound root rollouts.
package telemetry

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const ReadBudget = 262144
const MaxRecordBytes = 65536

var eventUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var eventIdentifier = regexp.MustCompile(`^[A-Za-z0-9_.:/+\-]{1,128}$`)
var eventANSI = regexp.MustCompile("\x1b(?:\\[[0-?]*[ -/]*[@-~]|\\][^\\x07]*(?:\\x07|\x1b\\\\))")

type SessionReader struct {
	Path, SessionID         string
	Invalidated             bool
	BytesRead               int64
	offset, previousSize    int64
	identity                os.FileInfo
	initialized, discarding bool
	buffer                  []byte
	turn                    string
	calls                   map[string]bool
	state                   map[string]any
}

func NewSessionReader(path, id string) *SessionReader {
	r := &SessionReader{Path: path, SessionID: id}
	r.empty()
	if !eventUUID.MatchString(id) {
		r.invalidate()
	}
	return r
}
func (r *SessionReader) empty() {
	r.state = map[string]any{"activity": "waiting", "session_id": r.SessionID}
	for _, k := range strings.Fields("model effort context_remaining five_hour weekly tool updated_at quota_updated_at quota_scope context_used context_window input_tokens cached_tokens") {
		r.state[k] = nil
	}
	r.calls = map[string]bool{}
	r.turn = ""
}
func (r *SessionReader) invalidate() { r.Invalidated = true; r.empty(); r.buffer = nil }
func (r *SessionReader) openChecked() (*os.File, error) {
	fd, e := syscall.Open(r.Path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), r.Path)
	info, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	current, e := os.Lstat(r.Path)
	if e != nil {
		f.Close()
		return nil, e
	}
	if !info.Mode().IsRegular() || !current.Mode().IsRegular() || !os.SameFile(info, current) || (r.identity != nil && !os.SameFile(info, r.identity)) || info.Size() < r.previousSize {
		f.Close()
		return nil, os.ErrInvalid
	}
	r.identity = info
	r.previousSize = info.Size()
	return f, nil
}
func eventDecode(raw []byte) map[string]any {
	var m map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&m) != nil {
		return nil
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil
	}
	return m
}
func eventMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func eventString(v any) string      { s, _ := v.(string); return s }
func eventInt(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, e := n.Int64()
		return i, e == nil && i >= 0
	case int:
		return int64(n), n >= 0
	case int64:
		return n, n >= 0
	}
	return 0, false
}
func eventNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, e := n.Float64()
		return f, e == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case int:
		return float64(n), true
	}
	return 0, false
}
func (r *SessionReader) header(raw []byte) bool {
	m := eventDecode(raw)
	p := eventMap(m["payload"])
	return m["type"] == "session_meta" && p != nil && p["id"] == r.SessionID && p["session_id"] == r.SessionID && p["source"] == "cli" && p["parent_thread_id"] == nil
}
func (r *SessionReader) Read() map[string]any {
	if r.Invalidated {
		return map[string]any{}
	}
	f, e := r.openChecked()
	if e != nil {
		r.invalidate()
		return map[string]any{}
	}
	defer f.Close()
	data := make([]byte, ReadBudget)
	n, e := f.ReadAt(data, r.offset)
	if e != nil && e != io.EOF {
		r.invalidate()
		return map[string]any{}
	}
	data = data[:n]
	r.BytesRead += int64(n)
	r.offset += int64(n)
	if !r.initialized {
		boundary := bytes.IndexByte(data, '\n')
		if boundary < 0 || !r.header(data[:boundary]) {
			r.invalidate()
			return map[string]any{}
		}
		r.initialized = true
		if r.previousSize > ReadBudget {
			r.offset = max(int64(boundary+1), r.previousSize-ReadBudget)
			r.discarding = r.offset > int64(boundary+1)
			return eventClone(r.state)
		}
		data = data[boundary+1:]
	}
	r.consume(data)
	if r.Invalidated {
		return map[string]any{}
	}
	return eventClone(r.state)
}
func eventClone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if child, ok := v.(map[string]any); ok {
			out[k] = eventClone(child)
		} else {
			out[k] = v
		}
	}
	return out
}
func (r *SessionReader) consume(data []byte) {
	if r.discarding {
		b := bytes.IndexByte(data, '\n')
		if b < 0 {
			return
		}
		data = data[b+1:]
		r.discarding = false
	}
	data = append(r.buffer, data...)
	r.buffer = nil
	pieces := bytes.Split(data, []byte{'\n'})
	for _, raw := range pieces[:len(pieces)-1] {
		if len(raw) <= MaxRecordBytes {
			r.record(raw)
		}
		if r.Invalidated {
			return
		}
	}
	partial := pieces[len(pieces)-1]
	if len(partial) > MaxRecordBytes {
		r.discarding = true
	} else {
		r.buffer = bytes.Clone(partial)
	}
}
func (r *SessionReader) record(raw []byte) {
	m := eventDecode(raw)
	p := eventMap(m["payload"])
	if p == nil {
		return
	}
	if m["type"] == "session_meta" {
		if !r.header(raw) {
			r.invalidate()
		}
		return
	}
	stamp, err := time.Parse(time.RFC3339Nano, eventString(m["timestamp"]))
	if err != nil {
		return
	}
	ts := float64(stamp.Unix()) + float64(stamp.Nanosecond())/1e9
	switch m["type"] {
	case "turn_context":
		r.setModel(p["model"], p["effort"])
		r.state["updated_at"] = ts
	case "event_msg":
		if r.event(p, ts) {
			r.state["updated_at"] = ts
		}
	}
}
func (r *SessionReader) setModel(model, effort any) {
	s := eventString(model)
	r.state["model"] = nil
	if len(s) <= 512 {
		s = eventANSI.ReplaceAllString(s, "")
		if eventIdentifier.MatchString(s) {
			r.state["model"] = s
		}
	}
	r.state["effort"] = nil
	for _, v := range strings.Fields("none minimal low medium high xhigh max ultra") {
		if effort == v {
			r.state["effort"] = v
		}
	}
}

// Context calculation derives from OpenAI Codex rust-v0.153.4 protocol.rs,
// Apache-2.0; see NOTICE and LICENSES/Apache-2.0.txt. Rounding matches Rust.
func eventContext(info map[string]any) any {
	window, wok := eventInt(info["model_context_window"])
	tokens, tok := eventInt(eventMap(info["last_token_usage"])["total_tokens"])
	if !wok || !tok {
		return nil
	}
	if window <= 12000 {
		return 0
	}
	effective := window - 12000
	remaining := max(effective-max(tokens-12000, 0), 0)
	return int(math.Floor(float64(remaining)/float64(effective)*100 + 0.5))
}
func (r *SessionReader) event(p map[string]any, stamp float64) bool {
	kind := eventString(p["type"])
	if kind == "token_count" {
		info := eventMap(p["info"])
		r.state["context_remaining"] = eventContext(info)
		if info != nil {
			r.state["context_window"] = nil
			if n, ok := eventInt(info["model_context_window"]); ok {
				r.state["context_window"] = n
			}
			if last := eventMap(info["last_token_usage"]); last != nil {
				for dst, src := range map[string]string{"context_used": "total_tokens", "input_tokens": "input_tokens", "cached_tokens": "cached_input_tokens"} {
					r.state[dst] = nil
					if n, ok := eventInt(last[src]); ok {
						r.state[dst] = n
					}
				}
			}
		}
		r.quota(eventMap(p["rate_limits"]), stamp)
		return true
	}
	if kind == "thread_settings_applied" {
		settings := eventMap(p["thread_settings"])
		if p["thread_id"] != r.SessionID || settings == nil {
			return false
		}
		r.setModel(settings["model"], settings["reasoning_effort"])
		return true
	}
	turn := eventString(p["turn_id"])
	if !eventIdentifier.MatchString(turn) {
		return false
	}
	if kind == "task_started" || kind == "turn_started" {
		r.turn = turn
		r.calls = map[string]bool{}
		r.state["activity"] = "working"
		r.state["tool"] = nil
		return true
	}
	if turn != r.turn {
		return false
	}
	if kind == "task_complete" || kind == "turn_complete" || kind == "turn_aborted" {
		r.state["activity"] = "complete"
		if kind == "turn_aborted" {
			r.state["activity"] = "interrupted"
		}
		r.state["tool"] = nil
		r.calls = map[string]bool{}
		r.turn = ""
		return true
	}
	call := eventString(p["call_id"])
	if !eventIdentifier.MatchString(call) {
		return false
	}
	if kind == "exec_command_begin" {
		r.calls[call] = true
		r.state["tool"] = "exec_command"
		return true
	}
	if kind == "exec_command_end" && r.calls[call] {
		delete(r.calls, call)
		if len(r.calls) == 0 {
			r.state["tool"] = nil
		}
		return true
	}
	return false
}
func (r *SessionReader) quota(limits map[string]any, stamp float64) {
	if limits == nil {
		return
	}
	if limits["limit_id"] != nil && limits["limit_id"] != "codex" {
		if r.state["quota_scope"] != "codex" {
			r.state["quota_scope"] = "unsupported"
		}
		return
	}
	r.state["five_hour"] = nil
	r.state["weekly"] = nil
	for _, slot := range []string{"primary", "secondary"} {
		w := eventMap(limits[slot])
		minutes, ok := eventInt(w["window_minutes"])
		if !ok {
			continue
		}
		field := map[int64]string{300: "five_hour", 10080: "weekly"}[minutes]
		if field == "" {
			continue
		}
		used, ok := eventNumber(w["used_percent"])
		if !ok || used < 0 || used > 100 {
			continue
		}
		var reset any
		if w["resets_at"] != nil {
			n, ok := eventInt(w["resets_at"])
			if !ok {
				continue
			}
			reset = n
		}
		r.state[field] = map[string]any{"remaining": 100 - used, "resets_at": reset}
	}
	r.state["quota_scope"] = "codex"
	r.state["quota_updated_at"] = nil
	if r.state["five_hour"] != nil || r.state["weekly"] != nil {
		r.state["quota_updated_at"] = stamp
	}
}
func (r *SessionReader) ResetAfterAccountChange() {
	if r.Invalidated {
		return
	}
	if !r.initialized {
		r.Read()
	}
	if r.Invalidated {
		return
	}
	f, e := r.openChecked()
	if e != nil {
		r.invalidate()
		return
	}
	defer f.Close()
	r.offset = r.previousSize
	r.discarding = false
	if r.offset > 0 {
		b := make([]byte, 1)
		n, e := f.ReadAt(b, r.offset-1)
		r.BytesRead += int64(n)
		if e != nil {
			r.invalidate()
			return
		}
		r.discarding = b[0] != '\n'
	}
	r.buffer = nil
	r.empty()
}
