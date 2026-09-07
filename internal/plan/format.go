package plan

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// The formatting builders. One repeatCell carries a number format, a
// font, a background, an alignment, a wrap and a note, so a call that
// sets five of them costs one request rather than five.
//
// The mask is the whole mechanism. A field named in it and absent from
// the body is set back to its default, which is how bold is turned off
// and how a background is cleared. So a patch records what it set, and
// nothing it was not asked about is ever named.

// Patch is a cell format under construction, with the field mask that
// names exactly what has been set on it.
//
// Built rather than composed from a struct literal because the mask and
// the value have to agree: a struct carrying bold=false says nothing
// about whether the caller asked for it, and a mask written by hand
// beside it is a second place to be wrong.
type Patch struct {
	cell   gsheets.CellData
	fields []string
}

// format returns the cell's format, making it when the first field
// needs it.
func (p *Patch) format() *gsheets.CellFormat {
	if p.cell.UserEnteredFormat == nil {
		p.cell.UserEnteredFormat = &gsheets.CellFormat{}
	}
	return p.cell.UserEnteredFormat
}

// text returns the format's font, making it when the first field needs
// it.
func (p *Patch) text() *gsheets.TextFormat {
	f := p.format()
	if f.TextFormat == nil {
		f.TextFormat = &gsheets.TextFormat{}
	}
	return f.TextFormat
}

func (p *Patch) set(field string) { p.fields = append(p.fields, field) }

// Empty reports whether nothing has been set, so a caller does not send
// a request that changes nothing — which the API refuses rather than
// treating as a no-op.
func (p *Patch) Empty() bool { return len(p.fields) == 0 }

// NumberFormat sets how a value is displayed. An empty pattern leaves
// Google's default for the type.
func (p *Patch) NumberFormat(kind, pattern string) {
	p.format().NumberFormat = &gsheets.NumberFormat{Type: kind, Pattern: pattern}
	p.set("userEnteredFormat.numberFormat")
}

// Bold sets the bold switch.
//
// Passing false turns it off, which is why the caller's own argument is
// three-valued and this one is not: "leave it alone" is expressed by not
// calling the setter, so the field never reaches the mask.
func (p *Patch) Bold(v bool) {
	p.text().Bold = v
	p.set("userEnteredFormat.textFormat.bold")
}

// Italic sets the italic switch.
func (p *Patch) Italic(v bool) {
	p.text().Italic = v
	p.set("userEnteredFormat.textFormat.italic")
}

// Underline sets the underline switch.
func (p *Patch) Underline(v bool) {
	p.text().Underline = v
	p.set("userEnteredFormat.textFormat.underline")
}

// Strikethrough sets the strikethrough switch.
func (p *Patch) Strikethrough(v bool) {
	p.text().Strikethrough = v
	p.set("userEnteredFormat.textFormat.strikethrough")
}

// FontSize sets the point size.
func (p *Patch) FontSize(pt int) {
	p.text().FontSize = pt
	p.set("userEnteredFormat.textFormat.fontSize")
}

// FontFamily sets the typeface.
func (p *Patch) FontFamily(name string) {
	p.text().FontFamily = name
	p.set("userEnteredFormat.textFormat.fontFamily")
}

// TextColour sets the font colour. A nil style clears it, which the mask
// makes possible.
func (p *Patch) TextColour(style *gsheets.ColorStyle) {
	p.text().ForegroundColorStyle = style
	p.set("userEnteredFormat.textFormat.foregroundColorStyle")
}

// Background sets the cell's fill. A nil style clears it.
func (p *Patch) Background(style *gsheets.ColorStyle) {
	p.format().BackgroundColorStyle = style
	p.set("userEnteredFormat.backgroundColorStyle")
}

// HorizontalAlign sets left, centre or right.
func (p *Patch) HorizontalAlign(v string) {
	p.format().HorizontalAlign = v
	p.set("userEnteredFormat.horizontalAlignment")
}

// VerticalAlign sets top, middle or bottom.
func (p *Patch) VerticalAlign(v string) {
	p.format().VerticalAlign = v
	p.set("userEnteredFormat.verticalAlignment")
}

// Wrap sets what happens to text too long for its cell.
func (p *Patch) Wrap(v string) {
	p.format().WrapStrategy = v
	p.set("userEnteredFormat.wrapStrategy")
}

