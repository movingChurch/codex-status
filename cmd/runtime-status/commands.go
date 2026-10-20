package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/movingChurch/runtime-status/internal/adapters"
	"github.com/movingChurch/runtime-status/internal/engine"
	"github.com/movingChurch/runtime-status/internal/install"
	"github.com/movingChurch/runtime-status/internal/setup"
	"github.com/movingChurch/runtime-status/internal/telemetry"
)

func ownedSocket(o runnerOptions) bool {
	return o.Socket == filepath.Join(o.Runtime, "tmux.sock") || o.Socket == filepath.Join(o.Runtime, "tmux-"+o.Run+".sock")
}

func restoreLaunchEnvironment(environment []string) {
	tmux, pane := os.Getenv("TMUX"), os.Getenv("TMUX_PANE")
	os.Clearenv()
	for _, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if ok && name != "TMUX" && name != "TMUX_PANE" {
			_ = os.Setenv(name, value)
		}
	}
	if tmux != "" {
		_ = os.Setenv("TMUX", tmux)
	}
	if pane != "" {
		_ = os.Setenv("TMUX_PANE", pane)
	}
}

func nativeStartupQuota(kind string, args []string) bool {
	if kind != "codex" {
		return false
	}
	for _, name := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_BASE_URL", "OPENAI_ORGANIZATION", "OPENAI_PROJECT"} {
		if os.Getenv(name) != "" {
			return false
		}
	}
	// The startup app-server intentionally has no forwarded session arguments.
	// A different profile/provider must report its own exact-session quotas.
	for _, arg := range args {
		if arg == "--oss" || arg == "--local-provider" || strings.HasPrefix(arg, "--local-provider=") || arg == "--config" || strings.HasPrefix(arg, "--config=") || arg == "--profile" || strings.HasPrefix(arg, "--profile=") || strings.HasPrefix(arg, "-c") || strings.HasPrefix(arg, "-p") {
			return false
		}
	}
	return true
}
func runtimeExecutable(kind string) (string, error) {
	if kind == "codex" {
		return nativeExecutable()
	}
	name, override := "claude", "RUNTIME_STATUS_CLAUDE"
	if kind == "antigravity" {
		name = "agy"
		override = "RUNTIME_STATUS_ANTIGRAVITY"
	}
	if value := os.Getenv(override); value != "" {
		name = value
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	self, e := os.Executable()
	if e != nil {
		return "", e
	}
	self, _ = filepath.EvalSymlinks(self)
	if path == self {
		return "", errors.New("recursive runtime executable")
	}
	return path, nil
}
func statusLine(kind string) (map[string]any, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return nil, err
	}
	return statusLineFor(self, kind), nil
}

// statusLineFor names an explicit executable, so install can point at the installed copy.
func statusLineFor(self, kind string) map[string]any {
	value := map[string]any{"type": "command", "command": shellCommand([]string{self, "__callback", kind}), "padding": 0}
	if kind == "claude" {
		// Statusline events go quiet while idle; the timer keeps the 180-second stale limit unmet.
		value["refreshInterval"] = 15
	}
	if kind == "antigravity" {
		value["enabled"] = true
		value["stack_with_default"] = false
	}
	return value
}

func antigravityReady() bool {
	value, err := statusLine("antigravity")
	path, pathErr := settingsPath("antigravity")
	dir, dirErr := journalDir()
	if err != nil || pathErr != nil || dirErr != nil {
		return false
	}
	installed, err := setup.Installed(path, dir, value)
	return err == nil && installed
}

// activityHooks registers the launch-scoped Claude events that report HUD activity.
func activityHooks() map[string]any {
	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		return map[string]any{}
	}
	entry := func(matcher string) []any {
		return []any{map[string]any{"matcher": matcher, "hooks": []any{map[string]any{"type": "command", "command": shellCommand([]string{self, "__hook", "claude"}), "timeout": 1}}}}
	}
	out := map[string]any{}
	for event := range adapters.HookEvents {
		out[event] = entry("")
	}
	out["Notification"] = entry("permission_prompt|idle_prompt")
	return out
}

