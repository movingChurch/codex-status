package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/movingChurch/runtime-status/internal/accounts/logins"
	"github.com/movingChurch/runtime-status/internal/engine"
	"github.com/movingChurch/runtime-status/internal/telemetry"
)

// switchWindows are the open runtime-status windows of one runtime: those on
// its live home are relaunched by a switch, the caller's own last, and those
// on other homes, such as Orca's account homes, are left alone.
type switchWindows struct {
	runtimeDir string
	relaunch   []map[string]any
	skipped    []map[string]any
	// orphaned windows lost their tmux pane while their CLI still runs; a
	// switch ends those processes instead of relaunching them.
	orphaned []map[string]any
	// conversations holds the conversation each relaunched window resumes,
	// by run ID; a window missing from it restarts without one.
	conversations map[string]string
}

// readConversations reads each relaunched window's conversation before any
// process stops, all windows at once and at most two seconds in all.
func (w *switchWindows) readConversations() {
	w.conversations = map[string]string{}
	deadline := time.Now().Add(2 * time.Second)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, meta := range w.relaunch {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if id := conversationOf(w.runtimeDir, meta, deadline); id != "" {
				mu.Lock()
				w.conversations[stringOf(meta["id"])] = id
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

// fresh are the relaunched windows that restart without a conversation.
func (w switchWindows) fresh() []map[string]any {
	out := []map[string]any{}
	for _, meta := range w.relaunch {
		if w.conversations[stringOf(meta["id"])] == "" {
			out = append(out, meta)
		}
	}
	return out
}

// defaultRuntimeDir is the runtime directory of this user's windows.
func defaultRuntimeDir() string {
	return filepath.Join("/tmp", fmt.Sprintf("runtime-status-%d", os.Getuid()))
}

func openWindows(kind string) switchWindows {
	w := switchWindows{runtimeDir: defaultRuntimeDir()}
	home, err := os.UserHomeDir()
	if err != nil {
		return w
	}
	var own map[string]any
	panes := map[string]map[string]bool{}
	for _, meta := range engine.ListMetadata(w.runtimeDir) {
		switch {
		case meta["runtime"] != kind:
		case !logins.OnLiveHome(home, kind, stringOf(meta["home"])):
			w.skipped = append(w.skipped, meta)
		case !paneAlive(meta, panes):
			w.orphaned = append(w.orphaned, meta)
		case meta["id"] == os.Getenv("RUNTIME_STATUS_RUN"):
			own = meta
		default:
			w.relaunch = append(w.relaunch, meta)
		}
	}
	if own != nil {
		w.relaunch = append(w.relaunch, own)
	}
	return w
}

// paneAlive reads each tmux server's panes once into panes, by socket.
func paneAlive(meta map[string]any, panes map[string]map[string]bool) bool {
	pane := stringOf(meta["codex_pane"])
	if !panePattern.MatchString(pane) {
		return false
	}
	socket := stringOf(meta["tmux_socket"])
	alive, read := panes[socket]
	if !read {
		alive = map[string]bool{}
		out, _ := tmuxCall(socket, "list-panes", "-a", "-F", "#{pane_id} #{session_attached}")
		for _, line := range strings.Split(out, "\n") {
			if id, _, ok := strings.Cut(line, " "); ok && paneAttached(line, id) {
				alive[id] = true
			}
		}
		panes[socket] = alive
	}
	return alive[pane]
}

// paneAttached reports whether a "#{pane_id} #{session_attached}" answer names
// the pane in a session with a client; a session without one is a ghost left
// by a closed window and counts as ended.
func paneAttached(out, pane string) bool {
	id, attached, ok := strings.Cut(out, " ")
	return ok && id == pane && attached != "0"
}

func stringOf(v any) string { s, _ := v.(string); return s }

// holdsCaller reports whether the caller runs inside a window the switch
// relaunches, so the switch has to outlive that window.
func (w switchWindows) holdsCaller() bool {
	for _, meta := range w.relaunch {
		if meta["id"] == os.Getenv("RUNTIME_STATUS_RUN") {
			return true
		}
	}
	return false
}

func describeWindow(meta map[string]any) string {
	return fmt.Sprintf("%s (pid %.0f)", stringOf(meta["cwd"]), meta["pid"])
}

func describeWindows(metas []map[string]any) string {
	names := []string{}
	for _, meta := range metas {
		names = append(names, describeWindow(meta))
	}
	return strings.Join(names, ", ")
}

// forSwitch hands the windows to the switch; nil when none is open.
func (w switchWindows) forSwitch() *logins.Windows {
	if len(w.relaunch) == 0 && len(w.orphaned) == 0 {
		return nil
	}
	runs := []string{}
	for _, meta := range append(append([]map[string]any{}, w.relaunch...), w.orphaned...) {
		runs = append(runs, stringOf(meta["id"]))
	}
	return &logins.Windows{Runs: runs, Stop: func() error {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		self, _ = filepath.EvalSymlinks(self)
		flags := &helpFlags{}
		relaunch := func(meta map[string]any) error {
			if err := relaunchWindow(self, w.runtimeDir, meta, w.conversations[stringOf(meta["id"])], flags); err != nil {
				return fmt.Errorf("relaunch of %s: %w", describeWindow(meta), err)
			}
			return nil
		}
		// The other windows relaunch side by side, the caller's own after them.
		others, last := w.relaunch, map[string]any(nil)
		if w.holdsCaller() {
			others, last = w.relaunch[:len(w.relaunch)-1], w.relaunch[len(w.relaunch)-1]
		}
		errs := make([]error, len(others))
		var wg sync.WaitGroup
		for i, meta := range others {
			wg.Add(1)
			go func() { defer wg.Done(); errs[i] = relaunch(meta) }()
		}
		wg.Wait()
		if err = errors.Join(errs...); err != nil || last == nil {
			return err
		}
		return relaunch(last)
	}}
}

// helpFlags keeps each launcher's value options for one switch, so `--help`
// runs once per launcher however many windows resume.
type helpFlags struct {
	mu    sync.Mutex
	flags map[string]map[string]bool
}

func (h *helpFlags) of(launcher string) map[string]bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.flags == nil {
		h.flags = map[string]map[string]bool{}
	}
	if _, ok := h.flags[launcher]; !ok {
		h.flags[launcher] = valueFlags(nativeHelp(launcher))
	}
	return h.flags[launcher]
}

// relaunchWindow replaces a window's runner and HUD with new ones under a new
// run id, in the same pane and directory and with the same environment; the
// new runner starts its CLI once the switch ends, so it runs on the new login,
// and resumes conversation when it is not empty.
func relaunchWindow(self, runtimeDir string, meta map[string]any, conversation string, flags *helpFlags) error {
	socket, pane, hud := stringOf(meta["tmux_socket"]), stringOf(meta["codex_pane"]), stringOf(meta["hud_pane"])
	kind, launcher, cwd := stringOf(meta["runtime"]), stringOf(meta["launcher_executable"]), stringOf(meta["cwd"])
	if !panePattern.MatchString(pane) || launcher == "" || cwd == "" {
		return errors.New("window metadata incomplete")
	}
	session, err := tmuxCall(socket, "display-message", "-p", "-t", pane, "#{session_name}")
	if err != nil {
		return err
	}
	id := newID()
	words := []string{self, "__runner", "--runtime", runtimeDir, "--run", id, "--socket", socket, "--codex", launcher, "--kind", kind}
	var relay *launchEnvironment
	if pid, ok := meta["pid"].(float64); ok {
		if env := logins.ProcessEnv(int(pid)); env != nil {
			if relay, err = startLaunchEnvironment(runtimeDir, id, env); err != nil {
				return err
			}
			defer relay.Close()
			words = append(words, "--environment", "relay")
		}
	}
	words = append(words, "--")
	args := windowArgs(meta)
	if conversation != "" {
		// The runner's --resume starts `codex resume <id>`; Claude Code takes it as an option.
		args = resumeArgs(args, flags.of(launcher))
		if kind == "codex" {
			words = append(words[:len(words)-1], "--resume", conversation, "--")
		} else {
			args = append(args, "--resume", conversation)
		}
	}
	words = append(words, args...)
	if panePattern.MatchString(hud) {
		_, _ = tmuxCall(socket, "kill-pane", "-t", hud)
	}
	if _, err = tmuxCall(socket, "respawn-pane", "-k", "-t", pane, "-c", cwd, shellCommand(words)); err != nil {
		return err
	}
	if _, err = splitHUD(socket, session, pane, self, runtimeDir, id, kind); err != nil {
		return err
	}
	if relay != nil {
		if err = relay.Wait(time.Now().Add(launchEnvironmentTimeout)); err != nil {
			return err
		}
	}
	_, err = tmuxCall(socket, "wait-for", "-S", "ready-"+id)
	return err
}

// windowArgs are the native arguments a window was launched with; windows
// started before they were kept in the metadata take them from their runner's
// command line, split at spaces.
func windowArgs(meta map[string]any) []string {
	args := []string{}
	if kept, ok := meta["args"].([]any); ok {
		for _, arg := range kept {
			args = append(args, stringOf(arg))
		}
		return args
	}
	out, err := exec.Command("ps", "-axww", "-o", "args=").Output()
	if err != nil {
		return args
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, " __runner ") && strings.Contains(line, " --run "+stringOf(meta["id"])+" ") {
			if _, rest, ok := strings.Cut(line, " -- "); ok {
				return strings.Fields(rest)
			}
		}
	}
	return args
}

// splitHUD opens the HUD pane under pane and names it on the window.
func splitHUD(socket, session, pane, self, runtimeDir, id, kind string) (string, error) {
	hud, err := tmuxCall(socket, "split-window", "-v", "-d", "-l", "4", "-t", pane, "-P", "-F", "#{pane_id}", shellCommand([]string{self, "__render", runtimeDir, id}))
	if err != nil || !panePattern.MatchString(hud) || hud == pane {
		return "", errors.New("HUD pane unavailable")
	}
	if _, err = tmuxCall(socket, "set-option", "-w", "-t", pane, "@runtime_status_hud", hud); err != nil {
		return hud, err
	}
	if _, err = tmuxCall(socket, "set-option", "-p", "-t", hud, "@runtime_status_kind", kind); err != nil {
		return hud, err
	}
	// Index 0 alone, so the cleanup hook of sessionSettings at index 1 stays;
	// a set-hook without an index replaces every index of the hook.
	for _, event := range []string{"client-attached", "client-resized"} {
		if _, err = tmuxCall(socket, "set-hook", "-t", session, event+"[0]", "resize-pane -t "+hud+" -y 4"); err != nil {
			return hud, err
		}
	}
	return hud, nil
}

// useAccount runs `account use`: it names the windows to relaunch and asks
// once unless yes, and moves the switch out of a window it relaunches.
func useAccount(m *logins.Manager, kind, account string, yes bool) error {
	windows := openWindows(kind)
	if len(windows.relaunch)+len(windows.orphaned) > 0 && !yes {
		if len(windows.relaunch) > 0 {
			fmt.Fprintf(os.Stderr, "These %s windows restart on the new account: %s\n", kind, describeWindows(windows.relaunch))
		}
		if len(windows.orphaned) > 0 {
			fmt.Fprintf(os.Stderr, "These %s processes lost their window and end: %s\n", kind, describeWindows(windows.orphaned))
		}
		if !confirm("Switch? [y/N] ") {
			return errors.New("switch cancelled")
		}
	}
	if windows.holdsCaller() {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		cmd := exec.Command(self, "__account-switch", kind, account)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err = cmd.Start(); err != nil {
			return err
		}
		fmt.Printf("Switching %s to %s; this window restarts on it.\n", kind, account)
		return nil
	}
	windows.readConversations()
	a, err := m.Use(kind, account, windows.forSwitch())
	if err != nil {
		return err
	}
	fmt.Printf("%s now uses %s\n", a.Runtime, a.Label())
	if len(windows.relaunch) > 0 {
		fmt.Printf("Relaunched: %s\n", describeWindows(windows.relaunch))
	}
	if len(windows.skipped) > 0 {
		fmt.Printf("Skipped, on another home: %s\n", describeWindows(windows.skipped))
	}
	if len(windows.orphaned) > 0 {
		fmt.Printf("Ended, window gone: %s\n", describeWindows(windows.orphaned))
	}
	if fresh := windows.fresh(); len(fresh) > 0 {
		fmt.Printf("Restarted without a conversation: %s\n", describeWindows(fresh))
	}
	if m.FirstUse() {
		fmt.Fprintln(os.Stderr, termsNotice)
	}
	return nil
}

func confirm(prompt string) bool {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer func() { _ = tty.Close() }()
	_, _ = fmt.Fprint(tty, prompt)
	var answer string
	_, _ = fmt.Fscanln(tty, &answer)
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
}

// accountSwitch is the switch the HUD menu and a relaunched caller start
// outside any window; its result is shown on every client of the HUD server.
func accountSwitch(kind, account string) int {
	m, err := logins.NewManager()
	message := ""
	if err == nil {
		var a logins.Account
		windows := openWindows(kind)
		windows.readConversations()
		if a, err = m.Use(kind, account, windows.forSwitch()); err == nil {
			message = fmt.Sprintf("%s now uses %s", kind, a.Label())
			if fresh := len(windows.fresh()); fresh > 0 {
				message += fmt.Sprintf("; %d window(s) restarted without a conversation", fresh)
			}
		}
	}
	if err != nil {
		message = fmt.Sprintf("%s switch failed: %v", kind, err)
	}
	notify(filepath.Join(defaultRuntimeDir(), "tmux.sock"), message)
	if err != nil {
		return 1
	}
	return 0
}

func notify(socket, message string) {
	clients, err := tmuxCall(socket, "list-clients", "-F", "#{client_name}")
	if err != nil {
		return
	}
	for _, client := range strings.Fields(clients) {
		_, _ = tmuxCall(socket, "display-message", "-d", "5000", "-c", client, strings.ReplaceAll(message, "#", "##"))
	}
}

// accountMenu opens the account menu of a HUD click at column x of the first
// row; a click past the account label does nothing.
func accountMenu(kind, x, client, pane, socket string) int {
	home, err := os.UserHomeDir()
	if err != nil {
		return 1
	}
	label := logins.Selected(home, kind)
	column := 0
	if _, err = fmt.Sscan(x, &column); err != nil || label == "" || column >= telemetry.TextWidth(label+telemetry.AccountMark) {
		return 0
	}
	entries, err := logins.Listed(home)
	if err != nil {
		notify(socket, "accounts unavailable: "+err.Error())
		return 1
	}
	self, err := os.Executable()
	if err != nil {
		return 1
	}
	self, _ = filepath.EvalSymlinks(self)
	restarts := len(openWindows(kind).relaunch)
	// "--" ends the flags, since a disabled item starts with "-".
	args := []string{"-S", socket, "display-menu", "-M", "-O", "-c", client, "-t", pane, "-x", "P", "-y", "P", "-T", menuText(fmt.Sprintf("%s accounts, %d window(s) restart", kind, restarts)), "--"}
	for _, e := range entries {
		if e.Runtime != kind {
			continue
		}
		if e.Current {
			args = append(args, "-"+menuText("* "+e.Label()), "", "")
			continue
		}
		run := shellCommand([]string{self, "__account-switch", kind, e.ID})
		args = append(args, menuText("  "+e.Label()), "", switchCommand(run))
	}
	// display-menu returns only when the menu closes, so it is left running.
	if err = exec.Command("tmux", args...).Start(); err != nil {
		return 1
	}
	return 0
}

// switchCommand is what an account item runs: the switch itself, at once.
func switchCommand(run string) string {
	return "run-shell -b " + tmuxQuote(run)
}

// menuText keeps a label literal in a tmux format.
func menuText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, strings.ReplaceAll(s, "#", "##"))
}

func tmuxQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`).Replace(s) + `"`
}
