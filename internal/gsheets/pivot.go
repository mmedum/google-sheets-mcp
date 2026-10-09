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
//
// FilterSpecs is the filter form this server writes. A response carries
// the deprecated criteria map as well, keyed by the same offsets; a
// request that sends both has filterSpecs win, so a write that changes
// filters removes criteria from the raw pivot rather than modeling it.
type PivotTable struct {
	Source      *GridRange         `json:"source,omitempty"`
	Rows        []*PivotGroup      `json:"rows,omitempty"`
	Columns     []*PivotGroup      `json:"columns,omitempty"`
	Values      []*PivotValue      `json:"values,omitempty"`
	ValueLayout string             `json:"valueLayout,omitempty"`
	FilterSpecs []*PivotFilterSpec `json:"filterSpecs,omitempty"`
}

// PivotGroup is one row or column grouping.
//
// SortOrder is not optional in practice: a group without one is refused
// with "No sort order specified" (spike M), so every group this server
// builds carries one.
type PivotGroup struct {
	SourceColumnOffset int             `json:"sourceColumnOffset"`
	ShowTotals         bool            `json:"showTotals,omitempty"`
	SortOrder          string          `json:"sortOrder,omitempty"`
	Label              string          `json:"label,omitempty"`
	GroupRule          *PivotGroupRule `json:"groupRule,omitempty"`
}

// PivotGroupRule buckets a group's values rather than listing each one.
// One of the two is set. Google allows one group with a rule per source
// column. A rule made by hand in Sheets (manualRule) is read raw and
// never written here.
type PivotGroupRule struct {
	DateTimeRule  *DateTimeRule  `json:"dateTimeRule,omitempty"`
	HistogramRule *HistogramRule `json:"histogramRule,omitempty"`
}

// DateTimeRule groups dates by a part of them, such as YEAR_MONTH.
type DateTimeRule struct {
	Type string `json:"type,omitempty"`
}

// HistogramRule groups numbers into buckets of a fixed size. Interval
// is required and positive; Start and End are optional, and values
// outside them fall into one bucket each side.
type HistogramRule struct {
	Interval float64  `json:"interval,omitempty"`
	Start    *float64 `json:"start,omitempty"`
	End      *float64 `json:"end,omitempty"`
}

// PivotValue is one summarized column, or a calculated value.
//
// SourceColumnOffset and Formula are a union, and exactly one is set:
// a value sending both is refused. So the offset is a pointer, which
// sends a zero and leaves out an unset one. A formula value is
// summarized by SUM or CUSTOM only.
type PivotValue struct {
	SourceColumnOffset *int   `json:"sourceColumnOffset,omitempty"`
	Formula            string `json:"formula,omitempty"`
	SummarizeFunction  string `json:"summarizeFunction,omitempty"`
	Name               string `json:"name,omitempty"`
}

// PivotFilterSpec filters the source's rows by one column before they
// are summarized. The offset counts from the source's first column, like
// a group's.
type PivotFilterSpec struct {
	ColumnOffsetIndex int                  `json:"columnOffsetIndex"`
	FilterCriteria    *PivotFilterCriteria `json:"filterCriteria,omitempty"`
}

// PivotFilterCriteria is which values of a column are shown.
//
// With VisibleByDefault false, a value is shown when it is in
// VisibleValues and meets Condition; with it true, VisibleValues is
// ignored and a value is shown when it meets Condition. So a filter by
// condition alone sets it, and nothing here offers it as a choice.
type PivotFilterCriteria struct {
	VisibleValues    []string          `json:"visibleValues,omitempty"`
	Condition        *BooleanCondition `json:"condition,omitempty"`
	VisibleByDefault bool              `json:"visibleByDefault,omitempty"`
}

// A pivot as it comes back from a read is not a type here at all. It
// arrives as CellData.PivotTable, which is raw JSON, and manage_pivot_table
// edits that JSON rather than a struct built from it — the same reason
// the chart spec is raw: an update replaces the pivot whole, so a field
// this server does not model has to survive the round trip.
