package gapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/time/rate"
)

type staticToken struct{}

func (staticToken) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "at", TokenType: "Bearer"}, nil
}

// testClient wires a client to h with the limiters open and sleep
// instant, so a retry test takes microseconds rather than seconds.
func testClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(staticToken{}, Options{
		SheetsBaseURL: srv.URL + "/v4",
		DriveBaseURL:  srv.URL + "/drive/v3",
		ReadLimiter:   rate.NewLimiter(rate.Inf, 1),
		WriteLimiter:  rate.NewLimiter(rate.Inf, 1),
		AllowURL:      func(*url.URL) bool { return true },
		Sleep:         func(context.Context, time.Duration) error { return nil },
	})
	return c, srv
}

func TestGoogleOnlyAllowlist(t *testing.T) {
	for _, tc := range []struct {
		raw string
		ok  bool
	}{
		{"https://sheets.googleapis.com/v4/spreadsheets/x", true},
		{"https://www.googleapis.com/drive/v3/files", true},
		{"https://oauth2.googleapis.com/token", true},
		{"https://accounts.google.com/o/oauth2/auth", true},
		{"https://sheets.googleapis.com:443/v4", true},
		// The port is part of the check: a redirect to the right host on
		// another port is somebody else's listener.
		{"https://sheets.googleapis.com:8443/v4", false},
		{"http://sheets.googleapis.com/v4", false},
		{"https://sheets.googleapis.com.evil.example/v4", false},
		{"https://docs.googleapis.com/v1", false},
		{"https://127.0.0.1/v4", false},
	} {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := GoogleOnly(u); got != tc.ok {
			t.Errorf("GoogleOnly(%q) = %v, want %v", tc.raw, got, tc.ok)
		}
	}
}

func TestCredentialsGoNowhereElse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the request reached a host that is not Google's")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := New(staticToken{}, Options{SheetsBaseURL: srv.URL + "/v4", ReadLimiter: rate.NewLimiter(rate.Inf, 1)})
	_, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: CardFields})
	if err == nil || !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetSpreadsheet to a non-Google host = %v, want a refusal", err)
	}
}

func TestGetSpreadsheetSendsAFieldMaskAndNoGrid(t *testing.T) {
	var got url.Values
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(`{"spreadsheetId":"id","properties":{"title":"t"},"sheets":[{"properties":{"sheetId":0,"title":"Vandel"}}]}`))
	})
	s, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: CardFields})
	if err != nil {
		t.Fatalf("GetSpreadsheet: %v", err)
	}
	if s.Properties.Title != "t" || len(s.Sheets) != 1 {
		t.Errorf("decoded %+v", s)
	}
	if got.Get("fields") != CardFields {
		t.Errorf("field mask = %q", got.Get("fields"))
	}
	if got.Get("includeGridData") != "" {
		t.Error("the card must never ask for grid data")
	}
}

func TestGetSpreadsheetRefusesUnboundedReads(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the request should not have been sent")
		w.WriteHeader(http.StatusOK)
	})
	// An unmasked get returns the whole grid of a ten-million-cell
	// spreadsheet, and grid data without a range is the same thing said
	// differently. Both are refused before anything is sent.
	if _, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("an unmasked get = %v", err)
	}
	if _, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: GridFields, IncludeGridData: true}); !errors.Is(err, ErrInvalid) {
		t.Errorf("grid data with no range = %v", err)
	}
	if _, err := c.BatchGetValues(context.Background(), "id", nil, ValueOptions{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a batch with no ranges = %v", err)
	}
	// And the third way to ask, which the option guard above cannot see:
	// `includeGridData` is ignored when a field mask is set, so a mask
	// naming `data(` is a grid read whatever the option says. With no
	// range that is every cell of every sheet.
	if _, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: FormatFields}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a mask naming cell data with no range = %v", err)
	}
}

