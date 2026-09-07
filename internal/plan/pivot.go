package plan

import (
	"encoding/json"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// PivotFieldMask is the CellData field a pivot write names, and the only
// one it names: an updateCells carrying `pivotTable` alone leaves every
// other property of the cell where it was.
const PivotFieldMask = "pivotTable"

// WritePivot anchors a pivot table at a one-based cell.
//
// `start` rather than `range`, because a pivot's output is computed and
// its rectangle is not known here — a range would clear the named field
// across a rectangle this code would have to guess at.
func WritePivot(sheetID, col, row int, pivot json.RawMessage) *gsheets.Request {
	return &gsheets.Request{UpdateCells: &gsheets.UpdateCellsRequest{
		Start: &gsheets.GridCoordinate{
			SheetID:     sheetID,
			RowIndex:    a1.ZeroBased(row),
			ColumnIndex: a1.ZeroBased(col),
		},
		Rows:   []*gsheets.RowData{{Values: []*gsheets.CellData{{PivotTable: pivot}}}},
		Fields: PivotFieldMask,
	}}
}

// ClearPivot removes the pivot anchored at a cell, and its whole output
// with it. There is no deletePivotTable request: naming the field with
// no pivot in the cell is the delete.
func ClearPivot(sheetID, col, row int) *gsheets.Request {
	return WritePivot(sheetID, col, row, nil)
}
