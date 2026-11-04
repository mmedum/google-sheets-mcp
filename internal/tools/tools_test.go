package tools

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/v4/internal/config"
	"github.com/mmedum/google-sheets-mcp/v4/internal/service"
)

type fakeOut struct {
	Summary string `json:"summary"`
}

func (f fakeOut) Render() string { return f.Summary }

type noDryRun struct {
	Spreadsheet string `json:"spreadsheet"`
}

type withDryRun struct {
	Spreadsheet string `json:"spreadsheet"`
	DryRun      bool   `json:"dry_run,omitempty"`
}

type badDryRun struct {
	DryRun string `json:"dry_run,omitempty"`
}

// TestDryRunIsFoundByReflectionNotByMemory holds the dry-run rule: a
// write that offers dry_run and does not honor it is a preview that
// wrote, so the flag is looked for in the type rather than remembered
// per tool.
func TestDryRunIsFoundByReflectionNotByMemory(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind Kind
		in   any
		ok   bool
	}{
		{"read without dry_run", Read, noDryRun{}, true},
		{"read offering dry_run", Read, withDryRun{}, false},
		{"write without dry_run", Write, noDryRun{}, false},
		{"write with dry_run", Write, withDryRun{}, true},
		{"idempotent write with dry_run", IdempotentWrite, withDryRun{}, true},
		{"destructive with dry_run", Destructive, withDryRun{}, true},
		{"dry_run of the wrong type", Write, badDryRun{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDryRun(tc.kind, reflect.TypeOf(tc.in))
			if tc.ok && err != nil {
				t.Errorf("checkDryRun = %v, want no error", err)
			}
			if !tc.ok && err == nil {
				t.Error("checkDryRun accepted it")
			}
		})
	}
	if err := checkDryRun(Write, nil); err != nil {
		t.Errorf("a nil input type is not this check's business: %v", err)
	}
}

func TestRegisteringAWriteWithoutDryRunPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("a write tool with no dry_run was registered")
		}
		if !strings.Contains(r.(string), DryRunField) {
			t.Errorf("the panic does not name the field: %v", r)
		}
	}()
	s := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	add(s, Deps{}, Def[noDryRun, fakeOut]{Name: "bad_write", Kind: Write,
		Handle: func(context.Context, noDryRun) (fakeOut, error) { return fakeOut{}, nil }})
}

// TestRegistrationGatesAreServerSide covers the two rules a client
// cannot be trusted with. Annotations are hints the specification says a
// client may treat as untrusted, and a host in an auto-approve mode runs
// an annotated tool without asking anybody, so a tool that must not run
// is not registered at all.
func TestRegistrationGatesAreServerSide(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.Config
		want map[Kind]bool
	}{
		{"default", config.Config{}, map[Kind]bool{Read: true, Write: true, IdempotentWrite: true, Destructive: false, Connected: false, Notifying: true}},
		{"destructive enabled", config.Config{EnableDestructive: true},
			map[Kind]bool{Read: true, Write: true, IdempotentWrite: true, Destructive: true, Connected: false, Notifying: true}},
		{"data sources enabled", config.Config{EnableDataSources: true},
			map[Kind]bool{Read: true, Write: true, IdempotentWrite: true, Destructive: false, Connected: true, Notifying: true}},
		{"read only", config.Config{ReadOnly: true},
			map[Kind]bool{Read: true, Write: false, IdempotentWrite: false, Destructive: false, Connected: false, Notifying: false}},
		{"read only wins over destructive", config.Config{ReadOnly: true, EnableDestructive: true},
			map[Kind]bool{Read: true, Write: false, IdempotentWrite: false, Destructive: false, Connected: false, Notifying: false}},
		{"read only wins over data sources", config.Config{ReadOnly: true, EnableDataSources: true},
			map[Kind]bool{Read: true, Write: false, IdempotentWrite: false, Destructive: false, Connected: false, Notifying: false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for kind, want := range tc.want {
				if got := allowed(kind, tc.cfg); got != want {
					t.Errorf("allowed(%v) = %v, want %v", kind, got, want)
				}
			}
		})
	}
}

