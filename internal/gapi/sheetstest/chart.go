package sheetstest

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// The fake's charts and slicers.
//
// Each refusal here is one the live API makes, recorded by spike L, and
// the fake is deliberately no more forgiving than the thing it stands in
// for: a chart with no chart kind, a position that says nothing and a
// move with no field mask are all refused, because a server that only
// works against this fake is a server that has been tested against a
// kinder API than the one it will meet.
//
// One of them is not a refusal at all, and it is the reason this file
// reaches into the dimension code: deleting a charted column leaves the
// chart alive with its series gone and says nothing. That is what the
// live run found, so it is what the fake does.

// errChartInternal is Google's own answer to a basicChart with neither
// domains nor series: HTTP 500, "Internal error encountered". It is
// reproduced because §6.5 classes a 500 as retryable, so a client that
// trusts the status retries it forever — and the only defence is the
// server refusing to send the request, which a test can only prove
// against a fake that answers the way the API does.
var errChartInternal = &statusError{status: 500, code: "INTERNAL", message: "Internal error encountered."}

// statusError lets the fake answer with a status other than 400.
type statusError struct {
	status  int
	code    string
	message string
}

func (e *statusError) Error() string { return e.message }

// applyChart is the fake's half of the phase 4 union.
func applyChart(d *Doc, req *gsheets.Request) (*gsheets.Reply, bool, error) {
	switch {
	case req.AddChart != nil:
		return addChart(d, req.AddChart)
	case req.UpdateChartSpec != nil:
		return updateChartSpec(d, req.UpdateChartSpec)
	case req.AddSlicer != nil:
		return addSlicer(d, req.AddSlicer)
	case req.UpdateSlicerSpec != nil:
		return updateSlicerSpec(d, req.UpdateSlicerSpec)
	case req.DeleteEmbeddedObject != nil:
		return deleteEmbeddedObject(d, req.DeleteEmbeddedObject.ObjectID)
	case req.UpdateEmbeddedObjectPosition != nil:
		return moveEmbeddedObject(d, req.UpdateEmbeddedObjectPosition)
	case req.UpdateCells != nil:
		return applyCells(d, req.UpdateCells)
	}
	return applySource(d, req)
}

func addChart(d *Doc, req *gsheets.AddChartRequest) (*gsheets.Reply, bool, error) {
	if req.Chart == nil || req.Chart.Spec == nil {
		//nolint:staticcheck // Google's own wording, kept verbatim: the fake replays messages rather than paraphrasing them
		return nil, true, errors.New("Invalid requests[0].addChart: no chart spec")
	}
	spec := req.Chart.Spec
	if spec.BasicChart == nil && spec.PieChart == nil {
		//nolint:staticcheck // Google's own wording, kept verbatim: the fake replays messages rather than paraphrasing them
		return nil, true, errors.New("Invalid requests[0].addChart: One of basicChart, pieChart, bubbleChart, " +
			"candelstickChart, histogramChart, or orgChart must be set on chartSpec.")
	}
	if spec.BasicChart != nil && len(spec.BasicChart.Domains) == 0 && len(spec.BasicChart.Series) == 0 {
		return nil, true, errChartInternal
	}
	sh, pos, err := placeObject(d, req.Chart.Position)
	if err != nil {
		return nil, true, err
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, true, err
	}
	chart := &gsheets.EmbeddedChart{ChartID: nextObjectID(d), Spec: raw, Position: pos}
	sh.Charts = append(sh.Charts, chart)
	return &gsheets.Reply{AddChart: &gsheets.AddChartReply{Chart: chart}}, true, nil
}

