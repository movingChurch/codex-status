package logins

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Login is one runtime's credentials with the identity they belong to.
type Login struct {
	ID    string
	Email string
	Data  []byte
}

// runtime reads and writes the single live login of one runtime's home.
type runtime interface {
	// check refuses a store layout this package does not swap.
	check() error
	live() (Login, error)
	write(data []byte) error
	parse(data []byte) (Login, error)
}

var errNoLogin = errors.New("no login in use")

type codex struct {
	home string
	env  func(string) string
}

func (c codex) dir() string { return filepath.Join(c.home, ".codex") }

var storeSetting = regexp.MustCompile(`(?m)^\s*cli_auth_credentials_store\s*=\s*"([a-z]+)"`)

func (c codex) check() error {
	if v := c.env("CODEX_HOME"); v != "" && filepath.Clean(v) != c.dir() {
		return fmt.Errorf("CODEX_HOME is set to %s; switching covers only %s", v, c.dir())
	}
	if raw, err := os.ReadFile(filepath.Join(c.dir(), "config.toml")); err == nil {
		if m := storeSetting.FindSubmatch(raw); m != nil && string(m[1]) != "file" {
			return fmt.Errorf("cli_auth_credentials_store = %q; switching needs the file store", m[1])
		}
	}
	return nil
}
func (c codex) live() (Login, error) {
	raw, err := os.ReadFile(filepath.Join(c.dir(), "auth.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Login{}, errNoLogin
	}
	if err != nil {
		return Login{}, err
	}
	return c.parse(raw)
}
func (c codex) write(data []byte) error {
	return atomicWrite(filepath.Join(c.dir(), "auth.json"), data, 0600)
}

// parse takes the ChatGPT account id and the email claim of the id token.
func (codex) parse(data []byte) (Login, error) {
	var auth struct {
		Tokens struct {
			AccountID string `json:"account_id"`
			IDToken   string `json:"id_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &auth); err != nil || auth.Tokens.AccountID == "" {
		return Login{}, errors.New("codex login has no ChatGPT account")
	}
	login := Login{ID: auth.Tokens.AccountID, Data: data}
	if parts := strings.Split(auth.Tokens.IDToken, "."); len(parts) == 3 {
		if payload, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			var claims struct {
				Email string `json:"email"`
			}
			if json.Unmarshal(payload, &claims) == nil {
				login.Email = claims.Email
			}
		}
	}
	return login, nil
}

// Claude Code keeps its OAuth tokens in a Keychain item and the account
// profile in ~/.claude.json; only those two parts are swapped, so MCP tokens
// and settings stay.
type claude struct {
	home    string
	user    string
	env     func(string) string
	secrets Secrets
}

const claudeService = "Claude Code-credentials"

// scopedService is the item name Claude Code derives from its config dir.
func scopedService(dir string) string {
	return claudeService + "-" + sha8(dir)
}
func (c claude) dir() string { return filepath.Join(c.home, ".claude") }
func (c claude) profile() string {
	return filepath.Join(c.home, ".claude.json")
}

func (c claude) check() error {
	if v := c.env("CLAUDE_CONFIG_DIR"); v != "" && filepath.Clean(v) != c.dir() {
		return fmt.Errorf("CLAUDE_CONFIG_DIR is set to %s; switching covers only %s", v, c.dir())
	}
	return nil
}

// fallback is the file Claude Code reads when the Keychain has no login.
func (c claude) fallback() string { return filepath.Join(c.dir(), ".credentials.json") }

type claudeLogin struct {
	OAuth   json.RawMessage `json:"oauth"`
	Account json.RawMessage `json:"account"`
}

func (c claude) live() (Login, error) {
	item, err := c.secrets.Get(claudeService, c.user)
	if errors.Is(err, ErrNotFound) {
		return Login{}, errNoLogin
	}
	if err != nil {
		return Login{}, err
	}
	var creds map[string]json.RawMessage
	if json.Unmarshal(item, &creds) != nil || creds["claudeAiOauth"] == nil {
		return Login{}, errNoLogin
	}
	profile, err := readObject(c.profile())
	if err != nil {
		return Login{}, err
	}
	data, _ := json.Marshal(claudeLogin{OAuth: creds["claudeAiOauth"], Account: profile["oauthAccount"]})
	return c.parse(data)
}

// write replaces claudeAiOauth in every Claude item that exists for the
// default config dir and in the fallback file when it exists, then
// oauthAccount in ~/.claude.json.
func (c claude) write(data []byte) error {
	var next claudeLogin
	if err := json.Unmarshal(data, &next); err != nil || next.OAuth == nil {
		return errors.New("stored claude login is unreadable")
	}
	for i, service := range []string{claudeService, scopedService(c.dir())} {
		item, err := c.secrets.Get(service, c.user)
		if errors.Is(err, ErrNotFound) && i > 0 {
			continue
		}
		creds := map[string]json.RawMessage{}
		if err == nil {
			_ = json.Unmarshal(item, &creds)
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		creds["claudeAiOauth"] = next.OAuth
		raw, _ := json.Marshal(creds)
		if err = c.secrets.Set(service, c.user, raw); err != nil {
			return err
		}
	}
	if _, err := os.Stat(c.fallback()); err == nil {
		creds, err := readObject(c.fallback())
		if err != nil {
			return err
		}
		creds["claudeAiOauth"] = next.OAuth
		raw, _ := json.Marshal(creds)
		if err = atomicWrite(c.fallback(), raw, 0600); err != nil {
			return err
		}
	}
	profile, err := readObject(c.profile())
	if err != nil {
		return err
	}
	profile["oauthAccount"] = next.Account
	raw, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(c.profile(), raw, 0600)
}

func (claude) parse(data []byte) (Login, error) {
	var stored claudeLogin
	if err := json.Unmarshal(data, &stored); err != nil {
		return Login{}, err
	}
	var account struct {
		AccountUUID      string `json:"accountUuid"`
		OrganizationUUID string `json:"organizationUuid"`
		Email            string `json:"emailAddress"`
	}
	if json.Unmarshal(stored.Account, &account) != nil || account.AccountUUID == "" {
		return Login{}, errors.New("claude login has no account profile")
	}
	id := account.AccountUUID
	if account.OrganizationUUID != "" {
		id += "/" + account.OrganizationUUID
	}
	return Login{ID: id, Email: account.Email, Data: data}, nil
}

func readObject(path string) (map[string]json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	object := map[string]json.RawMessage{}
	if err = json.Unmarshal(bytes.TrimSpace(raw), &object); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object", path)
	}
	return object, nil
}
