package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	"github.com/mmedum/google-sheets-mcp/internal/config"
	"github.com/mmedum/google-sheets-mcp/internal/userconfig"
	"github.com/mmedum/google-sheets-mcp/internal/version"
)

// This package is the whole command-line surface — login, logout,
// status, doctor and the flag parsing in front of them — and it had no
// tests at all. It is also outside `-coverpkg=./internal/...`, so the
// coverage floor could not see that it had none.
//
// Nothing here reaches the network. The commands that would are driven
// only as far as their argument handling, and the live driver covers the
// rest against a real account.

// exampleClientID is the repository's one blessed client-id fixture,
// allowed by value in .gitleaks.toml so that a real one in the same file
// is still a finding. It has the shape a real id has — the secret
// scanner flagged an earlier, hand-rolled version of this test — which
// is exactly what makes it worth masking.
const exampleClientID = "123456789012-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com" // leakcheck:allow

// isolate points the config directory at a temporary one, so a test
// never reads or writes the person's real profile.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GSHEETS_CONFIG_DIR", dir)
	// The token file is the other thing that could escape the sandbox.
	t.Setenv("GSHEETS_REFRESH_TOKEN", "")
	return dir
}

func TestVersionFlagPrintsToStdoutAndNothingElse(t *testing.T) {
	isolate(t)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != version.Info() {
		t.Errorf("stdout = %q, want %q", got, version.Info())
	}
	// stdout carries JSON-RPC on the server path, so anything the
	// command prints there is worth asserting exactly.
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", stderr.String())
	}
}

