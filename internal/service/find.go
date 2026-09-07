package service

import (
	"context"
	"regexp"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/grid"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// FindRequest is what find_in_spreadsheet asks for.
type FindRequest struct {
	Spreadsheet string
	// Sheet limits the search to one tab. Empty searches every tab.
	Sheet string
	// Query is plain text; Regex is an RE2 pattern. Exactly one.
	Query string
	Regex string
	// MatchCase applies to Query. A regex says so with (?i) itself.
	MatchCase bool
	// SearchFormulas looks under the values as well as at them, and
	// SearchNotes beside them.
	SearchFormulas bool
	SearchNotes    bool
	Budget         Budget
}

// FindResult is find_in_spreadsheet's answer.
type FindResult struct {
	Matches        string    `json:"matches" jsonschema:"the hits as text, each with its sheet and A1 address"`
	Spreadsheet    string    `json:"spreadsheet" jsonschema:"the spreadsheet id"`
	Hits           []FindHit `json:"hits" jsonschema:"one entry per match"`
	CellsRead      int       `json:"cells_read" jsonschema:"how many cells were read to answer this"`
	Truncated      bool      `json:"truncated,omitempty" jsonschema:"true when a limit stopped the search before the end"`
	StoppedBy      string    `json:"stopped_by,omitempty" jsonschema:"which limit stopped it: cells (raise max_cells, or narrow with sheet) or matches (raise max_matches; the cells were read)"`
	DataEndedEarly bool      `json:"data_ended_early,omitempty" jsonschema:"every sheet searched ran out of values before the budget ran out of room, so a wider search will probably find nothing more"`
	SheetsSearched []string  `json:"sheets_searched" jsonschema:"the sheets actually covered, in order"`
}

// FindHit is one match.
type FindHit struct {
	Sheet   string `json:"sheet"`
	Address string `json:"address"`
	Kind    string `json:"kind" jsonschema:"value, formula or note: where the match was found"`
	Text    string `json:"text"`
	Formula string `json:"formula,omitempty"`
}

// Render is the text half.
func (r FindResult) Render() string { return r.Matches }

// Find searches a spreadsheet for text or an RE2 pattern.
//
// The Sheets API has no query endpoint, so this reads and matches here.
// That makes the budget the whole design: the ranges are resolved
// against each sheet's real extent before the call, the whole search is
// one spreadsheets.get with a field mask, and a search that ran out of
// budget says so rather than reporting "no matches" for a spreadsheet it
// only partly read.
func (s *Service) Find(ctx context.Context, req FindRequest) (*FindResult, error) {
	if (req.Query == "") == (req.Regex == "") {
		return nil, Errorf("invalid", "give exactly one of query (plain text) or regex (an RE2 pattern)")
	}
	match, err := matcher(req)
	if err != nil {
		return nil, err
	}
	bud, err := s.budget(req.Budget)
	if err != nil {
		return nil, err
	}
	ref, err := s.Resolve(ctx, req.Spreadsheet)
	if err != nil {
		return nil, err
	}
	sp, err := s.card(ctx, ref.ID)
	if err != nil {
		return nil, err
	}

	sheets, err := s.sheetsToSearch(sp, req.Sheet, ref)
	if err != nil {
		return nil, err
	}
	// The budget is divided across the sheets rather than spent in
	// order. Taken greedily, the first sheet swallows all of it and the
	// rest are searched a row deep or not at all — so a spreadsheet with
	// two sheets answers for one and says only that it was "truncated".
	// A live run found exactly that: a term present on both sheets came
	// back with one match.
	full := make([]a1.Rect, len(sheets))
	sizes := make([]int, len(sheets))
	for i, p := range sheets {
		rows, cols := extent(p)
		full[i] = a1.WholeSheet.Clamp(rows, cols)
		sizes[i], _ = full[i].Cells()
	}
	allowance := Share(bud.Cells, sizes)

	var ranges []string
	var windows []a1.Rect
	var props []*gsheets.SheetProperties
	stopped := render.Complete
	for i, p := range sheets {
		if allowance[i] <= 0 {
			stopped = render.CellBudget
			continue
		}
		window, cut := fit(full[i], allowance[i])
		if cut {
			stopped = render.CellBudget
		}
		ranges = append(ranges, a1.Format(p.Title, window))
		windows = append(windows, window)
		props = append(props, p)
	}
	if len(ranges) == 0 {
		return nil, Errorf("invalid", "max_cells is too small to read even one row")
	}

	// One request, however many sheets: a batch counts once against
	// quota, so several ranges cost what one does.
	got, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{
		Fields: gapi.GridFields, Ranges: ranges, IncludeGridData: true,
	})
	if err != nil {
		return nil, wrap(err)
	}

	res := &FindResult{Spreadsheet: ref.ID}
	// Whether every sheet ran out of data before its window ran out of
	// room. It does not prove the rows below are empty — nothing short
	// of reading them does — but it is the difference between "there is
	// more to look at" and "the grid is allocated and empty".
	dataEndedEarly := true
	var rendered []render.Match
search:
	for i, p := range props {
		res.SheetsSearched = append(res.SheetsSearched, p.Title)
		data, _, _ := sheetData(got, p.SheetID)
		g := grid.Build(p.Title, p.SheetID, windows[i], data, grid.AsRaw)
		if g.DataRows >= windows[i].Rows() {
			dataEndedEarly = false
		}
		for r, row := range g.Cells {
			for c, cell := range row {
				// Counted as they are looked at, not as they are asked
				// for: a search that stops at the first match would
				// otherwise report a whole window as searched, and the
				// number is there to say what the answer cost.
				res.CellsRead++
				kind, text := firstMatch(cell, req, match)
				if kind == "" {
					continue
				}
				if len(res.Hits) >= bud.Matches {
					// A different dial from the cell budget: these cells
					// were read, so raising max_cells would change
					// nothing. But it does not *replace* a cell budget
					// that already bit — raising max_matches would then
					// never reach the sheets the budget excluded, and
					// the caller would be sent to the wrong dial by the
					// very field that exists to stop that.
					if stopped == render.Complete {
						stopped = render.MatchLimit
					}
					break search
				}
				hit := FindHit{Sheet: p.Title, Address: g.Address(r, c), Kind: kind, Text: text, Formula: cell.Formula}
				res.Hits = append(res.Hits, hit)
				rendered = append(rendered, render.Match(hit))
			}
		}
	}
	res.Truncated = stopped != render.Complete
	res.StoppedBy = string(stopped)
	res.DataEndedEarly = dataEndedEarly
	res.Matches = render.Matches(rendered, res.CellsRead, stopped, dataEndedEarly)
	return res, nil
}