// Every field mask this server sends is balanced. Google answers one
// that is not with a bare 400, and the in-memory fake refuses it too, but
// a test here names which mask it is.
func TestFieldMasksAreBalanced(t *testing.T) {
	for name, mask := range map[string]string{
		"CardFields": CardFields, "GridFields": GridFields, "ReadFields": ReadFields, "FormatFields": FormatFields,
		"FormatTargetFields": FormatTargetFields, "RuleFields": RuleFields, "ChartFields": ChartFields,
		"CommentFields": CommentFields, "PivotFields": PivotFields, "PivotExtentFields": PivotExtentFields,
		"SearchFields": SearchFields, "FileFields": FileFields,
	} {
		depth := 0
		for _, r := range mask {
			switch r {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth < 0 {
				break
			}
		}
		if depth != 0 {
			t.Errorf("%s is unbalanced by %d: %s", name, depth, mask)
		}
	}
}

// Comments come only with the comments view, and the view goes with the
// mask that names them: without it Google refuses the mask (spike R).
func TestGetSpreadsheetAsksForTheCommentsView(t *testing.T) {
	var got url.Values
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(`{"spreadsheetId":"id","comments":[{"commentId":"AAAAc1","anchorId":"AAAAa1","status":"OPEN",` +
			`"headPost":{"postId":"AAAAp1","content":"Unit cost?","author":{"displayName":"Jane Doe","me":true}}}],` +
			`"sheets":[{"properties":{"sheetId":7,"title":"Vandel"},"commentAnchors":[{"anchorId":"AAAAa1",` +
			`"range":{"sheetId":7,"startRowIndex":1,"endRowIndex":2,"startColumnIndex":1,"endColumnIndex":2}}]}]}`))
	})
	s, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: CommentFields, Comments: true})
	if err != nil {
		t.Fatalf("GetSpreadsheet: %v", err)
	}
	if got.Get("commentsViewMode") != "COMMENTS_VIEW_MODE_INCLUDED" || got.Get("fields") != CommentFields {
		t.Errorf("query = %v", got)
	}
	if len(s.Comments) != 1 || s.Comments[0].HeadPost.Content != "Unit cost?" || !s.Comments[0].HeadPost.Author.Me ||
		len(s.Sheets[0].CommentAnchors) != 1 || *s.Sheets[0].CommentAnchors[0].Range.EndRowIndex != 2 {
		t.Errorf("decoded %+v", s)
	}

	got = nil
	if _, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: CommentFields}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a comments mask without the comments view = %v", err)
	}
	if got != nil {
		t.Error("the refused read was sent")
	}
	if _, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: CardFields}); err != nil || got.Has("commentsViewMode") {
		t.Errorf("the card asked for the comments view: %v %v", err, got)
	}
}

// The other side of that rule: a mask naming no cell fields is a
// metadata read and goes through with no range, which is how the card
// and the conditional format rules are read.
func TestMetadataMasksNeedNoRange(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"spreadsheetId":"id"}`))
	})
	for name, fields := range map[string]string{"card": CardFields, "rules": RuleFields} {
		if _, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: fields}); err != nil {
			t.Errorf("the %s mask was refused: %v", name, err)
		}
	}
}

func TestValueOptionsReachTheWire(t *testing.T) {
	var got url.Values
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(`{"range":"'Vandel'!A1:B2","majorDimension":"ROWS","values":[["a",1]]}`))
	})
	vr, err := c.GetValues(context.Background(), "id", "'Vandel'!A1:B2", ValueOptions{Render: RenderFormula})
	if err != nil {
		t.Fatalf("GetValues: %v", err)
	}
	if len(vr.Values) != 1 || len(vr.Values[0]) != 2 {
		t.Errorf("decoded %+v", vr)
	}
	if got.Get("valueRenderOption") != RenderFormula {
		t.Errorf("render option = %q", got.Get("valueRenderOption"))
	}
	if got.Get("dateTimeRenderOption") != "FORMATTED_STRING" {
		t.Errorf("date render = %q; a serial number is unreadable", got.Get("dateTimeRenderOption"))
	}
}

func TestReadRetriesTransientFailures(t *testing.T) {
	attempts := 0
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":503,"status":"UNAVAILABLE","message":"try later"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"spreadsheetId":"id"}`))
	})
	if _, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: CardFields}); err != nil {
		t.Fatalf("GetSpreadsheet: %v", err)
	}
	if attempts != 3 {
		t.Errorf("took %d attempts, want 3", attempts)
	}
}

func TestReadGivesUpAndKeepsTheClass(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Quota exceeded for quota metric 'Read requests'"}}`))
	})
	_, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: CardFields})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("a 429 gave %v", err)
	}
	// Google's own meaning survives: a quota refusal says so, rather
	// than becoming a generic failure the model reads as the person's
	// problem to fix.
	if !strings.Contains(err.Error(), "Quota exceeded") {
		t.Errorf("the quota message was lost: %v", err)
	}
	if Class(err) != "rate_limited" {
		t.Errorf("Class = %q", Class(err))
	}
}

