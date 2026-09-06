package sheetstest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
)

// coercionTable is spike A's transcript, recorded live on 2026-09-06 and
// replayed here.
//
// Never simulated. A fake that parsed input would inherit whatever this
// project believes about Google's parser, and a whole test suite would
// then agree with the belief rather than with Google — which is exactly
// how a sibling shipped a search bug its own tests confirmed. An input
// that is not in this table is stored as it was sent, and a test that
// needs a coercion uses one of these.
var coercionTable = buildCoercionTable()

func buildCoercionTable() map[string]*gsheets.CellData {
	date := func(serial float64, pattern, shown string) *gsheets.CellData {
		return &gsheets.CellData{
			UserEnteredValue:  &gsheets.ExtendedValue{NumberValue: &serial},
			EffectiveValue:    &gsheets.ExtendedValue{NumberValue: &serial},
			FormattedValue:    shown,
			UserEnteredFormat: &gsheets.CellFormat{NumberFormat: &gsheets.NumberFormat{Type: "DATE", Pattern: pattern}},
		}
	}
	currency := func(v float64, shown string) *gsheets.CellData {
		return &gsheets.CellData{
			UserEnteredValue: &gsheets.ExtendedValue{NumberValue: &v},
			EffectiveValue:   &gsheets.ExtendedValue{NumberValue: &v},
			FormattedValue:   shown,
			UserEnteredFormat: &gsheets.CellFormat{
				NumberFormat: &gsheets.NumberFormat{Type: "CURRENCY", Pattern: `"$"#,##0.00`},
			},
		}
	}
	return map[string]*gsheets.CellData{
		"1-2":        date(46024, "m-d", "1-2"),
		"2026-09-05": date(46270, "yyyy-mm-dd", "2026-09-05"),
		"$100.15":    currency(100.15, "$100.15"),
		"007":        Num(7, "7"),
		"TRUE":       Bool(true),
		"'0123":      Str("0123"),
		"=1+2":       Formula("=1+2", 3, "3"),
	}
}

// Coerced returns what the live probe recorded for one input under
// USER_ENTERED, so a test can assert against the same transcript the
// fake replays rather than against a second copy of it.
func Coerced(input string) (*gsheets.CellData, bool) {
	c, ok := coercionTable[input]
	return c, ok
}

// store turns one sent value into the cell Google would keep.
//
// USER_ENTERED consults the recorded table first. A string beginning
// with "=" that is not in the table is stored as a formula with no
// result: this fake does not evaluate formulas, and inventing a result
// would be inventing Google's calculation engine one function at a time.
func store(v any, input string) *gsheets.CellData {
	if s, ok := v.(string); ok && input == gapi.InputUserEntered {
		if cell, found := Coerced(s); found {
			copied := *cell
			return &copied
		}
		if strings.HasPrefix(s, "=") {
			return &gsheets.CellData{UserEnteredValue: &gsheets.ExtendedValue{FormulaValue: &s}}
		}
	}
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		return Str(t)
	case bool:
		return Bool(t)
	case float64:
		return Num(t, "")
	case int:
		return Num(float64(t), "")
	}
	return nil
}

func (s *Server) valuesUpdate(w http.ResponseWriter, r *http.Request) {
	d, sh, rect, ok := s.writeTarget(w, r, "/values/", "")
	if !ok {
		return
	}
	var in gsheets.ValueRange
	if !decodeBody(w, r, &in) {
		return
	}
	input := r.URL.Query().Get("valueInputOption")
	if input == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "valueInputOption is required")
		return
	}
	// The real API refuses an array that reaches past the range it was
	// given, and names the row it stopped at. Verified live.
	if rect.LastRow != 0 && rect.FirstRow+len(in.Values)-1 > rect.LastRow {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
			"Requested writing within range ["+a1.Format(sh.Props.Title, rect)+"], but tried writing to row ["+
				strconv.Itoa(rect.FirstRow+len(in.Values)-1)+"]")
		return
	}
	written := s.put(sh, rect.FirstRow, rect.FirstCol, in.Values, input)
	writeJSON(w, updateResponse(d, sh, written, in.Values, r.URL.Query()))
}

