package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/config"
	"github.com/mmedum/google-sheets-mcp/internal/redact"
	"github.com/mmedum/google-sheets-mcp/internal/userconfig"
	"github.com/mmedum/google-sheets-mcp/internal/version"
)

// statusSchemaVersion is the version of the JSON object `status --json`
// prints. A caller may branch on it; it changes only when a field is
// removed or its meaning changes, never when one is added.
const statusSchemaVersion = 1

// statusReport is everything `status` knows, collected once and rendered
// either as the lines a person reads or as the object a script parses.
//
// One collector, two renderers, because the alternative drifts. The text
// answers "is this configured" with a label, and a label is free to be
// reworded in any release; the object is the part promised not to move.
//
// Nothing here contacts Google, and nothing here resolves a token: it
// reports what the profile recorded, which is what this command has
// always reported. `doctor` is the one that asks Google.
type statusReport struct {
	SchemaVersion int     `json:"schema_version"`
	Binary        string  `json:"binary"`
	Version       string  `json:"version"`
	Profile       string  `json:"profile"`
	ConfigDir     *string `json:"config_dir"`
	// Account is masked to the domain, and masked HERE: the encoder
	// writes straight to the stream, so a collector keeping the full
	// address would put it on stdout. The domain is the half a diagnosis
	// uses; the local part is never an input to any command.
	Account     *string           `json:"account"`
	Credentials statusCredentials `json:"credentials"`
	Scopes      statusScopes      `json:"scopes"`
	Settings    statusSettings    `json:"settings"`
}

type statusCredentials struct {
	// Configured is the field worth branching on: false means no profile
	// has been written and every tool will refuse until `login` runs.
	// It is always present, so an unconfigured object is distinguishable
	// from a truncated or unparseable one.
	Configured bool `json:"configured"`
	// TokenStore is what the profile recorded — not a live lookup.
	TokenStore *string `json:"token_store"`
	// Reason says why nothing is configured, and is null when it is.
	Reason *string `json:"reason"`
	// ClientSecretPath keeps the OAuth client id masked: it arrives
	// through the path, because the Cloud console names the file after
	// it.
	ClientSecretPath *string `json:"client_secret_path"`
}

type statusScopes struct {
	Granted []string `json:"granted"`
}

type statusSettings struct {
	ReadOnly    bool `json:"read_only"`
	Destructive bool `json:"destructive"`
}

// newStatusReport collects the state. The error it returns is a real
// failure to read the profile, not an unconfigured one: not being
// configured is a state this reports, not a reason to stop.
func newStatusReport(cfg config.Config) (statusReport, error) {
	r := statusReport{
		SchemaVersion: statusSchemaVersion,
		Binary:        "google-sheets-mcp",
		Version:       version.String(),
		Profile:       cfg.Profile,
		Scopes:        statusScopes{Granted: []string{}},
		Settings: statusSettings{
			ReadOnly:    cfg.ReadOnly,
			Destructive: cfg.EnableDestructive,
		},
	}
	if dir, err := userconfig.ProfileDir(cfg.Profile); err == nil {
		r.ConfigDir = orNil(dir)
	}

	uc, err := userconfig.Load(cfg.Profile)
	switch {
	case errors.Is(err, userconfig.ErrNotFound):
		r.Credentials.Reason = orNil("not configured: run `google-sheets-mcp login`")
		return r, nil
	case err != nil:
		return r, err
	}
	r.Credentials.Configured = true
	r.Account = orNil(redact.Account(uc.AccountEmail))
	r.Credentials.TokenStore = orNil(uc.TokenStore)
	r.Credentials.ClientSecretPath = orNil(redact.Path(uc.ClientSecretPath))
	r.Scopes.Granted = orEmpty(uc.Scopes)
	return r, nil
}

// writeText writes the same lines, in the same order, that `status` has
// always printed.
func (r statusReport) writeText(w io.Writer) {
	_, _ = fmt.Fprintf(w, "%s\nprofile:        %s\n", version.Info(), r.Profile)
	if r.ConfigDir != nil {
		_, _ = fmt.Fprintf(w, "config dir:     %s\n", *r.ConfigDir)
	}
	if !r.Credentials.Configured {
		_, _ = fmt.Fprintln(w, "not configured: run `google-sheets-mcp login`")
	} else {
		_, _ = fmt.Fprintf(w, "account:        %s\nclient secret:  %s\ntoken store:    %s\nscopes:         %s\n",
			orElse(deref(r.Account), "(not recorded)"), orNone(deref(r.Credentials.ClientSecretPath)),
			orNone(deref(r.Credentials.TokenStore)), orNone(strings.Join(r.Scopes.Granted, " ")))
	}
	_, _ = fmt.Fprintf(w, "read-only:      %v\ndestructive:    %v\n", r.Settings.ReadOnly, r.Settings.Destructive)
}

// writeJSON writes the object, indented and newline-terminated, so the
// whole of stdout is one JSON value.
func (r statusReport) writeJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Paths are not HTML, and an escaped ampersand is a path a caller
	// cannot compare against its own.
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

// orNil turns an unset string into the JSON null that says so: an empty
// string is a value, and a caller cannot tell a value it does not
// recognise from one that is not there.
func orNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// orEmpty keeps a list a list; a nil slice marshals as null.
func orEmpty(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