func TestUnknownCommandPrintsUsageAndFails(t *testing.T) {
	isolate(t)
	var stdout, stderr bytes.Buffer
	err := run([]string{"frobnicate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("an unknown command was accepted")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("error = %v, want it to name the command", err)
	}
	if !strings.Contains(stderr.String(), "google-sheets-mcp login") {
		t.Errorf("usage was not printed:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("usage went to stdout, which carries JSON-RPC: %q", stdout.String())
	}
}

// A misconfigured server fails before it announces itself, and the
// message names the setting.
func TestInvalidConfigurationFailsBeforeAnythingRuns(t *testing.T) {
	isolate(t)
	t.Setenv("GSHEETS_MAX_CELLS", "banana")
	var stdout, stderr bytes.Buffer
	err := run([]string{"status"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("a bad setting was accepted")
	}
	if !strings.Contains(err.Error(), "max-cells") {
		t.Errorf("error = %v, want it to name the setting", err)
	}
}

func TestBadFlagIsRefused(t *testing.T) {
	isolate(t)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--no-such-flag"}, &stdout, &stderr); err == nil {
		t.Fatal("an unknown flag was accepted")
	}
}

// status is what the issue form asks people to paste, so what it prints
// matters more than that it printed.
func TestStatusMasksWhatIsMeantToBePasted(t *testing.T) {
	dir := isolate(t)
	if err := userconfig.Save("default", userconfig.Config{
		AccountEmail:     "someone@example.test",
		ClientSecretPath: filepath.Join(dir, "client_secret_"+exampleClientID+".json"),
		TokenStore:       "keyring",
		Scopes:           []string{"https://www.googleapis.com/auth/spreadsheets"},
	}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cfg, err := settingsFor(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := status(context.Background(), cfg, &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	got := out.String()
	// The address and the client id both reach this output, and both are
	// on the never-list. The client id arrives through the file name.
	for _, forbidden := range []string{"someone@example.test", exampleClientID} {
		if strings.Contains(got, forbidden) {
			t.Errorf("status printed %q unmasked:\n%s", forbidden, got)
		}
	}
	for _, want := range []string{"profile: default", "token store: keyring", "read-only:", "destructive tools:"} {
		if !strings.Contains(got, want) {
			t.Errorf("status is missing %q:\n%s", want, got)
		}
	}
}

// With nothing stored, status says what to do rather than failing.
func TestStatusOnAFreshProfileSaysWhatToRun(t *testing.T) {
	isolate(t)
	var out bytes.Buffer
	cfg, err := settingsFor(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := status(context.Background(), cfg, &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "login") {
		t.Errorf("an unconfigured profile does not point at login:\n%s", out.String())
	}
}

// The server exits quietly when the client goes away, and loudly when
// something actually broke. The two codes are the SDK's own, and reading
// them wrongly makes every disconnect look like a crash in a log the
// person is asked to paste.
func TestCleanDisconnect(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"no error", nil, true},
		{"the context was cancelled", context.Canceled, true},
		{"the client closed the pipe", io.EOF, true},
		{"wrapped EOF", errors.Join(errors.New("read stdin"), io.EOF), true},
		{"the SDK's disconnect codes", &jsonrpc.Error{Code: -32004}, true},
		{"the other disconnect code", &jsonrpc.Error{Code: -32003}, true},
		{"a real protocol error", &jsonrpc.Error{Code: -32600}, false},
		{"anything else", errors.New("the disk caught fire"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanDisconnect(tc.err); got != tc.want {
				t.Errorf("cleanDisconnect(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestSmallHelpers(t *testing.T) {
	if plural(1, "sheet", "sheets") != "sheet" || plural(0, "sheet", "sheets") != "sheets" ||
		plural(2, "sheet", "sheets") != "sheets" {
		t.Error("plural does not agree with the count")
	}
	if orNone("") != "(none)" || orNone("x") != "x" {
		t.Error("orNone")
	}
	if accountSuffix("") != "" {
		t.Error("accountSuffix invents a suffix for no account")
	}
	// The address is masked here as it is everywhere else this output
	// can be pasted.
	if got := accountSuffix("someone@example.test"); strings.Contains(got, "someone@example.test") {
		t.Errorf("accountSuffix printed the address unmasked: %q", got)
	}
	if detailSuffix("") != "" || !strings.Contains(detailSuffix("why"), "why") {
		t.Error("detailSuffix")
	}
}

func TestBoundedClientTakesTheConfiguredTimeout(t *testing.T) {
	cfg, err := settingsFor(t)
	if err != nil {
		t.Fatal(err)
	}
	if got := boundedClient(cfg); got.Timeout != cfg.HTTPTimeout {
		t.Errorf("timeout = %s, want %s; the oauth2 library falls back to http.DefaultClient, which has none",
			got.Timeout, cfg.HTTPTimeout)
	}
}

// login needs a client secret, and says so rather than opening a browser
// and failing there.
func TestLoginWithoutAClientSecretSaysSo(t *testing.T) {
	isolate(t)
	cfg, err := settingsFor(t)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = login(context.Background(), cfg, filepath.Join(t.TempDir(), "nothing.json"), &out)
	if err == nil {
		t.Fatal("login with no client secret was accepted")
	}
}

// logout on a profile with nothing stored does not pretend it revoked
// anything.
func TestLogoutWithNothingStored(t *testing.T) {
	isolate(t)
	cfg, err := settingsFor(t)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// -yes so it never waits on a prompt; -local so it never reaches
	// Google's revocation endpoint.
	if err := logout(context.Background(), cfg, true, true, &out); err != nil {
		t.Logf("logout reported: %v", err)
	}
	if strings.Contains(out.String(), "Signed out") && !strings.Contains(out.String(), "default") {
		t.Errorf("logout claimed a sign-out without naming the profile:\n%s", out.String())
	}
}

// settingsFor builds a valid Config the way run() does, from the
// environment the test has set.
func settingsFor(t *testing.T) (config.Config, error) {
	t.Helper()
	s := config.Define(flag.NewFlagSet("test", flag.ContinueOnError), os.Getenv)
	return s.Build()
}

// --dump-schemas is what the schema-diff gate reads, and it has to work
// with no credentials at all: the server starts without them on purpose.
func TestDumpSchemasWorksWithoutCredentials(t *testing.T) {
	isolate(t)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--dump-schemas"}, &stdout, &stderr); err != nil {
		t.Fatalf("--dump-schemas: %v", err)
	}
	var dump struct {
		Server string `json:"server"`
		Tools  []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &dump); err != nil {
		t.Fatalf("the dump is not JSON: %v", err)
	}
	if dump.Server == "" || len(dump.Tools) == 0 {
		t.Fatalf("the dump is empty: %+v", dump)
	}
	// The full surface, destructive tools included: a dump that showed
	// only the default configuration would let a gated tool's schema
	// change without the diff gate seeing it.
	names := map[string]bool{}
	for _, tool := range dump.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"read_range", "write_values", "delete_sheet", "delete_dimensions"} {
		if !names[want] {
			t.Errorf("the dump omits %s; --dump-schemas must show the whole surface", want)
		}
	}
}

// doctor checks the setup in the order it has to be set up. With nothing
// configured it names the first missing thing rather than failing at
// whatever it reached first.
func TestDoctorWithNothingConfigured(t *testing.T) {
	isolate(t)
	cfg, err := settingsFor(t)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := doctor(context.Background(), cfg, "", &out); err == nil {
		t.Fatal("doctor passed with nothing configured")
	}
	got := out.String()
	if !strings.Contains(got, "OAuth client JSON") {
		t.Errorf("doctor does not name the first thing to set up:\n%s", got)
	}
	// The later checks are skipped rather than reported as failures:
	// one missing credential should not read as five broken things.
	if strings.Count(got, "fail") > 2 {
		t.Errorf("doctor reported every check as a failure:\n%s", got)
	}
}

// doctor prints the report even when it fails, because the report is
// the answer.
func TestDoctorPrintsBeforeItFails(t *testing.T) {
	isolate(t)
	cfg, err := settingsFor(t)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_ = doctor(context.Background(), cfg, "", &out)
	if !strings.Contains(out.String(), "profile: default") {
		t.Errorf("doctor failed without printing its header:\n%s", out.String())
	}
}