func (s *Server) valuesAppend(w http.ResponseWriter, r *http.Request) {
	d, sh, rect, ok := s.writeTarget(w, r, "/values/", ":append")
	if !ok {
		return
	}
	var in gsheets.ValueRange
	if !decodeBody(w, r, &in) {
		return
	}
	input := r.URL.Query().Get("valueInputOption")
	insert := r.URL.Query().Get("insertDataOption")
	if input == "" || insert == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "valueInputOption and insertDataOption are required")
		return
	}

	// Table detection as the live probe found it: the contiguous block
	// the given range's start falls in, or the last block on the sheet
	// when the range covers the whole thing. The rows go after it.
	s.mu.Lock()
	table := detectTable(sh, rect)
	s.mu.Unlock()
	first := table.LastRow + 1
	if insert == gapi.InsertRows {
		s.mu.Lock()
		shiftRowsDown(sh, first, len(in.Values))
		s.mu.Unlock()
	}
	written := s.put(sh, first, table.FirstCol, in.Values, input)
	out := &gsheets.AppendValuesResponse{
		SpreadsheetID: d.ID,
		TableRange:    a1.Format(sh.Props.Title, table),
		Updates:       updateResponse(d, sh, written, in.Values, r.URL.Query()),
	}
	writeJSON(w, out)
}

func (s *Server) valuesClear(w http.ResponseWriter, r *http.Request) {
	d, sh, rect, ok := s.writeTarget(w, r, "/values/", ":clear")
	if !ok {
		return
	}
	s.mu.Lock()
	for row := rect.FirstRow; row <= rect.LastRow; row++ {
		for col := rect.FirstCol; col <= rect.LastCol; col++ {
			// Values only. The API keeps everything else on the cell, so
			// a note survives a clear and a fake that dropped it would
			// let a wrong warning pass its tests.
			if c := sh.At(row, col); c != nil {
				c.UserEnteredValue, c.EffectiveValue, c.FormattedValue = nil, nil, ""
			}
		}
	}
	s.mu.Unlock()
	writeJSON(w, &gsheets.ClearValuesResponse{
		SpreadsheetID: d.ID, ClearedRange: a1.Format(sh.Props.Title, rect),
	})
}

// put writes cells and returns the rectangle it covered.
func (s *Server) put(sh *Sheet, firstRow, firstCol int, values [][]any, input string) a1.Rect {
	s.mu.Lock()
	defer s.mu.Unlock()
	width := 0
	for i, row := range values {
		if len(row) > width {
			width = len(row)
		}
		for j, v := range row {
			// A cell with no value is skipped rather than cleared, which
			// is the API's own rule and the reason write_values refuses
			// a ragged array.
			if v == nil {
				continue
			}
			sh.Set(firstRow+i, firstCol+j, store(v, input))
		}
	}
	return a1.Rect{
		FirstRow: firstRow, FirstCol: firstCol,
		LastRow: firstRow + len(values) - 1, LastCol: firstCol + width - 1,
	}
}

// updateResponse builds what a values write returns, including the
// stored values when the request asked for them.
func updateResponse(d *Doc, sh *Sheet, rect a1.Rect, sent [][]any, q url.Values) *gsheets.UpdateValuesResponse {
	cells := 0
	for _, row := range sent {
		cells += len(row)
	}
	out := &gsheets.UpdateValuesResponse{
		SpreadsheetID:  d.ID,
		UpdatedRange:   a1.Format(sh.Props.Title, rect),
		UpdatedRows:    rect.Rows(),
		UpdatedColumns: rect.Cols(),
		UpdatedCells:   cells,
	}
	if q.Get("includeValuesInResponse") == "true" {
		vr, _ := values(d, out.UpdatedRange, q.Get("responseValueRenderOption"))
		out.UpdatedData = vr
	}
	return out
}

