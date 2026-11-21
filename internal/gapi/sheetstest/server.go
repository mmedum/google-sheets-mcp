package sheetstest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmedum/google-sheets-mcp/v4/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v4/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/v4/internal/gsheets"
	"golang.org/x/time/rate"
)

// Call is one request that reached the fake.
//
// The method is recorded as well as the operation, because a guarantee
// stated over a list somebody wrote by hand decays every time the
// surface grows: a test that asserts what reached the wire is reading
// the surface as it is.
type Call struct {
	Method string
	Op     string
	Query  url.Values
	Body   string
}

// Failure is an injected response.
type Failure struct {
	Status int
	Body   string
	Header http.Header
	// Cut closes the connection without answering, which is what a
	// dropped network looks like from here.
	Cut bool
	// Delay holds the response back, for deadline tests.
	Delay time.Duration
	// Applied makes the request first and answers with the failure
	// after: a write that landed though its reply said it failed, which
	// is what an HTTP 500 cannot rule out.
	Applied bool
	// Pass answers the call as if nothing were injected, so a queue can
	// fail a later call to an op and not an earlier one.
	Pass bool
}

// Server is a fake Sheets and Drive behind httptest.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	docs     map[string]*Doc
	files    map[string]*gapi.File
	calls    []Call
	always   map[string]Failure
	once     map[string][]Failure
	notFound map[string]bool
}

// New starts a fake and registers the standard fixture.
// New takes a testing.TB rather than a *testing.T so a benchmark can
// drive the fake too. Measuring the read path without it would mean
// measuring the renderer alone, which is the half that was never in
// doubt.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		docs:     map[string]*Doc{},
		files:    map[string]*gapi.File{},
		always:   map[string]Failure{},
		once:     map[string][]Failure{},
		notFound: map[string]bool{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.route))
	t.Cleanup(s.Close)
	return s
}

// Add registers a spreadsheet and the Drive file that stands for it.
func (s *Server) Add(d *Doc, f *gapi.File) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d != nil {
		s.docs[d.ID] = d
	}
	if f != nil {
		s.files[f.ID] = f
	}
}

// AddFile registers a Drive file with no spreadsheet behind it, which is
// what a search has to exclude.
func (s *Server) AddFile(f *gapi.File) { s.Add(nil, f) }

// Doc returns a registered spreadsheet.
func (s *Server) Doc(id string) *Doc {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[id]
}

// Calls returns every request that reached the fake, in order.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Reset forgets the recorded calls.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
}

// Fail makes every call to op fail this way until it is cleared.
func (s *Server) Fail(op string, f Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.always[op] = f
}

// FailOnce queues one failure for op. Queue several to make a call fail
// and then succeed, which is what a retry test needs.
func (s *Server) FailOnce(op string, f Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.once[op] = append(s.once[op], f)
}

// Clear removes injected failures for op.
func (s *Server) Clear(op string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.always, op)
	delete(s.once, op)
}

// Options are gapi.Options pointed at this fake, with the limiters wide
// open and the backoff instant: a test that waited for the real
// per-minute quota, or for five real backoffs, would take a minute. The
// timing itself is covered where it lives, in the client's own tests.
func (s *Server) Options() gapi.Options {
	return gapi.Options{
		SheetsBaseURL: s.URL + "/v4",
		DriveBaseURL:  s.URL + "/drive/v3",
		ReadLimiter:   rate.NewLimiter(rate.Inf, 1),
		WriteLimiter:  rate.NewLimiter(rate.Inf, 1),
		AllowURL:      func(*url.URL) bool { return true },
		Sleep:         func(context.Context, time.Duration) error { return nil },
	}
}

// Client is a gapi.Client wired to this fake with a static token.
func (s *Server) Client() *gapi.Client { return gapi.New(staticToken{}, s.Options()) }

