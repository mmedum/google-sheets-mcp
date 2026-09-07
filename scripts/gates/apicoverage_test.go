package main

import (
	"os"
	"strings"
	"testing"
)

// chdirRoot puts a test at the repository root, which is where the gate
// runs and where its two files live.
func chdirRoot(t *testing.T) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}

// The API coverage gate, watched failing on every path it claims to
// guard. A gate nobody has seen fail is not yet a gate: "found nothing"
// and "looked at nothing" print the same sentence.

// surface is a small published API to judge: one method the client
// calls, one it does not, and one request kind of each.
func surface() []apiItem {
	return []apiItem{
		{API: "sheets", Kind: kindMethod, Name: "values.get", Verb: "GET", Path: "v4/spreadsheets/{id}/values/{range}"},
		{API: "sheets", Kind: kindMethod, Name: "values.batchUpdate", Verb: "POST", Path: "v4/spreadsheets/{id}/values:batchUpdate"},
		{API: "sheets", Kind: kindRequest, Name: "addChart"},
		{API: "sheets", Kind: kindRequest, Name: "addFilterView"},
	}
}

func code() (built, called map[string]bool) {
	return map[string]bool{"addChart": true}, map[string]bool{"values.get": true}
}

// clean is a record that matches both the surface and the code.
func clean() []coverageRow {
	return []coverageRow{
		{API: "sheets", Kind: kindMethod, Name: "values.get", Verdict: verdictUsed, Reason: "gapi.GetValues", Line: 1},
		{API: "sheets", Kind: kindMethod, Name: "values.batchUpdate", Verdict: verdictOut, Reason: "one range per call", Line: 2},
		{API: "sheets", Kind: kindRequest, Name: "addChart", Verdict: verdictUsed, Reason: "plan.AddChart", Line: 3},
		{API: "sheets", Kind: kindRequest, Name: "addFilterView", Verdict: verdictOut, Reason: "a saved view", Line: 4},
	}
}

func TestJudgeAcceptsARecordThatMatches(t *testing.T) {
	built, called := code()
	if problems := judge(surface(), clean(), built, called); len(problems) > 0 {
		t.Errorf("a matching record was refused:\n  %s", strings.Join(problems, "\n  "))
	}
}

func TestJudgeFailures(t *testing.T) {
	built, called := code()
	for _, tc := range []struct {
		name string
		rows func() []coverageRow
		want string
	}{
		{
			// The one that makes this a completeness claim: Google adds
			// a capability and the build stops.
			name: "a published item nobody judged",
			rows: func() []coverageRow { return clean()[:3] },
			want: "published and has no verdict",
		},
		{
			name: "a verdict for something no longer published",
			rows: func() []coverageRow {
				return append(clean(), coverageRow{
					API: "sheets", Kind: kindRequest, Name: "addGone",
					Verdict: verdictOut, Reason: "went away", Line: 9,
				})
			},
			want: "has a verdict and is not in",
		},
		{
			// The drift this gate exists for: somebody adds a builder
			// and the record still says the API is not called.
			name: "the code builds what the record calls out",
			rows: func() []coverageRow {
				rows := clean()
				rows[2].Verdict, rows[2].Reason = verdictOut, "we do not do charts"
				return rows
			},
			want: "recorded as deliberately out and this repository builds it",
		},
		{
			name: "the record claims something no code builds",
			rows: func() []coverageRow {
				rows := clean()
				rows[3].Verdict, rows[3].Reason = verdictUsed, "plan.AddFilterView"
				return rows
			},
			want: "recorded as used and nothing in this repository builds it",
		},
		{
			name: "a method the record claims and the client never calls",
			rows: func() []coverageRow {
				rows := clean()
				rows[1].Verdict, rows[1].Reason = verdictUsed, "gapi.BatchUpdateValues"
				return rows
			},
			want: "recorded as used and nothing in this repository calls it",
		},
		{
			name: "a verdict with no reason",
			rows: func() []coverageRow {
				rows := clean()
				rows[1].Reason = ""
				return rows
			},
			want: "has a verdict and no reason",
		},
		{
			name: "a verdict that is neither used nor out",
			rows: func() []coverageRow {
				rows := clean()
				rows[1].Verdict = "maybe"
				return rows
			},
			want: `verdict "maybe" is not`,
		},
		{
			name: "a kind that is neither a method nor a request",
			rows: func() []coverageRow {
				rows := clean()
				rows[1].Kind = "endpoint"
				return rows
			},
			want: `kind "endpoint" is not`,
		},
		{
			name: "the same item judged twice",
			rows: func() []coverageRow {
				rows := clean()
				second := rows[0]
				second.Line = 20
				return append(rows, second)
			},
			want: "has a second verdict",
		},
		{
			// A wildcard is the one shortcut this file allows, and it may
			// only ever excuse. Claiming a whole API is called is a claim
			// no code backs.
			name: "a wildcard claiming a whole API is used",
			rows: func() []coverageRow {
				return append(clean(), coverageRow{
					API: "sheets", Kind: kindMethod, Name: "*",
					Verdict: verdictUsed, Reason: "we call everything", Line: 30,
				})
			},
			want: "may only say out",
		},
		{
			// And a wildcard must not quietly absorb something the
			// client really calls, which is the way it could hide
			// coverage rather than excuse its absence.
			name: "a wildcard covering something the client calls",
			rows: func() []coverageRow {
				rows := clean()[1:] // drop the explicit values.get row
				return append(rows, coverageRow{
					API: "sheets", Kind: kindMethod, Name: "*",
					Verdict: verdictOut, Reason: "the rest is out", Line: 30,
				})
			},
			want: "and this repository uses it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := judge(surface(), tc.rows(), built, called)
			if len(problems) == 0 {
				t.Fatal("accepted")
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("problems = %v, want one mentioning %q", problems, tc.want)
			}
		})
	}
}

