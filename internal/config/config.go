// Package config loads and validates runtime configuration.
//
// Environment variables (GSHEETS_*) are the source of truth because
// every MCP client that matters passes only command, args and env to a
// stdio server. Each setting also has a flag bound to the same name; a
// flag given explicitly overrides the environment. Validation runs once
// at start so a misconfigured server fails before it announces itself.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// EnvPrefix is prepended to every environment variable name.
const EnvPrefix = "GSHEETS_"

// LogLevel is a typed enum constrained at load time.
type LogLevel string

// Allowed LogLevel values.
const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// Slog returns the slog.Level for this level.
func (l LogLevel) Slog() slog.Level {
	switch l {
	case LogDebug:
		return slog.LevelDebug
	case LogWarn:
		return slog.LevelWarn
	case LogError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LogFormat is a typed enum constrained at load time.
type LogFormat string

// Allowed LogFormat values.
const (
	LogText LogFormat = "text"
	LogJSON LogFormat = "json"
)

// Read budgets. The defaults keep a read well inside a client's result
// limit; the maxima keep it inside the process.
const (
	DefaultMaxCells = 5000
	MaxMaxCells     = 50000
	DefaultMaxChars = 20000
	MaxMaxChars     = 400000
)

// Config is the validated runtime configuration.
type Config struct {
	Profile           string
	LogLevel          LogLevel
	LogFormat         LogFormat
	ReadOnly          bool
	EnableDestructive bool
	// EnableDataSources registers manage_data_source and adds the
	// bigquery.readonly scope to what login asks for. Off by default:
	// Connected Sheets cannot be reached without that scope (§17.6a),
	// and a Sheets server whose login screen asks for BigQuery is asking
	// most of its users to grant access to a product they do not have.
	EnableDataSources bool
	// AllowExternalFormulas lets a write create a formula that reaches
	// outside the spreadsheet: IMPORTXML, IMPORTDATA, IMPORTHTML,
	// IMPORTFEED, IMAGE and HYPERLINK fetch a URL from Google's servers,
	// and IMPORTRANGE pulls in another spreadsheet. Off by default, and
	// the operator's to set rather than an argument: a model persuaded to
	// write one would set the argument too (§7.3).
	AllowExternalFormulas bool
	MaxCells              int
	MaxChars              int
	HTTPTimeout           time.Duration
	WriteTimeout          time.Duration
	ClientSecretPath      string
}

// Settings holds the raw string values before validation. Flags and the
// environment both feed it; Build turns it into a Config.
type Settings struct {
	Profile               string
	LogLevel              string
	LogFormat             string
	ReadOnly              string
	EnableDestructive     string
	EnableDataSources     string
	AllowExternalFormulas string
	MaxCells              string
	MaxChars              string
	HTTPTimeout           string
	WriteTimeout          string
	ClientSecretPath      string
	// removed are the settings a release took out, as the environment
	// gives them, read only to refuse one still set.
	removed map[string]string
	// vars is every variable Define reads, in order.
	vars []Var
}

// Var is one variable the server reads.
type Var struct {
	// Name is the environment variable.
	Name string
	// Default is what the server uses when neither the variable nor its
	// flag is set, written as a person would set it.
	Default string
	// Switch marks a setting that is on or off.
	Switch bool
	// Removed marks a setting a release took out, read only to refuse it.
	Removed bool
}

// The variables read outside Define. internal/userconfig reads the
// profile directory before any flag is parsed, and internal/credentials
// the refresh token; each takes its name from here, so config imports
// neither, and the bundle gate imports no keyring.
const (
	EnvConfigDir    = EnvPrefix + "CONFIG_DIR"
	EnvRefreshToken = EnvPrefix + "REFRESH_TOKEN"
)

// envOnly is every variable read outside Define, for Vars.
var envOnly = []string{EnvConfigDir, EnvRefreshToken}

// DefaultProfile is the profile used when none is named.
const DefaultProfile = "default"

// Lookup is the variable name as env gives it, without the spaces around
// it. Spaces alone are "", unset as an empty variable is, so a form field
// cleared to a space means the default rather than a value the server
// refuses. Define reads every setting through it, and so does each
// reader of a variable outside Define.
func Lookup(env func(string) string, name string) string {
	return strings.TrimSpace(env(name))
}

// removedSettings are the settings a release took out, each with what to
// do instead. Off is still accepted. On, or a value that is not a
// boolean, is refused at start: ignored, it would quietly allow what its
// owner set it to stop.
var removedSettings = []struct{ key, instead string }{
	{"REQUIRE_PROMPT", "leave " + EnvPrefix + "ENABLE_DESTRUCTIVE and " + EnvPrefix +
		"ENABLE_DATA_SOURCES off for a client that runs with nobody watching"},
}

// removedSet refuses each removed setting that is set to anything but
// off.
func (s *Settings) removedSet() []error {
	var errs []error
	for _, r := range removedSettings {
		if on, err := parseBool(r.key, s.removed[r.key]); err == nil && !on {
			continue
		}
		errs = append(errs, fmt.Errorf("%w: %s%s was removed: the server no longer asks the person, and the client's "+
			"own approval decides each call. Unset it, and %s", ErrInvalid, EnvPrefix, r.key, r.instead))
	}
	return errs
}

// Define registers one flag per setting on fs. Each flag defaults to the
// matching GSHEETS_* variable, so a flag on the command line wins over
// the environment and the environment wins over the built-in default.
//
// Each variable it reads is recorded for Vars, which the staleness and
// bundle gates read, so neither can fall behind a setting added here.
func Define(fs *flag.FlagSet, env func(string) string) *Settings {
	s := &Settings{removed: map[string]string{}}
	for _, r := range removedSettings {
		s.removed[r.key] = Lookup(env, EnvPrefix+r.key)
		s.vars = append(s.vars, Var{Name: EnvPrefix + r.key, Removed: true})
	}
	bind := func(p *string, name, key, fallback, usage string, isSwitch bool) {
		v := Lookup(env, EnvPrefix+key)
		if v == "" {
			v = fallback
		}
		fs.StringVar(p, name, v, usage+" [env "+EnvPrefix+key+"]")
		s.vars = append(s.vars, Var{Name: EnvPrefix + key, Default: fallback, Switch: isSwitch})
	}
	def := func(p *string, name, key, fallback, usage string) { bind(p, name, key, fallback, usage, false) }
	sw := func(p *string, name, key, fallback, usage string) { bind(p, name, key, fallback, usage, true) }
	def(&s.Profile, "profile", "PROFILE", DefaultProfile, "named configuration profile")
	def(&s.LogLevel, "log-level", "LOG_LEVEL", string(LogInfo), "log level: debug, info, warn, error")
	def(&s.LogFormat, "log-format", "LOG_FORMAT", string(LogText), "log format: text, json")
	sw(&s.ReadOnly, "read-only", "READ_ONLY", "false", "register only read tools and request read-only scopes")
	sw(&s.EnableDestructive, "enable-destructive", "ENABLE_DESTRUCTIVE", "false", "register the destructive tools; each still needs confirm on the call")
	sw(&s.EnableDataSources, "enable-data-sources", "ENABLE_DATA_SOURCES", "false", "register manage_data_source and connect_data_source, and ask for the bigquery.readonly scope at login")
	sw(&s.AllowExternalFormulas, "allow-external-formulas", "ALLOW_EXTERNAL_FORMULAS", "false",
		"let a write create IMPORTXML, IMPORTDATA, IMPORTHTML, IMPORTFEED, IMAGE, HYPERLINK or IMPORTRANGE")
	def(&s.MaxCells, "max-cells", "MAX_CELLS", strconv.Itoa(DefaultMaxCells), "default cell budget for a read")
	def(&s.MaxChars, "max-chars", "MAX_CHARS", strconv.Itoa(DefaultMaxChars), "default character budget for a read")
	def(&s.HTTPTimeout, "http-timeout", "HTTP_TIMEOUT", "60s", "per-attempt timeout for a read")
	def(&s.WriteTimeout, "write-timeout", "WRITE_TIMEOUT", "180s", "timeout for a write, matching the 180s Sheets allows a request")
	def(&s.ClientSecretPath, "client-secret", "CLIENT_SECRET", "", "path to the OAuth Desktop client JSON (overrides the stored profile setting)")
	return s
}

// Vars is every variable the server reads, sorted by name: Define's,
// the removed ones included, and the ones read outside it. Define's are
// recorded rather than listed, so they cannot fall behind. The bundle
// and staleness gates derive what they hold from these.
func Vars() []Var {
	vars := defined()
	for _, name := range envOnly {
		vars = append(vars, Var{Name: name})
	}
	slices.SortFunc(vars, func(a, b Var) int { return strings.Compare(a.Name, b.Name) })
	return vars
}

// defined is every variable Define reads, in the order it reads them.
func defined() []Var {
	fs := flag.NewFlagSet("vars", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return Define(fs, func(string) string { return "" }).vars
}

// EnvVars is the names of Vars, sorted. A name listed twice is a
// variable read under two rules, which a test refuses, so it is not
// compacted.
func EnvVars() []string {
	vars := Vars()
	names := make([]string, 0, len(vars))
	for _, v := range vars {
		names = append(names, v.Name)
	}
	return names
}

// Load is Define, Parse and Build for a caller with no flags of its own.
func Load(args []string, env func(string) string) (Config, error) {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	s := Define(fs, env)
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return s.Build()
}

// ProfileRule is profilePattern in words, which Build's refusal and the
// bundle's profile field both give.
const ProfileRule = "up to 64 characters from a to z, 0 to 9, - and _, starting with a letter or digit"

var (
	profilePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	logLevels      = map[LogLevel]bool{LogDebug: true, LogInfo: true, LogWarn: true, LogError: true}
	logFormats     = map[LogFormat]bool{LogText: true, LogJSON: true}
)

// asciiLower lowers A to Z alone, so a character Unicode folds into a
// to z, such as the Kelvin sign, is refused rather than renamed.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("config: invalid")

// Build validates the settings and returns a Config.
func (s *Settings) Build() (Config, error) {
	var c Config
	var errs []error

	c.Profile = asciiLower(strings.TrimSpace(s.Profile))
	if !profilePattern.MatchString(c.Profile) {
		errs = append(errs, fmt.Errorf("%w: profile %q must be %s", ErrInvalid, s.Profile, ProfileRule))
	}

	c.LogLevel = LogLevel(strings.ToLower(strings.TrimSpace(s.LogLevel)))
	if !logLevels[c.LogLevel] {
		errs = append(errs, fmt.Errorf("%w: log level %q (want debug, info, warn, error)", ErrInvalid, s.LogLevel))
	}
	c.LogFormat = LogFormat(strings.ToLower(strings.TrimSpace(s.LogFormat)))
	if !logFormats[c.LogFormat] {
		errs = append(errs, fmt.Errorf("%w: log format %q (want text, json)", ErrInvalid, s.LogFormat))
	}

	var err error
	if c.ReadOnly, err = parseBool("read-only", s.ReadOnly); err != nil {
		errs = append(errs, err)
	}
	if c.EnableDataSources, err = parseBool("enable-data-sources", s.EnableDataSources); err != nil {
		errs = append(errs, err)
	}
	if c.EnableDestructive, err = parseBool("enable-destructive", s.EnableDestructive); err != nil {
		errs = append(errs, err)
	}
	if c.AllowExternalFormulas, err = parseBool("allow-external-formulas", s.AllowExternalFormulas); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, s.removedSet()...)

	if c.MaxCells, err = parseCount("max-cells", s.MaxCells, DefaultMaxCells, MaxMaxCells); err != nil {
		errs = append(errs, err)
	}
	if c.MaxChars, err = parseCount("max-chars", s.MaxChars, DefaultMaxChars, MaxMaxChars); err != nil {
		errs = append(errs, err)
	}

	if c.HTTPTimeout, err = parseTimeout("http-timeout", s.HTTPTimeout); err != nil {
		errs = append(errs, err)
	}
	if c.WriteTimeout, err = parseTimeout("write-timeout", s.WriteTimeout); err != nil {
		errs = append(errs, err)
	}

	c.ClientSecretPath = strings.TrimSpace(s.ClientSecretPath)

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return c, nil
}

func parseBool(name, v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	}
	return false, fmt.Errorf("%w: %s %q (want true or false)", ErrInvalid, name, v)
}

func parseCount(name, v string, fallback, maxValue int) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%w: %s %q is not a number", ErrInvalid, name, v)
	}
	if n < 1 || n > maxValue {
		return 0, fmt.Errorf("%w: %s %d must be between 1 and %d", ErrInvalid, name, n, maxValue)
	}
	return n, nil
}

func parseTimeout(name, v string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%w: %s %q: %w", ErrInvalid, name, v, err)
	}
	if d <= 0 || d > 10*time.Minute {
		return 0, fmt.Errorf("%w: %s %s must be between 1s and 10m", ErrInvalid, name, d)
	}
	return d, nil
}

// NewLogger builds the process logger. It writes to w, which must be
// stderr on the server path: stdout carries only JSON-RPC frames.
func NewLogger(c Config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel.Slog()}
	if c.LogFormat == LogJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
