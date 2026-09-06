package grid

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
)

// CheckpointPrefix marks a checkpoint so a caller can tell one from any
// other opaque string this server hands out.
const CheckpointPrefix = "ck_"

// Checkpoint hashes what a read saw: the spreadsheet, the range, and the
// canonical form of the values.
//
// It stands in for a write guard the platform does not have. Sheets has
// no writeControl, no requiredRevisionId and no ETag, and the API says
// in its own words that a spreadsheet may not reflect exactly your
// changes afterwards because of collaborators. So this narrows the
// window between reading and writing and cannot close it, and every
// description that mentions it says so. Where a real guarantee is
// needed, a protected range is the one the platform offers.
func Checkpoint(spreadsheetID string, g *Grid) string {
	h := sha256.New()
	// One reused buffer, written once per row. Hashing string by string
	// allocates a copy of every cell and of every separator, which on a
	// 50 000-cell read is a hundred thousand allocations for a value
	// twelve characters long.
	buf := make([]byte, 0, 4096)
	add := func(parts ...string) {
		for _, p := range parts {
			buf = append(buf, p...)
			buf = append(buf, 0x1f)
		}
	}
	add(spreadsheetID, g.Sheet, strconv.Itoa(g.SheetID), a1.FormatRect(g.Rect))
	for _, row := range g.Cells {
		for _, c := range row {
			// The formula, not its result: a recalculation that changes a
			// number without anybody editing the sheet is not a conflict
			// to report, and a formula that changed is.
			if c.Formula != "" {
				add("f", c.Formula)
				continue
			}
			add(string(c.Kind), c.Display)
		}
		buf = append(buf, 0x1e)
		_, _ = h.Write(buf)
		buf = buf[:0]
	}
	_, _ = h.Write(buf)
	return CheckpointPrefix + hex.EncodeToString(h.Sum(nil))[:12]
}

// IsCheckpoint reports whether s has the shape this server hands out, so
// a caller passing something else is told what went wrong rather than
// being refused for a mismatch it cannot see.
func IsCheckpoint(s string) bool {
	if !strings.HasPrefix(s, CheckpointPrefix) {
		return false
	}
	rest := s[len(CheckpointPrefix):]
	if len(rest) != 12 {
		return false
	}
	_, err := hex.DecodeString(rest)
	return err == nil
}
