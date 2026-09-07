package render

import (
	"fmt"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// The formatting half of the renderer: how a cell's format is put into
// words, and how a range's formatting is summarised.
//
// The words live here rather than in the service, for the reason the
// rest of this package exists: phrasing decided above the renderer is
// phrasing the goldens cannot cover.

// Style is one cell's format as a person would describe it.
//
// A flat, comparable struct on purpose. Two cells are in the same block
// when their styles are equal, and equality over a tree of pointers is
// equality of addresses, which would put every cell in its own block.
type Style struct {
	NumberFormat  string
	Bold          bool
	Italic        bool
	Underline     bool
	Strikethrough bool
	FontFamily    string
	FontSize      int
	TextColour    string
	Background    string
	Horizontal    string
	Vertical      string
	Wrap          string
	Borders       string
}

// StyleOf reads a cell format into the view model.
func StyleOf(f *gsheets.CellFormat) Style {
	var s Style
	if f == nil {
		return s
	}
	if n := f.NumberFormat; n != nil {
		s.NumberFormat = strings.ToLower(strings.ReplaceAll(n.Type, "_", " "))
		if n.Pattern != "" {
			s.NumberFormat += " " + n.Pattern
		}
	}
	if t := f.TextFormat; t != nil {
		s.Bold, s.Italic, s.Underline, s.Strikethrough = t.Bold, t.Italic, t.Underline, t.Strikethrough
		s.FontFamily, s.FontSize = t.FontFamily, t.FontSize
		s.TextColour = HexColour(t.ForegroundColorStyle)
	}
	s.Background = HexColour(f.BackgroundColorStyle)
	s.Horizontal = lower(f.HorizontalAlign)
	s.Vertical = lower(f.VerticalAlign)
	s.Wrap = lower(f.WrapStrategy)
	s.Borders = describeBorders(f.Borders)
	return s
}

func lower(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", " ")) }

// Describe puts the style into words, in the order a person reads them:
// what the value looks like, then the text, then the cell.
func (s Style) Describe() string {
	var parts []string
	if s.NumberFormat != "" {
		parts = append(parts, s.NumberFormat)
	}
	for _, sw := range []struct {
		on   bool
		name string
	}{{s.Bold, "bold"}, {s.Italic, "italic"}, {s.Underline, "underline"}, {s.Strikethrough, "strikethrough"}} {
		if sw.on {
			parts = append(parts, sw.name)
		}
	}
	if s.FontFamily != "" {
		parts = append(parts, s.FontFamily)
	}
	if s.FontSize > 0 {
		parts = append(parts, fmt.Sprintf("%dpt", s.FontSize))
	}
	if s.TextColour != "" {
		parts = append(parts, "text "+s.TextColour)
	}
	if s.Background != "" {
		parts = append(parts, "background "+s.Background)
	}
	if s.Horizontal != "" {
		parts = append(parts, s.Horizontal)
	}
	if s.Vertical != "" {
		parts = append(parts, s.Vertical)
	}
	if s.Wrap != "" {
		parts = append(parts, s.Wrap)
	}
	if s.Borders != "" {
		parts = append(parts, s.Borders)
	}
	if len(parts) == 0 {
		return "no formatting of its own"
	}
	return strings.Join(parts, ", ")
}

// describeBorders names the edges that are drawn, and the one style they
// share when they share one.
//
// Per edge would be four clauses for a box, which is the commonest
// border there is and the least worth spelling out.
func describeBorders(b *gsheets.Borders) string {
	if b == nil {
		return ""
	}
	var drawn []string
	styles := map[string]bool{}
	for _, e := range []struct {
		border *gsheets.Border
		name   string
	}{{b.Top, "top"}, {b.Bottom, "bottom"}, {b.Left, "left"}, {b.Right, "right"}} {
		if e.border == nil || e.border.Style == "" || e.border.Style == gsheets.BorderNone {
			continue
		}
		drawn = append(drawn, e.name)
		styles[lower(e.border.Style)+colourSuffix(e.border.ColorStyle)] = true
	}
	if len(drawn) == 0 {
		return ""
	}
	name := "borders " + strings.Join(drawn, "+")
	if len(drawn) == 4 {
		name = "boxed"
	}
	if len(styles) == 1 {
		for s := range styles {
			return name + " " + s
		}
	}
	return name
}

func colourSuffix(c *gsheets.ColorStyle) string {
	if hex := HexColour(c); hex != "" {
		return " " + hex
	}
	return ""
}

// HexColour turns the API's colour union back into what a person wrote.
//
// The inverse of the parser in plan, and it lives here because it is a
// rendering: a theme colour has no hex at all and is named instead.
func HexColour(c *gsheets.ColorStyle) string {
	if c == nil {
		return ""
	}
	if c.ThemeColor != "" {
		return lower(c.ThemeColor)
	}
	rgb := c.RGBColor
	if rgb == nil {
		return ""
	}
	// White is not special-cased, and it was. The argument for skipping
	// it was that white is the default fill and naming it would bury the
	// coloured blocks — but a background only reaches a cell's own format
	// when somebody set it, and setting white is how highlighting is
	// cleared. Skipped, such a cell described itself as having no format
	// of its own while the guard still refused clear_format over it and
	// named it: the read and the guard describing the same cell
	// differently.
	return fmt.Sprintf("#%02x%02x%02x", channel(rgb.Red), channel(rgb.Green), channel(rgb.Blue))
}

func channel(v float64) int {
	n := int(v*255 + 0.5)
	return min(max(n, 0), 255)
}

// Block is one rectangle of identically formatted cells.
//
// Style is the description rather than the struct: the description is
// what the cells were grouped by, so carrying both would let a block
// print words it was not grouped on.
type Block struct {
	Range string
	Style string
}

// Formatting is everything read_formatting reports.
type Formatting struct {
	Range string
	// Blocks are the rectangles the range falls into, largest first in
	// reading order.
	Blocks []Block
	// The things attached to the sheet that decide what a cell looks
	// like, or that a formatting answer would be wrong to omit.
	Merges     []string
	Rules      []NamedItem
	Bandings   []NamedItem
	Validation []NamedItem
	Notes      []NamedItem
	Protected  []NamedItem
	Footer     string
}

// Formats renders a formatting read.
//
// Blocks first, because they are the answer; the attached objects after,
// because they explain a block that looks unformatted and is not — a
// conditional rule and a banding both colour cells that carry no format
// of their own.
func Formats(f Formatting) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Formatting of %s\n\n", f.Range)
	if len(f.Blocks) == 0 {
		b.WriteString("  nothing in this range carries formatting of its own\n")
	}
	for _, blk := range f.Blocks {
		fmt.Fprintf(&b, "  %-14s %s\n", blk.Range, blk.Style)
	}
	rangeSection(&b, "Merged", plainItems(f.Merges))
	rangeSection(&b, "Conditional rules on this sheet", f.Rules)
	rangeSection(&b, "Banding", f.Bandings)
	rangeSection(&b, "Validation", f.Validation)
	rangeSection(&b, "Notes", f.Notes)
	rangeSection(&b, "Protected", f.Protected)
	if f.Footer != "" {
		fmt.Fprintf(&b, "\n%s\n", f.Footer)
	}
	return b.String()
}

func plainItems(ranges []string) []NamedItem {
	out := make([]NamedItem, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, NamedItem{Range: r})
	}
	return out
}

