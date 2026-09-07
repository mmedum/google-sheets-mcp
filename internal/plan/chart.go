package plan

import (
	"encoding/json"

	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// The phase 4 builders: charts, slicers and the embedded-object requests
// that serve both. A pivot table has no request of its own and is built
// in pivot.go, through updateCells.

// AddChart adds a chart at a position.
func AddChart(spec *gsheets.ChartSpec, pos *gsheets.EmbeddedObjectPosition) *gsheets.Request {
	return &gsheets.Request{AddChart: &gsheets.AddChartRequest{
		Chart: &gsheets.NewChart{Spec: spec, Position: pos},
	}}
}

// UpdateChartSpec replaces a chart's spec.
//
// The spec is raw and whole on purpose. UpdateChartSpecRequest carries
// no field mask and the API refuses a spec that names no chart kind, so
// there is no partial update to build: what goes out is what was read,
// with the caller's edit applied to it.
func UpdateChartSpec(chartID int, spec json.RawMessage) *gsheets.Request {
	return &gsheets.Request{UpdateChartSpec: &gsheets.UpdateChartSpecRequest{
		ChartID: chartID, Spec: spec,
	}}
}

// AddSlicer adds a slicer over a range.
func AddSlicer(spec *gsheets.SlicerSpec, pos *gsheets.EmbeddedObjectPosition) *gsheets.Request {
	return &gsheets.Request{AddSlicer: &gsheets.AddSlicerRequest{
		Slicer: &gsheets.NewSlicer{Spec: spec, Position: pos},
	}}
}

// UpdateSlicerSpec replaces a slicer's spec under a field mask.
//
// The mask is an argument rather than a constant, and it is required:
// without one the API refuses the request outright, the same way it
// refuses a move with no mask.
func UpdateSlicerSpec(slicerID int, spec *gsheets.SlicerSpec, fields string) *gsheets.Request {
	return &gsheets.Request{UpdateSlicerSpec: &gsheets.UpdateSlicerSpecRequest{
		SlicerID: slicerID, Spec: spec, Fields: fields,
	}}
}

// DeleteEmbeddedObject removes a chart or a slicer. One request serves
// both, and its reply is empty for both, so the caller reads what it is
// about to remove before sending this.
func DeleteEmbeddedObject(objectID int) *gsheets.Request {
	return &gsheets.Request{DeleteEmbeddedObject: &gsheets.DeleteEmbeddedObjectRequest{
		ObjectID: objectID,
	}}
}

// MoveEmbeddedObject moves or resizes a chart or slicer.
//
// fields is required by the API — without it the request is refused
// outright rather than read as "everything set here" — so it is an
// argument rather than a constant: a move sets anchorCell, a resize sets
// the two pixel fields, and doing both sets all three.
func MoveEmbeddedObject(objectID int, pos *gsheets.EmbeddedObjectPosition, fields string) *gsheets.Request {
	return &gsheets.Request{UpdateEmbeddedObjectPosition: &gsheets.UpdateEmbeddedObjectPositionRequest{
		ObjectID: objectID, NewPosition: pos, Fields: fields,
	}}
}

// OnNewSheet asks Google to make a sheet for the object.
//
// That sheet comes back with sheetType OBJECT and no gridProperties at
// all, so nothing reading a card may assume a sheet has a grid.
func OnNewSheet() *gsheets.EmbeddedObjectPosition {
	return &gsheets.EmbeddedObjectPosition{NewSheet: true}
}

// ChartSource is one domain or series: a rectangle read as a single
// column or a single row.
func ChartSource(r *gsheets.GridRange) *gsheets.ChartData {
	return &gsheets.ChartData{SourceRange: &gsheets.ChartSourceRange{
		Sources: []*gsheets.GridRange{r},
	}}
}