func updateChartSpec(d *Doc, req *gsheets.UpdateChartSpecRequest) (*gsheets.Reply, bool, error) {
	chart := findChart(d, req.ChartID)
	if chart == nil {
		return nil, true, errors.New("Invalid requests[0].updateChartSpec: No chart with id: " + strconv.Itoa(req.ChartID))
	}
	// The API refuses a spec naming no chart kind rather than merging it
	// into the one that is there, which is the whole reason manage_chart
	// reads before it writes.
	var spec map[string]any
	if err := json.Unmarshal(req.Spec, &spec); err != nil {
		//nolint:staticcheck // Google's own wording, kept verbatim: the fake replays messages rather than paraphrasing them
		return nil, true, errors.New("Invalid requests[0].updateChartSpec: unreadable spec")
	}
	if !anyChartKind(spec) {
		//nolint:staticcheck // Google's own wording, kept verbatim: the fake replays messages rather than paraphrasing them
		return nil, true, errors.New("Invalid requests[0].updateChartSpec: One of basicChart, pieChart, bubbleChart, " +
			"candelstickChart, histogramChart, or orgChart must be set on chartSpec.")
	}
	chart.Spec = append(json.RawMessage(nil), req.Spec...)
	return &gsheets.Reply{}, true, nil
}

func addSlicer(d *Doc, req *gsheets.AddSlicerRequest) (*gsheets.Reply, bool, error) {
	if req.Slicer == nil || req.Slicer.Spec == nil || req.Slicer.Spec.DataRange == nil {
		//nolint:staticcheck // Google's own wording, kept verbatim: the fake replays messages rather than paraphrasing them
		return nil, true, errors.New("Invalid requests[0].addSlicer: no data range")
	}
	sh, pos, err := placeObject(d, req.Slicer.Position)
	if err != nil {
		return nil, true, err
	}
	spec := *req.Slicer.Spec
	slicer := &gsheets.Slicer{SlicerID: nextObjectID(d), Spec: &spec, Position: pos}
	sh.Slicers = append(sh.Slicers, slicer)
	return &gsheets.Reply{AddSlicer: &gsheets.AddSlicerReply{Slicer: slicer}}, true, nil
}

func updateSlicerSpec(d *Doc, req *gsheets.UpdateSlicerSpecRequest) (*gsheets.Reply, bool, error) {
	// The same requirement as the move, and the fake did not have it:
	// every unit test passed against a fake that accepted a maskless
	// update, and the live run refused the first one it was given.
	if req.Fields == "" {
		//nolint:staticcheck // Google's own wording, kept verbatim
		return nil, true, errors.New("Invalid requests[0].updateSlicerSpec: At least one field must be listed in " +
			"'fields'. (Use '*' to indicate all fields.)")
	}
	for _, sh := range d.Sheets {
		for _, sl := range sh.Slicers {
			if sl.SlicerID == req.SlicerID {
				spec := *req.Spec
				sl.Spec = &spec
				return &gsheets.Reply{}, true, nil
			}
		}
	}
	return nil, true, errors.New("Invalid requests[0].updateSlicerSpec: No slicer with id: " + strconv.Itoa(req.SlicerID))
}

// deleteEmbeddedObject removes a chart or a slicer and says nothing
// about what it removed, which is exactly what the API does.
func deleteEmbeddedObject(d *Doc, id int) (*gsheets.Reply, bool, error) {
	for _, sh := range d.Sheets {
		for i, c := range sh.Charts {
			if c.ChartID == id {
				sh.Charts = append(sh.Charts[:i], sh.Charts[i+1:]...)
				return &gsheets.Reply{}, true, nil
			}
		}
		for i, sl := range sh.Slicers {
			if sl.SlicerID == id {
				sh.Slicers = append(sh.Slicers[:i], sh.Slicers[i+1:]...)
				return &gsheets.Reply{}, true, nil
			}
		}
	}
	return nil, true, errors.New("Invalid requests[0].deleteEmbeddedObject: No embedded object with id: " + strconv.Itoa(id))
}

