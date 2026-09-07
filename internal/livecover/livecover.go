// Package livecover holds what "the live driver covers the tool surface"
// means, so the static gate and the driver itself cannot disagree about
// it.
//
// There are two checks and they answer different questions. The gate in
// scripts/gates reads the driver's source, runs in CI where there are no
// credentials, and catches an option added without a step. The driver
// records what it actually sent and checks the same thing at the end of
// a real run — which is the authoritative answer, because a step can
// exist in the source and never execute: sitting in a slice nobody
// passes to run, or behind a condition that was false.
//
// Both read the exemption list below, so an option excused in one is
// excused in the other and a reason written once is the reason both give.
package livecover

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Undrivable are the tool options a live run cannot exercise, each with
// the reason. Anything not here must be sent by a step.
var Undrivable = map[string]string{
	"read_range.max_chars": "the character budget is bounded by max_cells in any live sheet the driver builds, " +
		"so a live step could not tell the two cuts apart; the unit tests drive both",
	"search_spreadsheets.page_token": "needs more spreadsheets than the driver creates to produce a second page; " +
		"the fake covers paging",
}

// UndrivableTools are the tools a live run cannot exercise at all, each
// with the reason. Separate from Undrivable because the two failures are
// different: an option nobody sends is a gap in the driver, and a tool
// nobody can call is a limit of the account the driver runs against.
//
// A tool listed here is still built, still unit-tested and still in the
// schema dump. What it is not is proven against Google, and saying so
// here is better than a step that fakes a pass.
var UndrivableTools = map[string]string{
	"manage_data_source": "Connected Sheets needs a BigQuery-enabled Cloud project with billing attached, and " +
		"addDataSource needs the bigquery.readonly scope this server asks for only under " +
		"GSHEETS_ENABLE_DATA_SOURCES. Spike N sent every call in the tool and recorded what came back (§18); " +
		"driving it for real would charge somebody's project on every run",
	"delete_data_source": "there is nothing to delete: the driver cannot create a data source, for the reason " +
		"manage_data_source cannot be driven. Spike N sent deleteDataSource live and recorded the reply — a 400 " +
		"naming the id, which is also how §18 knows this call needs no BigQuery scope",
}

// Tool is one tool and the options it accepts.
type Tool struct {
	Name    string
	Options []string
}

// Report is what either check found.
type Report struct {
	Covered  int
	Total    int
	Gaps     []string
	Excused  []string
	NoCaller []string
}

// Check compares what was sent against the surface that exists.
//
// sent maps a tool name to the options seen with it. A tool absent from
// sent was never called at all, which is worse than a missing option and
// is reported separately.
func Check(sent map[string]map[string]bool, tools []Tool) Report {
	var r Report
	for _, t := range tools {
		options := slices.Clone(t.Options)
		sort.Strings(options)
		args, called := sent[t.Name]
		if !called {
			if reason := UndrivableTools[t.Name]; reason != "" {
				r.Excused = append(r.Excused, t.Name+": "+reason)
				continue
			}
			r.NoCaller = append(r.NoCaller, t.Name)
			r.Total += len(options)
			continue
		}
		for _, opt := range options {
			r.Total++
			key := t.Name + "." + opt
			switch {
			case args[opt]:
				r.Covered++
			case Undrivable[key] != "":
				r.Excused = append(r.Excused, key+": "+Undrivable[key])
			default:
				r.Gaps = append(r.Gaps, key)
			}
		}
	}
	sort.Strings(r.Gaps)
	sort.Strings(r.Excused)
	sort.Strings(r.NoCaller)
	return r
}

// Err turns a report into the failure a gate or a run should give, or
// nil when the surface is covered.
func Err(r Report) error {
	problems := make([]string, 0, len(r.NoCaller)+len(r.Gaps))
	for _, name := range r.NoCaller {
		problems = append(problems, name+" is registered and no step calls it")
	}
	for _, gap := range r.Gaps {
		problems = append(problems, gap+" is never sent")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%d gap(s) between the tool surface and the live driver:\n  %s\n"+
		"add a step, or record the option in livecover.Undrivable with the reason it cannot be driven",
		len(problems), strings.Join(problems, "\n  "))
}

// Summary is the one-line result.
func Summary(r Report) string {
	return fmt.Sprintf("%d of %d tool options exercised, %d recorded as undrivable", r.Covered, r.Total, len(r.Excused))
}