// rangeSection lists things by where they are rather than by what they
// are called, which is the card's order the other way round: in a
// formatting answer the range is what the reader is looking for, and
// most of these have no name at all.
func rangeSection(b *strings.Builder, title string, items []NamedItem) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s:\n", title)
	for _, it := range items {
		line := "  " + it.Range
		if it.Name != "" {
			line = fmt.Sprintf("  %s (%s)", it.Range, it.Name)
		}
		if it.Detail != "" {
			line += " — " + it.Detail
		}
		fmt.Fprintf(b, "%s\n", line)
	}
}

// Applied is one op a write performed, as its result names it.
type Applied struct {
	// Kind is what was done — "background", "merge", "sort" — and Value
	// what it was done with, empty for an op that takes none.
	Kind  string
	Value string
}

// Ops is what a formatting, range or transform write did.
//
// Parts, not prose. The service used to hand this finished sentences —
// "%d occurrence(s) replaced in %d cell(s)…" and four hand-written notes
// about addresses moving — which is the same mistake §17a.9 records for
// the structural tools, in a tool written after they were fixed. The
// templates are below, where the goldens can reach them.
type Ops struct {
	Range   string
	Applied []Applied
	// Counted is what the API reported afterwards. Nil when the request
	// returns no count, which most of them do not.
	Counted *Counts
	// Blockers is what would stop the write, for a preview.
	Blockers []string
	// Shifted says rows moved, so an address or a checkpoint the caller
	// is holding no longer points where it did. Emptied names a range a
	// cut left blank.
	Shifted bool
	Emptied string
}

// What a transform's reply counted. Three of the nine actions answer
// with a number and the rest with nothing, and the three count different
// things.
const (
	CountReplaced = "replaced"
	CountTrimmed  = "trimmed"
	CountDeduped  = "deduped"
)