// A write says destructiveHint true unless it only adds, idempotentHint
// only as an IdempotentWrite, and openWorldHint only when it reaches
// outside the spreadsheet: Connected, and Notifying, whose comment can
// email its assignee. A read says readOnlyHint and nothing else.
func TestAnnotationsFollowTheKind(t *testing.T) {
	read := annotationsFor(Read, false)
	if !read.ReadOnlyHint || !read.IdempotentHint || read.OpenWorldHint == nil || *read.OpenWorldHint {
		t.Errorf("read annotations = %+v", read)
	}
	for _, k := range []Kind{Write, IdempotentWrite, Destructive, Connected, Notifying} {
		for _, adds := range []bool{false, true} {
			if adds && k == Destructive {
				continue // checkHints refuses it
			}
			a := annotationsFor(k, adds)
			if a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint == adds ||
				a.IdempotentHint != (k == IdempotentWrite) ||
				a.OpenWorldHint == nil || *a.OpenWorldHint != (k == Connected || k == Notifying) {
				t.Errorf("kind %v, adds %v: annotations = %+v", k, adds, a)
			}
		}
	}
}

// TestOnlyDeleteDataSourcePrompts holds the marked set. Claude
// Code prompts for a marked tool on every call and refuses it where
// nobody can be asked. delete_data_source takes its sheet and unlinks
// every object built on the source, which Sheets cannot undo; every
// other delete removes one thing, often many times in a row, and the
// operator's own rules decide those.
func TestOnlyDeleteDataSourcePrompts(t *testing.T) {
	var got []string
	tools := surface(t)
	for name, tool := range tools {
		if tool.Meta[interactionKey] == true {
			got = append(got, name)
		}
	}
	slices.Sort(got)
	if want := []string{"delete_data_source"}; !slices.Equal(got, want) {
		t.Errorf("tools carrying %s = %v, want %v", interactionKey, got, want)
	}
}

// TestTheHintsAreExact holds the two annotations a client prompts by.
// VS Code prompts on every tool without readOnlyHint, so only the
// reads carry it. Codex and claude.ai prompt by destructiveHint, which
// the MCP schema reads as false only for "the tool performs only
// additive updates": so only the three writes that add say false, and
// every other write says true, gated or not. A write is destructive
// unless marked Adds, so a new one that should not be fails here.
func TestTheHintsAreExact(t *testing.T) {
	var readOnly, adds []string
	for name, tool := range surface(t) {
		a := tool.Annotations
		switch {
		case a.ReadOnlyHint:
			readOnly = append(readOnly, name)
		case a.DestructiveHint == nil:
			t.Errorf("%s writes and leaves destructiveHint unset", name)
		case !*a.DestructiveHint:
			adds = append(adds, name)
		}
	}
	slices.Sort(readOnly)
	slices.Sort(adds)
	if want := []string{"find_in_spreadsheet", "get_spreadsheet", "read_cell_comments", "read_formatting",
		"read_range", "search_spreadsheets"}; !slices.Equal(readOnly, want) {
		t.Errorf("readOnlyHint on %v, want %v", readOnly, want)
	}
	if want := []string{"append_rows", "connect_data_source", "create_spreadsheet"}; !slices.Equal(adds, want) {
		t.Errorf("destructiveHint false on %v, want %v", adds, want)
	}
}