// detectTable finds the contiguous block of rows an append writes after.
//
// Recorded behaviour, not invented: given a range inside the first
// block it reports that block, and given the whole sheet it reports the
// last one. Both were observed live on a sheet holding rows 1-3, a gap,
// and rows 6-7.
func detectTable(sh *Sheet, rect a1.Rect) a1.Rect {
	full := clamp(sh, rect)
	occupied := func(row int) bool {
		for col := full.FirstCol; col <= full.LastCol; col++ {
			if sh.At(row, col) != nil {
				return true
			}
		}
		return false
	}
	// Where to start looking: the range's own first row when it named
	// one, otherwise the last block on the sheet.
	start := full.FirstRow
	if rect.FirstRow == 0 {
		for row := full.LastRow; row >= full.FirstRow; row-- {
			if occupied(row) {
				start = row
				break
			}
		}
	}
	if !occupied(start) {
		return a1.Rect{FirstRow: start, FirstCol: full.FirstCol, LastRow: start - 1, LastCol: full.LastCol}
	}
	first, last := start, start
	for first > full.FirstRow && occupied(first-1) {
		first--
	}
	for last < full.LastRow && occupied(last+1) {
		last++
	}
	return a1.Rect{FirstRow: first, FirstCol: full.FirstCol, LastRow: last, LastCol: full.LastCol}
}

// shiftRowsDown makes room for inserted rows, moving everything at or
// below first down by n. Callers hold the lock.
func shiftRowsDown(sh *Sheet, first, n int) {
	if n <= 0 {
		return
	}
	moved := map[[2]int]*gsheets.CellData{}
	for key, cell := range sh.Cells {
		if key[0]+1 >= first {
			moved[[2]int{key[0] + n, key[1]}] = cell
			delete(sh.Cells, key)
		}
	}
	for key, cell := range moved {
		sh.Cells[key] = cell
	}
}

// writeTarget resolves the range in a values write's path.
func (s *Server) writeTarget(w http.ResponseWriter, r *http.Request, marker, suffix string) (*Doc, *Sheet, a1.Rect, bool) {
	d := s.Doc(spreadsheetID(r.URL.Path))
	if d == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Requested entity was not found.")
		return nil, nil, a1.Rect{}, false
	}
	raw := strings.TrimSuffix(r.URL.Path[strings.Index(r.URL.Path, marker)+len(marker):], suffix)
	rangeA1, err := url.PathUnescape(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "Unable to parse range: "+raw)
		return nil, nil, a1.Rect{}, false
	}
	ref, err := a1.Parse(rangeA1)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "Unable to parse range: "+rangeA1)
		return nil, nil, a1.Rect{}, false
	}
	sh := d.Find(ref.Sheet)
	if sh == nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "Unable to parse range: "+rangeA1)
		return nil, nil, a1.Rect{}, false
	}
	rect := ref.Rect
	if rect.FirstRow == 0 || rect.FirstCol == 0 {
		rect = clamp(sh, rect)
	}
	return d, sh, rect, true
}

func (s *Server) spreadsheetsCreate(w http.ResponseWriter, r *http.Request) {
	var in gsheets.NewSpreadsheet
	if !decodeBody(w, r, &in) {
		return
	}
	s.mu.Lock()
	id := "1SyntheticFixtureCreated" + strconv.Itoa(len(s.docs)) + strings.Repeat("Z", 12)
	d := &Doc{ID: id, Locale: "en_GB", TimeZone: "Etc/UTC", AutoRecalc: "ON_CHANGE"}
	if in.Properties != nil {
		d.Title = in.Properties.Title
		if in.Properties.Locale != "" {
			d.Locale = in.Properties.Locale
		}
		if in.Properties.TimeZone != "" {
			d.TimeZone = in.Properties.TimeZone
		}
	}
	// A sheets list replaces the default sheet rather than adding to it:
	// verified live, a create naming two sheets came back with exactly
	// those two and no third. With no list, Google makes one and names
	// it in the account's language — the fake names it something that is
	// not "Sheet1", so a test that assumed the English name fails here
	// rather than in somebody's Portuguese account.
	var titles []string
	for _, sh := range in.Sheets {
		if sh.Properties != nil && sh.Properties.Title != "" {
			titles = append(titles, sh.Properties.Title)
		}
	}
	if len(titles) == 0 {
		titles = []string{"Blad1"}
	}
	for i, t := range titles {
		d.Sheets = append(d.Sheets, &Sheet{Props: gsheets.SheetProperties{
			SheetID: (i + 1) * 7919, Title: t, Index: gsheets.Ptr(i), SheetType: "GRID",
			GridProperties: &gsheets.GridProperties{RowCount: 1000, ColumnCount: 26},
		}})
	}
	s.docs[id] = d
	s.files[id] = &gapi.File{ID: id, Name: d.Title, MimeType: gapi.SpreadsheetMimeType}
	s.mu.Unlock()
	writeJSON(w, docCard(d))
}