// ClientLogging is Client with a logger attached, for the test that
// asserts what a debug log does and does not carry.
func (s *Server) ClientLogging(log *slog.Logger) *gapi.Client {
	o := s.Options()
	o.Logger = log
	return gapi.New(staticToken{}, o)
}

func (s *Server) record(c Call) { s.mu.Lock(); s.calls = append(s.calls, c); s.mu.Unlock() }

func (s *Server) failure(op string) (Failure, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q := s.once[op]; len(q) > 0 {
		f := q[0]
		s.once[op] = q[1:]
		return f, true
	}
	f, ok := s.always[op]
	return f, ok
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	op, handler := s.dispatch(r)
	// io.ReadAll, not one Read: a single Read is not obliged to return
	// the whole body, and the batchUpdate assertions read what is
	// recorded here. A short read there would be a flaky test rather
	// than a failing one.
	body := ""
	if r.Body != nil {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<22))
		body = string(b)
		// Put it back. Reading it here to record it left the handlers
		// with an empty body, so every write came back as "Invalid JSON
		// payload received" — the fake's own error, describing the fake.
		r.Body = io.NopCloser(strings.NewReader(body))
	}
	s.record(Call{Method: r.Method, Op: op, Query: r.URL.Query(), Body: body})

	if f, ok := s.failure(op); ok && !f.Pass {
		if f.Delay > 0 {
			time.Sleep(f.Delay)
		}
		if f.Cut {
			// Hijacking and closing is what a dropped connection looks
			// like to the client: no status, no body.
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, err := hj.Hijack()
				if err == nil {
					_ = conn.Close()
					return
				}
			}
			panic("sheetstest: cannot hijack to cut the connection")
		}
		if f.Applied && handler != nil {
			handler(httptest.NewRecorder(), r)
		}
		for k, vs := range f.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.Status)
		body := f.Body
		if body == "" {
			body = errorBody(f.Status, "injected failure")
		}
		_, _ = w.Write([]byte(body))
		return
	}
	if handler == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "no such endpoint in the fake: "+r.URL.Path)
		return
	}
	handler(w, r)
}

func (s *Server) dispatch(r *http.Request) (string, http.HandlerFunc) {
	p := r.URL.Path
	switch {
	case p == "/drive/v3/about":
		return "drive.about.get", s.about
	case p == "/drive/v3/files":
		return "drive.files.list", s.filesList
	case strings.HasPrefix(p, "/drive/v3/files/"):
		return "drive.files.get", s.filesGet
	case strings.HasSuffix(p, "/developerMetadata:search"):
		return "developerMetadata.search", s.metadataSearch
	case strings.HasSuffix(p, "/values:batchGet"):
		return "values.batchGet", s.valuesBatchGet
	case strings.HasSuffix(p, ":append"):
		return "values.append", s.valuesAppend
	case strings.HasSuffix(p, ":clear"):
		return "values.clear", s.valuesClear
	case strings.HasSuffix(p, ":copyTo"):
		return "sheets.copyTo", s.sheetsCopyTo
	case strings.HasSuffix(p, ":batchUpdate"):
		return "spreadsheets.batchUpdate", s.spreadsheetsBatchUpdate
	case strings.Contains(p, "/values/"):
		if r.Method == http.MethodPut {
			return "values.update", s.valuesUpdate
		}
		return "values.get", s.valuesGet
	case p == "/v4/spreadsheets":
		return "spreadsheets.create", s.spreadsheetsCreate
	case strings.HasPrefix(p, "/v4/spreadsheets/"):
		return "spreadsheets.get", s.spreadsheetsGet
	}
	return "unknown", nil
}

// spreadsheetID pulls the id out of a Sheets path.
func spreadsheetID(p string) string {
	rest := strings.TrimPrefix(p, "/v4/spreadsheets/")
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	// The custom methods hang a ":verb" off the id itself, so
	// ":batchUpdate" was part of the id the fake looked up and every
	// structural write came back as a 404.
	if i := strings.IndexByte(rest, ':'); i >= 0 {
		rest = rest[:i]
	}
	id, err := url.PathUnescape(rest)
	if err != nil {
		return rest
	}
	return id
}

