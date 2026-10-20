package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// process stops, at most two seconds per window.
func (w *switchWindows) readConversations() {
	w.conversations = map[string]string{}
	for _, meta := range w.relaunch {
		if id := conversationOf(w.runtimeDir, meta, time.Now().Add(2*time.Second)); id != "" {
			w.conversations[stringOf(meta["id"])] = id
		}
	}
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

func openWindows(kind string) switchWindows {
	w := switchWindows{runtimeDir: filepath.Join("/tmp", fmt.Sprintf("runtime-status-%d", os.Getuid()))}
	home, err := os.UserHomeDir()
	if err != nil {
		return w
	}
	var own map[string]any
	for _, meta := range engine.ListMetadata(w.runtimeDir) {
		switch {
		case meta["runtime"] != kind:
		case !logins.OnLiveHome(home, kind, stringOf(meta["home"])):
			w.skipped = append(w.skipped, meta)
		case !paneAlive(meta):
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

func paneAlive(meta map[string]any) bool {
	pane := stringOf(meta["codex_pane"])
	if !panePattern.MatchString(pane) {
		return false
	}
	id, err := tmuxCall(stringOf(meta["tmux_socket"]), "display-message", "-p", "-t", pane, "#{pane_id}")
	return err == nil && id == pane
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
	return &logins.Windows{Runs: runs, Stop: func(string) error {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		self, _ = filepath.EvalSymlinks(self)
		for _, meta := range w.relaunch {
			if err = relaunchWindow(self, w.runtimeDir, meta, w.conversations[stringOf(meta["id"])]); err != nil {
				return fmt.Errorf("relaunch of %s: %w", describeWindow(meta), err)
			}
		}
		return nil
	}}
}

// relaunchWindow replaces a window's runner and HUD with new ones under a new
// run id, in the same pane and directory and with the same environment; the
// new runner starts its CLI once the switch ends, so it runs on the new login.
// The conversation is not carried over.
func relaunchWindow(self, runtimeDir string, meta map[string]any, conversation string) error {
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
		args = resumeArgs(args, valueFlags(nativeHelp(launcher)))
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
	for _, event := range []string{"client-attached", "client-resized"} {
		if _, err = tmuxCall(socket, "set-hook", "-t", session, event, "resize-pane -t "+hud+" -y 4"); err != nil {
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
			fmt.Fprintf(os.Stderr, "These %s windows restart on the new account, without their conversation: %s\n", kind, describeWindows(windows.relaunch))
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
	notify(filepath.Join("/tmp", fmt.Sprintf("runtime-status-%d", os.Getuid()), "tmux.sock"), message)
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
	m, err := logins.NewManager()
	if err != nil {
		return 1
	}
	entries, err := m.List()
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
	args := []string{"-S", socket, "display-menu", "-M", "-O", "-c", client, "-t", pane, "-x", "P", "-y", "P", "-T", menuText(kind + " accounts"), "--"}
	for _, e := range entries {
		if e.Runtime != kind {
			continue
		}
		if e.Current {
			args = append(args, "-"+menuText("* "+e.Label()), "", "")
			continue
		}
		prompt := menuText(fmt.Sprintf("Switch %s to %s? %d window(s) restart", kind, e.Label(), restarts))
		run := shellCommand([]string{self, "__account-switch", kind, e.ID})
		args = append(args, menuText("  "+e.Label()), "", confirmMenu(prompt, run))
	}
	// display-menu returns only when the menu closes, so it is left running.
	if err = exec.Command("tmux", args...).Start(); err != nil {
		return 1
	}
	return 0
}

// confirmMenu is the second menu an account item opens, Switch or Cancel,
// so the mouse finishes the switch.
func confirmMenu(prompt, run string) string {
	return "display-menu -M -O -x P -y P -T " + tmuxQuote(prompt) + " -- " + tmuxQuote("Switch") + ` "" ` + tmuxQuote("run-shell -b "+tmuxQuote(run)) + " " + tmuxQuote("Cancel") + ` "" ""`
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
