package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/plan"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// Chart actions.
const (
	ChartAdd    = "add"
	ChartUpdate = "update"
	ChartMove   = "move"
	ChartDelete = "delete"
	ChartList   = "list"
)

// The chart kinds this server can build, and what each compiles to.
//
// A kind absent from here can still be read, retitled, moved and deleted
// through the raw spec — what it cannot be is created, which is the
// honest limit of a builder that models two families out of thirteen.
var basicChartTypes = map[string]string{
	"column":       "COLUMN",
	"bar":          "BAR",
	"line":         "LINE",
	"area":         "AREA",
	"scatter":      "SCATTER",
	"stepped_area": "STEPPED_AREA",
}

var legendPositions = map[string]string{
	"bottom": "BOTTOM_LEGEND",
	"left":   "LEFT_LEGEND",
	"right":  "RIGHT_LEGEND",
	"top":    "TOP_LEGEND",
	"none":   "NO_LEGEND",
}

var stackedTypes = map[string]string{
	"none":    "NOT_STACKED",
	"stacked": "STACKED",
	"percent": "PERCENT_STACKED",
}

// stackable are the chart types stackedType applies to, in the
// reference's own words: "Area, Bar, Column, Combo, and Stepped Area".
//
// Sent with any other type the API refuses the whole request — live, a
// LINE chart given NOT_STACKED came back "chartSpec.basicChart.stackedType
// not supported when chartSpec.basicChart.chartType is LINE". That is a
// clean 400 rather than a silent wrong answer, but it fails a call that
// asked for two things and could have had one, so the refusal is made
// here where it can say which types take the argument.
var stackable = map[string]bool{"area": true, "bar": true, "column": true, "stepped_area": true}

// stackedFor maps the caller's word onto the API's, refusing the pairing
// the API refuses.
func stackedFor(chartType, stacked string) (string, error) {
	stacked = strings.ToLower(strings.TrimSpace(stacked))
	if stacked == "" {
		return "", nil
	}
	v, ok := stackedTypes[stacked]
	if !ok {
		return "", Errorf("invalid", "stacked %q is not one of none, stacked, percent", stacked)
	}
	if kind := strings.ToLower(strings.TrimSpace(chartType)); !stackable[kind] {
		return "", Errorf("invalid",
			"a %s chart cannot be stacked; stacked applies to %s. Leave it out, or change chart_type",
			kind, join(stackableNames()))
	}
	return v, nil
}

func stackableNames() []string { return sortedKeys(stackable) }

// ChartRequest is what manage_chart asks for.
type ChartRequest struct {
	Spreadsheet string
	Sheet       string
	Action      string
	// Slicer switches the whole tool to slicers, which share the delete
	// and the move with charts and have a spec of their own.
	Slicer bool
	// ID names an existing chart or slicer, for update, move and delete.
	ID int

	Title     string
	Subtitle  string
	ChartType string
	Domain    string
	Series    []string
	Headers   int
	Legend    string
	Stacked   string
	AxisTitle string

	// Anchor is the cell the object's top-left corner sits on.
	Anchor   string
	NewSheet bool
	Width    int
	Height   int

	// Column is the slicer's filtering column, in A1 letters.
	Column string
	Range  string

	DryRun bool
}

// ChartRecord is one chart or slicer, as a listing reports it.
type ChartRecord struct {
	ID       int    `json:"id"`
	Kind     string `json:"kind" jsonschema:"chart or slicer"`
	Title    string `json:"title,omitempty"`
	Type     string `json:"type,omitempty" jsonschema:"the chart kind, as the API names it"`
	Sheet    string `json:"sheet,omitempty" jsonschema:"the sheet it sits on"`
	Position string `json:"position,omitempty" jsonschema:"the anchor cell, or its own sheet"`
	Sources  string `json:"reads,omitempty" jsonschema:"the ranges it charts"`
	// Broken says the chart has lost the data it was built on, which is
	// what a deleted column leaves behind and what nothing else reports.
	Broken bool `json:"broken,omitempty" jsonschema:"true when the chart has no series left, which is what deleting its source column does"`
}

// ChartResult is manage_chart's answer.
type ChartResult struct {
	Summary     string        `json:"summary"`
	Spreadsheet string        `json:"spreadsheet"`
	Action      string        `json:"action"`
	Charts      []ChartRecord `json:"charts,omitempty"`
	ID          int           `json:"id,omitempty" jsonschema:"the chart or slicer this call added or changed"`
	DryRun      bool          `json:"dry_run,omitempty"`
}

// Render is the text half.
func (r ChartResult) Render() string { return r.Summary }

// ManageChart answers manage_chart.
//
// Four of the five actions are shaped by what spike L found rather than
// by the reference: an update reads the spec and sends it back whole
// because the API refuses a partial one, a move must name its own field
// mask, a delete reports from a read taken before it because the reply
// is empty, and an add validates its own domain and series because the
// API's answer to a chart with neither is HTTP 500.
func (s *Service) ManageChart(ctx context.Context, req ChartRequest) (*ChartResult, error) {
	ref, err := s.Resolve(ctx, req.Spreadsheet)
	if err != nil {
		return nil, err
	}
	res := &ChartResult{Spreadsheet: ref.ID, Action: req.Action}
	switch req.Action {
	case ChartList:
		return s.listCharts(ctx, ref, req, res)
	case ChartAdd:
		return s.addChart(ctx, ref, req, res)
	case ChartUpdate:
		return s.updateChart(ctx, ref, req, res)
	case ChartMove:
		return s.moveChart(ctx, ref, req, res)
	case ChartDelete:
		return s.deleteChart(ctx, ref, req, res)
	}
	return nil, Errorf("invalid", "action %q is not one of add, update, move, delete, list", req.Action)
}