func (s *Server) spreadsheetsGet(w http.ResponseWriter, r *http.Request) {
	d := s.Doc(spreadsheetID(r.URL.Path))
	if d == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Requested entity was not found.")
		return
	}
	q := r.URL.Query()
	if q.Get("fields") == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "this fake refuses an unmasked get, as this server refuses to send one")
		return
	}
	// Google answers an unbalanced mask with this and nothing more. A
	// fake that reads no mask lets a mask one parenthesis over pass every
	// unit test and fail every live read.
	mask, ok := parseMask(q.Get("fields"))
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "Request contains an invalid argument.")
		return
	}
	out := docCard(d)
	if strings.Contains(q.Get("fields"), "comment") {
		switch q.Get("commentsViewMode") {
		case "COMMENTS_VIEW_MODE_INCLUDED", "COMMENTS_VIEW_MODE_DEFAULT_FOR_CURRENT_ACCESS":
		default:
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
				"Field mask may not contain comment-specific fields if comments are not requested")
			return
		}
		if d.CommentsDenied {
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
			return
		}
		for _, t := range d.Comments {
			out.Comments = append(out.Comments, cloneThread(t))
		}
		for i, sh := range d.Sheets {
			out.Sheets[i].CommentAnchors = wireAnchors(sh.CommentAnchors)
		}
	}
	// includeGridData is ignored when a mask is set: a mask that keeps
	// sheets.data asks for cells by itself.
	// https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/get
	grid := q.Get("includeGridData") == "true" || mask.keeps("sheets", "data")
	ranges := q["ranges"]
	if grid {
		for _, raw := range ranges {
			ref, err := a1.Parse(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "Unable to parse range: "+raw)
				return
			}
			sh := d.Find(ref.Sheet)
			if sh == nil {
				writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "Unable to parse range: "+raw)
				return
			}
			var target *gsheets.Sheet
			for _, o := range out.Sheets {
				if o.Properties.SheetID == sh.Props.SheetID {
					target = o
				}
			}
			target.Data = append(target.Data, gridData(sh, clamp(sh, ref.Rect)))
		}
	}
	writeMasked(w, out, mask)
}

// writeMasked answers with only the fields the mask names, as Google
// does, so a mask that leaves out a field the code reads fails a test
// rather than every live read.
// https://developers.google.com/workspace/sheets/api/guides/field-masks
func writeMasked(w http.ResponseWriter, v any, m fieldMask) {
	// The fake's own structs, so neither step can fail.
	raw, _ := json.Marshal(v)
	var tree any
	_ = json.Unmarshal(raw, &tree)
	writeJSON(w, prune(tree, m))
}

// fieldMask is a parsed mask: each name it keeps, and what it keeps
// below that name, with nil keeping everything.
type fieldMask map[string]fieldMask

// parseMask reads a mask such as "a,b/c,d.e,f(g,h(*))", and reports
// false for one whose parentheses do not balance, which Google answers
// with a bare 400.
func parseMask(mask string) (fieldMask, bool) {
	m, i, ok := parseMaskList(mask, 0)
	return m, ok && i == len(mask)
}

func parseMaskList(mask string, i int) (fieldMask, int, bool) {
	out, all := fieldMask{}, false
	for i < len(mask) && mask[i] != ')' {
		start := i
		for i < len(mask) && !strings.ContainsRune(",()", rune(mask[i])) {
			i++
		}
		// '/' and '.' both step into a field.
		path := strings.FieldsFunc(strings.TrimSpace(mask[start:i]), func(r rune) bool { return r == '/' || r == '.' })
		var sub fieldMask
		if i < len(mask) && mask[i] == '(' {
			var ok bool
			if sub, i, ok = parseMaskList(mask, i+1); !ok || i == len(mask) {
				return nil, i, false
			}
			i++ // the ')'
		}
		for j := len(path) - 1; j >= 1; j-- {
			if path[j] == "*" {
				sub = nil
			} else {
				sub = fieldMask{path[j]: sub}
			}
		}
		switch {
		case len(path) == 0:
		case path[0] == "*":
			all = true
		default:
			merge(out, path[0], sub)
		}
		if i < len(mask) && mask[i] == ',' {
			i++
		}
	}
	if all {
		return nil, i, true
	}
	return out, i, true
}