func (s *Server) spreadsheetsBatchUpdate(w http.ResponseWriter, r *http.Request) {
	d := s.Doc(spreadsheetID(r.URL.Path))
	if d == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Requested entity was not found.")
		return
	}
	var in gsheets.BatchUpdateSpreadsheetRequest
	if !decodeBody(w, r, &in) {
		return
	}
	s.mu.Lock()
	// Applied to a copy and swapped in on success. The API is explicit
	// that nothing is applied when any request is invalid, and applying
	// in place and stopping at the first failure left the fake in a
	// state the real API never produces — invisible while every batch
	// here carries one request, and waiting for the first that does not.
	working := d.clone()
	out := &gsheets.BatchUpdateSpreadsheetResponse{SpreadsheetID: d.ID}
	for _, req := range in.Requests {
		reply, err := applyRequest(working, req)
		if err != nil {
			s.mu.Unlock()
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
			return
		}
		out.Replies = append(out.Replies, reply)
	}
	reindex(working)
	*d = *working
	s.mu.Unlock()
	writeJSON(w, out)
}

func (s *Server) sheetsCopyTo(w http.ResponseWriter, r *http.Request) {
	d := s.Doc(spreadsheetID(r.URL.Path))
	if d == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Requested entity was not found.")
		return
	}
	rest := r.URL.Path[strings.Index(r.URL.Path, "/sheets/")+len("/sheets/"):]
	sheetID, err := strconv.Atoi(strings.TrimSuffix(rest, ":copyTo"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "sheetId is not a number")
		return
	}
	var in gsheets.CopySheetToAnotherSpreadsheetRequest
	if !decodeBody(w, r, &in) {
		return
	}
	dest := s.Doc(in.DestinationSpreadsheetID)
	if dest == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Requested entity was not found.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src := d.FindByID(sheetID)
	if src == nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "No sheet with id: "+strconv.Itoa(sheetID))
		return
	}
	copied := &Sheet{Props: src.Props, Cells: map[[2]int]*gsheets.CellData{}}
	copied.Props.SheetID = sheetID + 1
	copied.Props.Title = "Copy of " + src.Props.Title
	copied.Props.Index = gsheets.Ptr(len(dest.Sheets))
	for k, v := range src.Cells {
		copied.Cells[k] = v
	}
	dest.Sheets = append(dest.Sheets, copied)
	props := copied.Props
	writeJSON(w, &props)
}

// clone copies a Doc deeply enough for a batch to be undone by throwing
// it away: the sheet list, each sheet's properties, and its cells. A
// CellData is replaced wholesale by a write rather than mutated in
// place, except by values.clear, which is not part of a batch.
func (d *Doc) clone() *Doc {
	out := *d
	out.Sheets = make([]*Sheet, len(d.Sheets))
	for i, sh := range d.Sheets {
		copied := *sh
		copied.Cells = make(map[[2]int]*gsheets.CellData, len(sh.Cells))
		for k, v := range sh.Cells {
			copied.Cells[k] = v
		}
		out.Sheets[i] = &copied
	}
	return &out
}

