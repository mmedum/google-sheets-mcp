package gapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/oauth2"
)

// Sentinel error classes. Every error this package returns wraps exactly
// one of them, so a caller branches with errors.Is rather than on text.
var (
	ErrUnauthorized     = errors.New("unauthorized")
	ErrMissingScope     = errors.New("missing scope")
	ErrForbidden        = errors.New("forbidden")
	ErrNotFound         = errors.New("not found")
	ErrRateLimited      = errors.New("rate limited")
	ErrInvalid          = errors.New("invalid request")
	ErrUnavailable      = errors.New("unavailable")
	ErrAmbiguousOutcome = errors.New("ambiguous outcome")
	// ErrNoCredentials is the token-source error when no login exists.
	ErrNoCredentials = errors.New("no credentials stored; run `google-sheets-mcp login`")
)

// NoCredentials is a token source that always fails, so a server with no
// login still starts and answers every call with an actionable auth
// error. A server that exits instead shows the person "failed to
// connect" and the model never learns why.
type NoCredentials struct{ Reason error }

// Token implements oauth2.TokenSource.
func (n NoCredentials) Token() (*oauth2.Token, error) {
	if n.Reason != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoCredentials, n.Reason)
	}
	return nil, ErrNoCredentials
}

// APIError is a non-2xx response from Google, decoded from the standard
// error envelope.
//
// It carries the operation name rather than the URL. A Sheets URL holds
// the spreadsheet id and the range, and a Drive search URL holds the
// caller's search term, so an error that carried its URL would put the
// payload wherever the error is printed.
type APIError struct {
	Status  int
	RPC     string // e.g. NOT_FOUND, PERMISSION_DENIED
	Reason  string // e.g. ACCESS_TOKEN_SCOPE_INSUFFICIENT
	Message string
	Op      string // e.g. spreadsheets.get
}

func (e *APIError) Error() string {
	status := e.RPC
	if e.Reason != "" && e.Reason != e.RPC {
		if status == "" {
			status = e.Reason
		} else {
			status += " (" + e.Reason + ")"
		}
	}
	return fmt.Sprintf("google api %s: HTTP %d %s: %s", e.Op, e.Status, status, e.Message)
}

// rateLimit403 are the reasons Google answers 403 that mean a quota
// rather than a permission. The value says whether backing off can help.
//
// Sheets' own troubleshooting page documents 400, 429, 500 and 503 and
// no 403 reasons at all, so this table is the Drive API's vocabulary and
// is unverified for Sheets. Spike E (§15) observes the real shapes.
// Until then nothing is invented: an unlisted 403 stays a permission
// refusal.
var rateLimit403 = map[string]bool{
	reasonKey("rateLimitExceeded"):     true,
	reasonKey("userRateLimitExceeded"): true,
	reasonKey("dailyLimitExceeded"):    false,
}

// reasonKey folds the two spellings of a reason onto one key: the legacy
// envelope spells them camelCase, an ErrorInfo detail UPPER_SNAKE.
func reasonKey(reason string) string {
	return strings.ToLower(strings.ReplaceAll(reason, "_", ""))
}

// retryableThrottle reports whether backing off can clear a 403.
func retryableThrottle(e *APIError) bool { return e.Status == 403 && rateLimit403[reasonKey(e.Reason)] }

// throttled reports whether a 403 means "slow down" rather than "no".
func throttled(e *APIError) bool {
	_, ok := rateLimit403[reasonKey(e.Reason)]
	return e.Status == 403 && ok
}

// Unwrap maps the response onto a sentinel class.
func (e *APIError) Unwrap() error {
	msg := strings.ToLower(e.Message)
	switch {
	case e.Status == 401:
		return ErrUnauthorized
	case e.Status == 403 && (e.Reason == "ACCESS_TOKEN_SCOPE_INSUFFICIENT" || strings.Contains(msg, "insufficient authentication scopes")):
		return ErrMissingScope
	case throttled(e):
		return ErrRateLimited
	case e.Status == 403:
		return ErrForbidden
	case e.Status == 404:
		return ErrNotFound
	case e.Status == 429:
		return ErrRateLimited
	case e.Status >= 500:
		return ErrUnavailable
	case e.Status == 400:
		return ErrInvalid
	}
	return ErrUnavailable
}