// merge adds what sub keeps under name, keeping a field kept whole whole.
func merge(dst fieldMask, name string, sub fieldMask) {
	prev, ok := dst[name]
	switch {
	case !ok:
		dst[name] = sub
	case prev == nil:
	case sub == nil:
		dst[name] = nil
	default:
		for k, v := range sub {
			merge(prev, k, v)
		}
	}
}

// keeps reports whether m keeps the field at path.
func (m fieldMask) keeps(path ...string) bool {
	for _, name := range path {
		if m == nil {
			return true
		}
		sub, ok := m[name]
		if !ok {
			return false
		}
		m = sub
	}
	return true
}

// prune keeps what m names in v, element by element through arrays.
func prune(v any, m fieldMask) any {
	if m == nil {
		return v
	}
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for name, sub := range m {
			if x, ok := t[name]; ok {
				out[name] = prune(x, sub)
			}
		}
		return out
	case []any:
		for i := range t {
			t[i] = prune(t[i], m)
		}
		return t
	}
	return v
}

// docCard is a Doc as spreadsheets.get and spreadsheets.create both
// return it: everything but the cells.
func docCard(d *Doc) *gsheets.Spreadsheet {
	out := &gsheets.Spreadsheet{
		SpreadsheetID:  d.ID,
		SpreadsheetURL: gapi.SpreadsheetURL(d.ID),
		Properties: &gsheets.SpreadsheetProperties{
			Title: d.Title, Locale: d.Locale, TimeZone: d.TimeZone, AutoRecalc: d.AutoRecalc,
		},
		NamedRanges: d.NamedRanges,
		DataSources: d.DataSources,
	}
	for _, sh := range d.Sheets {
		props := sh.Props
		out.Sheets = append(out.Sheets, &gsheets.Sheet{
			Properties:         &props,
			Merges:             sh.Merges,
			ProtectedRanges:    sh.Protected,
			FilterViews:        sh.FilterViews,
			Tables:             sh.Tables,
			Charts:             sh.Charts,
			Slicers:            sh.Slicers,
			BandedRanges:       sh.Bandings,
			ConditionalFormats: sh.Conditional,
		})
	}
	return out
}

// clamp resolves a range against the sheet's allocated size, guarding a
// zero the same way the service does. Without the guard a fixture that
// left GridProperties unset clamped to nothing here and to 1000x26
// there, so the fake and the server disagreed about the same sheet.
// defaultRows and defaultCols are a grid's size when its properties
// leave it out, which is a new sheet's.
const defaultRows, defaultCols = 1000, 26

func clamp(sh *Sheet, rect a1.Rect) a1.Rect {
	rows, cols := defaultRows, defaultCols
	if gp := sh.Props.GridProperties; gp != nil {
		if gp.RowCount > 0 {
			rows = gp.RowCount
		}
		if gp.ColumnCount > 0 {
			cols = gp.ColumnCount
		}
	}
	return rect.Clamp(rows, cols)
}

// GridData builds the response the API would send for a rectangle, with
// trailing empty rows and trailing empty cells omitted the way it omits
// them. Exported so the renderer's goldens are generated from the ragged
// shape a real read produces rather than from a neat rectangle.
func GridData(sh *Sheet, rect a1.Rect) *gsheets.GridData { return gridData(sh, clamp(sh, rect)) }

