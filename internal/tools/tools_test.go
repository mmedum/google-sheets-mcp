package tools

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/internal/config"
	"github.com/mmedum/google-sheets-mcp/internal/service"
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

// TestDryRunIsFoundByReflectionNotByMemory is the rule §8 turns on: a
// write that offers dry_run and does not honour it is a preview that
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
		{"default", config.Config{}, map[Kind]bool{Read: true, Write: true, IdempotentWrite: true, Destructive: false, Connected: false}},
		{"destructive enabled", config.Config{EnableDestructive: true},
			map[Kind]bool{Read: true, Write: true, IdempotentWrite: true, Destructive: true, Connected: false}},
		{"data sources enabled", config.Config{EnableDataSources: true},
			map[Kind]bool{Read: true, Write: true, IdempotentWrite: true, Destructive: false, Connected: true}},
		{"read only", config.Config{ReadOnly: true},
			map[Kind]bool{Read: true, Write: false, IdempotentWrite: false, Destructive: false, Connected: false}},
		{"read only wins over destructive", config.Config{ReadOnly: true, EnableDestructive: true},
			map[Kind]bool{Read: true, Write: false, IdempotentWrite: false, Destructive: false, Connected: false}},
		{"read only wins over data sources", config.Config{ReadOnly: true, EnableDataSources: true},
			map[Kind]bool{Read: true, Write: false, IdempotentWrite: false, Destructive: false, Connected: false}},
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

func TestAnnotationsFollowTheKind(t *testing.T) {
	read := annotationsFor(Read)
	if !read.ReadOnlyHint || !read.IdempotentHint || read.OpenWorldHint == nil || *read.OpenWorldHint {
		t.Errorf("read annotations = %+v", read)
	}
	write := annotationsFor(Write)
	if write.ReadOnlyHint || write.DestructiveHint == nil || *write.DestructiveHint {
		t.Errorf("write annotations = %+v", write)
	}
	idem := annotationsFor(IdempotentWrite)
	if !idem.IdempotentHint || idem.DestructiveHint == nil || *idem.DestructiveHint {
		t.Errorf("idempotent annotations = %+v", idem)
	}
	destructive := annotationsFor(Destructive)
	if destructive.DestructiveHint == nil || !*destructive.DestructiveHint {
		t.Errorf("destructive annotations = %+v", destructive)
	}
	// The one Kind that reaches outside the spreadsheet, and the only
	// one whose open-world hint is true. That distinction is the reason
	// it is a Kind rather than a flag on an existing one.
	connected := annotationsFor(Connected)
	if connected.OpenWorldHint == nil || !*connected.OpenWorldHint {
		t.Errorf("connected annotations = %+v", connected)
	}
	for _, k := range []Kind{Read, Write, IdempotentWrite, Destructive} {
		if a := annotationsFor(k); a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("kind %v claims an open world; only Connected reaches one", k)
		}
	}
}

func TestDestructiveToolsAskForAPerson(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	d := Deps{Config: config.Config{EnableDestructive: true}}
	add(s, d, Def[withDryRun, fakeOut]{Name: "dangerous", Kind: Destructive,
		Handle: func(context.Context, withDryRun) (fakeOut, error) { return fakeOut{}, nil }})

	tools := listTools(t, s)
	tool, ok := tools["dangerous"]
	if !ok {
		t.Fatal("the destructive tool was not registered with the flag set")
	}
	if tool.Meta["anthropic/requiresUserInteraction"] != true {
		t.Errorf("meta = %v; a client should be asked to involve a person", tool.Meta)
	}
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
// The schema dump describes the whole tool surface, and it built that
// configuration by hand for three phases: ReadOnly off, EnableDestructive
// on. Phase 4 added a second gate and the tool behind it vanished from
// the dump — and so from the schema diff, which is the gate that would
// have said a tool had appeared. Every test was still green.
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