// mergeHooks appends the launcher's entries to each event of an explicit hooks object.
func mergeHooks(existing json.RawMessage, ours map[string]any) (json.RawMessage, error) {
	events := map[string][]json.RawMessage{}
	if len(existing) > 0 && string(existing) != "null" && json.Unmarshal(existing, &events) != nil {
		return nil, errors.New("invalid explicit hooks object")
	}
	for event, entries := range ours {
		for _, entry := range entries.([]any) {
			raw, _ := json.Marshal(entry)
			events[event] = append(events[event], raw)
		}
	}
	return json.Marshal(events)
}
func settingsPath(kind string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if kind == "antigravity" {
		return filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"), nil
	}
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(home, ".claude")
	}
	return filepath.Join(dir, "settings.json"), nil
}
func journalDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", install.Product, "settings"), nil
}

// installAll and uninstallAll back `runtime-status install` and `uninstall` without a runtime.
func installOptions() (install.Options, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return install.Options{}, err
	}
	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	return install.Options{Home: home, Source: self, Busy: install.Busy, Out: os.Stdout}, err
}
func installAll() int {
	o, err := installOptions()
	if err != nil {
		return 2
	}
	if _, e := exec.LookPath("agy"); e == nil {
		o.Antigravity = func(binary string) error {
			path, err := settingsPath("antigravity")
			if err != nil {
				return err
			}
			if _, e := os.Stat(install.LegacyJournal(o.Home)); e == nil {
				if err = setup.Restore(path, install.LegacyJournal(o.Home)); err != nil {
					return err
				}
			}
			dir, err := journalDir()
			if err == nil {
				err = os.MkdirAll(filepath.Dir(path), 0700)
			}
			if err == nil {
				err = setup.Install(path, dir, statusLineFor(binary, "antigravity"))
			}
			return err
		}
	}
	if err = install.Install(o); err != nil {
		fmt.Fprintln(os.Stderr, "runtime-status:", err)
		return 2
	}
	fmt.Println("Open a new shell to use codex, claude and agy with the HUD.")
	return 0
}
func uninstallAll() int {
	o, err := installOptions()
	if err != nil {
		return 2
	}
	err = install.Uninstall(o, func() error {
		path, err := settingsPath("antigravity")
		dir, dirErr := journalDir()
		if err != nil || dirErr != nil {
			return errors.Join(err, dirErr)
		}
		return setup.Restore(path, dir)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime-status:", err)
		return 2
	}
	return 0
}
func prepareNative(o runnerOptions, args []string) ([]string, error) {
	value, err := statusLine(o.Kind)
	if err != nil {
		return nil, err
	}
	if o.Kind == "claude" {
		for i, arg := range args {
			if arg == "--settings" || strings.HasPrefix(arg, "--settings=") {
				valueArg := strings.TrimPrefix(arg, "--settings=")
				end := i + 1
				if arg == "--settings" {
					if i+1 >= len(args) {
						return nil, errArguments
					}
					valueArg = args[i+1]
					end++
				}
				for _, remaining := range args[end:] {
					if remaining == "--settings" || strings.HasPrefix(remaining, "--settings=") {
						return nil, errors.New("multiple explicit settings sources are ambiguous")
					}
				}
				raw := []byte(valueArg)
				if !strings.HasPrefix(strings.TrimSpace(valueArg), "{") {
					f, e := os.Open(valueArg)
					if e != nil {
						return nil, e
					}
					info, statErr := f.Stat()
					if statErr != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
						_ = f.Close()
						return nil, errors.New("settings source must be a bounded regular file")
					}
					raw, e = io.ReadAll(io.LimitReader(f, (1<<20)+1))
					_ = f.Close()
					if e != nil {
						return nil, e
					}
				}
				if len(raw) > 1<<20 {
					return nil, errors.New("settings source exceeds size limit")
				}
				var settings map[string]json.RawMessage
				if json.Unmarshal(raw, &settings) != nil || settings == nil {
					return nil, errors.New("invalid explicit settings object")
				}
				replacement, _ := json.Marshal(value)
				settings["statusLine"] = replacement
				if settings["hooks"], err = mergeHooks(settings["hooks"], activityHooks()); err != nil {
					return nil, err
				}
				merged, _ := json.Marshal(settings)
				path := filepath.Join(o.Runtime, "launch-"+o.Run+".json")
				file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if openErr != nil {
					return nil, openErr
				}
				_, err = file.Write(merged)
				closeErr := file.Close()
				if err == nil {
					err = closeErr
				}
				if err != nil {
					return nil, err
				}
				out := append([]string{}, args[:i]...)
				out = append(out, "--settings", path)
				out = append(out, args[end:]...)
				return out, nil
			}
		}
		raw, _ := json.Marshal(map[string]any{"statusLine": value, "hooks": activityHooks()})
		return append(args, "--settings", string(raw)), nil
	}
	path, err := settingsPath(o.Kind)
	if err != nil {
		return nil, err
	}
	dir, err := journalDir()
	if err != nil {
		return nil, err
	}
	installed, err := setup.Installed(path, dir, value)
	if err != nil {
		return nil, err
	}
	if !installed {
		return nil, errors.New("antigravity requires explicit `runtime-status setup antigravity` before managed launch")
	}
	return args, nil
}

