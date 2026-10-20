package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/movingChurch/runtime-status/internal/accounts/logins"
)

const accountUsage = `Usage: runtime-status account list [--json]
       runtime-status account add codex|claude [--current]
       runtime-status account use codex|claude EMAIL|ALIAS
       runtime-status account rename codex|claude EMAIL|ALIAS NEW-ALIAS
       runtime-status account remove codex|claude EMAIL|ALIAS

` + termsNotice

const termsNotice = `Logins are copied into the macOS Keychain on this machine only. Rotating
accounts to get around usage limits can break provider terms and lead to
suspension; keep each stored login to yourself.`

// accountCommand runs the account subcommands; errors print one line.
func accountCommand(args []string) int {
	m, err := logins.NewManager()
	if err == nil {
		err = runAccount(m, args)
	}
	if err == errUsage {
		fmt.Fprintln(os.Stderr, accountUsage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime-status:", err)
		return 1
	}
	return 0
}

var errUsage = fmt.Errorf("usage")

func runAccount(m *logins.Manager, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	switch {
	case args[0] == "list" && (len(args) == 1 || len(args) == 2 && args[1] == "--json"):
		entries, err := m.List()
		if err != nil {
			return err
		}
		if len(args) == 2 {
			return json.NewEncoder(os.Stdout).Encode(entries)
		}
		for _, e := range entries {
			mark := " "
			if e.Current {
				mark = "*"
			}
			fmt.Printf("%s %-7s %s%s\n", mark, e.Runtime, e.Label(), aliasNote(e.Account))
		}
		return nil
	case args[0] == "add" && (len(args) == 2 || len(args) == 3 && args[2] == "--current"):
		if len(args) == 2 {
			fmt.Fprintln(os.Stderr, "Sign in with the account to add. The login in use stays unchanged.")
		}
		a, err := m.Add(args[1], len(args) == 3)
		if err == nil {
			fmt.Printf("Stored %s account %s\n", a.Runtime, a.Label())
			fmt.Fprintln(os.Stderr, termsNotice)
		}
		return err
	case args[0] == "use" && len(args) == 3:
		a, err := m.Use(args[1], args[2], nil)
		if err == nil {
			fmt.Printf("%s now uses %s\n", a.Runtime, a.Label())
			if m.FirstUse() {
				fmt.Fprintln(os.Stderr, termsNotice)
			}
		}
		return err
	case args[0] == "rename" && len(args) == 4:
		return m.Rename(args[1], args[2], args[3])
	case args[0] == "remove" && len(args) == 3:
		return m.Remove(args[1], args[2])
	}
	return errUsage
}

func aliasNote(a logins.Account) string {
	if a.Alias != "" && a.Email != "" && !strings.EqualFold(a.Alias, a.Email) {
		return " (" + a.Email + ")"
	}
	return ""
}
