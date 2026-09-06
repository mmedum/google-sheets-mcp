// Package service is where the rules live. Tools validate their input
// and shape a result; everything worth testing happens here.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mmedum/google-sheets-mcp/internal/config"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// API is the part of the Google client this package uses. An interface
// so the tests can drive the service without a network and without the
// retry loop's timing.
type API interface {
	GetSpreadsheet(ctx context.Context, id string, o gapi.GetOptions) (*gsheets.Spreadsheet, error)
	GetValues(ctx context.Context, id, a1Range string, o gapi.ValueOptions) (*gsheets.ValueRange, error)
	SearchSpreadsheets(ctx context.Context, q string, limit int, pageToken string) (*gapi.FileList, error)
	SearchDeveloperMetadata(ctx context.Context, id string, filters []*gsheets.DataFilter) (*gsheets.SearchDeveloperMetadataResponse, error)
	GetFile(ctx context.Context, id string) (*gapi.File, error)
	About(ctx context.Context) (*gapi.User, error)

	UpdateValues(ctx context.Context, id, a1Range string, values [][]any, o gapi.WriteOptions) (*gsheets.UpdateValuesResponse, error)
	AppendValues(ctx context.Context, id, a1Range string, values [][]any, o gapi.WriteOptions) (*gsheets.AppendValuesResponse, error)
	ClearValues(ctx context.Context, id, a1Range string) (*gsheets.ClearValuesResponse, error)
	CreateSpreadsheet(ctx context.Context, in *gsheets.NewSpreadsheet) (*gsheets.Spreadsheet, error)
	BatchUpdate(ctx context.Context, id string, in *gsheets.BatchUpdateSpreadsheetRequest) (*gsheets.BatchUpdateSpreadsheetResponse, error)
	CopySheetTo(ctx context.Context, id string, sheetID int, destination string) (*gsheets.SheetProperties, error)
}

// Classes is the closed vocabulary of error classes this server speaks.
//
// Twelve, where the shared standard has six. Each addition earns its
// place by asking the caller to do something the others do not:
// `blocked` is the write guard refusing what the API would have allowed;
// `ambiguous` says a reference matched several things and the caller
// must choose; `ambiguous_outcome` says a write may or may not have
// landed and the caller must go and look. Collapsing either of the last
// two into `invalid` loses the instruction.
//
// A gate holds this list shut from both sides: a class emitted and not
// declared fails, and so does one declared and never emitted.
func Classes() []string {
	return []string{
		"auth",              // no usable credentials
		"forbidden",         // the account may not do this
		"not_found",         // the thing named does not exist
		"ambiguous",         // the reference matched several things; choose one
		"invalid",           // the request cannot be built from these arguments
		"blocked",           // this server refuses; the API would have allowed it
		"conflict",          // the spreadsheet changed under a checkpoint
		"stale",             // a continuation or checkpoint no longer applies
		"unsupported",       // the API or this configuration cannot do it
		"rate_limited",      // quota; back off and retry
		"unavailable",       // Google could not be reached or could not answer
		"ambiguous_outcome", // a write may or may not have landed; go and look
	}
}

// Error is a tool-facing failure: "[class] actionable message".
//
// These are tool results rather than protocol errors, so the model sees
// them and can act. The message says what to do next, and it is never
// logged as text: it quotes the spreadsheet back — the sheet titles that
// do exist, the range that could not be parsed — which is the right
// answer to give a model and the wrong thing to write into a log.
type Error struct {
	Class string
	Msg   string
}

func (e *Error) Error() string { return "[" + e.Class + "] " + e.Msg }

// Errorf builds an Error.
func Errorf(class, format string, args ...any) *Error {
	return &Error{Class: class, Msg: fmt.Sprintf(format, args...)}
}

// wrap turns a client error into a tool-facing one, keeping Google's own
// meaning: a quota refusal says it is a quota refusal, rather than
// becoming something the person is told to go and fix.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	var se *Error
	if errors.As(err, &se) {
		return se
	}
	class := gapi.Class(err)
	msg := err.Error()
	switch class {
	case "auth":
		// The underlying error often already names the fix; saying it
		// twice reads like two different instructions.
		if strings.Contains(msg, "login") {
			return Errorf("auth", "%s", msg)
		}
		return Errorf("auth", "%s; run `google-sheets-mcp login`", msg)
	case "forbidden":
		if errors.Is(err, gapi.ErrMissingScope) {
			return Errorf("forbidden", "%s; the granted scopes do not cover this call, so re-run `google-sheets-mcp login`", msg)
		}
		return Errorf("forbidden", "%s", msg)
	case "rate_limited":
		return Errorf("rate_limited", "%s; this is Google's per-minute quota, which refills, so retry shortly", msg)
	}
	return &Error{Class: class, Msg: msg}
}

// Deps are what the service needs.
type Deps struct {
	API    API
	Config config.Config
	Logger *slog.Logger
	// Now is replaced in tests that exercise the metadata cache.
	Now func() time.Time
}

// Service holds the dependencies and the small cache in front of them.
type Service struct {
	api API
	cfg config.Config
	log *slog.Logger
	now func() time.Time

	mu    sync.Mutex
	cards map[string]cacheEntry
}

type cacheEntry struct {
	card *gsheets.Spreadsheet
	at   time.Time
}

// CacheWindow is how long a spreadsheet's metadata is reused.
//
// Long enough to span a model's turn, and no longer. Every tool call
// that names a sheet reads the card first, so a turn that calls
// get_spreadsheet and then two read_ranges pays for the metadata once
// instead of three times — and with one request in flight and sixty a
// minute, each saved call is about a second. At five seconds it never
// fired: the gap between two tool calls is model latency, not machine
// latency.
//
// Nothing depends on it for correctness. The cost of a stale entry is a
// sheet renamed inside the window, which fails loudly on the next call
// rather than reading the wrong rectangle.
const CacheWindow = 30 * time.Second

// New builds a Service.
func New(d Deps) *Service {
	s := &Service{api: d.API, cfg: d.Config, log: d.Logger, now: d.Now, cards: map[string]cacheEntry{}}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// card fetches a spreadsheet's metadata, reusing a recent copy.
func (s *Service) card(ctx context.Context, id string) (*gsheets.Spreadsheet, error) {
	s.mu.Lock()
	if e, ok := s.cards[id]; ok && s.now().Sub(e.at) < CacheWindow {
		s.mu.Unlock()
		return e.card, nil
	}
	s.mu.Unlock()

	sp, err := s.api.GetSpreadsheet(ctx, id, gapi.GetOptions{Fields: gapi.CardFields})
	if err != nil {
		return nil, wrap(err)
	}
	s.mu.Lock()
	s.cards[id] = cacheEntry{card: sp, at: s.now()}
	s.mu.Unlock()
	return sp, nil
}

// Rendered is what every tool's output type implements. Registration
// constrains the type parameter to it, so a tool that returns a result
// with no rendering does not compile — which is the only way a rule kept
// at twenty call sites stays kept.
type Rendered interface {
	Render() string
}

// join phrases a list for a message. One implementation, in the
// renderer, because a refusal and a result listing the same things
// should read the same way.
func join(items []string) string { return render.JoinAnd(items) }
