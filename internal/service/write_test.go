package service_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v4/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/v4/internal/service"
)

// wrote reports whether a value write reached the fake.
func wrote(srv *sheetstest.Server) bool {
	for _, c := range srv.Calls() {
		if c.Method == http.MethodPut || (c.Method == http.MethodPost && strings.HasPrefix(c.Op, "values.")) {
			return true
		}
	}
	return false
}

func TestWriteIntoEmptyCells(t *testing.T) {
	srv, svc := standard(t)
	res, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "E1",
		Values: [][]any{{"Trennow", float64(4)}, {"Bractal", float64(5)}},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.HasSuffix(res.Range, "E1:F2") {
		t.Errorf("Range = %q; the values decide the rectangle, anchored at the range", res.Range)
	}
	if res.Cells != 4 {
		t.Errorf("Cells = %d", res.Cells)
	}
	if !strings.HasPrefix(res.Checkpoint, "ck_") {
		t.Errorf("no checkpoint on a write: %q", res.Checkpoint)
	}
	// Both halves carry the rendering, and the region comes back
	// addressed so the caller can see what the cells hold now.
	if !strings.Contains(res.Render(), "E") || !strings.Contains(res.Render(), "Trennow") {
		t.Errorf("the text half does not show the region:\n%s", res.Render())
	}
	if !strings.Contains(res.Grid, "1 |") {
		t.Errorf("the structured half has no addressed grid:\n%s", res.Grid)
	}
	if !wrote(srv) {
		t.Error("no write reached the fake")
	}
}

// The guard is the reason this server exists. Each refusal names the
// cells and the argument that would allow it.
func TestWriteRefusesWhatItWouldDestroy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		req     service.WriteRequest
		want    []string
		notWant string
	}{
		{
			name: "occupied cells need overwrite",
			req: service.WriteRequest{
				Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B1",
				Values: [][]any{{"x", "y"}},
			},
			want: []string{"[blocked]", "A1", "not empty", "overwrite"},
		},
		{
			name: "formulas need their own acknowledgment",
			req: service.WriteRequest{
				Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "D2:D3",
				Values: [][]any{{float64(1)}, {float64(2)}}, Overwrite: true, OverwriteFormulas: false,
			},
			want: []string{"[blocked]", "D2", "formulas", "overwrite_formulas"},
		},
		{
			name: "a protected range cannot be acknowledged away",
			req: service.WriteRequest{
				Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:B1",
				Values: [][]any{{"x", "y"}}, Overwrite: true, OverwriteFormulas: true,
			},
			want:    []string{"[blocked]", "protected", "heading row"},
			notWant: "pass overwrite",
		},
		{
			name: "a partial merge is refused with the merge named",
			req: service.WriteRequest{
				Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A26:B26",
				Values: [][]any{{"x", "y"}}, Overwrite: true, OverwriteFormulas: true,
			},
			want: []string{"[blocked]", "merged", "A26:C26"},
		},
		{
			name: "a formula that fetches a URL is gated",
			req: service.WriteRequest{
				Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "E5",
				Values: [][]any{{`=IMPORTXML("https://example.test/","//p")`}},
			},
			want:    []string{"[blocked]", "fetches a URL", "unless the person running it sets GSHEETS_ALLOW_EXTERNAL_FORMULAS"},
			notWant: "pass ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, svc := standard(t)
			_, err := svc.Write(context.Background(), tc.req)
			if err == nil {
				t.Fatal("the write was allowed")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal = %q, missing %q", err, want)
				}
			}
			if tc.notWant != "" && strings.Contains(err.Error(), tc.notWant) {
				t.Errorf("refusal = %q, offers a way through that does not exist", err)
			}
			if wrote(srv) {
				t.Error("a refused write still reached the wire")
			}
		})
	}
}

func TestWriteWithBothAcknowledgmentsGoesThrough(t *testing.T) {
	srv, svc := standard(t)
	read := readCheckpoint(t, svc, sheetstest.FirstSheet, "D2:D3")
	res, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "D2:D3",
		Values: [][]any{{float64(1)}, {float64(2)}}, Overwrite: true, OverwriteFormulas: true, ExpectCheckpoint: read,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !wrote(srv) {
		t.Error("no write reached the fake")
	}
	if res.Cells != 2 {
		t.Errorf("Cells = %d", res.Cells)
	}
}