func TestNotFoundAndForbidden(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		class  string
	}{
		{404, `{"error":{"code":404,"status":"NOT_FOUND","message":"Requested entity was not found."}}`, "not_found"},
		{403, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"The caller does not have permission"}}`, "forbidden"},
		{403, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Request had insufficient authentication scopes.","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"ACCESS_TOKEN_SCOPE_INSUFFICIENT"}]}}`, "forbidden"},
		{401, `{"error":{"code":401,"status":"UNAUTHENTICATED","message":"Invalid Credentials"}}`, "auth"},
		{400, `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"Unable to parse range: Sheet1!A1:D3"}}`, "invalid"},
		{500, `{"error":{"code":500,"status":"INTERNAL","message":"Internal error"}}`, "unavailable"},
	} {
		c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		_, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: CardFields})
		if got := Class(err); got != tc.class {
			t.Errorf("HTTP %d classified as %q, want %q (%v)", tc.status, got, tc.class, err)
		}
	}
}

func TestMissingScopeIsForbiddenNotAuth(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Request had insufficient authentication scopes.","details":[{"reason":"ACCESS_TOKEN_SCOPE_INSUFFICIENT"}]}}`))
	})
	_, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: CardFields})
	if !errors.Is(err, ErrMissingScope) {
		t.Fatalf("a scope refusal gave %v", err)
	}
}

func TestThrottling403BacksOff(t *testing.T) {
	attempts := 0
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Rate Limit Exceeded","errors":[{"reason":"userRateLimitExceeded"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"spreadsheetId":"id"}`))
	})
	if _, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: CardFields}); err != nil {
		t.Fatalf("a throttling 403 was not retried: %v", err)
	}
	// A daily quota does not refill, so backing off cannot help, and it
	// is still a quota rather than a permission.
	if got := Class(&APIError{Status: 403, Reason: "dailyLimitExceeded"}); got != "rate_limited" {
		t.Errorf("a daily quota refusal classified as %q, want rate_limited", got)
	}
	if retryableThrottle(&APIError{Status: 403, Reason: "dailyLimitExceeded"}) {
		t.Error("a daily quota refusal must not be retried")
	}
	if throttled(&APIError{Status: 403, Reason: "appNotAuthorizedToFile"}) {
		t.Error("an ordinary permission refusal is not throttling")
	}
}

// TestRepeatabilityComesFromTheMethod is the rule §11 turns on. It is
// asserted over the request type rather than over the call sites,
// because a call added later has to inherit it.
func TestRepeatabilityComesFromTheMethod(t *testing.T) {
	for _, tc := range []struct {
		r          request
		writes     bool
		repeatable bool
	}{
		{request{method: http.MethodGet}, false, true},
		{request{method: http.MethodPut}, true, true},
		{request{method: http.MethodPost}, true, false},
		{request{method: http.MethodDelete}, true, false},
		// The three POSTs that only read. Without the flag they would go
		// on the write limiter, refuse to retry, and be blocked under a
		// dry run: three wrong answers from one inference.
		{request{method: http.MethodPost, readOnly: true}, false, true},
	} {
		if got := tc.r.writes(); got != tc.writes {
			t.Errorf("%s readOnly=%v writes() = %v", tc.r.method, tc.r.readOnly, got)
		}
		if got := tc.r.repeatable(); got != tc.repeatable {
			t.Errorf("%s readOnly=%v repeatable() = %v", tc.r.method, tc.r.readOnly, got)
		}
	}
}