// listCharts reports every chart and slicer, or those on one sheet.
func (s *Service) listCharts(ctx context.Context, ref Reference, req ChartRequest, res *ChartResult) (*ChartResult, error) {
	found, err := s.chartsIn(ctx, ref, strings.TrimSpace(req.Sheet))
	if err != nil {
		return nil, err
	}
	res.Charts = found
	res.Summary = render.ChartList(chartRows(found), strings.TrimSpace(req.Sheet))
	return res, nil
}

// addChart builds a chart from nothing and sends it.
func (s *Service) addChart(ctx context.Context, ref Reference, req ChartRequest, res *ChartResult) (*ChartResult, error) {
	sp, err := s.card(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	props, err := s.findSheet(sp, strings.TrimSpace(req.Sheet), ref)
	if err != nil {
		return nil, err
	}
	pos, err := s.chartPosition(ctx, ref, props, req)
	if err != nil {
		return nil, err
	}
	if req.Slicer {
		return s.addSlicer(ctx, ref, props, req, pos, res)
	}

	spec, sources, err := s.buildChartSpec(ctx, ref, props, req)
	if err != nil {
		return nil, err
	}
	act := render.ChartAct{
		Action: ChartAdd, Title: req.Title, Type: req.ChartType,
		Sheet: props.Title, Position: positionName(pos, props.Title), Sources: sources,
	}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.ChartPreview(act)
		return res, nil
	}
	reply, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{plan.AddChart(spec, pos)},
	})
	if err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	if r := firstReply(reply); r != nil && r.AddChart != nil && r.AddChart.Chart != nil {
		res.ID = r.AddChart.Chart.ChartID
		act.ID = res.ID
		// The reply says where it went, which for new_sheet is the only
		// cheap way to learn the sheet Google made.
		if p := r.AddChart.Chart.Position; p != nil && p.OverlayPosition == nil && p.SheetID != 0 {
			act.Position = fmt.Sprintf("a sheet of its own, id %d", p.SheetID)
		}
	}
	res.Summary = render.ChartDone(act)
	return res, nil
}

// addSlicer is the same act for the other embedded object.
func (s *Service) addSlicer(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	req ChartRequest, pos *gsheets.EmbeddedObjectPosition, res *ChartResult) (*ChartResult, error) {

	if strings.TrimSpace(req.Range) == "" {
		return nil, Errorf("invalid", "a slicer needs range, the block of data it filters")
	}
	target, err := s.ResolveRange(ctx, ref, props.Title, req.Range)
	if err != nil {
		return nil, err
	}
	column, err := slicerColumn(req.Column, target.Rect)
	if err != nil {
		return nil, err
	}
	spec := &gsheets.SlicerSpec{
		DataRange:   target.Rect.GridRange(target.Props.SheetID),
		ColumnIndex: column,
		Title:       req.Title,
	}
	act := render.ChartAct{
		Action: ChartAdd, Slicer: true, Title: req.Title, Sheet: props.Title,
		Position: positionName(pos, props.Title),
		Sources:  []string{a1.Format(target.Props.Title, target.Rect)},
	}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.ChartPreview(act)
		return res, nil
	}
	reply, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{plan.AddSlicer(spec, pos)},
	})
	if err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	if r := firstReply(reply); r != nil && r.AddSlicer != nil && r.AddSlicer.Slicer != nil {
		res.ID = r.AddSlicer.Slicer.SlicerID
		act.ID = res.ID
	}
	res.Summary = render.ChartDone(act)
	return res, nil
}

// updateChart edits a spec and sends it back whole.
//
// Whole, because `updateChartSpec` carries no field mask and refuses a
// spec naming no chart kind. Editing the JSON that was read, rather than
// a struct built from it, is what keeps a field this server does not
// model — an axis title, a series colour, a treemap's settings — from
// disappearing on a call that only meant to change the title.
func (s *Service) updateChart(ctx context.Context, ref Reference, req ChartRequest, res *ChartResult) (*ChartResult, error) {
	if req.ID == 0 {
		return nil, Errorf("invalid", "update needs id, which manage_chart list reports")
	}
	if req.Slicer {
		return s.updateSlicer(ctx, ref, req, res)
	}
	chart, props, err := s.findChart(ctx, ref, req.ID)
	if err != nil {
		return nil, err
	}
	// Where an unqualified range in this call resolves. The sheet the
	// chart sits on is the wrong answer for a chart on its own sheet:
	// that sheet is sheetType OBJECT with no cells at all, so a domain
	// of "A1:A10" would build a source range on a sheet that has none.
	data, err := s.chartDataSheet(ctx, ref, req, props)
	if err != nil {
		return nil, err
	}
	spec := map[string]any{}
	if len(chart.Spec) > 0 {
		if err := json.Unmarshal(chart.Spec, &spec); err != nil {
			return nil, Errorf("unsupported", "chart %d has a spec this server could not read back, so it cannot "+
				"be updated without losing what it holds", req.ID)
		}
	}
	changed, err := s.editChartSpec(ctx, ref, data, req, spec)
	if err != nil {
		return nil, err
	}
	if len(changed) == 0 {
		return nil, Errorf("invalid", "update was given nothing to change; pass title, subtitle, chart_type, "+
			"legend, stacked, domain or series")
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return nil, Errorf("invalid", "the edited chart spec could not be built: %v", err)
	}
	act := render.ChartAct{
		Action: ChartUpdate, ID: req.ID, Title: chartTitle(spec), Sheet: props.Title, Changed: changed,
	}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.ChartPreview(act)
		return res, nil
	}
	if _, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{plan.UpdateChartSpec(req.ID, body)},
	}); err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	res.ID = req.ID
	res.Summary = render.ChartDone(act)
	return res, nil
}

