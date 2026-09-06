package grid

import "github.com/mmedum/google-sheets-mcp/internal/a1"

// Run is a rectangle of cells that all say the same thing.
type Run[K comparable] struct {
	Rect a1.Rect
	// Sig is what they had in common, as the caller's own signature
	// function produced it.
	Sig K
}

// Runs groups a grid into the largest rectangles whose cells share a
// signature.
//
// It exists because a formatting answer per cell is unreadable and an
// answer per range is what a person would write: "A1:D1 bold, centred"
// rather than four identical lines. The grouping is not exact — a run is
// extended downwards only when the row below matches it column for
// column — and that is deliberate: a maximal-rectangle decomposition
// would split an L-shaped region into pieces chosen by the algorithm
// rather than by the sheet, and the pieces would move when an unrelated
// cell changed.
// The signature is any comparable value, which matters on a large read:
// a formatting summary groups on render.Style, a struct built to be
// compared, rather than on the sentence describing it. Grouping on the
// sentence meant building one per cell — up to fifty thousand of them,
// with an allocation each — to print at most a few hundred.
func Runs[K comparable](g *Grid, sig func(Cell) K) []Run[K] {
	// Pointers while the runs are growing, values at the end. A slice of
	// values would move under the pointers the moment it reallocated,
	// and the row that extended a run would extend a copy nobody reads.
	var out []*Run[K]
	// open are the runs still growing downwards, in the order they
	// started, so the result reads top to bottom and left to right.
	var open []*Run[K]
	for i, row := range g.Cells {
		next := make([]*Run[K], 0, len(open))
		for j := 0; j < len(row); {
			s := sig(row[j])
			last := j
			for last+1 < len(row) && sig(row[last+1]) == s {
				last++
			}
			rect := a1.Rect{
				FirstRow: g.Rect.FirstRow + i, FirstCol: g.Rect.FirstCol + j,
				LastRow: g.Rect.FirstRow + i, LastCol: g.Rect.FirstCol + last,
			}
			r := match(open, rect, s)
			if r != nil {
				r.Rect.LastRow = rect.LastRow
			} else {
				r = &Run[K]{Rect: rect, Sig: s}
				out = append(out, r)
			}
			next = append(next, r)
			j = last + 1
		}
		open = next
	}
	runs := make([]Run[K], 0, len(out))
	for _, r := range out {
		runs = append(runs, *r)
	}
	return runs
}

// match finds an open run this one continues: the same columns and the
// same signature, ending on the row above.
func match[K comparable](open []*Run[K], rect a1.Rect, sig K) *Run[K] {
	for _, r := range open {
		if r.Sig == sig && r.Rect.FirstCol == rect.FirstCol && r.Rect.LastCol == rect.LastCol &&
			r.Rect.LastRow == rect.FirstRow-1 {
			return r
		}
	}
	return nil
}
