// Codex Status launches the stock CLI with a dedicated, shared-collector HUD.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/movingChurch/codex-status/internal/engine"
	"github.com/movingChurch/codex-status/internal/telemetry"
)

var version = "0.3.0-alpha.1"
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var panePattern = regexp.MustCompile(`^%[0-9]+$`)
var errArguments = errors.New("invalid arguments")

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	var err error
	switch {
	case len(args) > 0 && args[0] == "run":
		if len(args) < 2 {
			return 2
		}
		kind := args[1]
		native := args[2:]
		if len(native) > 0 && native[0] == "--" {
			native = native[1:]
		}
		if kind != "codex" && kind != "claude" && kind != "antigravity" {
			return 2
		}
		if kind == "codex" && len(native) >= 2 && native[0] == "resume" && uuidPattern.MatchString(strings.ToLower(native[1])) {
			return launchRuntime(kind, strings.ToLower(native[1]), native[2:])
		}
		return launchRuntime(kind, "", native)
	case len(args) == 1 && args[0] == "install":
		return installAll()
	case len(args) == 1 && args[0] == "uninstall":
		return uninstallAll()
	case len(args) >= 2 && args[0] == "wrap":
		return wrapRuntime(args[1], args[2:])
	case len(args) > 0 && (args[0] == "preview" || args[0] == "doctor" || args[0] == "setup" || args[0] == "uninstall" || args[0] == "__callback" || args[0] == "__hook"):
		return management(args)
	case len(args) == 1 && (args[0] == "--help" || args[0] == "-h"):
		fmt.Println("Usage: codex-status [--resume UUID] [-- Codex arguments]\n       codex-status run codex|claude|antigravity -- [native arguments]\n       codex-status preview|doctor [runtime]\n       codex-status setup|uninstall claude|antigravity [--settings PATH]\n\nShared three-row tmux HUD. Codex requires no settings modification.\nClaude uses a per-launch override. Antigravity requires explicit setup.\n  --version     Print version\n  --wrap        Transparent codex shell integration\n\nScroll with the mouse wheel. Press q to leave scroll history.\nDetach with Ctrl-b d. Exit the native CLI normally to close its HUD.")
		return 0
	case len(args) == 1 && args[0] == "--version":
		fmt.Println("codex-status " + version)
		return 0
	case len(args) > 0 && args[0] == "--wrap":
		return wrap(args[1:])
	case len(args) == 2 && args[0] == "__collector":
		err = engine.Serve(args[1])
	case len(args) == 3 && args[0] == "__render":
		err = render(args[1], args[2])
	case len(args) > 0 && args[0] == "__runner":
		var options runnerOptions
		options, err = parseRunner(args[1:])
		if err == nil {
			return runner(options)
		}
	default:
		var resume string
		var nativeArgs []string
		resume, nativeArgs, err = parseLaunch(args)
		if err == nil {
			return launch(resume, nativeArgs)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "codex-status: could not start. Check arguments, tmux, Codex, and the private runtime directory.")
		return 2
	}
	return 0
}

func parseLaunch(args []string) (string, []string, error) {
	resume := ""
	if len(args) > 0 && args[0] == "--resume" {
		if len(args) < 2 || !uuidPattern.MatchString(strings.ToLower(args[1])) {
			return "", nil, errArguments
		}
		resume = strings.ToLower(args[1])
		args = args[2:]
	}
	if len(args) > 0 {
		if args[0] != "--" {
			return "", nil, errArguments
		}
		args = args[1:]
	}
	if len(args) > 0 {
		for _, command := range []string{"resume", "fork", "exec", "e", "review", "login", "logout", "mcp", "mcp-server", "app-server", "completion", "sandbox", "debug", "apply", "cloud", "features", "agents", "doctor", "update", "app", "queue", "archive", "delete", "unarchive", "remote-control", "exec-server", "help", "plugin", "migrate-rollouts"} {
			if args[0] == command {
				return "", nil, errArguments
			}
		}
	}
	for _, arg := range args {
		if arg == "--last" || arg == "--all" || arg == "--cd" || arg == "-C" || strings.HasPrefix(arg, "--cd=") || (strings.HasPrefix(arg, "-C") && len(arg) > 2) {
			return "", nil, errArguments
		}
	}
	return resume, args, nil
}