// updateSlicer replaces a slicer's spec, which does take a field mask
// and is small enough to model whole.
func (s *Service) updateSlicer(ctx context.Context, ref Reference, req ChartRequest, res *ChartResult) (*ChartResult, error) {
	slicer, props, err := s.findSlicer(ctx, ref, req.ID)
	if err != nil {
		return nil, err
	}
	spec := &gsheets.SlicerSpec{}
	if slicer.Spec != nil {
		*spec = *slicer.Spec
	}
	// The mask names the wire fields, beside the argument names the
	// result reports: the API takes one and the caller reads the other.
	var changed, fields []string
	if req.Title != "" {
		spec.Title = req.Title
		changed = append(changed, "title")
		fields = append(fields, "title")
	}
	if strings.TrimSpace(req.Range) != "" {
		target, err := s.ResolveRange(ctx, ref, props.Title, req.Range)
		if err != nil {
			return nil, err
		}
		spec.DataRange = target.Rect.GridRange(target.Props.SheetID)
		changed = append(changed, "range")
		fields = append(fields, "dataRange")
	}
	if strings.TrimSpace(req.Column) != "" {
		rect := a1.FromGridRange(spec.DataRange)
		column, err := slicerColumn(req.Column, rect)
		if err != nil {
			return nil, err
		}
		spec.ColumnIndex = column
		changed = append(changed, "column")
		fields = append(fields, "columnIndex")
	}
	if len(changed) == 0 {
		return nil, Errorf("invalid", "update was given nothing to change; pass title, range or column")
	}
	act := render.ChartAct{Action: ChartUpdate, Slicer: true, ID: req.ID, Title: spec.Title,
		Sheet: props.Title, Changed: changed}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.ChartPreview(act)
		return res, nil
	}
	if _, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{plan.UpdateSlicerSpec(req.ID, spec, strings.Join(fields, ","))},
	}); err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	res.ID = req.ID
	res.Summary = render.ChartDone(act)
	return res, nil
}

// moveChart moves or resizes an embedded object.
func (s *Service) moveChart(ctx context.Context, ref Reference, req ChartRequest, res *ChartResult) (*ChartResult, error) {
	if req.ID == 0 {
		return nil, Errorf("invalid", "move needs id, which manage_chart list reports")
	}
	title, props, err := s.findObject(ctx, ref, req.ID, req.Slicer)
	if err != nil {
		return nil, err
	}
	pos := &gsheets.EmbeddedObjectPosition{OverlayPosition: &gsheets.OverlayPosition{}}
	var fields []string
	if anchor := strings.TrimSpace(req.Anchor); anchor != "" {
		cell, err := s.anchorCell(ctx, ref, props, anchor)
		if err != nil {
			return nil, err
		}
		pos.OverlayPosition.AnchorCell = cell
		fields = append(fields, "anchorCell")
	}
	if req.Width > 0 {
		pos.OverlayPosition.WidthPixels = req.Width
		fields = append(fields, "widthPixels")
	}
	if req.Height > 0 {
		pos.OverlayPosition.HeightPixels = req.Height
		fields = append(fields, "heightPixels")
	}
	// The API refuses the request outright without a field mask, rather
	// than reading it as "everything given" — so an empty one is refused
	// here, where the message can name the arguments.
	if len(fields) == 0 {
		return nil, Errorf("invalid", "move needs anchor, width or height; none were given")
	}
	act := render.ChartAct{Action: ChartMove, Slicer: req.Slicer, ID: req.ID, Title: title,
		Sheet: props.Title, Changed: fields, Position: strings.TrimSpace(req.Anchor)}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.ChartPreview(act)
		return res, nil
	}
	reply, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{plan.MoveEmbeddedObject(req.ID, pos, strings.Join(fields, ","))},
	})
	if err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	// The reply carries where it landed, so the result reports the API's
	// answer rather than the request's own arguments.
	if r := firstReply(reply); r != nil && r.UpdateEmbeddedObjectPosition != nil {
		act.Position = positionName(r.UpdateEmbeddedObjectPosition.Position, props.Title)
	}
	res.ID = req.ID
	res.Summary = render.ChartDone(act)
	return res, nil
}

// deleteChart removes a chart or slicer, and says what it was.
//
// `deleteEmbeddedObject` replies `{}` for both, so everything the result
// reports comes from the read taken before the delete. A chart is not a
// gated destructive tool: its whole definition is readable first, which
// is the line §17c draws between a label and data.
func (s *Service) deleteChart(ctx context.Context, ref Reference, req ChartRequest, res *ChartResult) (*ChartResult, error) {
	if req.ID == 0 {
		return nil, Errorf("invalid", "delete needs id, which manage_chart list reports")
	}
	title, props, err := s.findObject(ctx, ref, req.ID, req.Slicer)
	if err != nil {
		return nil, err
	}
	act := render.ChartAct{Action: ChartDelete, Slicer: req.Slicer, ID: req.ID, Title: title, Sheet: props.Title}
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.ChartPreview(act)
		return res, nil
	}
	if _, err := s.api.BatchUpdate(ctx, ref.ID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{plan.DeleteEmbeddedObject(req.ID)},
	}); err != nil {
		return nil, wrap(err)
	}
	s.forget(ref.ID)
	res.ID = req.ID
	res.Summary = render.ChartDone(act)
	return res, nil
}

