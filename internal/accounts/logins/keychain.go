// Package logins keeps several native logins per runtime and swaps the one a
// runtime's single home uses; secrets stay in the macOS Keychain.
package logins

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// VaultService names the Keychain items holding stored logins.
const VaultService = "runtime-status accounts"

// ErrNotFound reports a missing Keychain item.
var ErrNotFound = errors.New("keychain item not found")

// Secrets reads and writes generic Keychain items.
type Secrets interface {
	Get(service, account string) ([]byte, error)
	// Set updates an existing item in place, so its access list stays.
	Set(service, account string, data []byte) error
	Delete(service, account string) error
}

// Keychain uses the login Keychain through /usr/bin/security; secrets travel
// on standard input and output only, never on a command line.
type Keychain struct{}

func guard() {
	if testing.Testing() {
		panic("tests must not touch the login Keychain")
	}
}

func (Keychain) Get(service, account string) ([]byte, error) {
	guard()
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", service, "-a", account, "-w").Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 44 {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("keychain read of %s refused or failed", service)
	}
	return bytes.TrimSuffix(out, []byte("\n")), nil
}

func (Keychain) Set(service, account string, data []byte) error {
	guard()
	if strings.ContainsAny(service+account, "\"\n\\") {
		return errors.New("unsafe keychain name")
	}
	cmd := exec.Command("/usr/bin/security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s \"%s\" -a \"%s\" -X %s\n", service, account, hex.EncodeToString(data)))
	out, err := cmd.CombinedOutput()
	if err != nil || bytes.Contains(out, []byte("error")) {
		return fmt.Errorf("keychain write of %s refused or failed", service)
	}
	return nil
}

func (Keychain) Delete(service, account string) error {
	guard()
	err := exec.Command("/usr/bin/security", "delete-generic-password", "-s", service, "-a", account).Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 44 {
		return nil
	}
	return err
}
