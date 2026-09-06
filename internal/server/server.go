// Package server wires the MCP SDK to the tools and offers a schema dump
// through an in-memory client session.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/internal/config"
	"github.com/mmedum/google-sheets-mcp/internal/service"
	"github.com/mmedum/google-sheets-mcp/internal/tools"
)

// Name is the MCP server name.
const Name = "google-sheets-mcp"

// SDKVersion is recorded in schema dumps, so a diff caused by an SDK
// upgrade can be told apart from a change to the tool surface.
const SDKVersion = "v1.7.0"

const instructions = "Google Sheets tools that work inside one spreadsheet. " +
	"Start with get_spreadsheet: it costs the same on any size of spreadsheet and gives the exact sheet titles every " +
	"other call needs. Never guess a sheet name — Google names the first sheet in the account's language, so it is " +
	"often not an English word — and never build a range by joining a title to a range yourself; pass sheet and range " +
	"separately and this server quotes the title, which is what keeps a whole-sheet reference meaning the sheet rather " +
	"than a named range that happens to share its name. " +
	"Then read_range for cells, with show=both when you need to see which numbers are computed, or " +
	"find_in_spreadsheet to locate something first. Ranges are A1 throughout; this server does the index arithmetic. " +
	"Reads are budgeted and say where to continue. " +
	"Files, folders, sharing, revisions and comment threads are not here: they belong to a server built on the Drive " +
	"API. A cell note is a Sheets field and is here."

// Deps are what the server needs.
type Deps struct {
	Service *service.Service
	Config  config.Config
	Logger  *slog.Logger
	Version string
}

// New builds the MCP server with every tool the configuration allows.
func New(d Deps) *mcp.Server {
	opts := &mcp.ServerOptions{Instructions: instructions}
	// The SDK's own logger writes session chatter, never a JSON-RPC
	// frame. At info it would put two lines per session into every
	// client's log file for nothing, so it is attached only at debug.
	if d.Logger != nil && d.Logger.Enabled(context.Background(), slog.LevelDebug) {
		opts.Logger = d.Logger
	}
	s := mcp.NewServer(&mcp.Implementation{Name: Name, Version: d.Version}, opts)
	if d.Logger != nil {
		s.AddReceivingMiddleware(logCalls(d.Logger))
	}
	tools.Register(s, tools.Deps{Service: d.Service, Config: d.Config, Logger: d.Logger})
	return s
}

// logCalls records that a call happened and how it went, and nothing
// about what it carried.
//
// The fields are the method, the tool name, the outcome and the
// duration. Arguments and results are the person's spreadsheet — cell
// values, formulas, sheet titles, ranges, search terms — and a log
// somebody is asked to paste into a bug report has to be safe to paste
// by construction rather than by their vigilance. The tool name is the
// one part of a request that comes from this server's own schema.
func logCalls(logger *slog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if !logger.Enabled(ctx, slog.LevelDebug) {
				return next(ctx, method, req)
			}
			start := time.Now()
			res, err := next(ctx, method, req)
			attrs := []any{"method", method, "ms", time.Since(start).Milliseconds()}
			if ct, ok := req.(*mcp.CallToolRequest); ok && ct.Params != nil {
				attrs = append(attrs, "tool", ct.Params.Name)
			}
			if err != nil {
				attrs = append(attrs, "failed", true)
			} else if ctr, ok := res.(*mcp.CallToolResult); ok && ctr != nil {
				// Whether the call refused, never what it said: a
				// refusal is where a message is most tempted to quote
				// what it refused.
				attrs = append(attrs, "tool_error", ctr.IsError)
			}
			logger.Debug("mcp call", attrs...)
			return res, err
		}
	}
}

// DumpSchemas writes the tool list as the wire would carry it. The SDK
// has no public enumerator, so an in-memory client asks the server —
// which also proves the schemas are well formed, since one invalid
// schema is not one broken tool but a whole session a validating client
// rejects.
func DumpSchemas(ctx context.Context, s *mcp.Server, w io.Writer, version string) error {
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		return fmt.Errorf("connect server: %w", err)
	}
	defer func() { _ = ss.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "schema-dump", Version: version}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		return fmt.Errorf("connect client: %w", err)
	}
	defer func() { _ = cs.Close() }()

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}
	sort.Slice(res.Tools, func(i, j int) bool { return res.Tools[i].Name < res.Tools[j].Name })
	tmpls, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil {
		return fmt.Errorf("list resource templates: %w", err)
	}
	sort.Slice(tmpls.ResourceTemplates, func(i, j int) bool {
		return tmpls.ResourceTemplates[i].URITemplate < tmpls.ResourceTemplates[j].URITemplate
	})

	out := struct {
		Server            string                  `json:"server"`
		Version           string                  `json:"version"`
		SDK               string                  `json:"sdk"`
		Tools             []*mcp.Tool             `json:"tools"`
		ResourceTemplates []*mcp.ResourceTemplate `json:"resourceTemplates"`
	}{Name, version, SDKVersion, res.Tools, tmpls.ResourceTemplates}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