// buildChartSpec turns the tool's arguments into a spec, and refuses the
// two shapes the API answers badly.
func (s *Service) buildChartSpec(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	req ChartRequest) (*gsheets.ChartSpec, []string, error) {

	kind := strings.ToLower(strings.TrimSpace(req.ChartType))
	if kind == "" {
		return nil, nil, Errorf("invalid", "add needs chart_type: one of %s", join(chartTypeNames()))
	}
	domain, domainName, err := s.chartData(ctx, ref, props, req.Domain, "domain")
	if err != nil {
		return nil, nil, err
	}
	if len(req.Series) == 0 {
		return nil, nil, Errorf("invalid", "add needs series, at least one range of values to chart")
	}
	var series []*gsheets.BasicChartSeries
	sources := []string{domainName}
	for _, one := range req.Series {
		data, name, err := s.chartData(ctx, ref, props, one, "series")
		if err != nil {
			return nil, nil, err
		}
		series = append(series, &gsheets.BasicChartSeries{Series: data})
		sources = append(sources, name)
	}

	spec := &gsheets.ChartSpec{Title: req.Title, Subtitle: req.Subtitle}
	switch kind {
	case "pie", "doughnut":
		if len(series) > 1 {
			return nil, nil, Errorf("invalid", "a %s chart draws one series and %d were given", kind, len(series))
		}
		pie := &gsheets.PieChartSpec{Domain: domain, Series: series[0].Series}
		if kind == "doughnut" {
			pie.PieHole = 0.5
		}
		if pos, err := legendOf(req.Legend); err != nil {
			return nil, nil, err
		} else if pos != "" {
			pie.LegendPosition = pos
		}
		spec.PieChart = pie
	default:
		chartType, ok := basicChartTypes[kind]
		if !ok {
			return nil, nil, Errorf("invalid", "chart_type %q is not one of %s", req.ChartType, join(chartTypeNames()))
		}
		basic := &gsheets.BasicChartSpec{
			ChartType:   chartType,
			HeaderCount: req.Headers,
			Domains:     []*gsheets.BasicChartDomain{{Domain: domain}},
			Series:      series,
		}
		if pos, err := legendOf(req.Legend); err != nil {
			return nil, nil, err
		} else if pos != "" {
			basic.LegendPosition = pos
		}
		stackedType, err := stackedFor(kind, req.Stacked)
		if err != nil {
			return nil, nil, err
		}
		basic.StackedType = stackedType
		if req.AxisTitle != "" {
			basic.Axis = []*gsheets.BasicChartAxis{{Position: "LEFT_AXIS", Title: req.AxisTitle}}
		}
		spec.BasicChart = basic
	}
	return spec, sources, nil
}

// chartData resolves one A1 range into a chart source.
//
// A domain or a series has to be a single column or a single row: the
// API says so, and its refusal names GridRange indices rather than the
// range the caller wrote.
func (s *Service) chartData(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	rangeA1, what string) (*gsheets.ChartData, string, error) {

	if strings.TrimSpace(rangeA1) == "" {
		return nil, "", Errorf("invalid", "add needs %s, a range to read from", what)
	}
	target, err := s.ResolveRange(ctx, ref, props.Title, rangeA1)
	if err != nil {
		return nil, "", err
	}
	rect := target.Rect
	oneColumn := rect.FirstCol != 0 && rect.FirstCol == rect.LastCol
	oneRow := rect.FirstRow != 0 && rect.FirstRow == rect.LastRow
	if !oneColumn && !oneRow {
		return nil, "", Errorf("invalid",
			"the %s range %s covers several columns and several rows; a chart reads one column or one row at a time, "+
				"so pass one range per series", what, a1.Format(target.Props.Title, rect))
	}
	return plan.ChartSource(rect.GridRange(target.Props.SheetID)),
		a1.Format(target.Props.Title, rect), nil
}

// editChartSpec applies the arguments to a spec that already exists, and
// says what it changed.
func (s *Service) editChartSpec(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	req ChartRequest, spec map[string]any) ([]string, error) {

	var changed []string
	if req.Title != "" {
		spec["title"] = req.Title
		changed = append(changed, "title")
	}
	if req.Subtitle != "" {
		spec["subtitle"] = req.Subtitle
		changed = append(changed, "subtitle")
	}

	basic, isBasic := spec["basicChart"].(map[string]any)
	pie, isPie := spec["pieChart"].(map[string]any)
	wantsData := strings.TrimSpace(req.Domain) != "" || len(req.Series) > 0
	wantsInner := wantsData || req.ChartType != "" || req.Legend != "" ||
		req.Stacked != "" || req.Headers > 0 || req.AxisTitle != ""
	if wantsInner && !isBasic && !isPie {
		// The spec is a kind this server does not build. Retitling it is
		// safe; rebuilding its data is not, and filling in a basicChart
		// here would replace whatever the chart really is.
		return nil, Errorf("unsupported",
			"chart %d is a %s, which this server can retitle, move and delete but not rebuild; "+
				"change its data in the Sheets interface", req.ID, chartKind(spec))
	}
	// A pie chart is one this server does build, so it is updatable —
	// within what a pie has. It draws one series and has no axes and no
	// stacking, and a refusal that names the argument beats one that
	// says the chart cannot be rebuilt, which for a pie is untrue.
	if isPie {
		return s.editPieSpec(ctx, ref, props, req, pie, changed)
	}
	changed, err := editBasicSpec(req, basic, changed)
	if err != nil {
		return nil, err
	}
	return s.editChartData(ctx, ref, props, req, basic, changed)
}

