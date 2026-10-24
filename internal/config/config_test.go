package config

import (
	"bytes"
	"errors"
	"flag"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func settings(env map[string]string, args ...string) (*Settings, error) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	s := Define(fs, func(k string) string { return env[k] })
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return s, nil
}

func TestDefaults(t *testing.T) {
	s, err := settings(nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Profile != "default" || c.LogLevel != LogInfo || c.LogFormat != LogText {
		t.Errorf("defaults are %+v", c)
	}
	if c.MaxCells != DefaultMaxCells || c.MaxChars != DefaultMaxChars {
		t.Errorf("budget defaults are %d cells, %d chars", c.MaxCells, c.MaxChars)
	}
	// 180 seconds is Sheets' own processing limit, so a write that runs
	// that long is the platform working, not this server hanging.
	if c.HTTPTimeout != 60*time.Second || c.WriteTimeout != 180*time.Second {
		t.Errorf("timeouts are %s read, %s write", c.HTTPTimeout, c.WriteTimeout)
	}
	if c.ReadOnly || c.EnableDestructive {
		t.Error("read-only and destructive both default to off")
	}
}

func TestEnvironmentThenFlag(t *testing.T) {
	env := map[string]string{
		EnvPrefix + "PROFILE":            "work",
		EnvPrefix + "LOG_LEVEL":          "debug",
		EnvPrefix + "READ_ONLY":          "true",
		EnvPrefix + "ENABLE_DESTRUCTIVE": "yes",
		EnvPrefix + "MAX_CELLS":          "1234",
	}
	s, err := settings(env)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Profile != "work" || c.LogLevel != LogDebug || !c.ReadOnly || !c.EnableDestructive || c.MaxCells != 1234 {
		t.Errorf("environment not honored: %+v", c)
	}

	// A flag on the command line beats the same setting in the
	// environment; clients pass env, a person debugging passes flags.
	s, err = settings(env, "-profile", "other", "-read-only=false")
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Profile != "other" || c.ReadOnly {
		t.Errorf("flags did not override the environment: %+v", c)
	}
}

func TestValidationRefusesAndSaysWhat(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"profile", map[string]string{EnvPrefix + "PROFILE": "Not A Profile"}, "profile"},
		{"log level", map[string]string{EnvPrefix + "LOG_LEVEL": "verbose"}, "log level"},
		{"log format", map[string]string{EnvPrefix + "LOG_FORMAT": "xml"}, "log format"},
		{"read only", map[string]string{EnvPrefix + "READ_ONLY": "maybe"}, "read-only"},
		{"max cells too big", map[string]string{EnvPrefix + "MAX_CELLS": "5000000"}, "max-cells"},
		{"max cells zero", map[string]string{EnvPrefix + "MAX_CELLS": "0"}, "max-cells"},
		{"max chars text", map[string]string{EnvPrefix + "MAX_CHARS": "lots"}, "max-chars"},
		{"timeout", map[string]string{EnvPrefix + "HTTP_TIMEOUT": "ages"}, "http-timeout"},
		{"timeout too long", map[string]string{EnvPrefix + "WRITE_TIMEOUT": "30m"}, "write-timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := settings(tc.env)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Build()
			if err == nil {
				t.Fatalf("%v was accepted", tc.env)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %q", err, tc.want)
			}
		})
	}
}

