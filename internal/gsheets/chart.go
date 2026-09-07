package gsheets

import "encoding/json"

// The chart, slicer and pivot types, added in phase 4.
//
// Two of them come in a reading half and a writing half, the way
// NewSheetProperties is separate from SheetProperties and for a sharper
// reason. A chart's spec is a union of thirteen chart kinds, most of
// which this server does not model, and `updateChartSpec` carries no
// field mask: it replaces the spec whole and refuses a partial one
// (spike L). So an update has to send back everything it read.
//
// Reading a spec into a struct and marshalling it again would drop every
// field the struct does not name — a waterfall chart's settings, an axis
// title, a series colour — and report success. That is a silent destroy
// of exactly the kind §4.3 exists to prevent, and it would be caused by
// this server rather than found in the API. So the spec is read as raw
// JSON and edited as JSON, and only the spec this server *builds* from
// nothing is typed.

// EmbeddedChart is a chart as it comes back from a read.
//
// Spec is raw for the reason above: it is round-tripped through
// manage_chart update, and a type here would quietly narrow it.
type EmbeddedChart struct {
	ChartID  int                     `json:"chartId,omitempty"`
	Spec     json.RawMessage         `json:"spec,omitempty"`
	Position *EmbeddedObjectPosition `json:"position,omitempty"`
}

// Slicer is a slicer as it comes back from a read.
type Slicer struct {
	SlicerID int                     `json:"slicerId,omitempty"`
	Spec     *SlicerSpec             `json:"spec,omitempty"`
	Position *EmbeddedObjectPosition `json:"position,omitempty"`
}

// SlicerSpec is a slicer's definition. Unlike a chart's, it is small and
// closed enough to model: one data range, one column, a title.
type SlicerSpec struct {
	DataRange   *GridRange `json:"dataRange,omitempty"`
	ColumnIndex int        `json:"columnIndex,omitempty"`
	Title       string     `json:"title,omitempty"`
}

// EmbeddedObjectPosition says where a chart or slicer sits. Exactly one
// of the three is used: SheetID for an object on its own sheet,
// OverlayPosition for one floating over a grid, and NewSheet only when
// writing.
type EmbeddedObjectPosition struct {
	SheetID         int              `json:"sheetId,omitempty"`
	OverlayPosition *OverlayPosition `json:"overlayPosition,omitempty"`
	NewSheet        bool             `json:"newSheet,omitempty"`
}

// OverlayPosition is a floating object's anchor and size.
type OverlayPosition struct {
	AnchorCell    *GridCoordinate `json:"anchorCell,omitempty"`
	OffsetXPixels int             `json:"offsetXPixels,omitempty"`
	OffsetYPixels int             `json:"offsetYPixels,omitempty"`
	WidthPixels   int             `json:"widthPixels,omitempty"`
	HeightPixels  int             `json:"heightPixels,omitempty"`
}

// ChartSpec is a chart this server builds from nothing, for add.
//
// It carries the two chart families manage_chart offers. Everything else
// a spreadsheet may hold — treemaps, waterfalls, org charts, scorecards
// — is readable, updatable and deletable through the raw spec above and
// simply cannot be created here, which is a smaller promise than the one
// a partial struct would silently make.
type ChartSpec struct {
	Title      string          `json:"title,omitempty"`
	Subtitle   string          `json:"subtitle,omitempty"`
	AltText    string          `json:"altText,omitempty"`
	BasicChart *BasicChartSpec `json:"basicChart,omitempty"`
	PieChart   *PieChartSpec   `json:"pieChart,omitempty"`
}

// BasicChartSpec is the bar/line/area/column/scatter family.
type BasicChartSpec struct {
	ChartType      string              `json:"chartType,omitempty"`
	LegendPosition string              `json:"legendPosition,omitempty"`
	StackedType    string              `json:"stackedType,omitempty"`
	HeaderCount    int                 `json:"headerCount,omitempty"`
	Domains        []*BasicChartDomain `json:"domains,omitempty"`
	Series         []*BasicChartSeries `json:"series,omitempty"`
	Axis           []*BasicChartAxis   `json:"axis,omitempty"`
}

