package render

import (
	"fmt"
	"strings"
)

// ChartAct is a change to a chart or a slicer, in parts.
//
// The parts rather than a sentence, for the reason the file above says:
// a preview, a result and a refusal describing the same act should read
// the same way, and they only do if one template writes all three.
type ChartAct struct {
	Action string
	// Slicer switches every noun in the templates. A slicer is not a
	// chart and telling somebody their chart moved when a slicer did is
	// the small kind of wrong that makes a result untrustworthy.
	Slicer bool
	ID     int
	Title  string
	Type   string
	Sheet  string
	// Position is where the object sits, already in A1.
	Position string
	// Sources are the ranges an add is charting.
	Sources []string
	// Changed names the arguments an update or a move applied.
	Changed []string
}

// noun says which object this is about.
func (a ChartAct) noun() string {
	if a.Slicer {
		return "slicer"
	}
	if a.Type != "" && a.Action == "add" {
		return a.Type + " chart"
	}
	return "chart"
}

// named is how a message refers to the object: by title where it has
// one, and by id otherwise, since an untitled chart has nothing else.
func (a ChartAct) named() string {
	switch {
	case a.Title != "" && a.ID != 0:
		return fmt.Sprintf("%q (id %d)", a.Title, a.ID)
	case a.Title != "":
		return fmt.Sprintf("%q", a.Title)
	case a.ID != 0:
		return fmt.Sprintf("id %d", a.ID)
	}
	return "it"
}

// Phrase says what the act does, in the lower case a sentence needs
// around it.
func (a ChartAct) Phrase() string {
	switch a.Action {
	case "add":
		where := ""
		if a.Position != "" {
			where = " at " + a.Position
		}
		reads := ""
		if len(a.Sources) > 0 {
			reads = " reading " + strings.Join(a.Sources, " and ")
		}
		title := ""
		if a.Title != "" {
			title = fmt.Sprintf(" called %q", a.Title)
		}
		return fmt.Sprintf("add a %s%s%s%s", a.noun(), title, where, reads)
	case "update":
		return fmt.Sprintf("change the %s of the %s %s", JoinAnd(a.Changed), a.noun(), a.named())
	case "move":
		where := ""
		if a.Position != "" {
			where = " to " + a.Position
		}
		return fmt.Sprintf("move the %s %s%s", a.noun(), a.named(), where)
	default:
		on := ""
		if a.Sheet != "" {
			on = fmt.Sprintf(" from %q", a.Sheet)
		}
		return fmt.Sprintf("delete the %s %s%s", a.noun(), a.named(), on)
	}
}

// ChartPreview renders what a chart change would do.
func ChartPreview(a ChartAct) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Dry run: nothing was sent. This would %s.\n", a.Phrase())
	chartNotes(&b, a, true)
	return b.String()
}

// ChartDone renders what it did.
func ChartDone(a ChartAct) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Done: %s.\n", upperFirst(a.Phrase()))
	if a.Action == "add" && a.ID != 0 {
		fmt.Fprintf(&b, "Its id is %d, which is how manage_chart names it from here.\n", a.ID)
	}
	chartNotes(&b, a, false)
	return b.String()
}

// chartNotes carries the two things the API says nothing about.
func chartNotes(b *strings.Builder, a ChartAct, preview bool) {
	if a.Action != "delete" {
		return
	}
	// Google's reply to deleteEmbeddedObject is empty, so everything
	// above came from a read taken first. Saying so is what lets a
	// caller trust a result that Google did not confirm.
	if preview {
		b.WriteString("Google's reply to a delete is empty, so this description comes from reading it first.\n")
		return
	}
	b.WriteString("Google's reply to a delete says nothing at all; what went is what was read beforehand.\n")
}

