package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// CardResult is get_spreadsheet's answer. The rendering travels inside
// the structured half as well as in the text one, so no client is shown
// the half without the sheet names in it.
type CardResult struct {
	Card        string   `json:"card" jsonschema:"the spreadsheet card as text: title, sheets with their ids and sizes, named ranges, tables, protected ranges and filter views"`
	Spreadsheet string   `json:"spreadsheet" jsonschema:"the spreadsheet id"`
	Title       string   `json:"title" jsonschema:"the spreadsheet title"`
	Link        string   `json:"link" jsonschema:"a URL that opens the spreadsheet"`
	Sheets      []string `json:"sheets" jsonschema:"every sheet title, in order, as later calls must name them"`
}

// Render is the text half.
func (r CardResult) Render() string { return r.Card }

// Card reads a spreadsheet's metadata and describes it.
//
// One spreadsheets.get with a field mask and no grid data, so it costs
// the same on a spreadsheet of ten cells and one of ten million. It is
// the first call: every later one names a sheet, and sheet names are
// read here rather than guessed anywhere.
func (s *Service) Card(ctx context.Context, ref string) (*CardResult, error) {
	r, err := s.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	sp, err := s.card(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	return s.describe(ctx, sp, r), nil
}

// describe turns a spreadsheets.get response into the card.
//
// Separate from Card because a create already holds the response and
// must not fetch it again: with one request in flight and sixty a
// minute, an avoidable call costs a second of somebody's wall clock.
func (s *Service) describe(ctx context.Context, sp *gsheets.Spreadsheet, r Reference) *CardResult {
	c := render.Card{ID: sp.SpreadsheetID}
	if c.ID == "" {
		c.ID = r.ID
	}
	// Built rather than taken from the response. Google's own
	// spreadsheetUrl carries an `?ouid=` query holding the signed-in
	// account's obfuscated id; the link this builds opens the same
	// spreadsheet and identifies nobody.
	c.Link = gapi.SpreadsheetURL(c.ID)
	if p := sp.Properties; p != nil {
		c.Title, c.Locale, c.TimeZone, c.Recalc = p.Title, p.Locale, p.TimeZone, p.AutoRecalc
	}
	// Drive knows the owner, the folder and the modification time;
	// Sheets does not. When the reference was a title, the search that
	// resolved it already carried all three, so asking again would be a
	// second request for bytes this call has in hand.
	f := r.File
	if f == nil {
		f, _ = s.api.GetFile(ctx, c.ID)
	}
	if f != nil {
		c.Modified = f.ModifiedTime
		if len(f.Owners) > 0 {
			c.Owner = f.Owners[0].EmailAddress
		}
	}

	titles, byID := sheetTitles(sp)
	for _, sh := range sp.Sheets {
		if sh.Properties == nil {
			continue
		}
		c.Sheets = append(c.Sheets, cardSheet(sh))
		c.Tables = append(c.Tables, tableItems(byID, sh)...)
		c.Protected = append(c.Protected, protectedItems(byID, sh)...)
		for _, f := range sh.FilterViews {
			c.FilterViews = append(c.FilterViews, render.NamedItem{Name: f.Title, Range: rangeText(byID, f.Range)})
		}
	}
	c.NamedRanges = namedRangeItems(byID, sp, titleSet(titles))

	return &CardResult{
		Card: render.Spreadsheet(c), Spreadsheet: c.ID, Title: c.Title, Link: c.Link, Sheets: titles,
	}
}

// sheetTitles reads the titles in order, and the id-to-title map every
// GridRange in the response has to be resolved through.
func sheetTitles(sp *gsheets.Spreadsheet) ([]string, map[int]string) {
	titles := make([]string, 0, len(sp.Sheets))
	byID := make(map[int]string, len(sp.Sheets))
	for _, sh := range sp.Sheets {
		if sh.Properties == nil {
			continue
		}
		titles = append(titles, sh.Properties.Title)
		byID[sh.Properties.SheetID] = sh.Properties.Title
	}
	return titles, byID
}

// cardSheet describes one tab, including what it holds that no read of
// the values would reveal.
func cardSheet(sh *gsheets.Sheet) render.CardSheet {
	p := sh.Properties
	cs := render.CardSheet{
		Title: p.Title, ID: p.SheetID, Index: deref(p.Index), Type: p.SheetType,
		Hidden: p.Hidden, TabColor: tabColor(p.TabColorStyle),
	}
	if g := p.GridProperties; g != nil {
		cs.Rows, cs.Cols = g.RowCount, g.ColumnCount
		cs.FrozenRows, cs.FrozenCols = g.FrozenRowCount, g.FrozenColumnCount
	}
	for _, h := range []struct {
		n    int
		what string
	}{
		{len(sh.Tables), "table"},
		{len(sh.Charts), "chart"},
		{len(sh.Slicers), "slicer"},
		{len(sh.Merges), "merge"},
		{len(sh.BandedRanges), "banded range"},
		{len(sh.ConditionalFormats), "conditional format rule"},
	} {
		if h.n > 0 {
			cs.Holds = append(cs.Holds, render.Plural(h.n, h.what))
		}
	}
	// The charts by name, which is what makes the count worth reading: a
	// sheet that "holds 3 charts" says nothing a caller can act on, and
	// the titles cost about 70 bytes each on a mask already being paid
	// for. An untitled chart is named by its id, because that is what
	// manage_chart takes.
	cs.Charts = chartNames(sh)
	return cs
}

// chartNames lists a sheet's charts the way a caller would name one.
func chartNames(sh *gsheets.Sheet) []string {
	out := make([]string, 0, len(sh.Charts))
	for _, c := range sh.Charts {
		var spec struct {
			Title string `json:"title"`
		}
		if len(c.Spec) > 0 {
			_ = json.Unmarshal(c.Spec, &spec)
		}
		if spec.Title == "" {
			out = append(out, fmt.Sprintf("id %d", c.ChartID))
			continue
		}
		out = append(out, spec.Title)
	}
	return out
}

func tableItems(byID map[int]string, sh *gsheets.Sheet) []render.NamedItem {
	out := make([]render.NamedItem, 0, len(sh.Tables))
	for _, t := range sh.Tables {
		out = append(out, render.NamedItem{Name: t.Name, Range: rangeText(byID, t.Range), Detail: columnTypes(t)})
	}
	return out
}

func protectedItems(byID map[int]string, sh *gsheets.Sheet) []render.NamedItem {
	out := make([]render.NamedItem, 0, len(sh.ProtectedRanges))
	for _, p := range sh.ProtectedRanges {
		detail := render.ProtectionState(p.RequestingUserCanEdit, p.WarningOnly)
		name := p.Description
		if name == "" {
			name = fmt.Sprintf("protected range %d", p.ProtectedRangeID)
		}
		out = append(out, render.NamedItem{Name: name, Range: rangeText(byID, p.Range), Detail: detail})
	}
	return out
}

// namedRangeItems calls out the trap on the card, where somebody will
// read it: an unquoted reference to a name that is also a sheet title
// resolves to the named range and reads a different rectangle.
func namedRangeItems(byID map[int]string, sp *gsheets.Spreadsheet, titles map[string]struct{}) []render.NamedItem {
	out := make([]render.NamedItem, 0, len(sp.NamedRanges))
	for _, n := range sp.NamedRanges {
		item := render.NamedItem{Name: n.Name, Range: rangeText(byID, n.Range)}
		if _, clash := titles[n.Name]; clash {
			item.Detail = "shares its name with a sheet; this server always quotes a sheet title, so the two stay apart"
		}
		out = append(out, item)
	}
	return out
}

func titleSet(titles []string) map[string]struct{} {
	m := make(map[string]struct{}, len(titles))
	for _, t := range titles {
		m[t] = struct{}{}
	}
	return m
}

func columnTypes(t *gsheets.Table) string {
	if len(t.ColumnProperties) == 0 {
		return ""
	}
	parts := make([]string, 0, len(t.ColumnProperties))
	for _, c := range t.ColumnProperties {
		parts = append(parts, c.ColumnName+" "+c.ColumnType)
	}
	return join(parts)
}

// rangeText renders a GridRange in A1, with its sheet quoted.
func rangeText(byID map[int]string, g *gsheets.GridRange) string {
	if g == nil {
		return ""
	}
	title, ok := byID[g.SheetID]
	if !ok {
		return a1.FormatRect(a1.FromGridRange(g))
	}
	return a1.Format(title, a1.FromGridRange(g))
}

func tabColor(cs *gsheets.ColorStyle) string {
	if cs == nil {
		return ""
	}
	if cs.ThemeColor != "" {
		return cs.ThemeColor
	}
	if cs.RGBColor == nil {
		return ""
	}
	c := cs.RGBColor
	return fmt.Sprintf("#%02x%02x%02x", int(c.Red*255+0.5), int(c.Green*255+0.5), int(c.Blue*255+0.5))
}

// deref reads an optional API integer, treating "unset" as zero. Sheet
// index is the one that matters: absent means Google has not said, and
// for a card that is the same answer as first.
func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