// A dry run is the same code path with the request never built.
func TestDryRunSendsNothing(t *testing.T) {
	srv, svc := standard(t)
	res, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B1",
		Values: [][]any{{"x", "y"}}, Overwrite: true, OverwriteFormulas: true, DryRun: true,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !res.DryRun || !strings.Contains(res.Render(), "nothing was sent") {
		t.Errorf("a dry run did not say so:\n%s", res.Render())
	}
	if !strings.Contains(res.Render(), "non-empty") {
		t.Errorf("a dry run must say what is there:\n%s", res.Render())
	}
	if wrote(srv) {
		t.Error("a dry run reached the wire")
	}
}

// §4.4: every write asks for the stored values back and names each one
// Google changed. The fake replays the live probe rather than parsing.
func TestCoercionIsReported(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "E1",
		Values: [][]any{{"007", "2026-09-05"}},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(res.Coerced) != 2 {
		t.Fatalf("%d coercion(s) from two coerced values: %+v", len(res.Coerced), res.Coerced)
	}
	byAddress := map[string]service.Coercion{}
	for _, c := range res.Coerced {
		byAddress[c.Address] = c
	}
	if got := byAddress["E1"]; got.Sent != "007" || got.Stored != "7" || got.Kind != "number" {
		t.Errorf("E1 = %+v", got)
	}
	// The date is the case that needs the display: 46270 on its own
	// reads as data loss.
	if got := byAddress["F1"]; got.Stored != "46270" || got.Displayed != "2026-09-05" {
		t.Errorf("F1 = %+v, want the serial paired with what the cell shows", got)
	}
	if !strings.Contains(res.Render(), "displayed") {
		t.Errorf("the summary does not pair the serial with the display:\n%s", res.Render())
	}
}

// literal is not a synonym for typed. Verified live, they differ on
// every input tested, and the server never substitutes one for the other.
func TestLiteralInputStoresWhatWasSent(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "E1",
		Values: [][]any{{"007"}}, Input: service.InputLiteral,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(res.Coerced) != 0 {
		t.Errorf("literal input was coerced: %+v", res.Coerced)
	}
}

func TestFormulasCreatedAreNamed(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "E1",
		Values: [][]any{{"=1+2"}},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(res.Formulas) != 1 || res.Formulas[0] != "E1" {
		t.Errorf("Formulas = %v, want [E1]", res.Formulas)
	}
}

// Refused rather than sent: a short row leaves the cells beyond it
// holding what they held, so a caller who believed they had replaced a
// rectangle would have replaced most of one.
func TestRaggedValuesAreRefused(t *testing.T) {
	srv, svc := standard(t)
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "E1",
		Values: [][]any{{"a", "b"}, {"c"}},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Fatalf("a ragged array gave %v", err)
	}
	for _, want := range []string{"row 2", "rectangle", "empty strings"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal = %q, missing %q", err, want)
		}
	}
	if wrote(srv) {
		t.Error("a refused write reached the wire")
	}
}

// Google's own refusal for this names a row number and neither the
// shape that was sent nor the room that was available.
func TestValuesBiggerThanTheRangeAreRefusedHere(t *testing.T) {
	srv, svc := standard(t)
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "E1:F2",
		Values: [][]any{{"a", "b"}, {"c", "d"}, {"e", "f"}},
	})
	if err == nil || !strings.Contains(err.Error(), "has room for") {
		t.Fatalf("an oversized array gave %v", err)
	}
	if wrote(srv) {
		t.Error("a refused write reached the wire")
	}
}

func TestWritePastTheSheetIsRefusedWithItsSize(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A60",
		Values: [][]any{{"x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "50 rows") || !strings.Contains(err.Error(), "manage_sheet resize") {
		t.Fatalf("a write past the sheet gave %v", err)
	}
}

// values.update skips cells it has no value for rather than clearing
// them, so a caller who thought they had replaced the range needs
// telling what is still there.
func TestTailLeftIsReported(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "E1:E5",
		Values: [][]any{{"a"}, {"b"}},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.TailLeft != "E3:E5" {
		t.Errorf("TailLeft = %q, want E3:E5", res.TailLeft)
	}
	if !strings.Contains(res.Render(), "skips cells") {
		t.Errorf("the summary does not explain the tail:\n%s", res.Render())
	}
}

func TestTSVIsTheSameWriteAsValues(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "E1",
		TSV: "Trennow\tBractal\nSkerry\tYalmic\n",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.HasSuffix(res.Range, "E1:F2") {
		t.Errorf("Range = %q", res.Range)
	}
}

func TestValuesAndTSVTogetherAreRefused(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "E1",
		Values: [][]any{{"a"}}, TSV: "b",
	})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("values and tsv together gave %v", err)
	}
}

