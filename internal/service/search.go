package service

import (
	"context"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/redact"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

// SearchRequest is what search_spreadsheets asks for.
type SearchRequest struct {
	Name          string
	Text          string
	Owner         string
	ModifiedAfter string
	Limit         int
	PageToken     string
}

// SearchResult lists spreadsheets.
type SearchResult struct {
	Results       string      `json:"results" jsonschema:"the matches as text: title, id, owner, modification time and folder"`
	Spreadsheets  []SearchHit `json:"spreadsheets" jsonschema:"one entry per match"`
	NextPageToken string      `json:"next_page_token,omitempty" jsonschema:"pass back as page_token for the next page"`
}

// SearchHit is one spreadsheet found.
type SearchHit struct {
	Title    string `json:"title"`
	ID       string `json:"spreadsheet"`
	Owner    string `json:"owner,omitempty"`
	Modified string `json:"modified,omitempty"`
	Link     string `json:"link,omitempty"`
}

// Render is the text half.
func (r SearchResult) Render() string { return r.Results }

// Search finds spreadsheets through Drive.
//
// Every value the caller supplies is escaped before it reaches the
// query. Interpolating one raw is how a shipped server let an apostrophe
// close the string early, detach the mimeType filter and return
// arbitrary files — so the escaping is the feature, not a detail of it.
//
// This is the only Drive call this server makes for the caller's own
// sake. Folders, sharing, trashing and revisions belong to a server
// built on the Drive API.
func (s *Service) Search(ctx context.Context, req SearchRequest) (*SearchResult, error) {
	clauses := []string{
		"mimeType = " + gapi.QuoteDriveValue(gapi.SpreadsheetMimeType),
		"trashed = false",
	}
	if v := strings.TrimSpace(req.Name); v != "" {
		clauses = append(clauses, "name contains "+gapi.QuoteDriveValue(v))
	}
	if v := strings.TrimSpace(req.Text); v != "" {
		clauses = append(clauses, "fullText contains "+gapi.QuoteDriveValue(v))
	}
	if v := strings.TrimSpace(req.Owner); v != "" {
		clauses = append(clauses, gapi.QuoteDriveValue(v)+" in owners")
	}
	if v := strings.TrimSpace(req.ModifiedAfter); v != "" {
		if !looksLikeRFC3339(v) {
			return nil, Errorf("invalid", "modified_after must be an RFC 3339 timestamp such as 2026-01-31T00:00:00Z, not %q", v)
		}
		clauses = append(clauses, "modifiedTime > "+gapi.QuoteDriveValue(v))
	}
	if len(clauses) == 2 && req.PageToken == "" {
		return nil, Errorf("invalid", "give at least one of name, text, owner or modified_after; "+
			"listing every spreadsheet in the account is a Drive job, not this server's")
	}

	list, err := s.api.SearchSpreadsheets(ctx, strings.Join(clauses, " and "), req.Limit, req.PageToken)
	if err != nil {
		return nil, wrap(err)
	}
	hs := hits(list.Files)
	res := &SearchResult{Results: render.Hits(hs, list.NextPageToken), NextPageToken: list.NextPageToken}
	res.Spreadsheets = make([]SearchHit, 0, len(hs))
	for _, h := range hs {
		res.Spreadsheets = append(res.Spreadsheets, SearchHit{
			Title: h.Title, ID: h.ID, Owner: h.Owner, Modified: h.Modified, Link: h.Link,
		})
	}
	return res, nil
}

// looksLikeRFC3339 is a shape check, not a parse: Drive rejects a
// malformed timestamp itself, and this turns that 400 into a message
// that names the field.
func looksLikeRFC3339(v string) bool {
	if len(v) < 20 || v[4] != '-' || v[7] != '-' || v[10] != 'T' {
		return false
	}
	for i, c := range v[:10] {
		if i == 4 || i == 7 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return strings.HasSuffix(v, "Z") || strings.Contains(v[10:], "+") || strings.Contains(v[11:], "-")
}

// Describe identifies the signed-in account for doctor.
//
// Masked, and the display name dropped entirely. The issue form asks
// people to paste doctor's output, so what it prints has to be safe to
// paste: enough for somebody to recognise their own account, and nothing
// for a reader of the report. A person's name is not enough for that.
func (s *Service) Describe(ctx context.Context) (string, error) {
	addr, err := s.Account(ctx)
	if err != nil {
		return "", err
	}
	return redact.Email(addr), nil
}

// Account is the signed-in address as Drive reports it, unredacted.
//
// For the profile to record, and nothing else: Describe is the form for
// anything printed, and the two share a call so a change to one cannot
// leave the other reading a different account. §17.8 masks what is meant
// to be pasted — a log, `doctor`, `status` — rather than what is stored
// locally, which is why this exists and why it has exactly one caller.
//
// It comes from Drive rather than from the token because tokeninfo
// returns an address only for a token carrying an email scope, and this
// server asks for neither `openid` nor `userinfo.email` (§17.6). The
// Drive call is one this server already makes.
func (s *Service) Account(ctx context.Context) (string, error) {
	u, err := s.api.About(ctx)
	if err != nil {
		return "", wrap(err)
	}
	return u.EmailAddress, nil
}
