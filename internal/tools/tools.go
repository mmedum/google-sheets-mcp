// Package tools registers the MCP tools. A handler validates its input,
// calls the service and hands back a result; every rule worth testing
// lives in the service.
//
// Registration is one function rather than a convention, because four
// rules kept by hand at twenty call sites are four ways to be quietly
// wrong. One Kind per tool decides its annotations, whether read-only
// mode keeps it, whether the client is asked to involve a person, and
// that its reply is rendered rather than dumped as JSON.
//
// Registration gates the tool; the service gates the act. `confirm` and
// the one gated action live in the service, where the caller can be told
// why — which is what stops Kind becoming a matrix.
package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/internal/config"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// Kind says which world a tool touches. It is an enum over that rather
// than a bag of flags, so it grows an axis the first time a tool touches
// a second one. This server has no disk axis at all: nothing is
// downloaded or uploaded and every value is inline.
type Kind int

// Kinds.
const (
	// Read changes nothing and is registered in read-only mode.
	Read Kind = iota
	// Write changes the spreadsheet. Repeating it is not the same as
	// doing it once.
	Write
	// IdempotentWrite changes the spreadsheet to a stated end state, so
	// repeating it lands in the same place.
	IdempotentWrite
	// Destructive removes something Sheets cannot bring back. It is not
	// registered at all unless the destructive flag is set, and it still
	// needs confirm on the call.
	Destructive
)

// Deps are what the tools need.
type Deps struct {
	Service *service.Service
	Config  config.Config
	Logger  *slog.Logger
}

// Register adds every tool the configuration allows.
func Register(s *mcp.Server, d Deps) {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	registerRead(s, d)
	registerWrite(s, d)
}

// Def is one tool.
//
// Out is constrained to service.Rendered, so a tool whose result has no
// rendering does not compile. That is the mechanism behind §4.9: every
// tool returns both halves and the structured half carries the
// rendering, so no client can be shown the half without the addresses in
// it.
type Def[In any, Out service.Rendered] struct {
	Name        string
	Description string
	Kind        Kind
	Handle      func(ctx context.Context, in In) (Out, error)
}

// add registers one tool if the configuration allows it.
func add[In any, Out service.Rendered](s *mcp.Server, d Deps, def Def[In, Out]) {
	// Before the registration gate, not after. Checked afterwards, a
	// destructive tool would never be checked in the default
	// configuration and no write tool would be checked under
	// GSHEETS_READ_ONLY — so the rule would hold only because the schema
	// dump happens to force the full surface. The check is pure and
	// costs nothing, so every Def is validated whether or not this
	// configuration registers it.
	if err := checkDryRun(def.Kind, reflect.TypeFor[In]()); err != nil {
		// A programming error, found the first time the server starts.
		// Failing loudly is the point: a write tool that offers dry_run
		// and ignores it is a preview that wrote.
		panic(fmt.Sprintf("tools: %s: %v", def.Name, err))
	}
	if !allowed(def.Kind, d.Config) {
		return
	}
	tool := &mcp.Tool{
		Name:        def.Name,
		Description: def.Description,
		Annotations: annotationsFor(def.Kind),
	}
	if def.Kind == Destructive {
		tool.Meta = mcp.Meta{"anthropic/requiresUserInteraction": true}
	}
	mcp.AddTool(s, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := def.Handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, fail(err)
		}
		// Content is set here, always. Left unset the SDK fills it with
		// the JSON of the output, which is the same bytes twice and the
		// one shape the specification asks callers not to send.
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: out.Render()}},
		}, out, nil
	})
}

// allowed applies the two registration gates. Both are server-side:
// annotations are hints the specification says a client may not trust,
// and a host in an auto-approve mode runs an annotated tool without
// asking anybody.
func allowed(k Kind, cfg config.Config) bool {
	switch k {
	case Read:
		return true
	case Destructive:
		return cfg.EnableDestructive && !cfg.ReadOnly
	default:
		return !cfg.ReadOnly
	}
}

func annotationsFor(k Kind) *mcp.ToolAnnotations {
	no, yes := new(false), new(true)
	switch k {
	case Read:
		return &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: no}
	case IdempotentWrite:
		return &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: no, OpenWorldHint: no}
	case Destructive:
		return &mcp.ToolAnnotations{DestructiveHint: yes, OpenWorldHint: no}
	default:
		return &mcp.ToolAnnotations{DestructiveHint: no, OpenWorldHint: no}
	}
}

// DryRunField is the name a write tool's preview flag must have.
const DryRunField = "dry_run"

// checkDryRun finds the flag by reflection rather than trusting each
// tool to declare it.
//
// A write that offers dry_run and does not honour it is a preview that
// wrote, so the flag is looked for in the type rather than remembered
// per tool. Reads have nothing to preview and must not offer one.
func checkDryRun(k Kind, in reflect.Type) error {
	if in == nil || in.Kind() != reflect.Struct {
		return nil
	}
	found := false
	for i := range in.NumField() {
		f := in.Field(i)
		if jsonName(f.Tag.Get("json")) != DryRunField {
			continue
		}
		if f.Type.Kind() != reflect.Bool {
			return fmt.Errorf("%s must be a bool, not %s", DryRunField, f.Type)
		}
		found = true
	}
	switch {
	case k == Read && found:
		return errors.New("a read has nothing to preview, so it must not offer " + DryRunField)
	case k != Read && !found:
		return errors.New("a write must offer " + DryRunField + "; a caller that cannot preview will not")
	}
	return nil
}

func jsonName(tag string) string {
	for i := 0; i < len(tag); i++ {
		if tag[i] == ',' {
			return tag[:i]
		}
	}
	return tag
}

// fail turns a service error into the "[class] message" a tool result
// carries. The SDK marks a returned error as isError, which is what the
// specification asks for: an error a model can read and act on, not a
// protocol failure it never sees.
//
// Anything unclassified goes through service.Errorf rather than having
// its prefix written here. A hand-built "[unavailable] " would be the
// one class emission the vocabulary gate cannot see, and the next one
// somebody writes would not have to be a class at all.
func fail(err error) error {
	var se *service.Error
	if !errors.As(err, &se) {
		se = service.Errorf("unavailable", "%s", err)
	}
	return errors.New(se.Error())
}
