package gapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
	// A daily quota does not refill, so backing off cannot help.
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
	// sent again. So is a 503.
	for _, status := range []int{429, 503} {
		err := &transientError{err: &APIError{Status: status}}
		if ok, _ := retryable(post, err); !ok {
			t.Errorf("a %d on an append should be retried; it never ran", status)
		}
	}
	// A 500 may have applied. Repeating it would append the rows twice.
	err := &transientError{err: &APIError{Status: 500}}
	if ok, _ := retryable(post, err); ok {
		t.Error("a 500 on an append must not be repeated")
	}
	if ok, _ := retryable(put, err); !ok {
		t.Error("a 500 on an update may be repeated; PUT is repeatable by construction")
	}
	// So may a cut connection.
	if ok, _ := retryable(post, ErrUnavailable); ok {
		t.Error("a dropped connection on an append must not be repeated")
	}

	if got := Class(markAmbiguous(ErrUnavailable)); got != "ambiguous_outcome" {
		t.Errorf("a failed append classified as %q, want ambiguous_outcome", got)
	}
	// A refusal Google explained is not ambiguous: it never ran.
	refused := error(&APIError{Status: 400, Message: "bad range"})
	if got := Class(markAmbiguous(refused)); got != "invalid" {
		t.Errorf("a 400 became %q; it never reached the spreadsheet", got)
	}
	if markAmbiguous(nil) != nil {
		t.Error("markAmbiguous(nil) must stay nil")
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
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
		t.Fatalf("a cancelled context gave %v", err)
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

// TestACancelledWriteIsStillAmbiguous covers the two ways out of the
// retry loop that are not a response: a context cancelled while waiting
// on the limiter, and one cancelled during the backoff. A write that has
// already been sent once may have landed, and saying "cancelled" instead
// tells the caller nothing about what is in their spreadsheet.
func TestACancelledWriteIsStillAmbiguous(t *testing.T) {
	attempts := 0
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":503,"status":"UNAVAILABLE","message":"try later"}}`))
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
		t.Fatalf("a cancelled append classified as %q (%v); it may already have inserted rows", got, err)
	}

	// And a read cancelled the same way is not ambiguous: nothing was
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
		t.Errorf("a cancelled read was reported as an ambiguous write")
	}
}