// witnessed is req with the checkpoint of the cells it would replace,
// from a dry run of the same call, as a caller passing overwrite carries
// it.
func witnessed(t *testing.T, svc *service.Service, req service.WriteRequest) service.WriteRequest {
	t.Helper()
	dry := req
	dry.DryRun = true
	res, err := svc.Write(context.Background(), dry)
	if err != nil {
		t.Fatalf("a dry run for the checkpoint: %v", err)
	}
	req.ExpectCheckpoint = res.Checkpoint
	return req
}

// readCheckpoint is the checkpoint read_range gives a rectangle, which is
// what an overwrite of exactly that rectangle is held to.
func readCheckpoint(t *testing.T, svc *service.Service, sheet, rng string) string {
	t.Helper()
	read, err := svc.Read(context.Background(), service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheet, Range: rng,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(read.Checkpoint, "ck_") {
		t.Fatalf("read_range gave no checkpoint: %q", read.Checkpoint)
	}
	return read.Checkpoint
}

// overwrite carries the person's decision to replace what was read, so
// it is held to what was read: without expect_checkpoint it is refused,
// and the refusal hands over the checkpoint of the cells as this call
// read them, the same one read_range gives that rectangle. Nothing is
// written until it comes back.
func TestOverwriteNeedsTheCheckpointOfWhatItReplaces(t *testing.T) {
	srv, svc := standard(t)
	ctx := context.Background()
	ck := readCheckpoint(t, svc, sheetstest.SecondSheet, "A1:B1")
	for _, req := range []service.WriteRequest{
		{Overwrite: true},
		{Overwrite: true, OverwriteFormulas: true},
	} {
		req.Spreadsheet, req.Sheet, req.Range, req.Values = sheetstest.FixtureID, sheetstest.SecondSheet, "A1:B1",
			[][]any{{"x", "y"}}
		_, err := svc.Write(ctx, req)
		want := "[invalid] overwrite needs expect_checkpoint, so that what it replaces is what was read: 'Ürväl'!A1:B1 " +
			"reads " + ck + " now. Pass that, and a change there before the write is refused"
		if err == nil || err.Error() != want {
			t.Errorf("overwrite %v, overwrite_formulas %v:\n got %v\nwant %s", req.Overwrite, req.OverwriteFormulas, err, want)
		}
	}
	if wrote(srv) {
		t.Fatal("an overwrite with no witness reached the wire")
	}
	if _, err := svc.Write(ctx, service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B1",
		Values: [][]any{{"x", "y"}}, Overwrite: true, ExpectCheckpoint: ck,
	}); err != nil || !wrote(srv) {
		t.Errorf("with the checkpoint: %v, wrote %v", err, wrote(srv))
	}
}

