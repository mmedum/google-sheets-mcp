package config

import (
	"bytes"
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
		t.Errorf("environment not honoured: %+v", c)
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
		t.Errorf("level not honoured: %q", buf.String())
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
