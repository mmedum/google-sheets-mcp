package a1_test

import (
	"testing"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
)

// A1 parsing is on the path of every call, several times: a range is
// parsed, converted to a GridRange, and formatted back. §11 has no
// target for it because it was never in doubt; the number is here so a
// change that made it allocate would be visible rather than argued
// about.

func BenchmarkParse(b *testing.B) {
	for _, ref := range []string{"A1:D40", "'Yalmic''s Bractal'!B2:ZZ1000", "B:D", "2:5"} {
		b.Run(ref, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := a1.Parse(ref); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkFormat(b *testing.B) {
	rect := a1.Rect{FirstCol: 2, FirstRow: 2, LastCol: 702, LastRow: 1000}
	b.ReportAllocs()
	for b.Loop() {
		_ = a1.Format("Yalmic's Bractal", rect)
	}
}

// ColumnName and ParseColumn are the pair a round trip goes through, and
// the three-letter case is the one with the loop in it.
func BenchmarkColumnRoundTrip(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		name, err := a1.ColumnName(18278)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := a1.ParseColumn(name); err != nil {
			b.Fatal(err)
		}
	}
}