// editBasicSpec applies the settings that need no range resolved, which
// is what keeps the two halves of an edit readable apart.
func editBasicSpec(req ChartRequest, basic map[string]any, changed []string) ([]string, error) {
	if req.ChartType != "" {
		kind, ok := basicChartTypes[strings.ToLower(strings.TrimSpace(req.ChartType))]
		if !ok {
			return nil, Errorf("invalid", "chart_type %q cannot be changed to on an existing chart; it is one of %s",
				req.ChartType, join(basicChartTypeNames()))
		}
		basic["chartType"] = kind
		changed = append(changed, "chart_type")
	}
	if req.Legend != "" {
		pos, err := legendOf(req.Legend)
		if err != nil {
			return nil, err
		}
		basic["legendPosition"] = pos
		changed = append(changed, "legend")
	}
	if req.Headers > 0 {
		basic["headerCount"] = req.Headers
		changed = append(changed, "headers")
	}
	if req.AxisTitle != "" {
		basic["axis"] = []any{map[string]any{"position": "LEFT_AXIS", "title": req.AxisTitle}}
		changed = append(changed, "axis_title")
	}
	if req.Stacked != "" {
		// Against the type the chart will have after this call, which is
		// the new one where the caller gave one and the existing one
		// otherwise. Checking it against the old type would refuse a
		// call that turns a line chart into a stacked column chart, and
		// accept the one that does the reverse.
		kind := req.ChartType
		if kind == "" {
			kind, _ = basic["chartType"].(string)
		}
		v, err := stackedFor(kind, req.Stacked)
		if err != nil {
			return nil, err
		}
		basic["stackedType"] = v
		changed = append(changed, "stacked")
	}
	return changed, nil
}

// editChartData applies the settings that name a range.
func (s *Service) editChartData(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	req ChartRequest, basic map[string]any, changed []string) ([]string, error) {

	if strings.TrimSpace(req.Domain) != "" {
		data, _, err := s.chartData(ctx, ref, props, req.Domain, "domain")
		if err != nil {
			return nil, err
		}
		basic["domains"] = []any{map[string]any{"domain": data}}
		changed = append(changed, "domain")
	}
	if len(req.Series) > 0 {
		var out []any
		for _, one := range req.Series {
			data, _, err := s.chartData(ctx, ref, props, one, "series")
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"series": data})
		}
		basic["series"] = out
		changed = append(changed, "series")
	}
	return changed, nil
}

// editPieSpec is the same for the other kind this server builds. A pie
// has one series, no axes and no stacking, so the arguments that mean
// nothing here are refused by name rather than ignored.
func (s *Service) editPieSpec(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	req ChartRequest, pie map[string]any, changed []string) ([]string, error) {

	for _, unsupported := range []struct {
		set  bool
		name string
	}{
		{req.Stacked != "", "stacked"},
		{req.AxisTitle != "", "axis_title"},
		{req.Headers > 0, "headers"},
		{req.ChartType != "", "chart_type"},
	} {
		if unsupported.set {
			return nil, Errorf("invalid",
				"a pie chart has no %s. Delete it and add the chart you want instead", unsupported.name)
		}
	}
	if req.Legend != "" {
		pos, err := legendOf(req.Legend)
		if err != nil {
			return nil, err
		}
		pie["legendPosition"] = pos
		changed = append(changed, "legend")
	}
	if strings.TrimSpace(req.Domain) != "" {
		data, _, err := s.chartData(ctx, ref, props, req.Domain, "domain")
		if err != nil {
			return nil, err
		}
		pie["domain"] = data
		changed = append(changed, "domain")
	}
	switch {
	case len(req.Series) > 1:
		return nil, Errorf("invalid", "a pie chart draws one series and %d were given", len(req.Series))
	case len(req.Series) == 1:
		data, _, err := s.chartData(ctx, ref, props, req.Series[0], "series")
		if err != nil {
			return nil, err
		}
		pie["series"] = data
		changed = append(changed, "series")
	}
	return changed, nil
}

// chartDataSheet is the sheet an unqualified range in an update means.
//
// The sheet argument where the caller gave one, and the chart's own
// otherwise — refusing when that has no grid, which is what a chart on
// its own sheet sits on. Silently building a range against a sheet with
// no cells would be a chart pointed at nothing, reported as a success.
func (s *Service) chartDataSheet(ctx context.Context, ref Reference, req ChartRequest,
	props *gsheets.SheetProperties) (*gsheets.SheetProperties, error) {

	if name := strings.TrimSpace(req.Sheet); name != "" {
		sp, err := s.card(ctx, ref.ID)
		if err != nil {
			return nil, err
		}
		return s.findSheet(sp, name, ref)
	}
	if props != nil && props.GridProperties == nil {
		return nil, Errorf("invalid",
			"chart %d is on %q, which is a sheet of its own and holds no cells, so a range here has nothing to "+
				"resolve against. Pass sheet, or write the range with its sheet in it",
			req.ID, props.Title)
	}
	return props, nil
}

