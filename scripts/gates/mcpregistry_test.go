package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const oneBundle = "" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  google-sheets-mcp_1.4.0.mcpb\n" +
	"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  google-sheets-mcp_1.4.0_linux_amd64.tar.gz\n"

func checksumsFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGithubRepoStripsTheMajorVersion(t *testing.T) {
	cases := []struct {
		module, owner, repo string
		wantErr             bool
	}{
		{module: "github.com/mmedum/google-sheets-mcp", owner: "mmedum", repo: "google-sheets-mcp"},
		{module: "github.com/mmedum/google-sheets-mcp/v2", owner: "mmedum", repo: "google-sheets-mcp"},
		// Go adds the suffix from v2, so /v1 is an ordinary directory and
		// stripping it would name the wrong repository.
		{module: "github.com/mmedum/google-sheets-mcp/v1", wantErr: true},
		{module: "example.invalid/mmedum/thing", wantErr: true},
	}
	for _, tc := range cases {
		owner, repo, err := githubRepo(tc.module)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s was accepted as %s/%s", tc.module, owner, repo)
			}
			continue
		}
		if err != nil || owner != tc.owner || repo != tc.repo {
			t.Errorf("%s gave %s/%s (%v)", tc.module, owner, repo, err)
		}
	}
}

// The bundle's hash is what a client verifies before installing, so
// picking the wrong row, or a row that is not there, is worse than
// failing.
func TestTheBundleRowMustBeExactlyOne(t *testing.T) {
	name, sum, err := bundleRow(checksumsFile(t, oneBundle))
	if err != nil || name != "google-sheets-mcp_1.4.0.mcpb" || !strings.HasPrefix(sum, "aaaa") {
		t.Fatalf("a well-formed checksums file gave %q / %q (%v)", name, sum, err)
	}

	for _, tc := range []struct{ name, body, want string }{
		{"no bundle", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  x.tar.gz\n", "lists no .mcpb"},
		{"two bundles", oneBundle + "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc  other.mcpb\n", "more than one .mcpb"},
		{"empty", "\n", "no checksum rows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := bundleRow(checksumsFile(t, tc.body)); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
		})
	}
}

// The committed manifest has to produce a valid entry, or the first
// release after this is where that is discovered.
func TestTheEntryIsBuiltFromThisRepository(t *testing.T) {
	t.Chdir("../..")

	var out bytes.Buffer
	if err := registryPublish(&out, "v1.4.0", checksumsFile(t, oneBundle)); err != nil {
		t.Fatalf("registryPublish: %v", err)
	}
	var entry registryEntry
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatalf("the entry is not valid JSON: %v", err)
	}
	if entry.Name != "io.github.mmedum/google-sheets-mcp" {
		t.Errorf("namespace = %q", entry.Name)
	}
	if entry.Version != "1.4.0" || entry.Packages[0].Version != "1.4.0" {
		t.Errorf("version = %q / %q", entry.Version, entry.Packages[0].Version)
	}
	if !strings.Contains(entry.Packages[0].Identifier, "/download/v1.4.0/") {
		t.Errorf("identifier = %q", entry.Packages[0].Identifier)
	}
	if entry.Packages[0].FileSHA256 == "" {
		t.Error("no hash, which is what a client verifies before installing")
	}
	// The registry schema makes description required and caps it at 100.
	if n := len(entry.Description); n == 0 || n > registryDescriptionMax {
		t.Errorf("description is %d characters: %q", n, entry.Description)
	}
}

// A description too long for the registry is a manifest that has put the
// wrong text in the short field, and the refusal should say so.
func TestATooLongDescriptionIsRefusedWithTheFix(t *testing.T) {
	t.Chdir("../..")
	manifest, err := readManifest()
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := manifest["description"].(string); len(d) > registryDescriptionMax {
		t.Fatalf("the committed manifest's description is already %d characters", len(d))
	}
	if manifest["long_description"] == nil {
		t.Error("no long_description, so there is nowhere for the detail to live")
	}
}

func TestTheVersionMayCarryItsVOrNot(t *testing.T) {
	t.Chdir("../..")
	path := checksumsFile(t, oneBundle)
	var withV, without bytes.Buffer
	if err := registryPublish(&withV, "v2.3.4", path); err != nil {
		t.Fatal(err)
	}
	if err := registryPublish(&without, "2.3.4", path); err != nil {
		t.Fatal(err)
	}
	if withV.String() != without.String() {
		t.Fatal("v2.3.4 and 2.3.4 produced different entries")
	}
}
