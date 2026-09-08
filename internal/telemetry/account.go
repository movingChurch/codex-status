package telemetry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func accountNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, e := n.Float64()
		return f, e == nil
	}
	return 0, false
}

func accountResetValid(value any) bool {
	switch n := value.(type) {
	case int:
		return n >= 0
	case int64:
		return n >= 0
	case json.Number:
		parsed, err := n.Int64()
		return err == nil && parsed >= 0
	default:
		return false
	}
}

func accountNormalize(result map[string]any) map[string]any {
	buckets, _ := result["rateLimitsByLimitId"].(map[string]any)
	limits, ok := buckets["codex"].(map[string]any)
	if !ok {
		limits, ok = result["rateLimits"].(map[string]any)
	}
	if !ok {
		return map[string]any{}
	}
	out := map[string]any{"quota_scope": "codex", "quota_source": "account", "five_hour": nil, "weekly": nil}
	for _, slot := range []string{"primary", "secondary"} {
		value, _ := limits[slot].(map[string]any)
		duration, _ := accountNumber(value["windowDurationMins"])
		field := ""
		switch duration {
		case 300:
			field = "five_hour"
		case 10080:
			field = "weekly"
		}
		if field == "" {
			continue
		}
		used, valid := accountNumber(value["usedPercent"])
		reset := value["resetsAt"]
		if reset != nil {
			valid = valid && accountResetValid(reset)
		}
		out[field] = nil
		if valid && !math.IsNaN(used) && !math.IsInf(used, 0) && used >= 0 && used <= 100 {
			out[field] = map[string]any{"remaining": 100 - used, "resets_at": reset}
		}
	}
	if out["five_hour"] != nil || out["weekly"] != nil {
		out["quota_updated_at"] = float64(time.Now().UnixNano()) / 1e9
	}
	return out
}

// FetchAccount reads quota using one bounded app-server helper, without credentials or model requests.
func FetchAccount(ctx context.Context, home, executable string) map[string]any {
	result, err := accountFetch(ctx, home, executable)
	if err != nil {
		return map[string]any{"quota_error": "Account quota unavailable"}
	}
	return accountNormalize(result)
}

func accountFetch(parent context.Context, home, executable string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "app-server")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CODEX_HOME=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "CODEX_HOME="+home)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, outputWriter, err := os.Pipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	cmd.Stdout = outputWriter
	if err = cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		outputWriter.Close()
		return nil, err
	}
	outputWriter.Close()
	defer stdout.Close()
	stopRead := context.AfterFunc(ctx, func() { _ = stdout.Close() })
	defer stopRead()
	// Wait runs concurrently so context cancellation can terminate a silent helper.
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	defer func() { stdin.Close(); cancel(); <-waited }()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1048576)
	encoder := json.NewEncoder(stdin)
	response := func(id float64) (map[string]any, error) {
		for scanner.Scan() {
			var value map[string]any
			decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				return nil, err
			}
			responseID, numberID := accountNumber(value["id"])
			if !numberID || responseID != id {
				continue
			}
			if _, failed := value["error"]; failed {
				return nil, fmt.Errorf("account read failed")
			}
			result, _ := value["result"].(map[string]any)
			return result, nil
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	if err = encoder.Encode(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "codex_status", "version": "0.1.0"}}}); err != nil {
		return nil, err
	}
	if _, err = response(1); err != nil {
		return nil, err
	}
	if err = encoder.Encode(map[string]any{"method": "initialized"}); err != nil {
		return nil, err
	}
	if err = encoder.Encode(map[string]any{"id": 2, "method": "account/rateLimits/read"}); err != nil {
		return nil, err
	}
	return response(2)
}
