package main

// These settings belong only to this launcher's dedicated tmux server.

// sessionSettings end a HUD session once its last client goes away, so a
// closed terminal window leaves no session, runner or server behind. A
// client-attached hook sets the option: tmux ends every unattached session
// that has it whenever any client detaches, which would otherwise take a new
// session in the gap before the launcher attaches.
func sessionSettings(session string) [][]string {
	return [][]string{
		{"set-hook", "-t", session, "client-attached", "set-option -t " + tmuxQuote(session) + " destroy-unattached on"},
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