func moveEmbeddedObject(d *Doc, req *gsheets.UpdateEmbeddedObjectPositionRequest) (*gsheets.Reply, bool, error) {
	// Refused outright without a mask, rather than read as "everything
	// set here". A fake that accepted it would let the server ship a
	// request the API rejects on every call.
	if req.Fields == "" {
		//nolint:staticcheck // Google's own wording, kept verbatim
		return nil, true, errors.New("Invalid requests[0].updateEmbeddedObjectPosition: At least one field must be " +
			"listed in 'fields'. (Use '*' to indicate all fields.)")
	}
	chart := findChart(d, req.ObjectID)
	var current **gsheets.EmbeddedObjectPosition
	if chart != nil {
		current = &chart.Position
	} else {
		for _, sh := range d.Sheets {
			for _, sl := range sh.Slicers {
				if sl.SlicerID == req.ObjectID {
					current = &sl.Position
				}
			}
		}
	}
	if current == nil {
		return nil, true, errors.New("Invalid requests[0].updateEmbeddedObjectPosition: No embedded object with id: " +
			strconv.Itoa(req.ObjectID))
	}
	pos := &gsheets.OverlayPosition{}
	if *current != nil && (*current).OverlayPosition != nil {
		*pos = *(*current).OverlayPosition
	}
	if req.NewPosition != nil && req.NewPosition.OverlayPosition != nil {
		in := req.NewPosition.OverlayPosition
		if in.AnchorCell != nil {
			cell := *in.AnchorCell
			pos.AnchorCell = &cell
		}
		if in.WidthPixels > 0 {
			pos.WidthPixels = in.WidthPixels
		}
		if in.HeightPixels > 0 {
			pos.HeightPixels = in.HeightPixels
		}
	}
	*current = &gsheets.EmbeddedObjectPosition{OverlayPosition: pos}
	return &gsheets.Reply{UpdateEmbeddedObjectPosition: &gsheets.UpdateEmbeddedObjectPositionReply{
		Position: *current,
	}}, true, nil
}

// placeObject resolves a position onto a sheet, making one where the
// request asks for a sheet of its own.
//
// That sheet is sheetType OBJECT with no gridProperties, which is what
// the live API makes and what anything reading a card has to tolerate.
func placeObject(d *Doc, pos *gsheets.EmbeddedObjectPosition) (*Sheet, *gsheets.EmbeddedObjectPosition, error) {
	if pos == nil {
		//nolint:staticcheck // Google's own wording, kept verbatim
		return nil, nil, errors.New("Invalid requests[0].addChart: One of EmbeddedObjectPosition.overlayPosition " +
			"or EmbeddedObjectPosition.sheetId must be set")
	}
	if pos.NewSheet {
		sh := &Sheet{Props: gsheets.SheetProperties{
			SheetID: nextSheetID(d), Title: nextTitle(d, "Chart"), SheetType: "OBJECT",
		}}
		d.Sheets = append(d.Sheets, sh)
		return sh, &gsheets.EmbeddedObjectPosition{SheetID: sh.Props.SheetID}, nil
	}
	if pos.OverlayPosition == nil || pos.OverlayPosition.AnchorCell == nil {
		if pos.SheetID != 0 {
			if sh := d.FindByID(pos.SheetID); sh != nil {
				return sh, &gsheets.EmbeddedObjectPosition{SheetID: pos.SheetID}, nil
			}
		}
		//nolint:staticcheck // Google's own wording, kept verbatim
		return nil, nil, errors.New("Invalid requests[0].addChart: One of EmbeddedObjectPosition.overlayPosition " +
			"or EmbeddedObjectPosition.sheetId must be set")
	}
	sh := d.FindByID(pos.OverlayPosition.AnchorCell.SheetID)
	if sh == nil {
		return nil, nil, errors.New("No sheet with id: " + strconv.Itoa(pos.OverlayPosition.AnchorCell.SheetID))
	}
	overlay := *pos.OverlayPosition
	cell := *pos.OverlayPosition.AnchorCell
	overlay.AnchorCell = &cell
	// Google fills the size in when the request leaves it out, and the
	// numbers are its own: 600 by 371, read off a live reply.
	if overlay.WidthPixels == 0 {
		overlay.WidthPixels = 600
	}
	if overlay.HeightPixels == 0 {
		overlay.HeightPixels = 371
	}
	return sh, &gsheets.EmbeddedObjectPosition{OverlayPosition: &overlay}, nil
}