// A dry run lists what the same call would be refused for, and an
// overwrite with no expect_checkpoint is one of those, in write_values and
// transform_range alike.
func TestADryRunSaysAnOverwriteNeedsItsWitness(t *testing.T) {
	srv, svc := standard(t)
	ctx := context.Background()
	ck := readCheckpoint(t, svc, sheetstest.SecondSheet, "A1:B1")
	write, err := svc.Write(ctx, service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B1", Values: [][]any{{"x", "y"}},
		Overwrite: true, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "  overwrite needs expect_checkpoint, so that what it replaces is what was read: 'Ürväl'!A1:B1 reads " + ck +
		" now. Pass that, and a change there before the write is refused\n"
	if !strings.Contains(write.Render(), "would be refused:\n"+want) {
		t.Errorf("write_values dry run does not list the refusal %q:\n%s", want, write.Render())
	}

	srv.Doc(sheetstest.FixtureID).Find(sheetstest.SecondSheet).Set(1, 1, sheetstest.Str("Quorbin,Vandel"))
	split := transformReq(service.TransformTextToColumns, "A1:A2")
	split.Delimiter, split.Overwrite, split.DryRun = "comma", true, true
	preview, err := svc.Transform(ctx, split)
	if err != nil {
		t.Fatal(err)
	}
	want = "  overwrite needs expect_checkpoint, so that what it replaces is what was read: 'Ürväl'!B1:B2 reads " +
		preview.Checkpoint + " now. Pass that, and a change there before the write is refused\n"
	if !strings.Contains(preview.Render(), "would be refused:\n"+want) {
		t.Errorf("transform_range dry run does not list the refusal %q:\n%s", want, preview.Render())
	}
}

// The guard's refusal of occupied cells says what to pass with
// overwrite, and a dry run gives the same checkpoint, so neither needs a
// second read.
func TestARefusalAndADryRunGiveTheCheckpoint(t *testing.T) {
	_, svc := standard(t)
	ctx := context.Background()
	ck := readCheckpoint(t, svc, sheetstest.SecondSheet, "A1:B1")
	req := service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B1", Values: [][]any{{"x", "y"}},
	}
	_, err := svc.Write(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "; pass overwrite to allow it. Pass expect_checkpoint "+ck+
		" with it: that is 'Ürväl'!A1:B1 as this call read it, and a change there before the write is refused. ") {
		t.Errorf("refusal = %v", err)
	}
	req.DryRun = true
	res, err := svc.Write(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Checkpoint != ck || !strings.Contains(res.Render(), "Its checkpoint is "+ck+
		", which overwrite needs as expect_checkpoint.\n") {
		t.Errorf("dry run checkpoint %q, want %q:\n%s", res.Checkpoint, ck, res.Render())
	}
}

// A formula that reaches outside the spreadsheet is the operator's to
// allow, not the caller's: with GSHEETS_ALLOW_EXTERNAL_FORMULAS it is
// written, and named in the result.
func TestTheOperatorAllowsExternalFormulas(t *testing.T) {
	srv := sheetstest.Standard(t)
	cfg, err := settings()
	if err != nil {
		t.Fatal(err)
	}
	cfg.AllowExternalFormulas = true
	svc := service.New(service.Deps{API: srv.Client(), Config: cfg})
	res, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "E5",
		Values: [][]any{{`=IMPORTXML("https://example.test/","//p")`}},
	})
	if err != nil {
		t.Fatalf("the operator allowed it and it was refused: %v", err)
	}
	if len(res.Formulas) != 1 || res.Formulas[0] != "E5" {
		t.Errorf("formulas created = %v, want [E5]", res.Formulas)
	}
}

// A checkpoint narrows the window between a read and a write. It cannot
// close it, and the refusal says which of the two things went wrong.
func TestCheckpointGuardsTheWrite(t *testing.T) {
	srv, svc := standard(t)
	ctx := context.Background()
	read, err := svc.Read(ctx, service.ReadRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Matching: the write goes through.
	if _, err := svc.Write(ctx, service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B1",
		Values: [][]any{{"x", "y"}}, Overwrite: true, OverwriteFormulas: true, ExpectCheckpoint: read.Checkpoint,
	}); err != nil {
		t.Fatalf("a matching checkpoint refused the write: %v", err)
	}
	// The cells have changed now, so the same checkpoint is stale.
	srv.Reset()
	_, err = svc.Write(ctx, service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "A1:B1",
		Values: [][]any{{"p", "q"}}, Overwrite: true, OverwriteFormulas: true, ExpectCheckpoint: read.Checkpoint,
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[conflict]") {
		t.Fatalf("a stale checkpoint gave %v", err)
	}
	if !strings.Contains(err.Error(), "read of a different range") {
		t.Errorf("the conflict does not say what else it could be: %q", err)
	}
	if wrote(srv) {
		t.Error("a conflicting write reached the wire")
	}
}

func TestAMalformedCheckpointSaysWhatOneLooksLike(t *testing.T) {
	_, svc := standard(t)
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "E1",
		Values: [][]any{{"x"}}, ExpectCheckpoint: "yesterday",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") || !strings.Contains(err.Error(), "ck_") {
		t.Fatalf("a malformed checkpoint gave %v", err)
	}
}