// firstMatch reports where in a cell the pattern matched, and the text
// that matched. A model that searched for a number and found it inside a
// formula needs to be told which it was looking at.
func firstMatch(c grid.Cell, req FindRequest, match func(string) bool) (kind, text string) {
	if c.Display != "" && match(c.Display) {
		return "value", c.Display
	}
	if req.SearchFormulas && c.Formula != "" && match(c.Formula) {
		return "formula", c.Formula
	}
	if req.SearchNotes && c.Note != "" && match(c.Note) {
		return "note", c.Note
	}
	return "", ""
}

// Share divides a cell budget across sheets so every one of them is
// looked at.
//
// Max-min fair: each sheet is offered an equal share, a sheet that needs
// less than its share takes what it needs and the surplus goes back to
// the others. A spreadsheet of one big sheet and three small ones
// therefore searches all four, with the big one taking whatever the
// small ones did not.
func Share(total int, sizes []int) []int {
	out := make([]int, len(sizes))
	if len(sizes) == 0 || total <= 0 {
		return out
	}
	remaining := total
	for remaining > 0 {
		unfilled := 0
		for i, size := range sizes {
			if out[i] < size {
				unfilled++
			}
		}
		if unfilled == 0 {
			break
		}
		each := remaining / unfilled
		if each == 0 {
			break
		}
		before := remaining
		for i, size := range sizes {
			want := min(each, size-out[i])
			if want > 0 {
				out[i] += want
				remaining -= want
			}
		}
		if remaining == before {
			break
		}
	}
	// Whatever the integer division left over goes round one cell at a
	// time rather than filling the first sheet. It matters at the small
	// end: a budget below the number of sheets should still reach as
	// many of them as it can, since one row of one sheet answers less
	// than one row of each.
	for remaining > 0 {
		gave := false
		for i, size := range sizes {
			if remaining <= 0 {
				break
			}
			if out[i] < size {
				out[i]++
				remaining--
				gave = true
			}
		}
		if !gave {
			break
		}
	}
	return out
}

func matcher(req FindRequest) (func(string) bool, error) {
	if req.Regex != "" {
		re, err := regexp.Compile(req.Regex)
		if err != nil {
			return nil, Errorf("invalid", "regex %q does not compile: %v. This is RE2: no backreferences and no lookaround", req.Regex, err)
		}
		return re.MatchString, nil
	}
	if req.MatchCase {
		return func(s string) bool { return strings.Contains(s, req.Query) }, nil
	}
	// Folding in place rather than lowering a copy of every cell: a
	// search over the 50 000-cell budget looks at each cell up to three
	// times, and strings.ToLower allocates on all but the ones that are
	// already lower case.
	re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(req.Query))
	if err != nil {
		return nil, Errorf("invalid", "query %q could not be matched: %v", req.Query, err)
	}
	return re.MatchString, nil
}

// sheetsToSearch is every grid sheet, or the one named.
func (s *Service) sheetsToSearch(sp *gsheets.Spreadsheet, name string, ref Reference) ([]*gsheets.SheetProperties, error) {
	if strings.TrimSpace(name) != "" || ref.HasGid {
		p, err := s.findSheet(sp, strings.TrimSpace(name), ref)
		if err != nil {
			return nil, err
		}
		return []*gsheets.SheetProperties{p}, nil
	}
	var out []*gsheets.SheetProperties
	for _, sh := range sp.Sheets {
		if sh.Properties == nil {
			continue
		}
		// A chart-only sheet has no cells to search.
		if sh.Properties.SheetType != "" && sh.Properties.SheetType != "GRID" {
			continue
		}
		out = append(out, sh.Properties)
	}
	if len(out) == 0 {
		return nil, Errorf("not_found", "this spreadsheet has no grid sheet to search")
	}
	return out, nil
}
