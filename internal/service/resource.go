package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/config"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// MaxResourceChars is what one resource hands back.
//
// A resource is attached rather than asked for, so nothing downstream
// gets to say "show me less". The number is the read tools' own ceiling
// (config.MaxMaxChars), which is what a caller may already ask
// read_range for, so a resource can never be the expensive way to read
// something.
const MaxResourceChars = config.MaxMaxChars

// SheetCSVResult is one sheet's used range.
type SheetCSVResult struct {
	// Range is the used range in A1, which is what the CSV covers — not
	// the sheet's allocated size. It carries the sheet title, so the
	// resource's note can name the sheet without a field of its own.
	Range string
	CSV   string
	// Note is empty unless something was left out, and says what.
	Note string
}

// SheetCSV reads one sheet's used range as CSV.
//
// The used range, not the sheet: a new sheet is 1 000 by 26 whatever is
// on it, and handing back 26 000 commas describes nothing. The window
// sent to Google is still the allocated size clamped to a cell budget,
// because the used range is not knowable without reading — but what
// comes back is trimmed to the last row and column that hold anything.
func (s *Service) SheetCSV(ctx context.Context, spreadsheet, sheet string) (*SheetCSVResult, error) {
	at, err := s.locateRect(ctx, spreadsheet, sheet, "")
	if err != nil {
		return nil, err
	}
	ref, props, full := at.ref, at.props, at.rect

	// Bounded before the request, like every other read here: an
	// open-ended range never becomes an unbounded fetch (§4.6).
	window, cellsCut := fit(full, config.MaxMaxCells)
	got, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{
		Fields: gapi.GridFields, Ranges: []string{a1.Format(props.Title, window)}, IncludeGridData: true,
	})
	if err != nil {
		return nil, wrap(err)
	}
	data, _, _ := sheetData(got, props.SheetID)
	g := grid.Build(props.Title, props.SheetID, window, data, grid.AsRaw)

	used, empty := usedRange(g)
	res := &SheetCSVResult{}
	if empty {
		res.Range = a1.Format(props.Title, a1.Rect{
			FirstRow: window.FirstRow, FirstCol: window.FirstCol,
			LastRow: window.FirstRow, LastCol: window.FirstCol,
		})
		res.Note = fmt.Sprintf("%q is empty.", props.Title)
		return res, nil
	}
	res.Range = a1.Format(props.Title, used)

	// Trimmed before rendering, not after. The window is the sheet's
	// allocated size and the used range is usually a fraction of it, so
	// rendering the whole window first materialises every empty cell as
	// a string only to slice them away — 388 KB and a thousand
	// allocations to describe sixty cells, on a sparse sheet.
	g.Cells = g.Cells[:used.LastRow-window.FirstRow+1]
	for i := range g.Cells {
		g.Cells[i] = g.Cells[i][:used.LastCol-window.FirstCol+1]
	}
	g.Rect = used
	rows := render.Rows(g, render.ShowValues)

	csv, shownRows := render.SeparatedWithin(rows, ',', MaxResourceChars)
	res.CSV = csv

	var notes []string
	switch {
	case shownRows < len(rows):
		notes = append(notes, fmt.Sprintf("%d of %d rows; the rest is past this resource's %d-character limit",
			shownRows, len(rows), MaxResourceChars))
	case len(csv) > MaxResourceChars:
		// Every row was written and the result is still over budget,
		// which happens when one row alone exceeds it — 26 cells at the
		// 50 000-character cell limit is 1.3 MB. The row count cannot
		// say this, and a resource that hands back three times its own
		// stated limit in silence is the silent half-answer again.
		notes = append(notes, fmt.Sprintf("%d characters, past this resource's %d-character limit, because a single "+
			"row exceeds it on its own", len(csv), MaxResourceChars))
	}
	if cellsCut {
		notes = append(notes, fmt.Sprintf("the sheet is %d rows and this read stopped at row %d",
			full.LastRow, window.LastRow))
	}
	if len(notes) > 0 {
		notes = append(notes, "read_range reads on from where this stops")
		res.Note = "Cut short: " + strings.Join(notes, "; ") + "."
	}
	return res, nil
}

// usedRange is the rectangle that holds anything, which is what a person
// means by "the sheet". empty is true when nothing does.
func usedRange(g *grid.Grid) (a1.Rect, bool) {
	if g.DataRows == 0 {
		return a1.Rect{}, true
	}
	lastCol := -1
	// Only the rows that hold anything. Everything past DataRows is
	// empty by construction, and scanning it is 26 000 comparisons on a
	// sheet with twenty rows on it.
	for _, row := range g.Cells[:g.DataRows] {
		for j := len(row) - 1; j > lastCol; j-- {
			if !row[j].Empty() {
				lastCol = j
				break
			}
		}
	}
	if lastCol < 0 {
		return a1.Rect{}, true
	}
	return a1.Rect{
		FirstRow: g.Rect.FirstRow, FirstCol: g.Rect.FirstCol,
		LastRow: g.Rect.FirstRow + g.DataRows - 1,
		LastCol: g.Rect.FirstCol + lastCol,
	}, false
}