func findChart(d *Doc, id int) *gsheets.EmbeddedChart {
	for _, sh := range d.Sheets {
		for _, c := range sh.Charts {
			if c.ChartID == id {
				return c
			}
		}
	}
	return nil
}

func anyChartKind(spec map[string]any) bool {
	for _, k := range []string{
		"basicChart", "pieChart", "bubbleChart", "candlestickChart",
		"histogramChart", "orgChart", "treemapChart", "waterfallChart", "scorecardChart",
	} {
		if _, ok := spec[k]; ok {
			return true
		}
	}
	return false
}

// nextObjectID numbers charts and slicers from one counter, because
// deleteEmbeddedObject takes either and an id that meant both would hide
// a bug in which one this server addressed.
func nextObjectID(d *Doc) int {
	next := 1
	for _, sh := range d.Sheets {
		for _, c := range sh.Charts {
			if c.ChartID >= next {
				next = c.ChartID + 1
			}
		}
		for _, sl := range sh.Slicers {
			if sl.SlicerID >= next {
				next = sl.SlicerID + 1
			}
		}
	}
	return next
}

// nextTitle is the first free title under a prefix, which is how Google
// names the sheet it makes for a chart and for a data source alike.
func nextTitle(d *Doc, prefix string) string {
	for n := 1; ; n++ {
		title := prefix + strconv.Itoa(n)
		if d.Find(title) == nil {
			return title
		}
	}
}

// dropChartedColumns is the silent half, and the reason this file is not
// only about charts. Deleting a column that a chart's series reads
// leaves the chart in place with that series gone, and the API's reply
// says nothing at all (spike L).
func dropChartedColumns(d *Doc, sheetID int, rect a1.Rect, rows bool) {
	for _, sh := range d.Sheets {
		for _, c := range sh.Charts {
			c.Spec = specWithoutSources(c.Spec, sheetID, rect, rows)
		}
	}
}

// specWithoutSources removes every series whose source the band took.
func specWithoutSources(raw json.RawMessage, sheetID int, rect a1.Rect, rows bool) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		return raw
	}
	basic, ok := spec["basicChart"].(map[string]any)
	if !ok {
		return raw
	}
	list, _ := basic["series"].([]any)
	kept := make([]any, 0, len(list))
	for _, item := range list {
		if !sourceTaken(item, "series", sheetID, rect, rows) {
			kept = append(kept, item)
		}
	}
	if len(kept) == len(list) {
		return raw
	}
	basic["series"] = kept
	out, err := json.Marshal(spec)
	if err != nil {
		return raw
	}
	return out
}

// sourceTaken says whether one series or domain reads from the band.
func sourceTaken(item any, field string, sheetID int, rect a1.Rect, rows bool) bool {
	m, ok := item.(map[string]any)
	if !ok {
		return false
	}
	raw, err := json.Marshal(m[field])
	if err != nil {
		return false
	}
	var data gsheets.ChartData
	if err := json.Unmarshal(raw, &data); err != nil || data.SourceRange == nil {
		return false
	}
	for _, g := range data.SourceRange.Sources {
		if g == nil || g.SheetID != sheetID {
			continue
		}
		source := a1.FromGridRange(g)
		// A column delete takes a series that reads only those columns;
		// a row delete leaves the series and shortens it, which is what
		// live does and why the two axes are not the same case.
		if !rows && source.FirstCol >= rect.FirstCol && source.LastCol != 0 && source.LastCol <= rect.LastCol {
			return true
		}
	}
	return false
}