func management(args []string) int {
	command := args[0]
	kind := "codex"
	if len(args) > 1 {
		kind = args[1]
	}
	if command == "__callback" {
		return callback(kind)
	}
	if command == "__hook" {
		return hook()
	}
	if kind != "codex" && kind != "claude" && kind != "antigravity" {
		return 2
	}
	if command == "preview" {
		snapshot := map[string]any{"runtime": kind, "model": kind + " example", "context_remaining": 75, "context_status": "current", "context_used": 50000, "context_window": 200000, "weekly": map[string]any{"remaining": 65}, "state": "live", "session_id": "11111111-1111-4111-8111-111111111111", "activity": "working"}
		for _, row := range telemetry.Render(snapshot, 100, true) {
			fmt.Println(row)
		}
		return 0
	}
	if command == "doctor" {
		runtime, err := engine.ValidateRuntime("")
		if err != nil {
			fmt.Println("Private manager directory: unavailable")
			return 2
		}
		_, nativeErr := runtimeExecutable(kind)
		_, tmuxErr := exec.LookPath("tmux")
		fmt.Printf("Runtime: %s\nNative executable available: %t\nTmux available: %t\n", kind, nativeErr == nil, tmuxErr == nil)
		state, err := engine.RPC(runtime, map[string]any{"v": 1, "op": "status"}, time.Now().Add(time.Second))
		if err != nil {
			fmt.Println("Manager: not running")
		} else {
			fmt.Printf("Manager protocol: %v\nViews: %v\nRegistered sessions: %v\n", state["protocol"], state["clients"], state["runs"])
		}
		fmt.Println("Account identity: shared only after scoped native verification; unresolved connections remain isolated\nCompatibility: payload capabilities, no vendor version pin\nNative callback support: see docs/shared-hud/compatibility.md")
		if nativeErr != nil || tmuxErr != nil {
			return 2
		}
		return 0
	}
	if kind == "codex" {
		fmt.Fprintln(os.Stderr, "runtime-status: Codex needs no statusLine setting")
		return 2
	}
	path, err := settingsPath(kind)
	if err != nil {
		return 2
	}
	if len(args) > 2 {
		if len(args) != 4 || args[2] != "--settings" {
			return 2
		}
		path = args[3]
	}
	dir, err := journalDir()
	if err != nil {
		return 2
	}
	if command == "setup" {
		value, e := statusLine(kind)
		if e != nil {
			return 2
		}
		err = os.MkdirAll(filepath.Dir(path), 0700)
		if err == nil {
			err = setup.Install(path, dir, value)
		}
	} else {
		err = setup.Restore(path, dir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime-status:", err)
		return 2
	}
	fmt.Printf("%s %s statusLine: complete\n", command, kind)
	return 0
}

// callback is intentionally silent: the shared tmux pane owns presentation.
func callback(kind string) int {
	deadline := time.Now().Add(engine.CallbackBudget)
	id, root := os.Getenv("RUNTIME_STATUS_RUN"), os.Getenv("RUNTIME_STATUS_RUNTIME_DIR")
	if !uuidPattern.MatchString(id) || root == "" {
		return previousCallback(kind, deadline)
	}
	request := map[string]any{"v": 1, "op": "ticket", "run": id}
	var ticket map[string]any
	var err error
	for time.Until(deadline) > 10*time.Millisecond {
		ticket, err = engine.RPC(root, request, deadline)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil || ticket["ticket"] == nil {
		return 0
	}
	type input struct {
		data        map[string]any
		diagnostics map[string]any
		err         error
	}
	done := make(chan input, 1)
	go func() {
		raw, e := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
		if e != nil {
			done <- input{err: e}
			return
		}
		data, e := adapters.Normalize(kind, raw)
		var diagnostics map[string]any
		if e != nil {
			diagnostics = adapters.Diagnose(raw)
		}
		done <- input{data: data, diagnostics: diagnostics, err: e}
	}()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case result := <-done:
		if result.err != nil {
			op := "reject"
			if errors.Is(result.err, adapters.ErrSessionPending) {
				op = "pending"
			}
			_, _ = engine.RPC(root, map[string]any{"v": 1, "op": op, "run": id, "ticket": ticket["ticket"], "diagnostics": result.diagnostics}, deadline)
			return 0
		}
		_, _ = engine.RPC(root, map[string]any{"v": 1, "op": "publish", "run": id, "ticket": ticket["ticket"], "data": result.data}, deadline)
	case <-timer.C:
	}
	return 0
}

// hook forwards one Claude hook event as activity and always exits 0 without output,
// so a missing or slow manager never blocks or changes the native turn.
func hook() int {
	deadline := time.Now().Add(engine.CallbackBudget)
	id, root := os.Getenv("RUNTIME_STATUS_RUN"), os.Getenv("RUNTIME_STATUS_RUNTIME_DIR")
	if !uuidPattern.MatchString(id) || root == "" {
		return 0
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
	if err != nil {
		return 0
	}
	data, err := adapters.NormalizeHook(raw)
	if err != nil {
		return 0
	}
	_, _ = engine.RPC(root, map[string]any{"v": 1, "op": "activity", "run": id, "session_id": data["session_id"], "activity": data["activity"]}, deadline)
	return 0
}

func previousCallback(kind string, deadline time.Time) int {
	if kind != "claude" && kind != "antigravity" {
		return 0
	}
	type previous struct {
		value   map[string]any
		present bool
		err     error
	}
	ready := make(chan previous, 1)
	go func() {
		path, e := settingsPath(kind)
		if e != nil {
			ready <- previous{err: e}
			return
		}
		dir, e := journalDir()
		if e != nil {
			ready <- previous{err: e}
			return
		}
		value, present, e := setup.Previous(path, dir)
		ready <- previous{value, present, e}
	}()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case old := <-ready:
		if old.err != nil || !old.present || old.value["type"] != "command" {
			return 0
		}
		command, ok := old.value["command"].(string)
		if !ok || command == "" || strings.Contains(command, "__callback") {
			return 0
		}
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.WaitDelay = 10 * time.Millisecond
		_ = cmd.Run()
	case <-timer.C:
	}
	return 0
}