// Note sets the note beside a cell. An empty string removes it.
//
// A note is not a format, and it travels here because repeatCell is what
// writes one across a rectangle. It is also invisible in a values read,
// which is why the guard asks before replacing one.
func (p *Patch) Note(text string) {
	p.cell.Note = text
	p.set("note")
}

// Request compiles the patch into a repeatCell over a rectangle.
func (p *Patch) Request(sheetID int, rect a1.Rect) *gsheets.Request {
	cell := p.cell
	return &gsheets.Request{RepeatCell: &gsheets.RepeatCellRequest{
		Range:  rect.GridRange(sheetID),
		Cell:   &cell,
		Fields: strings.Join(p.fields, ","),
	}}
}

// ClearFormat removes every format on a rectangle and keeps the values.
//
// Its own request rather than a Patch field: the mask is the bare
// "userEnteredFormat", which cannot be combined with a mask naming
// fields under it. A call that clears and then sets sends both, in that
// order, and the batch is atomic.
func ClearFormat(sheetID int, rect a1.Rect) *gsheets.Request {
	return &gsheets.Request{RepeatCell: &gsheets.RepeatCellRequest{
		Range:  rect.GridRange(sheetID),
		Cell:   &gsheets.CellData{},
		Fields: "userEnteredFormat",
	}}
}

// Sides names which edges a border op draws.
type Sides struct {
	Top, Bottom, Left, Right bool
	InnerHorizontal          bool
	InnerVertical            bool
}

// AllSides is every edge, inner ones included: what "borders" means to
// somebody who did not say otherwise.
func AllSides() Sides {
	return Sides{Top: true, Bottom: true, Left: true, Right: true, InnerHorizontal: true, InnerVertical: true}
}

// ParseSides reads "all", "outer", "inner", or a list such as
// "top,bottom".
func ParseSides(s string) (Sides, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "all":
		return AllSides(), nil
	case "outer":
		return Sides{Top: true, Bottom: true, Left: true, Right: true}, nil
	case "inner":
		return Sides{InnerHorizontal: true, InnerVertical: true}, nil
	}
	var out Sides
	for _, part := range strings.Split(s, ",") {
		switch strings.TrimSpace(part) {
		case "top":
			out.Top = true
		case "bottom":
			out.Bottom = true
		case "left":
			out.Left = true
		case "right":
			out.Right = true
		case "inner_horizontal":
			out.InnerHorizontal = true
		case "inner_vertical":
			out.InnerVertical = true
		default:
			return Sides{}, fmt.Errorf("border side %q is not one of all, outer, inner, top, bottom, left, right, "+
				"inner_horizontal, inner_vertical", strings.TrimSpace(part))
		}
	}
	return out, nil
}

// ParseBorder reads a shorthand such as "1pt solid #cccccc", in the
// spelling a person has in their hand rather than the API's enum.
//
// "none" erases the edges instead of leaving them alone: the API's own
// rule is that an edge set to NONE is removed and an edge left out is
// untouched, and a caller who writes "none" means the first.
func ParseBorder(s string) (*gsheets.Border, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("write a border such as \"1pt solid #cccccc\", or \"none\" to remove one")
	}
	b := &gsheets.Border{}
	width, style := 0, ""
	for _, tok := range strings.Fields(s) {
		low := strings.ToLower(tok)
		switch {
		case strings.HasPrefix(tok, "#"):
			style, err := ParseColour(tok)
			if err != nil {
				return nil, err
			}
			b.ColorStyle = style
		case strings.HasSuffix(low, "pt"):
			n, err := strconv.Atoi(strings.TrimSuffix(low, "pt"))
			if err != nil || n < 1 || n > 3 {
				return nil, fmt.Errorf("border width %q is not 1pt, 2pt or 3pt", tok)
			}
			width = n
		case low == "none":
			return &gsheets.Border{Style: gsheets.BorderNone}, nil
		case low == "solid" || low == "dotted" || low == "dashed" || low == "double":
			style = low
		default:
			return nil, fmt.Errorf("%q is not part of a border; write a width (1pt), a style "+
				"(solid, dotted, dashed, double, none) and a colour (#cccccc)", tok)
		}
	}
	b.Style = borderStyle(style, width)
	return b, nil
}

// borderStyle folds the width into the API's enum, which is where the
// thickness lives: the separate width field is documented as deprecated,
// so a request carrying both would be asking for two things that can
// disagree.
func borderStyle(style string, width int) string {
	switch style {
	case "dotted":
		return gsheets.BorderDotted
	case "dashed":
		return gsheets.BorderDashed
	case "double":
		return gsheets.BorderDouble
	}
	switch width {
	case 2:
		return gsheets.BorderMedium
	case 3:
		return gsheets.BorderThick
	default:
		return gsheets.BorderThin
	}
}