// applyRequest is the fake's half of the batchUpdate union: the members
// this server builds, and nothing else.
func applyRequest(d *Doc, req *gsheets.Request) (*gsheets.Reply, error) {
	switch {
	case req == nil:
		return nil, errors.New("an empty request")
	case req.AddSheet != nil:
		p := req.AddSheet.Properties
		if p == nil || p.Title == "" {
			return nil, errors.New("addSheet needs a title")
		}
		if d.Find(p.Title) != nil {
			return nil, errors.New("A sheet with the name \"" + p.Title + "\" already exists.")
		}
		sh := &Sheet{Props: gsheets.SheetProperties{
			SheetID: nextSheetID(d), Title: p.Title, SheetType: "GRID",
			GridProperties: &gsheets.GridProperties{RowCount: 1000, ColumnCount: 26},
		}}
		if p.GridProperties != nil {
			if p.GridProperties.RowCount > 0 {
				sh.Props.GridProperties.RowCount = p.GridProperties.RowCount
			}
			if p.GridProperties.ColumnCount > 0 {
				sh.Props.GridProperties.ColumnCount = p.GridProperties.ColumnCount
			}
		}
		insertSheet(d, sh, p.Index)
		props := sh.Props
		return &gsheets.Reply{AddSheet: &gsheets.AddSheetReply{Properties: &props}}, nil

	case req.DeleteSheet != nil:
		for i, sh := range d.Sheets {
			if sh.Props.SheetID == req.DeleteSheet.SheetID {
				d.Sheets = append(d.Sheets[:i], d.Sheets[i+1:]...)
				return &gsheets.Reply{}, nil
			}
		}
		return nil, errors.New("No sheet with id: " + strconv.Itoa(req.DeleteSheet.SheetID))

	case req.DuplicateSheet != nil:
		src := d.FindByID(req.DuplicateSheet.SourceSheetID)
		if src == nil {
			return nil, errors.New("No sheet with id: " + strconv.Itoa(req.DuplicateSheet.SourceSheetID))
		}
		title := req.DuplicateSheet.NewSheetName
		if title == "" {
			title = "Copy of " + src.Props.Title
		}
		if d.Find(title) != nil {
			return nil, errors.New("A sheet with the name \"" + title + "\" already exists.")
		}
		copied := &Sheet{Props: src.Props, Cells: map[[2]int]*gsheets.CellData{}}
		copied.Props.SheetID = nextSheetID(d)
		copied.Props.Title = title
		for k, v := range src.Cells {
			copied.Cells[k] = v
		}
		insertSheet(d, copied, req.DuplicateSheet.InsertSheetIndex)
		props := copied.Props
		return &gsheets.Reply{DuplicateSheet: &gsheets.DuplicateSheetReply{Properties: &props}}, nil

	case req.UpdateSheetProperties != nil:
		return updateProperties(d, req.UpdateSheetProperties)
	}
	return applyDimension(d, req)
}

// applyDimension is the half of the union that acts on a band of rows or
// columns. Split out so neither half is too long to read.
func applyDimension(d *Doc, req *gsheets.Request) (*gsheets.Reply, error) {
	switch {
	case req.InsertDimension != nil:
		return dimension(d, req.InsertDimension.Range, func(sh *Sheet, r *gsheets.DimensionRange) {
			if r.Dimension == gsheets.DimensionRows {
				shiftRowsDown(sh, r.StartIndex+1, r.EndIndex-r.StartIndex)
			}
		})
	case req.DeleteDimension != nil:
		return dimension(d, req.DeleteDimension.Range, func(sh *Sheet, r *gsheets.DimensionRange) {
			deleteBand(sh, r)
		})
	// The rest change how the band looks rather than what is on it, so
	// the fake validates the range and stores nothing.
	case req.MoveDimension != nil:
		return dimension(d, req.MoveDimension.Source, noCellChange)
	case req.UpdateDimensionProperties != nil:
		return dimension(d, req.UpdateDimensionProperties.Range, noCellChange)
	case req.AutoResizeDimensions != nil:
		return dimension(d, req.AutoResizeDimensions.Dimensions, noCellChange)
	case req.AddDimensionGroup != nil:
		return dimension(d, req.AddDimensionGroup.Range, noCellChange)
	case req.DeleteDimensionGroup != nil:
		return dimension(d, req.DeleteDimensionGroup.Range, noCellChange)
	}
	return nil, errors.New("this fake does not know that request")
}