// chartPosition turns anchor or new_sheet into a position.
func (s *Service) chartPosition(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	req ChartRequest) (*gsheets.EmbeddedObjectPosition, error) {

	if req.NewSheet {
		if strings.TrimSpace(req.Anchor) != "" {
			return nil, Errorf("invalid", "new_sheet and anchor both say where the chart goes; pass one")
		}
		return plan.OnNewSheet(), nil
	}
	anchor := strings.TrimSpace(req.Anchor)
	if anchor == "" {
		return nil, Errorf("invalid",
			"add needs anchor, the cell the chart's top-left corner sits on, or new_sheet to give it a sheet of its own")
	}
	cell, err := s.anchorCell(ctx, ref, props, anchor)
	if err != nil {
		return nil, err
	}
	pos := &gsheets.EmbeddedObjectPosition{OverlayPosition: &gsheets.OverlayPosition{
		AnchorCell: cell, WidthPixels: req.Width, HeightPixels: req.Height,
	}}
	return pos, nil
}

// anchorCell resolves a single-cell A1 reference.
func (s *Service) anchorCell(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	cellA1 string) (*gsheets.GridCoordinate, error) {

	target, err := s.ResolveRange(ctx, ref, props.Title, cellA1)
	if err != nil {
		return nil, err
	}
	r := target.Rect
	if !r.OneCell() {
		return nil, Errorf("invalid", "anchor is one cell, such as E2, and %q is a range", cellA1)
	}
	return &gsheets.GridCoordinate{
		SheetID:     target.Props.SheetID,
		RowIndex:    a1.ZeroBased(r.FirstRow),
		ColumnIndex: a1.ZeroBased(r.FirstCol),
	}, nil
}

// chartsIn reads every chart and slicer, optionally on one sheet.
//
// The sheet goes through findSheet like every other listing in this
// server, rather than being compared with the title. Compared, a numeric
// sheet id — which the tool's own description offers — matched nothing
// and came back as "no charts", and so did a misspelled title: an empty
// answer where a [not_found] naming the sheets that exist is the answer.
func (s *Service) chartsIn(ctx context.Context, ref Reference, sheet string) ([]ChartRecord, error) {
	wanted := -1
	if sheet != "" {
		card, err := s.card(ctx, ref.ID)
		if err != nil {
			return nil, err
		}
		props, err := s.findSheet(card, sheet, ref)
		if err != nil {
			return nil, err
		}
		wanted = props.SheetID
	}
	sp, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{Fields: gapi.ChartFields})
	if err != nil {
		return nil, wrap(err)
	}
	var out []ChartRecord
	for _, sh := range sp.Sheets {
		if sh.Properties == nil {
			continue
		}
		if wanted >= 0 && sh.Properties.SheetID != wanted {
			continue
		}
		for _, c := range sh.Charts {
			out = append(out, chartRecord(c, sh.Properties.Title))
		}
		for _, sl := range sh.Slicers {
			out = append(out, slicerRecord(sl, sh.Properties.Title))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// findChart returns one chart and the sheet it sits on.
func (s *Service) findChart(ctx context.Context, ref Reference, id int) (*gsheets.EmbeddedChart, *gsheets.SheetProperties, error) {
	sp, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{Fields: gapi.ChartFields})
	if err != nil {
		return nil, nil, wrap(err)
	}
	for _, sh := range sp.Sheets {
		for _, c := range sh.Charts {
			if c.ChartID == id {
				return c, sh.Properties, nil
			}
		}
	}
	return nil, nil, s.noSuchObject(sp, id, false)
}

// findSlicer returns one slicer and the sheet it sits on.
func (s *Service) findSlicer(ctx context.Context, ref Reference, id int) (*gsheets.Slicer, *gsheets.SheetProperties, error) {
	sp, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{Fields: gapi.ChartFields})
	if err != nil {
		return nil, nil, wrap(err)
	}
	for _, sh := range sp.Sheets {
		for _, sl := range sh.Slicers {
			if sl.SlicerID == id {
				return sl, sh.Properties, nil
			}
		}
	}
	return nil, nil, s.noSuchObject(sp, id, true)
}

// findObject is the delete's and the move's lookup: either kind, and the
// title to report for it afterwards.
func (s *Service) findObject(ctx context.Context, ref Reference, id int, slicer bool) (string, *gsheets.SheetProperties, error) {
	if slicer {
		sl, props, err := s.findSlicer(ctx, ref, id)
		if err != nil {
			return "", nil, err
		}
		if sl.Spec != nil {
			return sl.Spec.Title, props, nil
		}
		return "", props, nil
	}
	c, props, err := s.findChart(ctx, ref, id)
	if err != nil {
		return "", nil, err
	}
	spec := map[string]any{}
	if len(c.Spec) > 0 {
		_ = json.Unmarshal(c.Spec, &spec)
	}
	return chartTitle(spec), props, nil
}

// noSuchObject names the ids that do exist, the way every other
// not_found in this server does.
func (s *Service) noSuchObject(sp *gsheets.Spreadsheet, id int, slicer bool) error {
	what := "chart"
	var have []string
	for _, sh := range sp.Sheets {
		if slicer {
			for _, sl := range sh.Slicers {
				have = append(have, fmt.Sprintf("%d", sl.SlicerID))
			}
			continue
		}
		for _, c := range sh.Charts {
			have = append(have, fmt.Sprintf("%d", c.ChartID))
		}
	}
	if slicer {
		what = "slicer"
	}
	if len(have) == 0 {
		return Errorf("not_found", "this spreadsheet has no %s with id %d, and no %ss at all", what, id, what)
	}
	return Errorf("not_found", "no %s with id %d in this spreadsheet; it has %s", what, id, join(have))
}

