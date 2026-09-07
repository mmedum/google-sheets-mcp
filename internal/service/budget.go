package service

import "github.com/mmedum/google-sheets-mcp/internal/config"

// Budget is what one call may read, render and hand back.
//
// One type rather than three numbers at three altitudes. The tool layer
// held the maxima, the service the defaults, the renderer the character
// cut, and `max_matches` had no maximum at all. Phase 1 adds five write
// tools that would each have copied the tool-layer half, so the policy
// moves here first: a limit copied is a limit that drifts.
type Budget struct {
	// Cells bounds what is fetched, and is applied before the request is
	// built so an open-ended range never becomes an unbounded read.
	Cells int
	// Chars bounds the rendering.
	Chars int
	// Matches bounds how many hits a search returns. A different dial
	// from Cells: those cells were read either way.
	Matches int
}

// MaxWriteCells bounds one write.
//
// Not the configured read default: a write says exactly what it is
// writing, so the number that matters is the one that keeps the request
// and the guard's read of the target inside this process. It lives here
// with the other limits rather than inline in the write path, for the
// reason Budget exists at all.
const MaxWriteCells = config.MaxMaxCells

// Match limits. Cells and characters take their defaults from the
// configuration, which a deployer sets; nothing about a match count
// varies per deployment, so both numbers live here.
const (
	DefaultMaxMatches = 200
	MaxMaxMatches     = 5000
)

// budget fills the zero fields from the configuration and refuses
// anything past the maximum this build allows.
//
// Refused rather than clamped. A caller who asks for 200 000 cells and
// silently gets 50 000 reads a footer describing a window they never
// asked for, and continues from a row that is not where they think they
// stopped.
func (s *Service) budget(b Budget) (Budget, error) {
	var err error
	if b.Cells, err = fitBudget("max_cells", b.Cells, s.cfg.MaxCells, config.MaxMaxCells); err != nil {
		return Budget{}, err
	}
	if b.Chars, err = fitBudget("max_chars", b.Chars, s.cfg.MaxChars, config.MaxMaxChars); err != nil {
		return Budget{}, err
	}
	if b.Matches, err = fitBudget("max_matches", b.Matches, DefaultMaxMatches, MaxMaxMatches); err != nil {
		return Budget{}, err
	}
	return b, nil
}

// fitBudget defaults a zero and refuses anything outside 1..maxValue.
func fitBudget(name string, asked, dflt, maxValue int) (int, error) {
	if asked == 0 {
		return dflt, nil
	}
	if asked < 1 || asked > maxValue {
		return 0, Errorf("invalid", "%s must be between 1 and %d", name, maxValue)
	}
	return asked, nil
}
