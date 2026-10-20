// Package logins keeps several native logins per runtime and swaps the one a
// runtime's single home uses; secrets stay in the macOS Keychain.
package logins

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
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

// One `security -i` line holds at most 4096 bytes, so a vault login longer
// than chunkSize is kept in chunk items named `<account>#<generation>.<n>` and the
// item itself holds only `chunks:<generation>:<count>`. A new generation is
// written before the index moves to it, so a crash never mixes two logins.
const (
	chunkSize   = 1800
	chunkPrefix = "chunks:"
)

func (k Keychain) Get(service, account string) ([]byte, error) {
	data, err := k.get(service, account)
	if err != nil || !bytes.HasPrefix(data, []byte(chunkPrefix)) {
		return data, err
	}
	gen, count, ok := parseIndex(data)
	if !ok || service != VaultService {
		return nil, fmt.Errorf("keychain item of %s is damaged", service)
	}
	var whole []byte
	for i := 0; i < count; i++ {
		part, err := k.get(service, chunkName(account, gen, i))
		if err != nil {
			return nil, fmt.Errorf("keychain item of %s is damaged", service)
		}
		whole = append(whole, part...)
	}
	return whole, nil
}

func (k Keychain) Set(service, account string, data []byte) error {
	if service != VaultService {
		return k.set(service, account, data)
	}
	old, _ := k.get(service, account)
	if len(data) <= chunkSize && !bytes.HasPrefix(data, []byte(chunkPrefix)) {
		if err := k.set(service, account, data); err != nil {
			return err
		}
		k.deleteChunks(service, account, old)
		return nil
	}
	gen := make([]byte, 4)
	if _, err := rand.Read(gen); err != nil {
		return err
	}
	g := hex.EncodeToString(gen)
	count := 0
	for start := 0; start < len(data); start += chunkSize {
		end := min(start+chunkSize, len(data))
		if err := k.set(service, chunkName(account, g, count), data[start:end]); err != nil {
			k.deleteChunks(service, account, []byte(fmt.Sprintf("%s%s:%d", chunkPrefix, g, count)))
			return err
		}
		count++
	}
	if err := k.set(service, account, []byte(fmt.Sprintf("%s%s:%d", chunkPrefix, g, count))); err != nil {
		return err
	}
	k.deleteChunks(service, account, old)
	return nil
}

func (k Keychain) Delete(service, account string) error {
	old, _ := k.get(service, account)
	if err := k.delete(service, account); err != nil {
		return err
	}
	k.deleteChunks(service, account, old)
	return nil
}

func chunkName(account, gen string, i int) string {
	return fmt.Sprintf("%s#%s.%d", account, gen, i)
}

func parseIndex(data []byte) (string, int, bool) {
	gen, n, ok := strings.Cut(strings.TrimPrefix(string(data), chunkPrefix), ":")
	count, err := strconv.Atoi(n)
	return gen, count, ok && err == nil && count > 0
}

// deleteChunks removes the chunk items an earlier index named.
func (k Keychain) deleteChunks(service, account string, index []byte) {
	if gen, count, ok := parseIndex(index); ok && bytes.HasPrefix(index, []byte(chunkPrefix)) {
		for i := 0; i < count; i++ {
			_ = k.delete(service, chunkName(account, gen, i))
		}
	}
}

func (Keychain) get(service, account string) ([]byte, error) {
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

func (Keychain) set(service, account string, data []byte) error {
	guard()
	if strings.ContainsAny(service+account, "\"\n\\") {
		return errors.New("unsafe keychain name")
	}
	cmd := exec.Command("/usr/bin/security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s \"%s\" -a \"%s\" -X %s\n", service, account, hex.EncodeToString(data)))
	out, err := cmd.CombinedOutput()
	if err != nil || bytes.Contains(out, []byte("error")) || bytes.Contains(out, []byte("unknown command")) {
		return fmt.Errorf("keychain write of %s refused or failed", service)
	}
	return nil
}

func (Keychain) delete(service, account string) error {
	guard()
	err := exec.Command("/usr/bin/security", "delete-generic-password", "-s", service, "-a", account).Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 44 {
		return nil
	}
	return err
}
