// Package gapi is a raw REST client for the Google Sheets API, with the
// one Drive call this server makes beside it.
//
// google.golang.org/api is deliberately not a dependency (§4.8): it
// brings gRPC, OpenTelemetry and the cloud auth stack for a binary that
// needs JSON over HTTP. Requests are built here and decode into
// internal/gsheets. Nothing under this package imports MCP.
//
// Two rules shape everything below.
//
// Repeatability comes from the HTTP method. GET and PUT may be repeated;
// a POST may not, because values.append inserts rows and batchUpdate
// makes a second sheet. Three POSTs in this API only read, and they are
// marked one by one — a syntax-tree test allows that flag only on those
// three by name, because a flag set wrongly on a real write would let it
// run during a preview.
//
// A log line never carries the payload. A Sheets URL holds the
// spreadsheet id and the range; a Drive search URL holds the caller's
// search term. So requests are logged by operation name and a truncated
// spreadsheet id, never by URL, and a transport error's own text is
// dropped rather than passed through.
package gapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/time/rate"
)

// Default endpoints.
const (
	DefaultSheetsBaseURL = "https://sheets.googleapis.com/v4"
	DefaultDriveBaseURL  = "https://www.googleapis.com/drive/v3"
)

// allowedHosts is checked with the port before any credential is
// attached. A redirect or a misconfigured base URL must not be able to
// send an access token anywhere else.
var allowedHosts = map[string]bool{
	"sheets.googleapis.com": true,
	"www.googleapis.com":    true,
	"oauth2.googleapis.com": true,
	"accounts.google.com":   true,
}

// GoogleOnly is the production URL allowlist: HTTPS, one of Google's own
// hosts, and the default port.
func GoogleOnly(u *url.URL) bool {
	if u.Scheme != "https" {
		return false
	}
	if p := u.Port(); p != "" && p != "443" {
		return false
	}
	return allowedHosts[u.Hostname()]
}

// RetryPolicy bounds retries for transient failures.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultRetry is exponential backoff with full jitter, capped at 30
// seconds over five attempts, which is Google's own advice for both 429
// and 503.
func DefaultRetry() RetryPolicy {
	return RetryPolicy{MaxAttempts: 5, BaseDelay: 500 * time.Millisecond, MaxDelay: 30 * time.Second}
}

// Options configure a Client. Zero values are production defaults.
type Options struct {
	// BaseTransport sits under the OAuth transport. nil uses http.DefaultTransport.
	BaseTransport http.RoundTripper
	SheetsBaseURL string
	DriveBaseURL  string
	Logger        *slog.Logger
	// ReadTimeout bounds one read attempt; WriteTimeout bounds a write,
	// and defaults to 180s because that is how long Sheets itself allows
	// a request to run before it times out.
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	Retry        RetryPolicy
	// Limiters hold the documented per-user quota: 60 reads and 60
	// writes a minute, refilled continuously, with a small burst.
	ReadLimiter  *rate.Limiter
	WriteLimiter *rate.Limiter
	UserAgent    string
	// Sleep is replaced in tests.
	Sleep func(context.Context, time.Duration) error
	// AllowURL decides which URLs receive credentials. nil means
	// GoogleOnly; tests point it at their own server.
	AllowURL func(u *url.URL) bool
}

// Client talks to Google with one user's credentials.
type Client struct {
	httpc        *http.Client
	sheets       string
	drive        string
	log          *slog.Logger
	readTimeout  time.Duration
	writeTimeout time.Duration
	retry        RetryPolicy
	readLim      *rate.Limiter
	writeLim     *rate.Limiter
	ua           string
	sleep        func(context.Context, time.Duration) error
	allowURL     func(*url.URL) bool
	// inFlight holds Google's own advice for avoiding 503s: at most one
	// request on the wire at a time.
	inFlight chan struct{}
}