func updateProperties(d *Doc, req *gsheets.UpdateSheetPropertiesRequest) (*gsheets.Reply, error) {
	p := req.Properties
	if p == nil {
		return nil, errors.New("updateSheetProperties needs properties")
	}
	sh := d.FindByID(p.SheetID)
	if sh == nil {
		return nil, errors.New("No sheet with id: " + strconv.Itoa(p.SheetID))
	}
	if req.Fields == "" {
		return nil, errors.New("updateSheetProperties needs a field mask")
	}
	// The mask is honoured, not ignored. A fake that applied every field
	// would let a request through that named the wrong one.
	for _, f := range strings.Split(req.Fields, ",") {
		switch strings.TrimSpace(f) {
		case "title":
			if other := d.Find(p.Title); other != nil && other != sh {
				return nil, errors.New("A sheet with the name \"" + p.Title + "\" already exists.")
			}
			sh.Props.Title = p.Title
		case "index":
			moveSheet(d, sh, p.Index)
		case "hidden":
			sh.Props.Hidden = p.Hidden
		case "tabColorStyle":
			sh.Props.TabColorStyle = p.TabColorStyle
		case "gridProperties.rowCount":
			sh.Props.GridProperties.RowCount = p.GridProperties.RowCount
		case "gridProperties.columnCount":
			sh.Props.GridProperties.ColumnCount = p.GridProperties.ColumnCount
		case "gridProperties.frozenRowCount":
			sh.Props.GridProperties.FrozenRowCount = p.GridProperties.FrozenRowCount
		case "gridProperties.frozenColumnCount":
			sh.Props.GridProperties.FrozenColumnCount = p.GridProperties.FrozenColumnCount
		default:
			return nil, errors.New("this fake does not know the field " + f)
		}
	}
	return &gsheets.Reply{}, nil
}

// noCellChange is a dimension request that moves no data.
func noCellChange(*Sheet, *gsheets.DimensionRange) {}

func dimension(d *Doc, r *gsheets.DimensionRange, apply func(*Sheet, *gsheets.DimensionRange)) (*gsheets.Reply, error) {
	if r == nil {
		return nil, errors.New("a dimension request needs a range")
	}
	sh := d.FindByID(r.SheetID)
	if sh == nil {
		return nil, errors.New("No sheet with id: " + strconv.Itoa(r.SheetID))
	}
	if r.EndIndex <= r.StartIndex {
		return nil, errors.New("the dimension range is empty")
	}
	apply(sh, r)
	return &gsheets.Reply{}, nil
}

func deleteBand(sh *Sheet, r *gsheets.DimensionRange) {
	n := r.EndIndex - r.StartIndex
	kept := map[[2]int]*gsheets.CellData{}
	for key, cell := range sh.Cells {
		axis := key[0]
		if r.Dimension == gsheets.DimensionColumns {
			axis = key[1]
		}
		switch {
		case axis >= r.StartIndex && axis < r.EndIndex:
			continue
		case axis >= r.EndIndex:
			moved := key
			if r.Dimension == gsheets.DimensionColumns {
				moved[1] -= n
			} else {
				moved[0] -= n
			}
			kept[moved] = cell
		default:
			kept[key] = cell
		}
	}
	sh.Cells = kept
}

func insertSheet(d *Doc, sh *Sheet, index *int) {
	if index == nil || *index < 0 || *index >= len(d.Sheets) {
		d.Sheets = append(d.Sheets, sh)
		return
	}
	d.Sheets = append(d.Sheets, nil)
	copy(d.Sheets[*index+1:], d.Sheets[*index:])
	d.Sheets[*index] = sh
}

// moveSheet takes the sheet out and puts it back, reading the index
// against the order before the move — which is what Google does, and
// what made a reorder to a later position land one short.
func moveSheet(d *Doc, sh *Sheet, index *int) {
	if index == nil {
		return
	}
	at := *index
	for i, other := range d.Sheets {
		if other == sh {
			if i < at {
				at--
			}
			d.Sheets = append(d.Sheets[:i], d.Sheets[i+1:]...)
			break
		}
	}
	insertSheet(d, sh, &at)
}

// reindex renumbers the sheets after any change to their order, so the
// index a card reports is the position the sheet is actually in.
func reindex(d *Doc) {
	for i, sh := range d.Sheets {
		sh.Props.Index = gsheets.Ptr(i)
	}
}

func nextSheetID(d *Doc) int {
	next := 1
	for _, sh := range d.Sheets {
		if sh.Props.SheetID >= next {
			next = sh.Props.SheetID + 1
		}
	}
	return next
}

func decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<22))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "the body could not be read")
		return false
	}
	if err := json.Unmarshal(body, into); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid JSON payload received.")
		return false
	}
	return true
}