// AuthError is a failure to obtain an access token: a revoked or expired
// refresh token, or a client secret that no longer matches.
type AuthError struct {
	Code string
	Msg  string
}

func (e *AuthError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("google auth: %s: %s", e.Code, e.Msg)
	}
	return "google auth: " + e.Code
}

// Unwrap classifies every auth failure as unauthorized.
func (e *AuthError) Unwrap() error { return ErrUnauthorized }

type googleErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Errors  []struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"errors"`
		Details []struct {
			Type   string `json:"@type"`
			Reason string `json:"reason"`
		} `json:"details"`
	} `json:"error"`
}

func parseAPIError(status int, op string, body []byte) *APIError {
	e := &APIError{Status: status, Op: op}
	var g googleErrorBody
	if err := json.Unmarshal(body, &g); err == nil && g.Error.Message != "" {
		e.Message = g.Error.Message
		e.RPC = g.Error.Status
		for _, d := range g.Error.Details {
			if d.Reason != "" {
				e.Reason = d.Reason
				break
			}
		}
		if e.Reason == "" && len(g.Error.Errors) > 0 {
			e.Reason = g.Error.Errors[0].Reason
		}
	} else {
		e.Message = strings.TrimSpace(string(body))
		if r := []rune(e.Message); len(r) > 300 {
			e.Message = string(r[:300]) + "…"
		}
		if e.Message == "" {
			e.Message = "empty error body"
		}
	}
	return e
}

// wrapTransportError turns errors from the oauth2 transport into typed
// auth errors, and everything else into ErrUnavailable.
//
// The error a transport reports carries the URL it was calling, and a
// Sheets URL carries the spreadsheet id, the range and — on a Drive
// search — the caller's search term. So the text is dropped and only the
// operation survives.
func wrapTransportError(op string, err error) error {
	if errors.Is(err, ErrNoCredentials) {
		return &AuthError{Code: "no_credentials", Msg: "run `google-sheets-mcp login`"}
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		code := re.ErrorCode
		if code == "" && re.Response != nil {
			code = fmt.Sprintf("HTTP %d", re.Response.StatusCode)
		}
		return &AuthError{Code: code, Msg: re.ErrorDescription}
	}
	if strings.Contains(err.Error(), "oauth2:") {
		return &AuthError{Code: "token", Msg: "the stored refresh token was refused"}
	}
	return fmt.Errorf("%w: %s could not reach Google (%s)", ErrUnavailable, op, transportReason(err))
}

// transportReason names the kind of network failure without repeating
// the URL the standard library puts in the message.
func transportReason(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "context deadline exceeded"), strings.Contains(s, "Client.Timeout"):
		return "timed out"
	case strings.Contains(s, "no such host"):
		return "DNS lookup failed"
	case strings.Contains(s, "connection refused"):
		return "connection refused"
	case strings.Contains(s, "connection reset"):
		return "connection reset"
	case strings.Contains(s, "certificate"), strings.Contains(s, "tls:"):
		return "TLS handshake failed"
	case strings.Contains(s, "EOF"):
		return "the connection closed early"
	}
	return "network error"
}

// Classes lists every class Class can return, so the closed-vocabulary
// gate can check this half against the service's half rather than
// trusting two comments to agree.
func Classes() []string {
	return []string{"auth", "forbidden", "not_found", "rate_limited", "invalid", "unavailable", "ambiguous_outcome"}
}

// Class returns the short class name for an error, as it appears in the
// "[class] message" a tool returns.
func Class(err error) string {
	switch {
	case errors.Is(err, ErrMissingScope), errors.Is(err, ErrForbidden):
		return "forbidden"
	case errors.Is(err, ErrUnauthorized):
		return "auth"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrAmbiguousOutcome):
		// Not "ambiguous", which means a reference matched several things
		// and asks the caller to choose. This one says a write may or may
		// not have landed and asks them to go and look.
		return "ambiguous_outcome"
	}
	return "unavailable"
}
