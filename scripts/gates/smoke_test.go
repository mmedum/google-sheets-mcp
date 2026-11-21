package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// fakeServerEnv makes this test binary stand in for a server that dies
// while it starts, on every platform CI runs, with no second binary.
const fakeServerEnv = "GATES_SMOKE_FAKE_SERVER"

// TestMain serves every test in the package; it only adds the stand-in.
func TestMain(m *testing.M) {
	if os.Getenv(fakeServerEnv) == "die" {
		fmt.Fprintln(os.Stderr, "config: the settings file is not valid JSON")
		os.Exit(3)
	}
	os.Exit(m.Run())
}

// A server that exits during initialization fails the gate with its exit
// code and what it said, which is what a person needs to fix it.
func TestSmokeNamesAServerThatDiesStarting(t *testing.T) {
	t.Setenv(fakeServerEnv, "die")
	err := smokeSession(os.Args[0])
	if err == nil {
		t.Fatal("a server that died starting passed the smoke gate")
	}
	for _, want := range []string{"the server exited 3 before it answered", "the settings file is not valid JSON"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not say %q:\n%v", want, err)
		}
	}
}