// TestAWildcardExcusesTheRest is the other half of the wildcard: it does
// what it is for, so the Drive rows do not have to be written sixty-one
// times.
func TestAWildcardExcusesTheRest(t *testing.T) {
	built, _ := code()
	rows := clean()[2:] // no method rows at all
	rows = append(rows, coverageRow{
		API: "sheets", Kind: kindMethod, Name: "*",
		Verdict: verdictOut, Reason: "the whole API is somebody else's", Line: 30,
	})
	problems := judge(surface(), rows, built, map[string]bool{})
	if len(problems) > 0 {
		t.Errorf("a wildcard did not excuse the rest:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestTheRealRecordIsRead is the floor this repository asks every gate
// for: the check has to have looked at something, or "found nothing" and
// "looked at nothing" are the same sentence.
func TestTheRealRecordIsRead(t *testing.T) {
	chdirRoot(t)
	s, err := readSurface()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := readCoverage()
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	for _, it := range s.Items {
		if it.API == "sheets" && it.Kind == kindRequest {
			requests++
		}
	}
	// §16 counts the union at 69. A snapshot that lost most of them
	// would pass every check above by judging what little was left.
	if requests < 60 {
		t.Errorf("the snapshot has %d Sheets request kind(s); the union had 69 when §16 was written", requests)
	}
	if len(rows) < 80 {
		t.Errorf("the record judges %d item(s), which is fewer than this server has ever called", len(rows))
	}
	// And the real thing passes, which is what make check runs.
	built, err := builtRequests()
	if err != nil {
		t.Fatal(err)
	}
	called, err := calledMethods()
	if err != nil {
		t.Fatal(err)
	}
	if problems := judge(s.Items, rows, built, called); len(problems) > 0 {
		t.Errorf("the committed record does not hold:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestTheCodeIsReadFromTheSyntaxTree checks the two readers against
// what this repository is known to do, so a parser that silently found
// nothing cannot pass as a client that calls nothing.
func TestTheCodeIsReadFromTheSyntaxTree(t *testing.T) {
	chdirRoot(t)
	built, err := builtRequests()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"addChart", "updateCells", "deleteDimension", "addSheet"} {
		if !built[want] {
			t.Errorf("internal/plan builds %s and the reader did not see it", want)
		}
	}
	if built["addFilterView"] {
		t.Error("the reader claims a request nothing here builds")
	}
	called, err := calledMethods()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"spreadsheets.get", "values.update", "drive.files.list"} {
		if !called[want] {
			t.Errorf("internal/gapi calls %s and the reader did not see it", want)
		}
	}
	if called["values.batchUpdate"] {
		t.Error("the reader claims a method nothing here calls")
	}
}
