package main

// These settings belong only to this launcher's dedicated tmux server.
func displaySettings() [][]string {
	return [][]string{
		{"set-option", "-g", "mouse", "on"},
		{"set-option", "-g", "status", "off"},
		{"set-option", "-g", "history-limit", "100000"},
		{"set-option", "-as", "terminal-features", ",*:hyperlinks"},
		{"bind-key", "-T", "root", "WheelUpPane", "if-shell", "-F", "-t", "=", "#{pane_in_mode}", "send-keys -M", "copy-mode -e -t ="},
		{"bind-key", "-T", "root", "WheelDownPane", "if-shell", "-F", "-t", "=", "#{pane_in_mode}", "send-keys -M", ""},
	}
}

// Arm cleanup only after the first attachment; setup starts without a client.
func attachmentHook(event, hud string) string {
	resize := "resize-pane -t " + hud + " -y 4"
	if event == "client-attached" {
		return "set-option -t codex destroy-unattached on; " + resize
	}
	return resize
}
