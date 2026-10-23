package config

import (
	"bytes"
	"errors"
	"flag"
	"log/slog"
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
