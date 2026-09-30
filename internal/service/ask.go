package service

import (
	"context"

	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/v3/internal/render"
)

// Asker puts a question to the person using the server before a write
// that cannot be undone, or that runs a query billed to a Cloud project
// (§9a). Ask returns nil when the write may go ahead, and an error to
// return in its place otherwise; the tools layer installs one per call,
// for the tools that ask. build makes the question, and is called only
// when it will be put or checked, so a read that only the question needs
// is not made for a client that cannot ask.
type Asker interface {
	Ask(ctx context.Context, build Build) error
}

// Build makes the question an asking write puts to the person.
type Build func() (render.Question, error)

type askerKey struct{}

// WithAsker returns a context whose asking writes are put to a.
func WithAsker(ctx context.Context, a Asker) context.Context {
	return context.WithValue(ctx, askerKey{}, a)
}

// ask is the last step before an asking write, after every read, every
// other guard and the dry run, so the question shows what the write
// would do and nothing is asked that a guard would refuse anyway. A
// write reached with no asker is refused: only a tool registered to ask
// may make one.
func ask(ctx context.Context, build Build) error {
	a, ok := ctx.Value(askerKey{}).(Asker)
	if !ok {
		return Errorf("blocked", "this write has no way to ask the person, which is a defect in "+
			"this server; nothing was changed")
	}
	return a.Ask(ctx, build)
}

// titleOf is a spreadsheet's title, as a question names it.
func titleOf(sp *gsheets.Spreadsheet) string {
	if sp == nil || sp.Properties == nil {
		return ""
	}
	return sp.Properties.Title
}
