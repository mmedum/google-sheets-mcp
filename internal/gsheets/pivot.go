package gsheets

// A pivot table is the odd structure in the API: there is no
// addPivotTable, no updatePivotTable and no deletePivotTable. A pivot is
// a field of one CellData, written through updateCells at the cell it is
// anchored to, and removed by an updateCells naming that field with an
// empty cell — which takes the whole computed output with it (spike M).
//
// The same reading/writing split as the chart types, and for the same
// reason: an update replaces the pivot whole, so what this server does
// not model has to survive the round trip as raw JSON.

// UpdateCellsRequest writes cells. Fields is a CellData field mask, and
// the API requires at least one: `pivotTable` alone writes a pivot and
// disturbs nothing else in the cell.
//
// Range and Start are alternatives. Start is one corner and lets the
// rows decide how far the write reaches, which is what anchoring a pivot
// needs; Range clears the fields named across the whole rectangle where
// the rows do not cover it.
type UpdateCellsRequest struct {
	Start  *GridCoordinate `json:"start,omitempty"`
	Range  *GridRange      `json:"range,omitempty"`
	Rows   []*RowData      `json:"rows,omitempty"`
	Fields string          `json:"fields,omitempty"`
}

// PivotTable is a pivot this server builds from nothing, for add.
type PivotTable struct {
	Source      *GridRange    `json:"source,omitempty"`
	Rows        []*PivotGroup `json:"rows,omitempty"`
	Columns     []*PivotGroup `json:"columns,omitempty"`
	Values      []*PivotValue `json:"values,omitempty"`
	ValueLayout string        `json:"valueLayout,omitempty"`
}

// PivotGroup is one row or column grouping.
//
// SortOrder is not optional in practice: a group without one is refused
// with "No sort order specified" (spike M), so every group this server
// builds carries one.
type PivotGroup struct {
	SourceColumnOffset int    `json:"sourceColumnOffset"`
	ShowTotals         bool   `json:"showTotals,omitempty"`
	SortOrder          string `json:"sortOrder,omitempty"`
	Label              string `json:"label,omitempty"`
}

// PivotValue is one summarised column.
type PivotValue struct {
	SourceColumnOffset int    `json:"sourceColumnOffset"`
	SummarizeFunction  string `json:"summarizeFunction,omitempty"`
	Name               string `json:"name,omitempty"`
}

// A pivot as it comes back from a read is not a type here at all. It
// arrives as CellData.PivotTable, which is raw JSON, and manage_pivot_table
// edits that JSON rather than a struct built from it — the same reason
// the chart spec is raw: an update replaces the pivot whole, so a field
// this server does not model has to survive the round trip.