func TestUnrepeatableWritesAreNotRepeated(t *testing.T) {
	post := request{method: http.MethodPost, op: "values.append"}
	put := request{method: http.MethodPut, op: "values.update"}

	// A 429 is a refusal to begin: nothing was applied, so it may be
	// sent again.
	if ok, _ := retryable(post, &transientError{err: &APIError{Status: 429}}); !ok {
		t.Error("a 429 on an append should be retried; it never ran")
	}
	// A 500 or a 503 may have applied. Repeating it would append the
	// rows twice.
	for _, status := range []int{500, 503} {
		if ok, _ := retryable(post, &transientError{err: &APIError{Status: status}}); ok {
			t.Errorf("a %d on an append must not be repeated", status)
		}
	}
	err := &transientError{err: &APIError{Status: 500}}
	if ok, _ := retryable(put, err); !ok {
		t.Error("a 500 on an update may be repeated; PUT is repeatable by construction")
	}
	// So may a cut connection.
	if ok, _ := retryable(post, ErrUnavailable); ok {
		t.Error("a dropped connection on an append must not be repeated")
	}

	if got := Class(markAmbiguous("values.append", ErrUnavailable)); got != "ambiguous_outcome" {
		t.Errorf("a failed append classified as %q, want ambiguous_outcome", got)
	}
	// A refusal Google explained is not ambiguous: it never ran.
	refused := error(&APIError{Status: 400, Message: "bad range"})
	if got := Class(markAmbiguous("values.append", refused)); got != "invalid" {
		t.Errorf("a 400 became %q; it never reached the spreadsheet", got)
	}
	if markAmbiguous("values.append", nil) != nil {
		t.Error("markAmbiguous of nil must stay nil")
	}
}

func TestRetryAfterIsHonored(t *testing.T) {
	if got := parseRetryAfter("7"); got != 7*time.Second {
		t.Errorf("parseRetryAfter(7) = %v", got)
	}
	if got := parseRetryAfter(""); got != 0 {
		t.Errorf("parseRetryAfter empty = %v", got)
	}
	if got := parseRetryAfter("nonsense"); got != 0 {
		t.Errorf("parseRetryAfter nonsense = %v", got)
	}
	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= 0 {
		t.Errorf("parseRetryAfter(date) = %v", got)
	}
	c := New(staticToken{}, Options{})
	if got := c.backoff(1, time.Hour); got != c.retry.MaxDelay {
		t.Errorf("a long Retry-After is capped at %v, got %v", c.retry.MaxDelay, got)
	}
	for attempt := 1; attempt <= 6; attempt++ {
		if d := c.backoff(attempt, 0); d <= 0 || d > c.retry.MaxDelay {
			t.Errorf("backoff(%d) = %v", attempt, d)
		}
	}
}

// TestTransportErrorsCarryNoURL is the logging rule made structural. A
// Sheets URL holds the spreadsheet id and the range; a Drive search URL
// holds the caller's search term. So the transport's own message, which
// always names the URL, is dropped rather than passed through.
func TestTransportErrorsCarryNoURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	srv.Close() // nothing is listening now

	c := New(staticToken{}, Options{
		SheetsBaseURL: base + "/v4",
		DriveBaseURL:  base + "/drive/v3",
		ReadLimiter:   rate.NewLimiter(rate.Inf, 1),
		AllowURL:      func(*url.URL) bool { return true },
		Sleep:         func(context.Context, time.Duration) error { return nil },
	})
	_, err := c.SearchSpreadsheets(context.Background(), "name contains 'Quorbin'", 5, "")
	if err == nil {
		t.Fatal("a dead endpoint returned success")
	}
	msg := err.Error()
	for _, forbidden := range []string{"Quorbin", base, "127.0.0.1", "http://"} {
		if strings.Contains(msg, forbidden) {
			t.Errorf("the error carries %q: %s", forbidden, msg)
		}
	}
	if !strings.Contains(msg, "drive.files.list") {
		t.Errorf("the error does not say which call failed: %s", msg)
	}
	if Class(err) != "unavailable" {
		t.Errorf("Class = %q", Class(err))
	}
}

func TestTransportReason(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Get \"https://x/y\": context deadline exceeded", "timed out"},
		{"Get \"https://x/y\": dial tcp: lookup x: no such host", "DNS lookup failed"},
		{"Get \"https://x/y\": dial tcp 1.2.3.4:443: connect: connection refused", "connection refused"},
		{"Get \"https://x/y\": read: connection reset by peer", "connection reset"},
		{"Get \"https://x/y\": tls: handshake failure", "TLS handshake failed"},
		{"Get \"https://x/y\": EOF", "the connection closed early"},
		{"something else entirely", "network error"},
	} {
		if got := transportReason(errors.New(tc.in)); got != tc.want {
			t.Errorf("transportReason(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNoCredentialsKeepsTheServerServing(t *testing.T) {
	c := New(NoCredentials{}, Options{
		SheetsBaseURL: "https://sheets.googleapis.com/v4",
		ReadLimiter:   rate.NewLimiter(rate.Inf, 1),
	})
	_, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: CardFields})
	if Class(err) != "auth" {
		t.Fatalf("a call without credentials gave %q (%v), want auth", Class(err), err)
	}
	if !strings.Contains(err.Error(), "login") {
		t.Errorf("the error does not name the fix: %v", err)
	}
	withReason := NoCredentials{Reason: errors.New("keyring locked")}
	if _, err := withReason.Token(); !strings.Contains(err.Error(), "keyring locked") {
		t.Errorf("the underlying reason was lost: %v", err)
	}
}

