package service_test

import (
	"context"
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// The end-to-end numbers §11 states targets for, measured through the
// fake rather than against Google.
//
// What that leaves out is the network, which dominates everything here
// by two orders of magnitude: a request against the real API is roughly
// 100 ms and one is in flight at a time. So these numbers are not a
// latency budget. They are the answer to a different question — whether
// this server's own work is a rounding error against the round trip, or
// something a caller would feel — and they say what happens when a
// budget is raised.

// benchService is a service over a large fixture, without the per-test
// helpers' logging.
func benchService(b *testing.B, rows, cols int) (*service.Service, string) {
	srv := sheetstest.New(b)
	doc, file := sheetstest.Large(rows, cols)
	srv.Add(doc, file)
	cfg, err := settings()
	if err != nil {
		b.Fatal(err)
	}
	return service.New(service.Deps{API: srv.Client(), Config: cfg}), doc.ID
}

// BenchmarkRead is the whole read path: resolve, fetch, build the grid,
// render it addressed. §11's target is a 5 000-cell read rendered under
// 20 ms.
func BenchmarkRead(b *testing.B) {
	svc, id := benchService(b, 1000, 10)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, err := svc.Read(ctx, service.ReadRequest{
			Spreadsheet: id, Sheet: "Bractal", Range: "A1:J500",
		})
		if err != nil {
			b.Fatal(err)
		}
		if res.Grid == "" {
			b.Fatal("rendered nothing")
		}
	}
}

// The card is one request and reads no cells, so its cost must not
// depend on the size of the spreadsheet behind it.
func BenchmarkCard(b *testing.B) {
	svc, id := benchService(b, 5000, 20)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := svc.Card(ctx, id); err != nil {
			b.Fatal(err)
		}
	}
}

// The sheet resource: the used range as CSV under one character budget.
func BenchmarkSheetCSV(b *testing.B) {
	svc, id := benchService(b, 1000, 10)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, err := svc.SheetCSV(ctx, id, "Bractal")
		if err != nil {
			b.Fatal(err)
		}
		if res.CSV == "" {
			b.Fatal("rendered nothing")
		}
	}
}
