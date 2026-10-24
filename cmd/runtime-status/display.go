package main

// These settings belong only to this launcher's dedicated tmux server.

// sessionSettings end a HUD session once its last client goes away, so a
// closed terminal window leaves no session, runner or server behind. A
// client-attached hook sets the option: tmux ends every unattached session
// that has it whenever any client detaches, which would otherwise take a new
// session in the gap before the launcher attaches. The hook sits at index 1,
// since splitHUD sets index 0 of client-attached to resize the HUD pane.
func sessionSettings(session string) [][]string {
	return [][]string{
		{"set-hook", "-t", session, "client-attached[1]", "set-option -t " + tmuxQuote(session) + " destroy-unattached on"},
	}
}

// displaySettings make a click on the first row of a HUD pane open the
// account menu of its runtime; other clicks keep tmux's default.
func displaySettings(self string) [][]string {
	onLabel := "#{&&:#{@runtime_status_kind},#{==:#{mouse_y},0}}"
	menu := shellCommand([]string{self, "__account-menu"}) + " '#{@runtime_status_kind}' '#{mouse_x}' '#{client_name}' '#{pane_id}' '#{socket_path}'"
	return [][]string{
		{"set-option", "-g", "mouse", "on"},
		{"set-option", "-g", "status", "off"},
		{"set-option", "-g", "history-limit", "100000"},
		{"set-option", "-as", "terminal-features", ",*:hyperlinks"},
		{"bind-key", "-T", "root", "WheelUpPane", "if-shell", "-F", "-t", "=", "#{pane_in_mode}", "send-keys -M", "copy-mode -e -t ="},
		// The menu opens on release, so no release closes it as it appears.
		{"bind-key", "-T", "root", "MouseDown1Pane", "if-shell", "-F", "-t", "=", onLabel, "", "select-pane -t = ; send-keys -M"},
		{"bind-key", "-T", "root", "MouseUp1Pane", "if-shell", "-F", "-t", "=", onLabel, "run-shell -b " + tmuxQuote(menu), "send-keys -M"},
		{"bind-key", "-T", "root", "WheelDownPane", "if-shell", "-F", "-t", "=", "#{pane_in_mode}", "send-keys -M", ""},
	}
}

// claudeHUDSetting turns the HUD of Claude Code windows off with "off"; any
// other value, or none, keeps it.
const claudeHUDSetting = "RUNTIME_STATUS_CLAUDE_HUD"

// hudShown reports whether a window of kind draws HUD rows. A Claude Code
// window without them still runs in its tmux session, keeps its run
// metadata for account switches, and leaves Claude Code its own statusLine.
func hudShown(kind string, getenv func(string) string) bool {
	return kind != "claude" || getenv(claudeHUDSetting) != "off"
}

// prepareWindow applies the launcher's tmux settings to a new session and
// splits the HUD under pane when shown; it returns the HUD pane or "".
func prepareWindow(socket, session, pane, self, runtimeDir, id, kind string, shown bool) (string, error) {
	for _, settings := range append(displaySettings(self), sessionSettings(session)...) {
		if _, err := tmuxCall(socket, settings...); err != nil {
			return "", err
		}
	}
	if !shown {
		return "", nil
	}
	return splitHUD(socket, session, pane, self, runtimeDir, id, kind)
}