func nativeExecutable() (string, error) {
	name := os.Getenv("CODEX_STATUS_CODEX")
	if name == "" {
		name = "codex"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	self, _ = filepath.EvalSymlinks(self)
	if path == self {
		return "", errors.New("recursive executable")
	}
	return path, nil
}

// wrap preserves subcommands and options that cannot be bound safely by the HUD.
func wrap(args []string) int {
	native, err := nativeExecutable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime-status: codex is not installed or not on PATH")
		return 127
	}
	if len(args) >= 2 && args[0] == "resume" && uuidPattern.MatchString(strings.ToLower(args[1])) {
		if _, _, err := parseLaunch(append([]string{"--resume", args[1], "--"}, args[2:]...)); err == nil {
			return launch(strings.ToLower(args[1]), args[2:])
		}
	}
	passthrough := len(args) > 0 && !strings.HasPrefix(args[0], "-")
	for _, arg := range args {
		if arg == "--help" || arg == "-h" || arg == "--version" || arg == "-V" {
			passthrough = true
		}
	}
	if _, _, err := parseLaunch(append([]string{"--"}, args...)); err != nil {
		passthrough = true
	}
	if !passthrough {
		return launch("", args)
	}
	if syscall.Exec(native, append([]string{native}, args...), os.Environ()) != nil {
		return 2
	}
	return 0
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func shellCommand(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = shellQuote(w)
	}
	return strings.Join(q, " ")
}
func tmuxCall(socket string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", append([]string{"-S", socket}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
func launch(resume string, args []string) int { return launchRuntime("codex", resume, args) }
func launchRuntime(kind, resume string, args []string) int {
	runtimeDir, err := engine.ValidateRuntime(filepath.Join("/tmp", fmt.Sprintf("codex-status-go-%d", os.Getuid())))
	if err != nil {
		return 2
	}
	native, err := runtimeExecutable(kind)
	if err != nil {
		return 2
	}
	if _, err = exec.LookPath("tmux"); err != nil {
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		return 2
	}
	self, _ = filepath.EvalSymlinks(self)
	cwd, err := os.Getwd()
	if err != nil {
		return 2
	}
	id := newID()
	socket := filepath.Join(runtimeDir, "tmux.sock")
	sessionName := "hud-" + id
	if len(socket) >= 104 {
		return 2
	}
	words := []string{self, "__runner", "--runtime", runtimeDir, "--run", id, "--socket", socket, "--codex", native}
	words = append(words, "--kind", kind)
	words = append(words, "--environment", "relay")
	if resume != "" {
		words = append(words, "--resume", resume)
	}
	words = append(words, "--")
	words = append(words, args...)
	if err = engine.EnsureCollector(runtimeDir); err != nil {
		fmt.Fprintln(os.Stderr, "codex-status: manager unavailable")
		return 2
	}
	if engine.Handshake(runtimeDir) != nil {
		fmt.Fprintln(os.Stderr, "codex-status: running manager is incompatible; finish its sessions before upgrading")
		return 2
	}
	relay, relayErr := startLaunchEnvironment(runtimeDir, id, os.Environ())
	if relayErr != nil {
		return 2
	}
	defer relay.Close()
	pane, err := tmuxCall(socket, "-f", "/dev/null", "new-session", "-d", "-s", sessionName, "-c", cwd, "-P", "-F", "#{pane_id}", shellCommand(words))
	if err != nil || !panePattern.MatchString(pane) {
		return 2
	}
	ready := false
	hud := ""
	defer func() {
		if !ready {
			if panePattern.MatchString(hud) {
				tmuxCall(socket, "kill-pane", "-t", hud)
			}
			tmuxCall(socket, "kill-pane", "-t", pane)
		}
	}()
	for _, settings := range displaySettings() {
		if _, err = tmuxCall(socket, settings...); err != nil {
			return 2
		}
	}
	hud, err = tmuxCall(socket, "split-window", "-v", "-d", "-l", "4", "-t", pane, "-P", "-F", "#{pane_id}", shellCommand([]string{self, "__render", runtimeDir, id}))
	if err != nil || !panePattern.MatchString(hud) || hud == pane {
		return 2
	}
	if _, err = tmuxCall(socket, "set-option", "-w", "-t", pane, "@codex_status_hud", hud); err != nil {
		return 2
	}
	if _, err = tmuxCall(socket, "select-pane", "-t", pane); err != nil {
		return 2
	}
	for _, event := range []string{"client-attached", "client-resized"} {
		if _, err = tmuxCall(socket, "set-hook", "-t", sessionName, event, "resize-pane -t "+hud+" -y 4"); err != nil {
			return 2
		}
	}
	// Signal only after all setup succeeds: failures above never terminate a live Codex.
	if err = relay.Wait(time.Now().Add(launchEnvironmentTimeout)); err != nil {
		return 2
	}
	if _, err = tmuxCall(socket, "wait-for", "-S", "ready-"+id); err != nil {
		return 2
	}
	ready = true
	cmd := exec.Command("tmux", "-S", socket, "attach-session", "-t", sessionName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "TMUX=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	// Replace the launcher with the tmux client; independent windows need no
	// additional persistent Go launcher after their panes have been prepared.
	tmuxPath, lookupErr := exec.LookPath("tmux")
	if lookupErr != nil {
		return 2
	}
	if err = syscall.Exec(tmuxPath, cmd.Args, cmd.Env); err != nil {
		return 2
	}
	return 0
}
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var e *exec.ExitError
	if errors.As(err, &e) {
		if e.ExitCode() >= 0 {
			return e.ExitCode()
		}
	}
	return 2
}

type runnerOptions struct {
	Runtime, Run, Socket, Codex, Resume string
	Kind                                string
	Environment                         string
	Args                                []string
}

func parseRunner(args []string) (runnerOptions, error) {
	var o runnerOptions
	o.Kind = "codex"
	fields := map[string]*string{"--runtime": &o.Runtime, "--run": &o.Run, "--socket": &o.Socket, "--codex": &o.Codex, "--resume": &o.Resume, "--kind": &o.Kind, "--environment": &o.Environment}
	seen := map[string]bool{}
	for len(args) > 0 {
		if args[0] == "--" {
			o.Args = args[1:]
			break
		}
		p, ok := fields[args[0]]
		if !ok || len(args) < 2 || seen[args[0]] {
			return o, errArguments
		}
		seen[args[0]] = true
		*p = args[1]
		args = args[2:]
	}
	if !uuidPattern.MatchString(o.Run) || !filepath.IsAbs(o.Runtime) || !filepath.IsAbs(o.Codex) || !ownedSocket(o) || (o.Resume != "" && !uuidPattern.MatchString(o.Resume)) || (o.Kind != "codex" && o.Kind != "claude" && o.Kind != "antigravity") || (o.Environment != "" && o.Environment != "relay") {
		return o, errArguments
	}
	return o, nil
}
func cleanupHUD(o runnerOptions, pane, hud string) {
	if !uuidPattern.MatchString(o.Run) || !ownedSocket(o) || !panePattern.MatchString(pane) || !panePattern.MatchString(hud) || pane == hud {
		return
	}
	tmuxCall(o.Socket, "kill-pane", "-t", hud)
}
func runner(o runnerOptions) int {
	if _, err := engine.ValidateRuntime(o.Runtime); err != nil {
		return 2
	}
	pane := os.Getenv("TMUX_PANE")
	if !panePattern.MatchString(pane) {
		return 2
	}
	if o.Environment == "relay" {
		environment, receiveErr := receiveLaunchEnvironment(o.Runtime, o.Run)
		if receiveErr != nil {
			return 2
		}
		restoreLaunchEnvironment(environment)
	}
	if _, err := tmuxCall(o.Socket, "wait-for", "ready-"+o.Run); err != nil {
		return 2
	}
	hud, err := tmuxCall(o.Socket, "show-options", "-wqv", "-t", pane, "@codex_status_hud")
	if err != nil || !panePattern.MatchString(hud) || hud == pane {
		return 2
	}
	defer cleanupHUD(o, pane, hud)
	cwd, err := os.Getwd()
	if err != nil {
		return 2
	}
	home := os.Getenv("CODEX_HOME")
	if o.Kind == "claude" {
		home = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	if o.Kind == "antigravity" {
		home = ""
	}
	if home == "" {
		h, e := os.UserHomeDir()
		if e != nil {
			return 2
		}
		home = filepath.Join(h, ".codex")
		if o.Kind == "claude" {
			home = filepath.Join(h, ".claude")
		}
		if o.Kind == "antigravity" {
			home = filepath.Join(h, ".gemini", "antigravity-cli")
		}
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return 2
	}
	if real, e := filepath.EvalSymlinks(home); e == nil {
		home = real
	}
	nativeArgs := []string{}
	if o.Resume != "" {
		nativeArgs = append(nativeArgs, "resume", o.Resume)
	}
	nativeArgs = append(nativeArgs, o.Args...)
	if o.Kind != "codex" {
		var prepareErr error
		nativeArgs, prepareErr = prepareNative(o, nativeArgs)
		if prepareErr != nil {
			fmt.Fprintln(os.Stderr, "codex-status:", prepareErr)
			return 2
		}
		defer func() { _ = os.Remove(filepath.Join(o.Runtime, "launch-"+o.Run+".json")) }()
	}
	cmd := exec.Command(o.Codex, nativeArgs...)
	cmd.Env = append(os.Environ(), "CODEX_STATUS_RUN="+o.Run, "CODEX_STATUS_RUNTIME_DIR="+o.Runtime)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	signals := make(chan os.Signal, 8)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	// Codex shares the foreground process group and receives terminal signals itself.
	if err = cmd.Start(); err != nil {
		return 2
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-signals:
			case <-done:
				return
			}
		}
	}()
	defer close(done)
	started := telemetry.ProcessIdentity(cmd.Process.Pid)
	for i := 0; started == "" && i < 20; i++ {
		time.Sleep(10 * time.Millisecond)
		started = telemetry.ProcessIdentity(cmd.Process.Pid)
	}
	if started != "" {
		effective := telemetry.ProcessExecutable(cmd.Process.Pid, started)
		// Sample across immediate launcher re-execs before sealing the receipt.
		for i, stable := 0, 0; i < 20 && stable < 2; i++ {
			time.Sleep(10 * time.Millisecond)
			next := telemetry.ProcessExecutable(cmd.Process.Pid, started)
			if next != "" && next == effective {
				stable++
			} else {
				effective = next
				stable = 0
			}
		}
		if effective == "" {
			effective = o.Codex
		}
		meta := map[string]any{"v": 1, "runtime": o.Kind, "id": o.Run, "home": home, "cwd": cwd, "tmux_socket": o.Socket, "codex_pane": pane, "hud_pane": hud, "executable": effective, "launcher_executable": o.Codex, "launched_at": float64(time.Now().UnixMilli()) / 1000, "account_quota": nativeStartupQuota(o.Kind, nativeArgs), "pid": cmd.Process.Pid, "started": started, "auth_epoch": telemetry.AuthEpoch(home)}
		if o.Resume != "" {
			meta["expected_session"] = o.Resume
		}
		if path, e := engine.WriteMetadata(o.Runtime, meta); e == nil {
			defer os.Remove(path)
		} else {
			cleanupHUD(o, pane, hud)
		}
	} else {
		cleanupHUD(o, pane, hud)
	}
	// Metadata failures never end the user's running Codex process.
	return exitCode(cmd.Wait())
}
func render(runtimeDir, id string) error {
	if !uuidPattern.MatchString(id) {
		return errArguments
	}
	if _, err := engine.ValidateRuntime(runtimeDir); err != nil {
		return err
	}
	deadline := time.Now().Add(12 * time.Second)
	for {
		if _, err := engine.ReadMetadata(runtimeDir, id); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("metadata unavailable")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := engine.EnsureCollector(runtimeDir); err != nil {
		return err
	}
	return engine.RenderClient(runtimeDir, id)
}
