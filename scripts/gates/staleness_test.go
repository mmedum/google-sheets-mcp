package main

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v4/internal/config"
)

// A permission rule names a registered tool, and every delete has one.
func TestPermissionRulesAreTools(t *testing.T) {
	deletes := []string{"clear_values", "delete_cell_comment", "delete_data_source", "delete_dimensions", "delete_sheet"}
	registered := append([]string{"write_values"}, deletes...)
	ok := `"mcp__google-sheets__delete_sheet", "mcp__google-sheets__delete_dimensions",
		"mcp__google-sheets__clear_values", "mcp__google-sheets__delete_cell_comment",
		"mcp__google-sheets__delete_data_source", "mcp__google-sheets__write_values"`
	if got := permissionRulesAreTools(ok, registered, deletes); len(got) != 0 {
		t.Errorf("a rule for every delete, each registered: %v", got)
	}
	renamed := strings.Replace(ok, "delete_sheet", "remove_sheet", 1)
	want := []string{
		"README.md gives a permission rule for remove_sheet, which the binary does not register",
		"README.md gives no permission rule for delete_sheet, which deletes",
	}
	if got := permissionRulesAreTools(renamed, registered, deletes); !slices.Equal(got, want) {
		t.Errorf("a rule for a tool that does not exist: %v", got)
	}
	repeated := strings.Repeat(`"mcp__google-sheets__clear_values" `, 5)
	want = []string{
		"README.md gives no permission rule for delete_cell_comment, which deletes",
		"README.md gives no permission rule for delete_data_source, which deletes",
		"README.md gives no permission rule for delete_dimensions, which deletes",
		"README.md gives no permission rule for delete_sheet, which deletes",
	}
	if got := permissionRulesAreTools(repeated, registered, deletes); !slices.Equal(got, want) {
		t.Errorf("one rule five times: %v", got)
	}
	want = []string{"the binary lists no tool that takes confirm; this check is not looking at the deletes"}
	if got := permissionRulesAreTools(ok, registered, nil); !slices.Equal(got, want) {
		t.Errorf("no deletes read: %v", got)
	}
}

// The configuration page names every variable the server reads, the
// removed one included, as a whole word, names no other, and states each
// of the bundle's field rules, what a profile name is among them, in the
// server's own words.
func TestConfigDocProblems(t *testing.T) {
	doc := string(repoFile(t, configDocPath))
	if got := configDocProblems(config.Vars(), fieldRules, doc); len(got) != 0 {
		t.Errorf("the committed page: %q", got)
	}
	for _, tc := range []struct {
		name, old, new string
		want           []string
	}{
		{"a variable left out", "`GSHEETS_WRITE_TIMEOUT`", "`the write timeout`",
			[]string{"docs/configuration.md does not document GSHEETS_WRITE_TIMEOUT"}},
		{"a removed variable left out", "`GSHEETS_REQUIRE_PROMPT`", "`the prompt setting`",
			[]string{"docs/configuration.md does not document GSHEETS_REQUIRE_PROMPT"}},
		{"a name kept only inside a longer one", "`GSHEETS_CONFIG_DIR`", "`GSHEETS_CONFIG_DIRECTORY`", []string{
			"docs/configuration.md does not document GSHEETS_CONFIG_DIR",
			"docs/configuration.md documents GSHEETS_CONFIG_DIRECTORY, which the code does not read",
		}},
		{"a variable the code does not read", "`GSHEETS_MAX_CHARS`", "`GSHEETS_MAX_CHARS` and `GSHEETS_MAX_ROWS`",
			[]string{"docs/configuration.md documents GSHEETS_MAX_ROWS, which the code does not read"}},
		{"the profile rule in other words", "- and _, starting", "`-` and `_`, starting",
			[]string{`docs/configuration.md does not say a profile name is "up to 64 characters from a to z, ` +
				`0 to 9, - and _, starting with a letter or digit"`}},
	} {
		if !strings.Contains(doc, tc.old) {
			t.Fatalf("%s: the page does not say %q", tc.name, tc.old)
		}
		if got := configDocProblems(config.Vars(), fieldRules, strings.ReplaceAll(doc, tc.old, tc.new)); !slices.Equal(got,
			tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	// A rule added to the bundle's field is held on the page too.
	rules := maps.Clone(fieldRules)
	rules[config.EnvPrefix+"LOG_LEVEL"] = fieldRule{words: "one of debug, info, warn and error", fails: "say a log level is"}
	want := []string{`docs/configuration.md does not say a log level is "one of debug, info, warn and error"`}
	if got := configDocProblems(config.Vars(), rules, doc); !slices.Equal(got, want) {
		t.Errorf("a rule the page does not state: %q, want %q", got, want)
	}
}
