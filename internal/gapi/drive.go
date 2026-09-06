package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// SpreadsheetMimeType is the Drive MIME type of a Google Sheet.
const SpreadsheetMimeType = "application/vnd.google-apps.spreadsheet"

// User is a Drive user reference.
type User struct {
	DisplayName  string `json:"displayName,omitempty"`
	EmailAddress string `json:"emailAddress,omitempty"`
}

// File is the files.get and files.list response subset this server
// reads. A field here means some code reads it, which is why `parents`
// is not one — see SearchFields.
type File struct {
	ID           string  `json:"id,omitempty"`
	Name         string  `json:"name,omitempty"`
	MimeType     string  `json:"mimeType,omitempty"`
	CreatedTime  string  `json:"createdTime,omitempty"`
	ModifiedTime string  `json:"modifiedTime,omitempty"`
	Owners       []*User `json:"owners,omitempty"`
	WebViewLink  string  `json:"webViewLink,omitempty"`
	Version      string  `json:"version,omitempty"`
	Trashed      bool    `json:"trashed,omitempty"`
}

// FileList is a page of files.list results.
type FileList struct {
	Files         []*File `json:"files"`
	NextPageToken string  `json:"nextPageToken,omitempty"`
}

// SearchFields and FileFields are what the two Drive calls ask for.
//
// `parents` is deliberately absent. It was here, and a file's parent is
// a raw folder id that nothing can use: no tool takes a folder, and
// turning one into a name needs a Drive call this server does not make.
// So it was an identifier fetched, returned and printed because it
// happened to be in the response — the "return the whole upstream
// payload" habit in miniature. A field mask is where data minimisation
// is actually cheap, so it is minimised here rather than masked later.
const SearchFields = "nextPageToken,files(id,name,mimeType,modifiedTime,createdTime,owners(displayName,emailAddress),webViewLink)"

// FileFields is what GetFile asks for.
const FileFields = "id,name,mimeType,createdTime,modifiedTime,owners(displayName,emailAddress),webViewLink,version,trashed"

// SearchSpreadsheets runs a Drive query across My Drive and shared
// drives, newest first. The caller builds q and is responsible for
// escaping every value it interpolates — QuoteDriveValue is how.
//
// This is the only Drive call this server makes. Finding, sharing,
// moving and trashing files belong to a server built on the Drive API.
func (c *Client) SearchSpreadsheets(ctx context.Context, q string, limit int, pageToken string) (*FileList, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	v := url.Values{}
	v.Set("q", q)
	v.Set("pageSize", strconv.Itoa(limit))
	v.Set("orderBy", "modifiedTime desc")
	v.Set("fields", SearchFields)
	v.Set("supportsAllDrives", "true")
	v.Set("includeItemsFromAllDrives", "true")
	v.Set("corpora", "allDrives")
	if pageToken != "" {
		v.Set("pageToken", pageToken)
	}
	body, err := c.do(ctx, request{
		op:     "drive.files.list",
		method: http.MethodGet,
		url:    c.drive + "/files?" + v.Encode(),
	})
	if err != nil {
		return nil, err
	}
	var out FileList
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: drive.files.list returned something this server cannot read", ErrUnavailable)
	}
	return &out, nil
}

// GetFile returns Drive metadata for one spreadsheet: the folder it sits
// in, who owns it, when it changed.
func (c *Client) GetFile(ctx context.Context, id string) (*File, error) {
	v := url.Values{}
	v.Set("fields", FileFields)
	v.Set("supportsAllDrives", "true")
	body, err := c.do(ctx, request{
		op:          "drive.files.get",
		spreadsheet: id,
		method:      http.MethodGet,
		url:         c.drive + "/files/" + url.PathEscape(id) + "?" + v.Encode(),
	})
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("%w: drive.files.get returned something this server cannot read", ErrUnavailable)
	}
	return &f, nil
}

// About returns the signed-in account, the cheapest authenticated call
// there is. doctor uses it to prove the token works before anything else.
func (c *Client) About(ctx context.Context) (*User, error) {
	body, err := c.do(ctx, request{
		op:     "drive.about.get",
		method: http.MethodGet,
		url:    c.drive + "/about?fields=user",
	})
	if err != nil {
		return nil, err
	}
	var a struct {
		User *User `json:"user"`
	}
	if err := json.Unmarshal(body, &a); err != nil || a.User == nil {
		return nil, fmt.Errorf("%w: drive.about.get returned no user", ErrUnavailable)
	}
	return a.User, nil
}

// QuoteDriveValue escapes a string for use inside single quotes in a
// Drive query.
//
// Interpolating a caller's text unescaped is how a shipped server let an
// apostrophe close the string early and detach the mimeType filter, so
// a search for a title with an apostrophe returned arbitrary files.
func QuoteDriveValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}
