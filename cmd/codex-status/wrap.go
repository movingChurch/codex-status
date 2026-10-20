package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// Pass-through rules per runtime: informational, non-interactive and management
// calls run the native CLI unchanged; everything else opens inside the HUD.
var passthroughFlags = map[string][]string{
	"claude":      {"-p", "--print", "-h", "--help", "-v", "--version", "--output-format", "--input-format"},
	"antigravity": {"-p", "--print", "--prompt", "-h", "--help", "-v", "--version", "--output-format", "--input-format"},
}
var passthroughCommands = map[string][]string{
	"claude":      {"auth", "mcp", "plugin", "plugins", "install", "update", "upgrade", "doctor", "setup-token", "agents", "help", "remote-control"},
	"antigravity": {"agent", "agents", "changelog", "help", "install", "mcp", "mic-serve", "models", "plugin", "plugins", "remote-control", "update"},
}
var nativeNames = map[string]string{"codex": "codex", "claude": "claude", "antigravity": "agy"}

func passthrough(kind string, args []string) bool {
	for _, arg := range args {
		for _, flag := range passthroughFlags[kind] {
			// Claude also accepts attached short print prompts such as -pquery.
			if arg == flag || strings.HasPrefix(arg, flag+"=") || (kind == "claude" && flag == "-p" && strings.HasPrefix(arg, "-p") && !strings.HasPrefix(arg, "--")) {
				return true
			}
		}
	}
	if len(args) > 0 {
		for _, command := range passthroughCommands[kind] {
			if args[0] == command {
				return true
			}
		}
	}
	return false
}

// wrapRuntime is the shell integration entry for one interactive native launch.
func wrapRuntime(kind string, args []string) int {
	if kind == "codex" {
		return wrap(args)
	}
	name, ok := nativeNames[kind]
	if !ok {
		return 2
	}
	native, err := runtimeExecutable(kind)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtime-status: %s is not installed or not on PATH\n", name)
		return 127
	}
	if !passthrough(kind, args) {
		if kind == "antigravity" && !antigravityReady() {
			fmt.Fprintln(os.Stderr, "runtime-status: run `runtime-status install` to enable the Antigravity HUD; starting agy without it")
		} else {
			return launchRuntime(kind, "", args)
		}
	}
	if syscall.Exec(native, append([]string{native}, args...), os.Environ()) != nil {
		return 2
	}
	return 0
}