// Borders draws the edges a Sides names around and inside a rectangle.
func Borders(sheetID int, rect a1.Rect, border *gsheets.Border, sides Sides) *gsheets.Request {
	req := &gsheets.UpdateBordersRequest{Range: rect.GridRange(sheetID)}
	if sides.Top {
		req.Top = border
	}
	if sides.Bottom {
		req.Bottom = border
	}
	if sides.Left {
		req.Left = border
	}
	if sides.Right {
		req.Right = border
	}
	if sides.InnerHorizontal {
		req.InnerHorizontal = border
	}
	if sides.InnerVertical {
		req.InnerVertical = border
	}
	return &gsheets.Request{UpdateBorders: req}
}

// ParseMerge reads how the cells are joined: one cell, one per row, or
// one per column.
func ParseMerge(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "all":
		return gsheets.MergeAll, nil
	case "rows":
		return gsheets.MergeRows, nil
	case "columns", "cols":
		return gsheets.MergeColumns, nil
	}
	return "", fmt.Errorf("merge %q is not all, rows or columns", s)
}

// Merge joins the cells of a rectangle.
func Merge(sheetID int, rect a1.Rect, kind string) *gsheets.Request {
	return &gsheets.Request{MergeCells: &gsheets.MergeCellsRequest{
		Range: rect.GridRange(sheetID), MergeType: kind,
	}}
}

// Unmerge splits every merge the rectangle covers.
func Unmerge(sheetID int, rect a1.Rect) *gsheets.Request {
	return &gsheets.Request{UnmergeCells: &gsheets.UnmergeCellsRequest{Range: rect.GridRange(sheetID)}}
}

// numberFormats are the named types, in the spelling a caller uses.
var numberFormats = map[string]string{
	"text":       gsheets.NumberFormatText,
	"number":     gsheets.NumberFormatNumber,
	"percent":    gsheets.NumberFormatPercent,
	"currency":   gsheets.NumberFormatCurrency,
	"date":       gsheets.NumberFormatDate,
	"time":       gsheets.NumberFormatTime,
	"date_time":  gsheets.NumberFormatDateTime,
	"scientific": gsheets.NumberFormatScientific,
}

// ParseNumberFormat reads "currency" or "date:yyyy-mm-dd": a named type,
// and optionally the pattern to use for it.
//
// The type is asked for rather than guessed from the pattern. A pattern
// alone would have to be classified — "yyyy" is a date and "#,##0" is a
// number — and a wrong guess formats somebody's column as the wrong kind
// of thing while looking like it worked.
func ParseNumberFormat(s string) (kind, pattern string, err error) {
	s = strings.TrimSpace(s)
	name := s
	if i := strings.IndexByte(s, ':'); i >= 0 {
		name, pattern = s[:i], strings.TrimSpace(s[i+1:])
	}
	kind, ok := numberFormats[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return "", "", fmt.Errorf("number_format %q does not start with a type; write one of "+
			"text, number, percent, currency, date, time, date_time, scientific, "+
			"optionally followed by a colon and a pattern such as date:yyyy-mm-dd", s)
	}
	return kind, pattern, nil
}

// ParseAlign reads a horizontal or vertical alignment.
func ParseAlign(s string, vertical bool) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "left":
		if !vertical {
			return gsheets.AlignLeft, nil
		}
	case "centre", "center", "middle":
		if vertical {
			return gsheets.AlignMiddle, nil
		}
		return gsheets.AlignCentre, nil
	case "right":
		if !vertical {
			return gsheets.AlignRight, nil
		}
	case "top":
		if vertical {
			return gsheets.AlignTop, nil
		}
	case "bottom":
		if vertical {
			return gsheets.AlignBottom, nil
		}
	}
	if vertical {
		return "", fmt.Errorf("vertical %q is not top, middle or bottom", s)
	}
	return "", fmt.Errorf("horizontal %q is not left, centre or right", s)
}

// ParseWrap reads what happens to text too long for its cell.
func ParseWrap(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "overflow":
		return gsheets.WrapOverflow, nil
	case "clip":
		return gsheets.WrapClip, nil
	case "wrap":
		return gsheets.WrapWrap, nil
	}
	return "", fmt.Errorf("wrap %q is not overflow, clip or wrap", s)
}