func TestInputEnumIsClosedBeforeAnythingIsSent(t *testing.T) {
	srv, svc := standard(t)
	_, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: "E1",
		Values: [][]any{{"x"}}, Input: "friendly",
	})
	if err == nil || !strings.Contains(err.Error(), "typed or literal") {
		t.Fatalf("an unknown input option gave %v", err)
	}
	if len(srv.Calls()) != 0 {
		t.Error("a typo cost a request against a per-minute quota")
	}
}

// A dry run answers even when the write it previews would be refused.
//
// The refusal's own last sentence is "dry_run shows what would change
// without sending anything". With the guard running first, a caller who
// followed that advice got the identical refusal — the one call that
// exists to explain a refusal was the one call the refusal blocked.
func TestDryRunPreviewsAWriteThatWouldBeRefused(t *testing.T) {
	srv, svc := standard(t)
	res, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "D2:D3",
		Values: [][]any{{float64(1)}, {float64(2)}}, DryRun: true,
	})
	if err != nil {
		t.Fatalf("a dry run over a target the guard would refuse: %v", err)
	}
	if !res.DryRun {
		t.Error("the result does not say it was a dry run")
	}
	// And it says what would stop it, so the preview is not a promise
	// that the write would go through.
	for _, want := range []string{"would be refused", "formula", "overwrite_formulas"} {
		if !strings.Contains(res.Render(), want) {
			t.Errorf("the preview does not name the blocker (%q):\n%s", want, res.Render())
		}
	}
	if wrote(srv) {
		t.Error("a dry run reached the wire")
	}
}

// The same for a protected range, which no acknowledgment clears: the
// preview has to say so rather than imply a flag would help.
func TestDryRunPreviewsAWriteNothingCanAllow(t *testing.T) {
	_, svc := standard(t)
	res, err := svc.Write(context.Background(), service.WriteRequest{
		Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.FirstSheet, Range: "A1:B1",
		Values: [][]any{{"x", "y"}}, DryRun: true,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(res.Render(), "protected") {
		t.Errorf("the preview does not mention the protection:\n%s", res.Render())
	}
}

// TestAFormulaIntoATableHeaderIsRefused is spike T Q7: Google replaces a
// formula written into a table's header cell with a column name of its
// own and reports the write a success. Nothing allows it, the dry run
// says so, and text there, a formula under literal input and a formula
// below the header are not held back.
func TestAFormulaIntoATableHeaderIsRefused(t *testing.T) {
	const want = `[blocked] C1 would hold a formula in a table's header row, where Google replaces it with a ` +
		`column name of its own, such as "Column 2", and still reports the write a success. Write the heading as text`
	srv := sheetstest.Standard(t)
	seedTypedTable(srv)
	svc := newService(t, srv)
	ctx := context.Background()
	write := func(rangeA1, input string, values [][]any, dryRun bool) (*service.WriteResult, error) {
		req := service.WriteRequest{
			Spreadsheet: sheetstest.FixtureID, Sheet: sheetstest.SecondSheet, Range: rangeA1, Input: input,
			Values: values, Overwrite: true, OverwriteFormulas: true, DryRun: dryRun,
		}
		if !dryRun {
			req = witnessed(t, svc, req)
		}
		return svc.Write(ctx, req)
	}
	if _, err := write("C1:D1", "typed", [][]any{{"=1+2", "Owner"}}, false); err == nil || err.Error() != want {
		t.Errorf("error =\n%v\nwant\n%s", err, want)
	}
	if wrote(srv) {
		t.Fatal("a refused write reached the fake")
	}
	preview, err := write("C1", "typed", [][]any{{"=1+2"}}, true)
	if err != nil || !strings.Contains(preview.Render(), `C1 would hold a formula in a table's header row`) {
		t.Errorf("the dry run does not say what stops it: %v\n%v", err, preview)
	}
	for _, ok := range []struct {
		rangeA1, input string
		values         [][]any
	}{
		{"C1", "typed", [][]any{{"Owner"}}},
		{"C1", "literal", [][]any{{"=1+2"}}},
		{"C2", "typed", [][]any{{"=1+2"}}},
	} {
		if _, err := write(ok.rangeA1, ok.input, ok.values, false); err != nil {
			t.Errorf("%s %s %v was refused: %v", ok.input, ok.rangeA1, ok.values, err)
		}
	}
}