// chartRows hands the renderer its own view of a listing, so the result
// type can grow a field without the templates knowing.
func chartRows(found []ChartRecord) []render.ChartRow {
	out := make([]render.ChartRow, 0, len(found))
	for _, c := range found {
		out = append(out, render.ChartRow{
			ID: c.ID, Kind: c.Kind, Title: c.Title, Type: c.Type,
			Sheet: c.Sheet, Position: c.Position, Sources: c.Sources, Broken: c.Broken,
		})
	}
	return out
}

// chartRecord flattens one chart for a listing.
func chartRecord(c *gsheets.EmbeddedChart, sheet string) ChartRecord {
	spec := map[string]any{}
	if len(c.Spec) > 0 {
		_ = json.Unmarshal(c.Spec, &spec)
	}
	rec := ChartRecord{
		ID: c.ChartID, Kind: "chart", Title: chartTitle(spec), Type: chartKind(spec), Sheet: sheet,
		Position: positionName(c.Position, sheet),
	}
	sources, series := chartSources(spec)
	rec.Sources = strings.Join(sources, ", ")
	// A chart with a domain and no series is what deleting its source
	// column leaves behind. Nothing else in the API says so.
	if _, ok := spec["basicChart"]; ok && series == 0 {
		rec.Broken = true
	}
	return rec
}

func slicerRecord(sl *gsheets.Slicer, sheet string) ChartRecord {
	rec := ChartRecord{ID: sl.SlicerID, Kind: "slicer", Sheet: sheet, Position: positionName(sl.Position, sheet)}
	if sl.Spec != nil {
		rec.Title = sl.Spec.Title
		if sl.Spec.DataRange != nil {
			rec.Sources = a1.FormatRect(a1.FromGridRange(sl.Spec.DataRange))
		}
	}
	return rec
}

// chartTitle reads a title out of a raw spec.
func chartTitle(spec map[string]any) string {
	if t, ok := spec["title"].(string); ok {
		return t
	}
	return ""
}

// chartKind names which arm of the spec union is set, in the API's own
// words, so a chart this server cannot rebuild is still named in the
// refusal that says so.
func chartKind(spec map[string]any) string {
	for _, k := range []string{
		"basicChart", "pieChart", "bubbleChart", "candlestickChart", "histogramChart",
		"orgChart", "treemapChart", "waterfallChart", "scorecardChart",
	} {
		if _, ok := spec[k]; ok {
			if k == "basicChart" {
				if basic, ok := spec[k].(map[string]any); ok {
					if t, ok := basic["chartType"].(string); ok {
						return strings.ToLower(t)
					}
				}
			}
			return strings.TrimSuffix(k, "Chart")
		}
	}
	return "chart"
}

// chartSources lists the ranges a basic chart reads, and how many series
// it has left.
func chartSources(spec map[string]any) ([]string, int) {
	basic, ok := spec["basicChart"].(map[string]any)
	if !ok {
		return nil, 0
	}
	// The outer name and the inner one, spelled out. Deriving the second
	// from the first by trimming an "s" gives "serie", which is a key
	// nothing has — and the failure is silent: the chart lists its
	// domain, no series at all, and reads as a chart with one range.
	var out []string
	for _, group := range []struct{ list, item string }{
		{"domains", "domain"},
		{"series", "series"},
	} {
		list, _ := basic[group.list].([]any)
		for _, entry := range list {
			m, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			inner, ok := m[group.item].(map[string]any)
			if !ok {
				continue
			}
			out = append(out, rawSources(inner)...)
		}
	}
	list, _ := basic["series"].([]any)
	return out, len(list)
}

// rawSources reads the GridRanges out of one ChartData, through the wire
// type, so the zero-based conversion stays in internal/a1.
func rawSources(data map[string]any) []string {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var parsed gsheets.ChartData
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.SourceRange == nil {
		return nil
	}
	var out []string
	for _, g := range parsed.SourceRange.Sources {
		out = append(out, a1.FormatRect(a1.FromGridRange(g)))
	}
	return out
}

// positionName says where an object sits, in A1 where it has an anchor.
func positionName(pos *gsheets.EmbeddedObjectPosition, sheet string) string {
	if pos == nil {
		return ""
	}
	if pos.NewSheet {
		return "a sheet of its own"
	}
	if pos.OverlayPosition == nil || pos.OverlayPosition.AnchorCell == nil {
		if pos.SheetID != 0 {
			return "a sheet of its own"
		}
		return ""
	}
	cell := pos.OverlayPosition.AnchorCell
	name, err := a1.CellName(a1.OneBased(cell.ColumnIndex), a1.OneBased(cell.RowIndex))
	if err != nil {
		return ""
	}
	if sheet == "" {
		return name
	}
	return name + " on " + sheet
}

// columnOffset turns a column letter into an offset into a rectangle,
// which is what both a slicer and a pivot table take.
//
// One conversion for both, because they are the same arithmetic with
// different words around it — and both have to treat an unbounded left
// edge as column A, which is the off-by-one the pivot side shipped with
// until a review found it.
func columnOffset(column string, rect a1.Rect) (int, bool) {
	col, err := a1.ParseColumn(strings.TrimSpace(column))
	if err != nil {
		return 0, false
	}
	first := rect.FirstCol
	if first == 0 {
		first = 1
	}
	offset := col - first
	if offset < 0 || (rect.LastCol != 0 && col > rect.LastCol) {
		return 0, false
	}
	return offset, true
}

