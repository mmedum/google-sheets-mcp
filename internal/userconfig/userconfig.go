// Package userconfig stores non-secret, per-profile settings that
// survive between runs: where the OAuth client JSON lives, which account
// was logged in, where the refresh token ended up, and which scopes were
// granted. It lives at os.UserConfigDir()/google-sheets-mcp, overridden
// by GSHEETS_CONFIG_DIR; a non-default profile lives under
// profiles/<name>/ below that.
//
// Nothing secret is written here. The refresh token is
// internal/credentials' business.
package userconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AppDir is the directory name under the user's config directory.
const AppDir = "google-sheets-mcp"

// EnvDir overrides the base directory.
const EnvDir = "GSHEETS_CONFIG_DIR"

// DefaultProfile is the profile used when none is named.
const DefaultProfile = "default"

// ErrNotFound means this profile has no config file yet.
var ErrNotFound = errors.New("userconfig: no config file for this profile; run `google-sheets-mcp login`")

// Config is the stored, non-secret profile state.
type Config struct {
	ClientSecretPath string    `json:"client_secret_path,omitempty"`
	AccountEmail     string    `json:"account_email,omitempty"`
	TokenStore       string    `json:"token_store,omitempty"`
	Scopes           []string  `json:"scopes,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// BaseDir returns the application config directory.
func BaseDir() (string, error) {
	if v := os.Getenv(EnvDir); v != "" {
		return v, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("userconfig: locate user config dir: %w", err)
	}
	return filepath.Join(d, AppDir), nil
}

// ProfileDir returns the directory holding one profile's files.
func ProfileDir(profile string) (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	if profile == "" || profile == DefaultProfile {
		return base, nil
	}
	return filepath.Join(base, "profiles", profile), nil
}

// profileFile names one file inside a profile's directory.
func profileFile(profile, name string) (string, error) {
	dir, err := ProfileDir(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// Path returns the config file path for the profile.
func Path(profile string) (string, error) { return profileFile(profile, "config.json") }

// DefaultClientSecretPath is where login looks for the OAuth client JSON
// when no path is given.
func DefaultClientSecretPath(profile string) (string, error) {
	return profileFile(profile, "client_secret.json")
}

// TokenFilePath is the plaintext fallback location for the refresh token.
func TokenFilePath(profile string) (string, error) { return profileFile(profile, "token.json") }

// ResolveClientSecretPath picks the OAuth client JSON: the first
// non-empty override, then whatever the profile remembers, then the
// default location.
//
// One answer, because the chain used to be written out at every call
// site and the copies had already drifted — two of them trimmed
// whitespace and one did not, so a stored path of " " was skipped at
// login and used when serving.
func ResolveClientSecretPath(profile string, overrides ...string) (string, error) {
	for _, v := range overrides {
		if v = strings.TrimSpace(v); v != "" {
			return v, nil
		}
	}
	c, err := Load(profile)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", err
	}
	if v := strings.TrimSpace(c.ClientSecretPath); v != "" {
		return v, nil
	}
	return DefaultClientSecretPath(profile)
}

// Load reads the profile's config. ErrNotFound if absent.
func Load(profile string) (Config, error) {
	p, err := Path(profile)
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrNotFound
	}
	if err != nil {
		return Config{}, fmt.Errorf("userconfig: read %s: %w", p, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("userconfig: parse %s: %w", p, err)
	}
	return c, nil
}

// Save writes the profile's config with owner-only permissions.
func Save(profile string, c Config) error {
	p, err := Path(profile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("userconfig: create %s: %w", filepath.Dir(p), err)
	}
	c.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("userconfig: encode: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("userconfig: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("userconfig: replace %s: %w", p, err)
	}
	return nil
}

// Profiles lists every configured profile name, the default included.
//
// It exists so `logout` can say what it is really about to do: Google
// revokes the *grant*, not one token, so revoking in one profile signs
// the account out of every profile sharing that OAuth client. Measured,
// not assumed — a live run signed out of a second profile that nothing
// had touched.
func Profiles() ([]string, error) {
	base, err := BaseDir()
	if err != nil {
		return nil, err
	}
	var out []string
	if _, err := os.Stat(filepath.Join(base, "config.json")); err == nil {
		out = append(out, DefaultProfile)
	}
	entries, err := os.ReadDir(filepath.Join(base, "profiles"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("userconfig: list profiles: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(base, "profiles", e.Name(), "config.json")); err == nil {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// SharingClient returns the other configured profiles using the same
// OAuth client as this one, which are the profiles a revocation here
// will also sign out.
func SharingClient(profile string) ([]string, error) {
	mine, err := Load(profile)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(mine.ClientSecretPath) == "" {
		return nil, nil
	}
	names, err := Profiles()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range names {
		if name == profile {
			continue
		}
		other, err := Load(name)
		if err != nil {
			continue
		}
		if other.ClientSecretPath == mine.ClientSecretPath {
			out = append(out, name)
		}
	}
	return out, nil
}

// Remove deletes the profile's config file. A missing file is fine.
func Remove(profile string) error {
	p, err := Path(profile)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("userconfig: remove %s: %w", p, err)
	}
	return nil
}