// New builds a client whose requests carry tokens from ts.
func New(ts oauth2.TokenSource, o Options) *Client {
	base := o.BaseTransport
	if base == nil {
		base = http.DefaultTransport
	}
	c := &Client{
		httpc:        &http.Client{Transport: &oauth2.Transport{Source: ts, Base: base}},
		sheets:       strings.TrimRight(o.SheetsBaseURL, "/"),
		drive:        strings.TrimRight(o.DriveBaseURL, "/"),
		log:          o.Logger,
		readTimeout:  o.ReadTimeout,
		writeTimeout: o.WriteTimeout,
		retry:        o.Retry,
		readLim:      o.ReadLimiter,
		writeLim:     o.WriteLimiter,
		ua:           o.UserAgent,
		sleep:        o.Sleep,
		allowURL:     o.AllowURL,
		inFlight:     make(chan struct{}, 1),
	}
	if c.allowURL == nil {
		c.allowURL = GoogleOnly
	}
	if c.sheets == "" {
		c.sheets = DefaultSheetsBaseURL
	}
	if c.drive == "" {
		c.drive = DefaultDriveBaseURL
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.readTimeout <= 0 {
		c.readTimeout = 60 * time.Second
	}
	if c.writeTimeout <= 0 {
		c.writeTimeout = 180 * time.Second
	}
	// Field by field, not all or nothing. Filling the policy in only
	// when MaxAttempts was left out meant a caller who set attempts and
	// nothing else kept MaxDelay at zero — and a zero delay reaches
	// rand.Int64N(0), which panics the process on the first retryable
	// failure instead of backing off.
	d := DefaultRetry()
	if c.retry.MaxAttempts <= 0 {
		c.retry.MaxAttempts = d.MaxAttempts
	}
	if c.retry.BaseDelay <= 0 {
		c.retry.BaseDelay = d.BaseDelay
	}
	if c.retry.MaxDelay <= 0 {
		c.retry.MaxDelay = d.MaxDelay
	}
	// 60 a minute is one a second, which is also Google's advice for
	// concurrency. The burst lets a tool that reads then writes go
	// straight through.
	if c.readLim == nil {
		c.readLim = rate.NewLimiter(rate.Limit(1), 5)
	}
	if c.writeLim == nil {
		c.writeLim = rate.NewLimiter(rate.Limit(1), 5)
	}
	if c.ua == "" {
		c.ua = "google-sheets-mcp"
	}
	if c.sleep == nil {
		c.sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	return c
}

// request is one call to Google.
type request struct {
	// op is the API method name. It is what gets logged, and what an
	// error names, in place of a URL that would carry the payload.
	op string
	// spreadsheet is logged truncated, as the correlation key an
	// operator needs to trace a retry back to the call that caused it.
	spreadsheet string
	method      string
	url         string
	body        []byte
	// readOnly marks a POST that only reads. The Sheets API has exactly
	// three: spreadsheets.getByDataFilter, values.batchGetByDataFilter
	// and developerMetadata.search. A POST is a write by default, so a
	// method added later without thought fails closed, and a test over
	// this package's syntax tree refuses this field on anything but
	// those three by name.
	readOnly bool
}

// writes reports whether this request changes the spreadsheet. The
// method decides, so an unconsidered POST counts as a write.
func (r request) writes() bool {
	return !r.readOnly && r.method != http.MethodGet
}

// repeatable reports whether sending the request twice is harmless.
// values.update is a PUT and is; values.append and batchUpdate are POSTs
// that duplicate rows and sheets, and are not.
func (r request) repeatable() bool {
	switch r.method {
	case http.MethodGet, http.MethodPut:
		return true
	default:
		return r.readOnly
	}
}

// do performs one logical request with rate limiting and retries.
func (c *Client) do(ctx context.Context, r request) ([]byte, error) {
	lim, timeout := c.readLim, c.readTimeout
	if r.writes() {
		lim, timeout = c.writeLim, c.writeTimeout
	}

	// Every path out of the loop below goes through this, because the
	// two that did not — a cancelled context during the rate-limit wait
	// or during the backoff — returned a bare context error for a write
	// that had already been sent once. Hard rule 8 is that an
	// unrepeatable write whose outcome nobody can know says so.
	var lastErr error
	finish := func(err error) error {
		if r.writes() && !r.repeatable() && lastErr != nil {
			return markAmbiguous(lastErr)
		}
		return err
	}

	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		// Taken inside the loop: a retry costs quota like any other call.
		if err := lim.Wait(ctx); err != nil {
			return nil, finish(err)
		}
		res, err := c.once(ctx, r, timeout)
		if err == nil {
			c.log.DebugContext(ctx, "google api",
				"op", r.op, "spreadsheet", ShortID(r.spreadsheet),
				"status", res.status, "ms", res.elapsed.Milliseconds(), "attempt", attempt)
			return res.body, nil
		}
		lastErr = err
		retry, after := retryable(r, err)
		c.log.DebugContext(ctx, "google api failed",
			"op", r.op, "spreadsheet", ShortID(r.spreadsheet), "attempt", attempt,
			"class", Class(err), "status", statusOf(err),
			"retry", retry && attempt < c.retry.MaxAttempts)
		if !retry || attempt == c.retry.MaxAttempts {
			break
		}
		if err := c.sleep(ctx, c.backoff(attempt, after)); err != nil {
			return nil, finish(err)
		}
	}
	return nil, finish(lastErr)
}

type attemptResult struct {
	status  int
	body    []byte
	elapsed time.Duration
}

// transientError carries a Retry-After hint alongside an APIError.
type transientError struct {
	err   error
	after time.Duration
}

func (t *transientError) Error() string { return t.err.Error() }
func (t *transientError) Unwrap() error { return t.err }

func (c *Client) once(ctx context.Context, r request, timeout time.Duration) (*attemptResult, error) {
	// One request on the wire at a time, which is Google's own advice
	// for avoiding 503s.
	select {
	case c.inFlight <- struct{}{}:
		defer func() { <-c.inFlight }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rdr io.Reader
	if r.body != nil {
		rdr = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, r.url, rdr)
	if err != nil {
		return nil, fmt.Errorf("%w: %s could not be addressed", ErrInvalid, r.op)
	}
	if !c.allowURL(req.URL) {
		return nil, fmt.Errorf("%w: refusing to send credentials to %s", ErrForbidden, req.URL.Hostname())
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.ua)
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, err := c.httpc.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() == context.Canceled {
			return nil, err
		}
		return nil, wrapTransportError(r.op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: %s response could not be read (%s)", ErrUnavailable, r.op, transportReason(err))
	}
	res := &attemptResult{status: resp.StatusCode, body: data, elapsed: time.Since(start)}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return res, nil
	}
	apiErr := parseAPIError(resp.StatusCode, r.op, data)
	if resp.StatusCode == 429 || resp.StatusCode >= 500 || retryableThrottle(apiErr) {
		return nil, &transientError{err: apiErr, after: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	return nil, apiErr
}

// retryable decides whether an attempt may be repeated.
//
// A repeatable request retries on anything transient. A write that is
// not repeatable retries only where Google's answer proves it never
// began: 429 is a refusal to start, and 503 is Google declining to serve
// the request at all. A 500 after the request began, or a connection cut
// mid-flight, may have applied and is reported as such instead.
func retryable(r request, err error) (bool, time.Duration) {
	var te *transientError
	if errors.As(err, &te) {
		if r.repeatable() {
			return true, te.after
		}
		var ae *APIError
		if errors.As(te.err, &ae) && (ae.Status == 429 || ae.Status == 503) {
			return true, te.after
		}
		return false, 0
	}
	if errors.Is(err, ErrUnavailable) {
		return r.repeatable(), 0
	}
	return false, 0
}

// markAmbiguous re-classifies a failed unrepeatable write whose outcome
// nobody can know from here.
func markAmbiguous(err error) error {
	if err == nil {
		return nil
	}
	// A refusal Google explained is not ambiguous: it never ran.
	var ae *APIError
	if errors.As(err, &ae) && ae.Status < 500 && ae.Status != 429 {
		return err
	}
	if !errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrRateLimited) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrAmbiguousOutcome, err)
}

func statusOf(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

func (c *Client) backoff(attempt int, after time.Duration) time.Duration {
	if after > 0 {
		return min(after, c.retry.MaxDelay)
	}
	d := c.retry.BaseDelay << (attempt - 1)
	if d > c.retry.MaxDelay || d <= 0 {
		d = c.retry.MaxDelay
	}
	// Full jitter, with a floor so a retry is not effectively immediate.
	return time.Duration(rand.Int64N(int64(d)) + int64(d)/4) //nolint:gosec // jitter, not security
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// ShortID truncates a spreadsheet id for a log line. Six characters of a
// 44-character base64url id cannot be looked up or pasted into a URL,
// which is what makes it a correlation key rather than a disclosure.
func ShortID(id string) string {
	if id == "" {
		return ""
	}
	if len(id) > 6 {
		return id[:6] + "…"
	}
	return id
}