// gridData builds the response for a rectangle, with trailing empty rows
// and trailing empty cells omitted the way the API omits them.
func gridData(sh *Sheet, rect a1.Rect) *gsheets.GridData {
	g := &gsheets.GridData{StartRow: rect.FirstRow - 1, StartColumn: rect.FirstCol - 1}
	lastRow := 0
	rows := make([][]*gsheets.CellData, rect.Rows())
	for i := range rows {
		row := make([]*gsheets.CellData, rect.Cols())
		last := 0
		for j := range row {
			c := sh.At(rect.FirstRow+i, rect.FirstCol+j)
			row[j] = c
			if c != nil {
				last = j + 1
			}
		}
		rows[i] = row[:last]
		if last > 0 {
			lastRow = i + 1
		}
	}
	for _, row := range rows[:lastRow] {
		g.RowData = append(g.RowData, &gsheets.RowData{Values: row})
	}
	return g
}

func (s *Server) valuesGet(w http.ResponseWriter, r *http.Request) {
	d := s.Doc(spreadsheetID(r.URL.Path))
	if d == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Requested entity was not found.")
		return
	}
	raw := r.URL.Path[strings.Index(r.URL.Path, "/values/")+len("/values/"):]
	rangeA1, err := url.PathUnescape(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "Unable to parse range: "+raw)
		return
	}
	vr, apiErr := values(d, rangeA1, r.URL.Query().Get("valueRenderOption"))
	if apiErr != nil {
		writeError(w, apiErr.status, apiErr.rpc, apiErr.message)
		return
	}
	writeJSON(w, vr)
}

func (s *Server) valuesBatchGet(w http.ResponseWriter, r *http.Request) {
	d := s.Doc(spreadsheetID(r.URL.Path))
	if d == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Requested entity was not found.")
		return
	}
	out := &gsheets.BatchGetValuesResponse{SpreadsheetID: d.ID}
	for _, raw := range r.URL.Query()["ranges"] {
		vr, apiErr := values(d, raw, r.URL.Query().Get("valueRenderOption"))
		if apiErr != nil {
			writeError(w, apiErr.status, apiErr.rpc, apiErr.message)
			return
		}
		out.ValueRanges = append(out.ValueRanges, vr)
	}
	writeJSON(w, out)
}

type fakeError struct {
	status  int
	rpc     string
	message string
}

func values(d *Doc, rangeA1, render string) (*gsheets.ValueRange, *fakeError) {
	ref, err := a1.Parse(rangeA1)
	if err != nil {
		return nil, &fakeError{http.StatusBadRequest, "INVALID_ARGUMENT", "Unable to parse range: " + rangeA1}
	}
	sh := d.Find(ref.Sheet)
	if sh == nil {
		// Verbatim the message a shipped server hit on a non-English
		// account, which is the failure this fixture exists to reproduce.
		return nil, &fakeError{http.StatusBadRequest, "INVALID_ARGUMENT", "Unable to parse range: " + rangeA1}
	}
	rect := clamp(sh, ref.Rect)
	out := &gsheets.ValueRange{
		Range:          a1.Format(sh.Props.Title, rect),
		MajorDimension: "ROWS",
	}
	rows := make([][]any, rect.Rows())
	lastRow := 0
	for i := range rows {
		row := make([]any, rect.Cols())
		last := 0
		for j := range row {
			v := renderCell(sh.At(rect.FirstRow+i, rect.FirstCol+j), render)
			row[j] = v
			if v != "" && v != nil {
				last = j + 1
			}
		}
		rows[i] = row[:last]
		if last > 0 {
			lastRow = i + 1
		}
		for j := range rows[i] {
			if rows[i][j] == nil {
				rows[i][j] = ""
			}
		}
	}
	// Trailing empty rows are omitted, and interior gaps come back as
	// empty strings. A ragged response is normal.
	out.Values = rows[:lastRow]
	return out, nil
}