// ChartList renders the charts and slicers in a spreadsheet.
func ChartList(charts []ChartRow, sheet string) string {
	var b strings.Builder
	where := "this spreadsheet"
	if sheet != "" {
		where = fmt.Sprintf("%q", sheet)
	}
	if len(charts) == 0 {
		fmt.Fprintf(&b, "No charts or slicers in %s.\n", where)
		return b.String()
	}
	fmt.Fprintf(&b, "%d chart(s) and slicer(s) in %s:\n", len(charts), where)
	for _, c := range charts {
		fmt.Fprintf(&b, "  %-10d %-8s %s\n", c.ID, c.Kind, chartLine(c))
	}
	broken := 0
	for _, c := range charts {
		if c.Broken {
			broken++
		}
	}
	if broken > 0 {
		fmt.Fprintf(&b, "\n%d of them has no series left, which is what deleting a charted column does: the chart "+
			"is still there and draws nothing. Give it a series with manage_chart update, or delete it.\n", broken)
	}
	return b.String()
}

// ChartRow is one line of a listing. The renderer's own view of a chart,
// so the service can grow its record without changing this file.
type ChartRow struct {
	ID       int
	Kind     string
	Title    string
	Type     string
	Sheet    string
	Position string
	Sources  string
	Broken   bool
}

func chartLine(c ChartRow) string {
	parts := []string{}
	if c.Title != "" {
		parts = append(parts, fmt.Sprintf("%q", c.Title))
	}
	if c.Type != "" && c.Type != "chart" {
		parts = append(parts, c.Type)
	}
	if c.Position != "" {
		parts = append(parts, "at "+c.Position)
	}
	if c.Sources != "" {
		parts = append(parts, "reading "+c.Sources)
	}
	if c.Broken {
		parts = append(parts, "NO SERIES")
	}
	if len(parts) == 0 {
		return "(untitled)"
	}
	return strings.Join(parts, "  ")
}

// ChartLoss is one chart the band reads, and how much of it goes.
//
// The counts, not just the name. The first version of this said "nothing
// to draw" for every chart the band touched, and a live two-series chart
// under a one-column delete lost one series and kept the other — so the
// refusal was warning about something that would not happen. A refusal
// that overstates is one somebody learns to skip.
type ChartLoss struct {
	Title string
	Lost  int
	Total int
}

// phrase says what happens to one chart, in the words its own numbers
// make true.
//
// A whole clause rather than a name, because it goes into a sentence
// that already has a list in it. The first version returned a fragment
// and the caller wrapped it — "would leave the chart(s) "X", which would
// be left with nothing to draw in place" — which no gate could catch and
// reading one live transcript did.
func (c ChartLoss) phrase() string {
	if c.Lost >= c.Total {
		return fmt.Sprintf("%q would be left with nothing to draw", c.Title)
	}
	return fmt.Sprintf("%q would lose %d of its %d series", c.Title, c.Lost, c.Total)
}

func (c ChartLoss) past() string {
	if c.Lost >= c.Total {
		return fmt.Sprintf("%q has nothing left to draw", c.Title)
	}
	return fmt.Sprintf("%q has lost %d of its %d series", c.Title, c.Lost, c.Total)
}

// ChartsAffected is the clause a delete's refusal adds when a chart
// reads the band.
//
// Not "takes with it": the chart is not deleted. It stays where it is,
// keeps its title, and draws less — which is why it is worth a clause of
// its own rather than a count beside the cells.
func ChartsAffected(losses []ChartLoss) string {
	if len(losses) == 0 {
		return ""
	}
	parts := make([]string, 0, len(losses))
	for _, c := range losses {
		parts = append(parts, c.phrase())
	}
	return JoinAnd(parts)
}

// ChartsLost is the same clause after the fact.
func ChartsLost(losses []ChartLoss) string {
	if len(losses) == 0 {
		return ""
	}
	parts := make([]string, 0, len(losses))
	for _, c := range losses {
		parts = append(parts, c.past())
	}
	return "Those cells were charted: " + JoinAnd(parts) +
		". The chart(s) are still there, and Google's reply does not mention them.\n"
}