func TestAuthErrorFromTheTokenEndpoint(t *testing.T) {
	err := wrapTransportError("values.get", &oauth2.RetrieveError{
		ErrorCode: "invalid_grant", ErrorDescription: "Token has been expired or revoked.",
	})
	var ae *AuthError
	if !errors.As(err, &ae) || ae.Code != "invalid_grant" {
		t.Fatalf("wrapTransportError gave %v", err)
	}
	if Class(err) != "auth" {
		t.Errorf("Class = %q", Class(err))
	}
	// A refused refresh token must not leak its own value into the text.
	generic := wrapTransportError("values.get", errors.New("oauth2: cannot fetch token: 400 Bad Request\nResponse: refresh_token=1//0abcdef"))
	if strings.Contains(generic.Error(), "1//0") {
		t.Errorf("a refresh token reached the message: %v", generic)
	}
}

func TestShortID(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"abc", "abc"},
		{"1SyntheticFixtureSpreadsheetIdXXXXXXXXXXXXXXX", "1Synth…"},
	} {
		if got := ShortID(tc.in); got != tc.want {
			t.Errorf("ShortID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAPIErrorText(t *testing.T) {
	e := &APIError{Status: 403, RPC: "PERMISSION_DENIED", Reason: "ACCESS_TOKEN_SCOPE_INSUFFICIENT", Message: "no", Op: "values.get"}
	s := e.Error()
	for _, want := range []string{"values.get", "403", "PERMISSION_DENIED", "ACCESS_TOKEN_SCOPE_INSUFFICIENT", "no"} {
		if !strings.Contains(s, want) {
			t.Errorf("%q missing %q", s, want)
		}
	}
	if got := (&AuthError{Code: "invalid_grant"}).Error(); !strings.Contains(got, "invalid_grant") {
		t.Errorf("AuthError = %q", got)
	}
	// A body that is not the standard envelope still produces something
	// a person can act on.
	plain := parseAPIError(502, "values.get", []byte("<html>Bad Gateway</html>"))
	if !strings.Contains(plain.Message, "Bad Gateway") {
		t.Errorf("a non-JSON body gave %q", plain.Message)
	}
	if empty := parseAPIError(500, "values.get", nil); empty.Message != "empty error body" {
		t.Errorf("an empty body gave %q", empty.Message)
	}
	long := parseAPIError(500, "values.get", []byte(strings.Repeat("x", 500)))
	if len([]rune(long.Message)) > 310 {
		t.Errorf("a long body was not truncated: %d runes", len([]rune(long.Message)))
	}
}

func TestClassesAreTheOnesClassReturns(t *testing.T) {
	declared := map[string]bool{}
	for _, c := range Classes() {
		declared[c] = true
	}
	for _, err := range []error{
		ErrUnauthorized, ErrMissingScope, ErrForbidden, ErrNotFound,
		ErrRateLimited, ErrInvalid, ErrUnavailable, ErrAmbiguousOutcome,
		errors.New("something nobody classified"),
	} {
		if !declared[Class(err)] {
			t.Errorf("Class(%v) = %q, which Classes() does not declare", err, Class(err))
		}
	}
}

func TestQuoteDriveValue(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Quorbin", `'Quorbin'`},
		{"Yalmic's", `'Yalmic\'s'`},
		{`back\slash`, `'back\\slash'`},
		{`' or 1=1`, `'\' or 1=1'`},
	} {
		if got := QuoteDriveValue(tc.in); got != tc.want {
			t.Errorf("QuoteDriveValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSpreadsheetURL(t *testing.T) {
	if got := SpreadsheetURL("abc"); got != "https://docs.google.com/spreadsheets/d/abc/edit" {
		t.Errorf("SpreadsheetURL = %q", got)
	}
}

func TestContextCancellationIsNotRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := c.GetSpreadsheet(ctx, "id", GetOptions{Fields: CardFields})
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("a canceled context gave %v", err)
	}
}

// TestAPartialRetryPolicyDoesNotPanic is the difference between a
// half-filled configuration and a crash. Filling the policy in only when
// MaxAttempts was absent left MaxDelay at zero, and a zero delay reaches
// rand.Int64N(0), which panics.
func TestAPartialRetryPolicyDoesNotPanic(t *testing.T) {
	for _, policy := range []RetryPolicy{
		{},
		{MaxAttempts: 3},
		{BaseDelay: time.Millisecond},
		{MaxDelay: time.Second},
		{MaxAttempts: 2, BaseDelay: time.Millisecond},
	} {
		c := New(staticToken{}, Options{Retry: policy})
		for attempt := 1; attempt <= 6; attempt++ {
			d := c.backoff(attempt, 0)
			if d <= 0 {
				t.Errorf("policy %+v gave backoff %v on attempt %d", policy, d, attempt)
			}
		}
	}
}

// TestACanceledWriteIsStillAmbiguous covers the two ways out of the
// retry loop that are not a response: a context canceled while waiting
// on the limiter, and one canceled during the backoff. A write that has
// already been sent once may have landed, and saying "canceled" instead
// tells the caller nothing about what is in their spreadsheet.
func TestACanceledWriteIsStillAmbiguous(t *testing.T) {
	attempts := 0
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		// A 429, the one answer that lets an append reach the backoff.
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`))
	}))
	defer srv.Close()

	c := New(staticToken{}, Options{
		SheetsBaseURL: srv.URL + "/v4",
		ReadLimiter:   rate.NewLimiter(rate.Inf, 1),
		WriteLimiter:  rate.NewLimiter(rate.Inf, 1),
		AllowURL:      func(*url.URL) bool { return true },
		// The cancellation lands during the backoff, after the request
		// has been sent once.
		Sleep: func(context.Context, time.Duration) error { cancel(); return ctx.Err() },
	})
	_, err := c.do(ctx, request{op: "values.append", spreadsheet: "id", method: http.MethodPost, url: srv.URL + "/v4/x"})
	if got := Class(err); got != "ambiguous_outcome" {
		t.Fatalf("a canceled append classified as %q (%v); it may already have inserted rows", got, err)
	}

	// And a read canceled the same way is not ambiguous: nothing was
	// changed, so there is nothing to go and look at.
	ctx2, cancel2 := context.WithCancel(context.Background())
	c2 := New(staticToken{}, Options{
		SheetsBaseURL: srv.URL + "/v4",
		ReadLimiter:   rate.NewLimiter(rate.Inf, 1),
		AllowURL:      func(*url.URL) bool { return true },
		Sleep:         func(context.Context, time.Duration) error { cancel2(); return ctx2.Err() },
	})
	_, err = c2.do(ctx2, request{op: "values.get", spreadsheet: "id", method: http.MethodGet, url: srv.URL + "/v4/x"})
	if got := Class(err); got == "ambiguous_outcome" {
		t.Errorf("a canceled read was reported as an ambiguous write")
	}
}

// Google indents its JSON unless told not to, and prettyPrint is a
// system parameter of every Google API rather than a Sheets feature, so
// this client asks once for every request instead of at each place that
// builds a query. A read_range over a large grid is what pays for the
// indentation, and it is the call this server exists to make.
func TestCompactJSONIsAskedForOnEveryRequest(t *testing.T) {
	var queries []string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		_, _ = w.Write([]byte(`{"spreadsheetId":"abc","properties":{"title":"T"}}`))
	})
	if _, err := c.GetSpreadsheet(context.Background(), "abc", GetOptions{Fields: "spreadsheetId,properties.title"}); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 {
		t.Fatalf("want one request, got %d", len(queries))
	}
	if !strings.Contains(queries[0], "prettyPrint=false") {
		t.Errorf("query %q does not ask for compact JSON", queries[0])
	}
}

// A caller that has said so itself is not overruled, and the parameters
// the call site set survive being joined.
func TestCompactJSONLeavesAnExplicitChoiceAlone(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://sheets.googleapis.com/v4/spreadsheets/x?fields=a&prettyPrint=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	askCompactJSON(req)
	if !strings.Contains(req.URL.RawQuery, "prettyPrint=true") {
		t.Errorf("an explicit prettyPrint was overruled: %q", req.URL.RawQuery)
	}
	req, err = http.NewRequestWithContext(context.Background(), http.MethodGet,
		"https://sheets.googleapis.com/v4/spreadsheets/x?fields=a", nil)
	if err != nil {
		t.Fatal(err)
	}
	askCompactJSON(req)
	q := req.URL.Query()
	if q.Get("fields") != "a" || q.Get("prettyPrint") != "false" {
		t.Errorf("want both fields and prettyPrint, got %q", req.URL.RawQuery)
	}
}

// TestAnAppendIsNotRepeatedAfterA5xx is the rule that no 5xx proves
// anything about a write. Google's code.proto says of UNAVAILABLE that it
// is "not always safe to retry non-idempotent operations", so an append
// that got a 500, a 503 or a cut connection may already have inserted
// its rows.
func TestAnAppendIsNotRepeatedAfterA5xx(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply func(http.ResponseWriter)
	}{
		{"500", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":500,"status":"INTERNAL","message":"Internal error encountered."}}`))
		}},
		{"503", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":503,"status":"UNAVAILABLE","message":"The service is currently unavailable."}}`))
		}},
		{"connection cut", func(w http.ResponseWriter) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				tc.reply(w)
			})
			_, err := c.AppendValues(context.Background(), "id", "A1", [][]any{{"x"}},
				WriteOptions{Input: InputRaw, Insert: "INSERT_ROWS"})
			if n := attempts.Load(); n != 1 {
				t.Errorf("the append was sent %d times, want 1", n)
			}
			if got := Class(err); got != "ambiguous_outcome" {
				t.Errorf("Class = %q, want ambiguous_outcome (%v)", got, err)
			}
			// The model must be told to look before it repeats the call.
			if !strings.Contains(err.Error(), "values.append may have been applied; read the spreadsheet before repeating it") {
				t.Errorf("the error does not say to read first: %v", err)
			}
		})
	}
}

