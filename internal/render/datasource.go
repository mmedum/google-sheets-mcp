package render

import (
	"fmt"
	"strings"
)

// SourceAct is a change to a Connected Sheets data source, in parts.
type SourceAct struct {
	Action  string
	ID      string
	Project string
	Query   string
	Table   string
	Dataset string
	Sheet   string
	// State and Error are the refresh's, not the call's. A call that
	// worked and a refresh that failed are different things and the
	// templates keep them apart.
	State string
	Error string
}

// Phrase says what the act does.
func (a SourceAct) Phrase() string {
	switch a.Action {
	case "add":
		what := fmt.Sprintf("a query against the project %q", a.Project)
		if a.Table != "" {
			what = fmt.Sprintf("the BigQuery table %s.%s, charged to the project %q", a.Dataset, a.Table, a.Project)
		}
		return "connect " + what
	case "refresh":
		return "refresh " + a.named()
	case "cancel_refresh":
		return "cancel the refresh of " + a.named()
	default:
		on := ""
		if a.Sheet != "" {
			on = fmt.Sprintf(", and the sheet %q Google made for it", a.Sheet)
		}
		return fmt.Sprintf("delete the data source %s%s", a.ID, on)
	}
}

func (a SourceAct) named() string {
	if a.ID == "" {
		return "every data source in this spreadsheet"
	}
	return "the data source " + a.ID
}

// SourcePreview renders what a data source change would do.
func SourcePreview(a SourceAct) string {
	return fmt.Sprintf("Dry run: nothing was sent. This would %s.\n", a.Phrase())
}

// SourceDone renders what it did.
//
// A refresh is asynchronous, so this never says the data arrived: it
// says what state Google put the execution in, and repeats the error
// Google gave when there is one. A call that returns 200 with a FAILED
// execution inside it is a call that worked and a refresh that did not.
func SourceDone(a SourceAct) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Done: %s.\n", upperFirst(a.Phrase()))
	if a.ID != "" && a.Action == "add" {
		fmt.Fprintf(&b, "Its id is %s, which is how manage_data_source names it from here.\n", a.ID)
	}
	switch {
	case a.Error != "":
		fmt.Fprintf(&b, "The refresh itself did not succeed: %s (%s). The request was accepted; the query was not.\n",
			a.Error, a.State)
	case a.State != "" && a.State != "SUCCEEDED":
		fmt.Fprintf(&b, "The refresh is %s. It runs in the background, so the sheet catches up after this call "+
			"rather than during it.\n", strings.ToLower(a.State))
	}
	return b.String()
}

// SourceRow is one line of a listing.
type SourceRow struct {
	ID    string
	Sheet string
	Kind  string
}

// SourceList renders the data sources connected to a spreadsheet.
func SourceList(sources []SourceRow) string {
	var b strings.Builder
	if len(sources) == 0 {
		b.WriteString("No data sources are connected to this spreadsheet.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "%d data source(s):\n", len(sources))
	for _, s := range sources {
		line := "  " + s.ID
		if s.Kind != "" {
			line += "  " + s.Kind
		}
		if s.Sheet != "" {
			line += fmt.Sprintf("  on the sheet %q", s.Sheet)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