func renderCell(c *gsheets.CellData, render string) any {
	if c == nil {
		return nil
	}
	switch render {
	case gapi.RenderFormula:
		switch v := c.UserEnteredValue; {
		case v == nil:
			return nil
		case v.FormulaValue != nil:
			return *v.FormulaValue
		case v.StringValue != nil:
			return *v.StringValue
		case v.NumberValue != nil:
			return *v.NumberValue
		case v.BoolValue != nil:
			return *v.BoolValue
		}
		return nil
	case gapi.RenderUnformatted:
		switch v := c.EffectiveValue; {
		case v == nil:
			return nil
		case v.StringValue != nil:
			return *v.StringValue
		case v.NumberValue != nil:
			return *v.NumberValue
		case v.BoolValue != nil:
			return *v.BoolValue
		case v.ErrorValue != nil:
			return v.ErrorValue.Display()
		}
		return nil
	default:
		if c.FormattedValue == "" {
			return nil
		}
		return c.FormattedValue
	}
}

func (s *Server) about(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"user": map[string]string{
		"displayName": "Fixture Account", "emailAddress": "fixture@example.test",
	}})
}

func (s *Server) filesGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/drive/v3/files/")
	id, _ = url.PathUnescape(id)
	s.mu.Lock()
	f := s.files[id]
	s.mu.Unlock()
	if f == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "File not found: "+id)
		return
	}
	writeDriveMasked(w, r, f)
}

// writeDriveMasked answers a Drive read with what its mask names, and
// whole without one, as Drive does.
// https://developers.google.com/workspace/drive/api/guides/fields-parameter
func writeDriveMasked(w http.ResponseWriter, r *http.Request, v any) {
	fields := r.URL.Query().Get("fields")
	if fields == "" {
		writeJSON(w, v)
		return
	}
	mask, ok := parseMask(fields)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid field selection "+fields)
		return
	}
	writeMasked(w, v, mask)
}

func (s *Server) filesList(w http.ResponseWriter, r *http.Request) {
	clauses, err := parseDriveQuery(r.URL.Query().Get("q"))
	if err != nil {
		// Drive answers a malformed query with 400. An unescaped
		// apostrophe in a caller's search term lands here, which is what
		// keeps the escaping honest.
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid Value: "+err.Error())
		return
	}
	s.mu.Lock()
	files := make([]*gapi.File, 0, len(s.files))
	for _, f := range s.files {
		files = append(files, f)
	}
	s.mu.Unlock()

	out := &gapi.FileList{Files: []*gapi.File{}}
	for _, f := range files {
		if matches(f, clauses) {
			out.Files = append(out.Files, f)
		}
	}
	sortFiles(out.Files)
	if n, _ := strconv.Atoi(r.URL.Query().Get("pageSize")); n > 0 && len(out.Files) > n {
		out.Files = out.Files[:n]
		out.NextPageToken = "next-page-fixture"
	}
	writeDriveMasked(w, r, out)
}

func sortFiles(files []*gapi.File) {
	slices.SortStableFunc(files, func(a, b *gapi.File) int {
		return strings.Compare(b.ModifiedTime, a.ModifiedTime) // newest first
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, rpc, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(errorBodyRPC(status, rpc, message)))
}

func errorBody(status int, message string) string {
	return errorBodyRPC(status, rpcFor(status), message)
}

func errorBodyRPC(status int, rpc, message string) string {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{
		"code": status, "status": rpc, "message": message,
	}})
	return string(b)
}

func rpcFor(status int) string {
	switch status {
	case 400:
		return "INVALID_ARGUMENT"
	case 401:
		return "UNAUTHENTICATED"
	case 403:
		return "PERMISSION_DENIED"
	case 404:
		return "NOT_FOUND"
	case 429:
		return "RESOURCE_EXHAUSTED"
	case 503:
		return "UNAVAILABLE"
	}
	return "INTERNAL"
}
