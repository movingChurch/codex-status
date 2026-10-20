package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/movingChurch/runtime-status/internal/engine"
)

// A window relaunched by a switch resumes the conversation it was running:
// its ID is read from the manager before any process stops, and used only when
// the runtime has that conversation stored, since a resume of an unknown ID
// ends the CLI at once.

// conversationOf returns the ID a relaunch of meta resumes, or empty.
func conversationOf(runtimeDir string, meta map[string]any, deadline time.Time) string {
	id := strings.ToLower(engine.SessionID(runtimeDir, stringOf(meta["id"]), deadline))
	if !uuidPattern.MatchString(id) || !conversationStored(stringOf(meta["runtime"]), stringOf(meta["home"]), stringOf(meta["cwd"]), id) {
		return ""
	}
	return id
}

var projectDirChars = regexp.MustCompile(`[^A-Za-z0-9-]`)

// conversationStored checks the runtime's own transcript of id: Claude Code
// keeps `projects/<cwd with other characters as ->/<id>.jsonl` under its config
// dir, Codex `sessions/YYYY/MM/DD/rollout-…-<id>.jsonl` under its home.
func conversationStored(kind, home, cwd, id string) bool {
	switch kind {
	case "claude":
		_, err := os.Stat(filepath.Join(home, "projects", projectDirChars.ReplaceAllString(cwd, "-"), id+".jsonl"))
		return err == nil
	case "codex":
		matches, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl"))
		return len(matches) > 0
	}
	return false
}

// resumeDrops are Claude Code's options that pick a conversation; a resumed
// relaunch replaces them, with any value they carry.
var resumeDrops = map[string]bool{"--resume": true, "-r": true, "--continue": true, "-c": true, "--session-id": true, "--fork-session": true, "--from-pr": true, "--teleport": true}

var helpValueFlag = regexp.MustCompile(`(?m)^\s+(?:(-[A-Za-z]),\s+)?(--[A-Za-z0-9-]+)[ =]<`)

// valueFlags lists the options the CLI's --help shows taking a value.
func valueFlags(help string) map[string]bool {
	flags := map[string]bool{}
	for _, m := range helpValueFlag.FindAllStringSubmatch(help, -1) {
		flags[m[2]] = true
		if m[1] != "" {
			flags[m[1]] = true
		}
	}
	return flags
}

// resumeArgs keeps a window's options with their values and drops its
// positional words, such as a first prompt, and the conversation options of
// resumeDrops, so the resumed conversation gets no prompt twice.
func resumeArgs(args []string, values map[string]bool) []string {
	kept := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		name, _, inline := strings.Cut(arg, "=")
		takes := !inline && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && (values[name] || resumeDrops[name] && name != "--continue" && name != "-c" && name != "--fork-session")
		if !resumeDrops[name] {
			kept = append(kept, arg)
			if takes {
				kept = append(kept, args[i+1])
			}
		}
		if takes {
			i++
		}
	}
	return kept
}

func nativeHelp(launcher string) string {
	out, _ := exec.Command(launcher, "--help").Output()
	return string(out)
}