// A hint on a kind it does not describe is a defect found when the
// server starts: the mark only on a tool the destructive flag registers,
// and Adds only on a write that flag does not gate.
func TestAHintOnTheWrongKindPanics(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  Def[withDryRun, fakeOut]
	}{
		{"the mark on a write", Def[withDryRun, fakeOut]{Name: "w", Kind: Write, PromptEveryCall: true}},
		{"the mark on a connected tool", Def[withDryRun, fakeOut]{Name: "c", Kind: Connected, PromptEveryCall: true}},
		{"adds on a destructive tool", Def[withDryRun, fakeOut]{Name: "d", Kind: Destructive, Adds: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("registered without a panic")
				}
			}()
			s := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
			add(s, Deps{Config: FullSurface(config.Config{})}, tc.def)
		})
	}
	// A read takes no dry_run, so it is a Def of its own type.
	t.Run("adds on a read", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Error("registered without a panic")
			}
		}()
		s := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
		add(s, Deps{Config: FullSurface(config.Config{})}, Def[noDryRun, fakeOut]{Name: "r", Kind: Read, Adds: true})
	})
	// The kinds each hint is for register.
	s := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	d := Deps{Config: FullSurface(config.Config{})}
	add(s, d, Def[withDryRun, fakeOut]{Name: "marked", Kind: Destructive, PromptEveryCall: true})
	add(s, d, Def[withDryRun, fakeOut]{Name: "adds", Kind: IdempotentWrite, Adds: true})
	add(s, d, Def[withDryRun, fakeOut]{Name: "notifies", Kind: Notifying, Adds: true})
	if got := listTools(t, s); len(got) != 3 {
		t.Errorf("registered %d of the three", len(got))
	}
}

// surface is every tool the full surface registers, as a client lists it.
func surface(t *testing.T) map[string]*mcp.Tool {
	t.Helper()
	cfg := FullSurface(config.Config{})
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(s, Deps{Service: service.New(service.Deps{Config: cfg}), Config: cfg})
	tools := listTools(t, s)
	if len(tools) < 20 {
		t.Fatalf("the full surface lists %d tools; this check is not looking at a server", len(tools))
	}
	return tools
}

func TestFailFormatsTheClass(t *testing.T) {
	err := fail(service.Errorf("blocked", "there is a formula in the way"))
	if err.Error() != "[blocked] there is a formula in the way" {
		t.Errorf("fail = %q", err.Error())
	}
	// Anything unclassified still arrives in the same shape, so a model
	// never has to parse two formats.
	other := fail(errors.New("something went wrong"))
	if !strings.HasPrefix(other.Error(), "[unavailable] ") {
		t.Errorf("fail on a bare error = %q", other.Error())
	}
}

func TestJSONName(t *testing.T) {
	for tag, want := range map[string]string{
		"dry_run":           "dry_run",
		"dry_run,omitempty": "dry_run",
		",omitempty":        "",
		"":                  "",
	} {
		if got := jsonName(tag); got != want {
			t.Errorf("jsonName(%q) = %q, want %q", tag, got, want)
		}
	}
}

func listTools(t *testing.T, s *mcp.Server) map[string]*mcp.Tool {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		out[tool.Name] = tool
	}
	return out
}

// TestFullSurfaceRegistersEverything is the claim FullSurface makes,
// held by a test rather than by its comment.
//
// The schema dump describes the whole tool surface. A configuration
// built by hand there misses the next gate, and the tool behind it
// vanishes from the dump and so from the schema diff, the gate that
// would say a tool had appeared, with every test still green.
//
// So this enumerates the gates instead of naming them: no combination of
// them may register a tool the full surface does not.
func TestFullSurfaceRegistersEverything(t *testing.T) {
	full := registeredUnder(t, FullSurface(config.Config{}))
	if len(full) == 0 {
		t.Fatal("the full surface registers nothing, so this test is reading nothing")
	}
	for _, readOnly := range []bool{false, true} {
		for _, destructive := range []bool{false, true} {
			for _, dataSources := range []bool{false, true} {
				cfg := config.Config{
					ReadOnly: readOnly, EnableDestructive: destructive, EnableDataSources: dataSources,
				}
				for name := range registeredUnder(t, cfg) {
					if !full[name] {
						t.Errorf("%s registers with read_only=%v destructive=%v data_sources=%v "+
							"and is missing from the full surface, so the schema dump would not carry it",
							name, readOnly, destructive, dataSources)
					}
				}
			}
		}
	}
}

// registeredUnder is the tool surface one configuration produces.
func registeredUnder(t *testing.T, cfg config.Config) map[string]bool {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(s, Deps{Service: service.New(service.Deps{Config: cfg}), Config: cfg})
	out := map[string]bool{}
	for name := range listTools(t, s) {
		out[name] = true
	}
	return out
}