func TestBuildReportsEveryProblemAtOnce(t *testing.T) {
	s, err := settings(map[string]string{
		EnvPrefix + "LOG_LEVEL":  "verbose",
		EnvPrefix + "LOG_FORMAT": "xml",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Build()
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "log level") || !strings.Contains(err.Error(), "log format") {
		t.Errorf("only one problem reported: %v", err)
	}
}

func TestLoggerWritesWhereItIsTold(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(Config{LogLevel: LogDebug, LogFormat: LogJSON}, &buf)
	log.Debug("hello", "n", 1)
	if !strings.Contains(buf.String(), `"msg":"hello"`) {
		t.Errorf("json handler wrote %q", buf.String())
	}

	buf.Reset()
	log = NewLogger(Config{LogLevel: LogWarn, LogFormat: LogText}, &buf)
	log.Info("quiet")
	if buf.Len() != 0 {
		t.Errorf("level not honored: %q", buf.String())
	}
}

func TestLogLevelSlog(t *testing.T) {
	for level, want := range map[LogLevel]slog.Level{
		LogDebug: slog.LevelDebug,
		LogInfo:  slog.LevelInfo,
		LogWarn:  slog.LevelWarn,
		LogError: slog.LevelError,
		"":       slog.LevelInfo,
	} {
		if got := level.Slog(); got != want {
			t.Errorf("%q.Slog() = %v, want %v", level, got, want)
		}
	}
}

// GSHEETS_REQUIRE_PROMPT went with the server's question. Set to
// anything but off, the server refuses to start rather than quietly
// allow what its owner set it to stop; off, or unset, it is ignored.
// Its flag is gone.
func TestRemovedRequirePrompt(t *testing.T) {
	for v, refused := range map[string]bool{"true": true, "1": true, "maybe": true, "false": false, "0": false, "": false} {
		s, err := settings(map[string]string{EnvPrefix + "REQUIRE_PROMPT": v})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Build()
		if got := err != nil; got != refused || (refused && !errors.Is(err, ErrInvalid)) {
			t.Errorf("GSHEETS_REQUIRE_PROMPT=%q: err = %v, want refused %v", v, err, refused)
		}
		if refused && !strings.Contains(err.Error(), "GSHEETS_REQUIRE_PROMPT was removed") {
			t.Errorf("GSHEETS_REQUIRE_PROMPT=%q: the refusal does not name it: %v", v, err)
		}
	}
	if _, err := settings(nil, "-require-prompt"); err == nil {
		t.Error("the removed -require-prompt flag was accepted")
	}
}

// Formulas that reach outside the spreadsheet are the operator's to
// allow, and off unless they say so.
func TestExternalFormulasAreOffUnlessTheOperatorSaysSo(t *testing.T) {
	for env, want := range map[string]bool{"": false, "false": false, "true": true} {
		s, err := settings(map[string]string{EnvPrefix + "ALLOW_EXTERNAL_FORMULAS": env})
		if err != nil {
			t.Fatal(err)
		}
		c, err := s.Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if c.AllowExternalFormulas != want {
			t.Errorf("GSHEETS_ALLOW_EXTERNAL_FORMULAS=%q: %v, want %v", env, c.AllowExternalFormulas, want)
		}
	}
}

// Vars is what the bundle gate reads a setting's kind from, so each
// claim it makes is held to what Build does. A switch takes true and
// false and refuses anything else, and nothing else does that. A removed
// setting takes false and refuses true. Each default, and an empty
// value, configure what setting nothing does, which is what lets a
// bundle leave a field blank.
func TestVarsDescribeWhatBuildDoes(t *testing.T) {
	build := func(env map[string]string) (Config, error) {
		s, err := settings(env)
		if err != nil {
			return Config{}, err
		}
		return s.Build()
	}
	unset, err := build(nil)
	if err != nil {
		t.Fatal(err)
	}
	var switches, removed []string
	for _, v := range registry(t) {
		accepts := func(value string) bool {
			_, err := build(map[string]string{v.Name: value})
			return err == nil
		}
		if boolean := accepts("true") && accepts("false") && !accepts("maybe"); boolean != v.Switch {
			t.Errorf("%s: Switch is %t, and Build reads it as a boolean: %t", v.Name, v.Switch, boolean)
		}
		if gone := accepts("false") && !accepts("true") && !accepts("maybe"); gone != v.Removed {
			t.Errorf("%s: Removed is %t, and Build refuses it on: %t", v.Name, v.Removed, gone)
		}
		if v.Switch {
			switches = append(switches, v.Name)
		}
		if v.Removed {
			removed = append(removed, v.Name)
		}
		for _, value := range []string{v.Default, ""} {
			c, err := build(map[string]string{v.Name: value})
			if err != nil {
				t.Errorf("%s=%q is refused: %v", v.Name, value, err)
				continue
			}
			if !reflect.DeepEqual(c, unset) {
				t.Errorf("%s=%q configures something:\n got %+v\nwant %+v", v.Name, value, c, unset)
			}
		}
	}
	want := []string{"GSHEETS_ALLOW_EXTERNAL_FORMULAS", "GSHEETS_ENABLE_DATA_SOURCES", "GSHEETS_ENABLE_DESTRUCTIVE",
		"GSHEETS_READ_ONLY"}
	if !slices.Equal(switches, want) {
		t.Errorf("switches %v, want %v", switches, want)
	}
	if want := []string{"GSHEETS_REQUIRE_PROMPT"}; !slices.Equal(removed, want) {
		t.Errorf("Vars marks %v removed, want %v", removed, want)
	}
}

// EnvVars is every variable the server reads, the removed one and the
// two read outside Define included.
func TestEnvVars(t *testing.T) {
	want := []string{
		"GSHEETS_ALLOW_EXTERNAL_FORMULAS", "GSHEETS_CLIENT_SECRET", "GSHEETS_CONFIG_DIR",
		"GSHEETS_ENABLE_DATA_SOURCES", "GSHEETS_ENABLE_DESTRUCTIVE", "GSHEETS_HTTP_TIMEOUT", "GSHEETS_LOG_FORMAT",
		"GSHEETS_LOG_LEVEL", "GSHEETS_MAX_CELLS", "GSHEETS_MAX_CHARS", "GSHEETS_PROFILE", "GSHEETS_READ_ONLY",
		"GSHEETS_REFRESH_TOKEN", "GSHEETS_REQUIRE_PROMPT", "GSHEETS_WRITE_TIMEOUT",
	}
	if got := EnvVars(); !slices.Equal(got, want) {
		t.Errorf("EnvVars =\n%v\nwant\n%v", got, want)
	}
}

// varsFloor is how many variables the server reads today, on purpose:
// the floor moves with it, so a registry that records nothing cannot
// pass a walk over it.
const varsFloor = 15

// registry is Vars, refused below the floor.
func registry(t *testing.T) []Var {
	t.Helper()
	vars := Vars()
	if len(vars) < varsFloor {
		t.Fatalf("Vars has %d variables, want at least %d", len(vars), varsFloor)
	}
	return vars
}

// Define reads each variable once, and records each once for Vars. Two
// settings on one variable would leave it to whichever Build reads
// last, and a variable Define reads and something outside it reads too
// would be read under two rules; EnvVars lists that one twice.
func TestDefineReadsEachVariableOnce(t *testing.T) {
	registry(t)
	reads := map[string]int{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	Define(fs, func(name string) string { reads[name]++; return "" })
	var listed []string
	for _, v := range defined() {
		listed = append(listed, v.Name)
	}
	slices.Sort(listed)
	if read := slices.Sorted(maps.Keys(reads)); !slices.Equal(listed, read) {
		t.Errorf("Define records %v\nDefine reads %v", listed, read)
	}
	for name, n := range reads {
		if n != 1 {
			t.Errorf("Define reads %s %d times", name, n)
		}
	}
	if names := EnvVars(); !slices.Equal(names, slices.Compact(slices.Clone(names))) {
		t.Errorf("EnvVars names a variable twice: %v", names)
	}
}

// A profile name is held to ProfileRule, and the refusal says it. Only A
// to Z are lowered: a character Unicode folds into a to z, such as the
// Kelvin sign, is refused rather than renamed to another profile.
func TestProfileRule(t *testing.T) {
	if ProfileRule != "up to 64 characters from a to z, 0 to 9, - and _, starting with a letter or digit" {
		t.Errorf("ProfileRule = %q", ProfileRule)
	}
	build := func(name string) (Config, error) {
		s, err := settings(map[string]string{EnvPrefix + "PROFILE": name})
		if err != nil {
			return Config{}, err
		}
		return s.Build()
	}
	for _, tc := range []struct{ name, want string }{
		{"Work", "work"}, {" work ", "work"}, {"a", "a"}, {"9-", "9-"}, {"a-b_c", "a-b_c"},
		{strings.Repeat("a", 64), strings.Repeat("a", 64)},
	} {
		if c, err := build(tc.name); err != nil || c.Profile != tc.want {
			t.Errorf("GSHEETS_PROFILE=%q: profile %q, %v; want %q", tc.name, c.Profile, err, tc.want)
		}
	}
	for _, name := range []string{strings.Repeat("a", 65), "-a", "_a", "a.b", "a b", "caf\u00e9", "\u212aey"} {
		_, err := build(name)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "must be "+ProfileRule) {
			t.Errorf("GSHEETS_PROFILE=%q: %v; want it refused, naming the rule", name, err)
		}
	}
}

// Load is Define, Parse and Build: a flag is read, and an unknown one
// refused.
func TestLoad(t *testing.T) {
	c, err := Load([]string{"-read-only", "true"}, func(string) string { return "" })
	if err != nil || !c.ReadOnly {
		t.Errorf("Load = %+v, %v; want read-only", c, err)
	}
	if _, err := Load([]string{"-unknown"}, func(string) string { return "" }); err == nil {
		t.Error("an unknown flag was accepted")
	}
}