// Counts is what a transform's reply said it did.
//
// Kind says which of the three replies this is, rather than leaving the
// renderer to work it out from which fields happen to be zero — a
// replacement that matched nothing and a trim that changed nothing are
// both all-zero, and they do not read the same.
type Counts struct {
	Kind        string
	Occurrences int
	Cells       int
	Formulas    int
	Rows        int
}

// Total is the one number a result reports as "how much changed".
func (c *Counts) Total() int {
	switch {
	case c == nil:
		return 0
	case c.Kind == CountReplaced:
		return c.Occurrences
	}
	return c.Cells
}

// Sentence is what the counts read as.
func (c *Counts) Sentence() string {
	switch {
	case c == nil:
		return ""
	case c.Kind == CountTrimmed:
		return fmt.Sprintf("%d cell(s) had whitespace trimmed.", c.Cells)
	case c.Kind == CountDeduped:
		return fmt.Sprintf("%d duplicate row(s) removed.", c.Cells)
	case c.Kind == CountReplaced:
		return fmt.Sprintf("%d occurrence(s) replaced in %d cell(s) and %d formula(s), across %d row(s).",
			c.Occurrences, c.Cells, c.Formulas, c.Rows)
	}
	return ""
}

// OpsDone renders what a batch of ops did.
func OpsDone(o Ops) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Done: %d change(s) to %s.\n", len(o.Applied), o.Range)
	opLines(&b, o.Applied)
	if sentence := o.Counted.Sentence(); sentence != "" {
		fmt.Fprintf(&b, "%s\n", sentence)
	}
	opNotes(&b, o, "moved")
	return b.String()
}

// OpsPreview renders what a batch of ops would do, having sent nothing.
func OpsPreview(o Ops) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Dry run: nothing was sent. This would make %d change(s) to %s.\n", len(o.Applied), o.Range)
	opLines(&b, o.Applied)
	if len(o.Blockers) > 0 {
		b.WriteString("\nAs asked, this would be refused:\n")
		for _, line := range o.Blockers {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	opNotes(&b, o, "would move")
	return b.String()
}

func opLines(b *strings.Builder, applied []Applied) {
	for _, a := range applied {
		if a.Value == "" {
			fmt.Fprintf(b, "  %s\n", a.Kind)
			continue
		}
		fmt.Fprintf(b, "  %s — %s\n", a.Kind, a.Value)
	}
}

// opNotes says the two things a caller cannot see in the list of ops:
// that rows moved, and that a cut emptied where it came from.
//
// The moved-addresses sentence is the one DimensionAct already gives for
// rows and columns, in the same words. Two wordings of one fact is what
// putting the phrasing above the renderer produces.
func opNotes(b *strings.Builder, o Ops, moved string) {
	if o.Shifted {
		fmt.Fprintf(b, "\nThe rows %s: an address or a checkpoint from before this call no longer points "+
			"where it did.\n", moved)
	}
	if o.Emptied != "" {
		fmt.Fprintf(b, "\nA cut empties the cells it came from, so %s is now blank.\n", o.Emptied)
	}
}

// ConditionText puts a condition into words, in the order it reads:
// what is tested, then what it is tested against.
//
// The API's own spelling with the underscores taken out, rather than a
// table of English per type. A table would have to grow a row for every
// condition the server learns, and a row it lacked would render as
// nothing at all — where the enum name, lower-cased, is already
// readable: "number greater 100", "text contains Quorbin".
func ConditionText(c *gsheets.BooleanCondition) string {
	if c == nil {
		return "no condition"
	}
	text := lower(c.Type)
	var values []string
	for _, v := range c.Values {
		switch {
		case v == nil:
		case v.UserEnteredValue != "":
			values = append(values, v.UserEnteredValue)
		case v.RelativeDate != "":
			values = append(values, lower(v.RelativeDate))
		}
	}
	if len(values) == 0 {
		return text
	}
	return text + " " + strings.Join(values, ", ")
}

// RuleText describes a conditional format rule: what it tests and what
// it does when the test passes.
func RuleText(r *gsheets.ConditionalFormatRule) string {
	switch {
	case r == nil:
		return ""
	case r.BooleanRule != nil:
		style := StyleOf(r.BooleanRule.Format)
		return ConditionText(r.BooleanRule.Condition) + " -> " + style.Describe()
	case r.GradientRule != nil:
		// Named rather than described in full. A gradient is three
		// interpolation points, and a caller who wants them can read the
		// rule; what matters here is that the colour on a cell comes
		// from a rule and not from the cell's own format.
		return "colour gradient"
	}
	return "rule with no condition"
}