// slicerColumn turns a column letter into the offset into the data
// range, which is what the API takes.
func slicerColumn(column string, rect a1.Rect) (int, error) {
	column = strings.TrimSpace(column)
	if column == "" {
		return 0, Errorf("invalid", "a slicer needs column, the column it filters on, such as A")
	}
	if _, err := a1.ParseColumn(column); err != nil {
		return 0, Errorf("invalid", "column %q is not a column letter", column)
	}
	offset, ok := columnOffset(column, rect)
	if !ok {
		return 0, Errorf("invalid", "column %s is outside the slicer's range %s",
			strings.ToUpper(column), a1.FormatRect(rect))
	}
	return offset, nil
}

// legendOf maps the tool's word to the API's.
func legendOf(legend string) (string, error) {
	legend = strings.ToLower(strings.TrimSpace(legend))
	if legend == "" {
		return "", nil
	}
	pos, ok := legendPositions[legend]
	if !ok {
		return "", Errorf("invalid", "legend %q is not one of bottom, left, right, top, none", legend)
	}
	return pos, nil
}

func chartTypeNames() []string {
	return append(basicChartTypeNames(), "pie", "doughnut")
}

func basicChartTypeNames() []string { return sortedKeys(basicChartTypes) }

// sortedKeys names the members of a table, for a message that lists what
// a caller may write. Three tables in this phase wanted it.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// firstReply is the one reply a single-request batch has.
func firstReply(r *gsheets.BatchUpdateSpreadsheetResponse) *gsheets.Reply {
	if r == nil || len(r.Replies) == 0 {
		return nil
	}
	return r.Replies[0]
}

// chartsOnBand names the charts that read a band of rows or columns, and
// how much of each one the band takes.
//
// It exists for delete_dimensions, and for the one behaviour the API has
// no reply for: a chart whose source column is deleted keeps its place,
// its title and its id, and loses that series. Nothing else would ever
// tell the caller, so the refusal does — before the confirm gate, which
// is where a refusal has to name what it is refusing.
//
// The counts are the second version of this. The first said "nothing to
// draw" for every chart the band touched, and the live run put a
// two-series chart under a one-column delete: the chart lost one series,
// kept the other, and the refusal had told the caller it would be left
// with nothing. A refusal that overstates is a refusal somebody learns
// to skip.
func (s *Service) chartsOnBand(ctx context.Context, ref Reference, props *gsheets.SheetProperties,
	band plan.Band) ([]render.ChartLoss, error) {

	sp, err := s.api.GetSpreadsheet(ctx, ref.ID, gapi.GetOptions{Fields: gapi.ChartFields})
	if err != nil {
		return nil, wrap(err)
	}
	rect := a1.Rect{}
	if band.Rows() {
		rect.FirstRow, rect.LastRow = band.First, band.Last
	} else {
		rect.FirstCol, rect.LastCol = band.First, band.Last
	}
	var out []render.ChartLoss
	for _, sh := range sp.Sheets {
		for _, c := range sh.Charts {
			lost, total := chartReads(c, props.SheetID, rect)
			if lost == 0 {
				continue
			}
			spec := map[string]any{}
			if len(c.Spec) > 0 {
				_ = json.Unmarshal(c.Spec, &spec)
			}
			name := chartTitle(spec)
			if name == "" {
				name = fmt.Sprintf("id %d", c.ChartID)
			}
			out = append(out, render.ChartLoss{Title: name, Lost: lost, Total: total})
		}
	}
	return out, nil
}

// chartReads counts how many of a chart's series the band takes, and how
// many it has. A domain the band takes counts as the whole chart going,
// since a chart with no categories draws nothing either.
func chartReads(c *gsheets.EmbeddedChart, sheetID int, band a1.Rect) (lost, total int) {
	if len(c.Spec) == 0 {
		return 0, 0
	}
	var spec struct {
		BasicChart *struct {
			Domains []struct {
				Domain *gsheets.ChartData `json:"domain"`
			} `json:"domains"`
			Series []struct {
				Series *gsheets.ChartData `json:"series"`
			} `json:"series"`
		} `json:"basicChart"`
		PieChart *struct {
			Domain *gsheets.ChartData `json:"domain"`
			Series *gsheets.ChartData `json:"series"`
		} `json:"pieChart"`
	}
	if err := json.Unmarshal(c.Spec, &spec); err != nil {
		return 0, 0
	}
	var series []*gsheets.ChartData
	var domains []*gsheets.ChartData
	if b := spec.BasicChart; b != nil {
		for _, d := range b.Domains {
			domains = append(domains, d.Domain)
		}
		for _, one := range b.Series {
			series = append(series, one.Series)
		}
	}
	if p := spec.PieChart; p != nil {
		domains = append(domains, p.Domain)
		series = append(series, p.Series)
	}
	total = len(series)
	for _, d := range domains {
		if dataReads(d, sheetID, band) {
			// The categories go, so every series goes with them.
			return max(total, 1), max(total, 1)
		}
	}
	for _, d := range series {
		if dataReads(d, sheetID, band) {
			lost++
		}
	}
	return lost, total
}

// dataReads says whether one domain or series overlaps the band.
func dataReads(d *gsheets.ChartData, sheetID int, band a1.Rect) bool {
	if d == nil || d.SourceRange == nil {
		return false
	}
	for _, g := range d.SourceRange.Sources {
		if g == nil || g.SheetID != sheetID {
			continue
		}
		// Unbounded on the other axis, so a band of columns is compared
		// with the source's columns and nothing else: a chart reading
		// B1:B20 is hit by deleting column B whichever rows the band
		// covers.
		source := a1.FromGridRange(g)
		if band.FirstRow == 0 {
			source.FirstRow, source.LastRow = 0, 0
		} else {
			source.FirstCol, source.LastCol = 0, 0
		}
		if source.Overlaps(band) {
			return true
		}
	}
	return false
}