// BasicChartDomain is what runs along the chart's category axis.
type BasicChartDomain struct {
	Domain *ChartData `json:"domain,omitempty"`
}

// BasicChartSeries is one line, bar or set of points.
type BasicChartSeries struct {
	Series     *ChartData `json:"series,omitempty"`
	TargetAxis string     `json:"targetAxis,omitempty"`
}

// BasicChartAxis is one labelled axis.
type BasicChartAxis struct {
	Position string `json:"position,omitempty"`
	Title    string `json:"title,omitempty"`
}

// PieChartSpec is a pie or doughnut.
type PieChartSpec struct {
	Domain         *ChartData `json:"domain,omitempty"`
	Series         *ChartData `json:"series,omitempty"`
	LegendPosition string     `json:"legendPosition,omitempty"`
	PieHole        float64    `json:"pieHole,omitempty"`
}

// ChartData is where one domain or series reads from.
type ChartData struct {
	SourceRange *ChartSourceRange `json:"sourceRange,omitempty"`
}

// ChartSourceRange is a domain's or series' ranges. The API requires
// exactly one dimension of each to have length 1.
type ChartSourceRange struct {
	Sources []*GridRange `json:"sources,omitempty"`
}

// AddChartRequest adds a chart.
type AddChartRequest struct {
	Chart *NewChart `json:"chart,omitempty"`
}

// NewChart is a chart that does not exist yet: a built spec and a
// position, and no id.
type NewChart struct {
	Spec     *ChartSpec              `json:"spec,omitempty"`
	Position *EmbeddedObjectPosition `json:"position,omitempty"`
}

// AddChartReply carries the id Google chose.
type AddChartReply struct {
	Chart *EmbeddedChart `json:"chart,omitempty"`
}

// UpdateChartSpecRequest replaces a chart's spec whole. Raw, because
// what goes back is what was read plus the edit.
type UpdateChartSpecRequest struct {
	ChartID int             `json:"chartId"`
	Spec    json.RawMessage `json:"spec,omitempty"`
}

// AddSlicerRequest adds a slicer.
type AddSlicerRequest struct {
	Slicer *NewSlicer `json:"slicer,omitempty"`
}

// NewSlicer is a slicer that does not exist yet.
type NewSlicer struct {
	Spec     *SlicerSpec             `json:"spec,omitempty"`
	Position *EmbeddedObjectPosition `json:"position,omitempty"`
}

// AddSlicerReply carries the id Google chose.
type AddSlicerReply struct {
	Slicer *Slicer `json:"slicer,omitempty"`
}

// UpdateSlicerSpecRequest replaces a slicer's spec.
//
// Fields is required, exactly as it is on the embedded-object move: "At
// least one field must be specified". The comment on the builder said
// this server set one for three hours while the struct had no such field
// — a claim in prose that the code did not make, and every unit test
// passed because the fake did not require it either. The live run
// refused the first slicer update it was given.
type UpdateSlicerSpecRequest struct {
	SlicerID int         `json:"slicerId"`
	Spec     *SlicerSpec `json:"spec,omitempty"`
	Fields   string      `json:"fields,omitempty"`
}

// DeleteEmbeddedObjectRequest removes a chart or a slicer. One request
// for both, and its reply is empty for both (spike L), so what was
// removed has to be read before it goes.
type DeleteEmbeddedObjectRequest struct {
	ObjectID int `json:"objectId"`
}

// UpdateEmbeddedObjectPositionRequest moves or resizes a chart.
//
// Fields is required: the API refuses the request without it rather than
// treating it as "everything given" (spike L).
type UpdateEmbeddedObjectPositionRequest struct {
	ObjectID    int                     `json:"objectId"`
	NewPosition *EmbeddedObjectPosition `json:"newPosition,omitempty"`
	Fields      string                  `json:"fields,omitempty"`
}

// UpdateEmbeddedObjectPositionReply carries where it ended up.
type UpdateEmbeddedObjectPositionReply struct {
	Position *EmbeddedObjectPosition `json:"position,omitempty"`
}