// TestAnAppendRetriesOnlyARefusalToBegin: a 429 never started, so the
// append goes again; a 400 is final and is sent once.
func TestAnAppendRetriesOnlyARefusalToBegin(t *testing.T) {
	attempts := 0
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"spreadsheetId":"id"}`))
	})
	opts := WriteOptions{Input: InputRaw, Insert: "INSERT_ROWS"}
	if _, err := c.AppendValues(context.Background(), "id", "A1", [][]any{{"x"}}, opts); err != nil {
		t.Fatalf("an append after a 429 failed: %v", err)
	}
	if attempts != 2 {
		t.Errorf("took %d attempts after a 429, want 2", attempts)
	}

	attempts = 0
	c, _ = testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"bad range"}}`))
	})
	_, err := c.AppendValues(context.Background(), "id", "A1", [][]any{{"x"}}, opts)
	if attempts != 1 {
		t.Errorf("a 400 was sent %d times, want 1", attempts)
	}
	if got := Class(err); got != "invalid" {
		t.Errorf("Class = %q, want invalid", got)
	}
}

// TestAReadGivesUpAfterFiveAttempts pins the attempt budget.
func TestAReadGivesUpAfterFiveAttempts(t *testing.T) {
	attempts := 0
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":503,"status":"UNAVAILABLE","message":"try later"}}`))
	})
	_, err := c.GetSpreadsheet(context.Background(), "id", GetOptions{Fields: CardFields})
	if attempts != 5 {
		t.Errorf("took %d attempts, want 5", attempts)
	}
	if got := Class(err); got != "unavailable" {
		t.Errorf("Class = %q, want unavailable", got)
	}
}

// TestACanceledRequestCarriesNoURL holds the logging rule on the one
// error the transport returns unwrapped. A *url.Error names the URL,
// which carries the spreadsheet id, the range and a Drive search term.
func TestACanceledRequestCarriesNoURL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c, srv := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	})
	_, err := c.SearchSpreadsheets(ctx, "name contains 'Quorbin'", 5, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled search gave %v", err)
	}
	for _, forbidden := range []string{"Quorbin", srv.URL, "http://"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Errorf("the error carries %q: %v", forbidden, err)
		}
	}
}
