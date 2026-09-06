package service

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mmedum/google-sheets-mcp/internal/a1"
	"github.com/mmedum/google-sheets-mcp/internal/gapi"
	"github.com/mmedum/google-sheets-mcp/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/internal/render"
)

var (
	// A spreadsheet id is base64url and long. The pattern is
	// deliberately loose about length and strict about the alphabet: a
	// title with a space or a full stop is never mistaken for an id.
	idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}$`)
	// Both spellings Sheets uses for the sheet in a URL.
	gidPattern = regexp.MustCompile(`[#?&]gid=([0-9]+)`)
	urlPattern = regexp.MustCompile(`/spreadsheets/d/([A-Za-z0-9_-]{20,})`)
)

// Reference is a resolved spreadsheet: its id, and the sheet a URL named
// if it named one.
type Reference struct {
	ID string
	// Gid is the sheet a URL's #gid= pointed at. A sheet named on the
	// call wins over it.
	Gid    int
	HasGid bool
	// File is Drive's answer, present only when the reference was a
	// title and Drive was asked. The search already returns the owner,
	// the folder and the modification time, so a caller that wants them
	// must not fetch them again: with one request in flight at a time
	// and sixty a minute, an avoidable call costs a second of somebody's
	// wall clock.
	File *gapi.File
}

// ParseReference reads an id or a URL. It returns false for anything
// else, which is then a title to look up through Drive.
func ParseReference(s string) (Reference, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Reference{}, false
	}
	if m := urlPattern.FindStringSubmatch(s); m != nil {
		r := Reference{ID: m[1]}
		if g := gidPattern.FindStringSubmatch(s); g != nil {
			if n, err := strconv.Atoi(g[1]); err == nil {
				r.Gid, r.HasGid = n, true
			}
		}
		return r, true
	}
	if idPattern.MatchString(s) {
		return Reference{ID: s}, true
	}
	return Reference{}, false
}

// Resolve turns whatever the caller passed into a spreadsheet id.
//
// A title goes through Drive, and more than one match is refused with
// the candidates listed. Taking the first match is how a server writes
// to the wrong spreadsheet, and the caller is the only one who can tell
// two spreadsheets with the same name apart.
func (s *Service) Resolve(ctx context.Context, ref string) (Reference, error) {
	// Trimmed once, here. ParseReference trims for the id and URL cases,
	// so a title with surrounding whitespace was the only reference that
	// could not match itself.
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Reference{}, Errorf("invalid", "name a spreadsheet: an id, a docs.google.com URL, or a title to look up")
	}
	if r, ok := ParseReference(ref); ok {
		return r, nil
	}
	q := fmt.Sprintf("mimeType = %s and name = %s and trashed = false",
		gapi.QuoteDriveValue(gapi.SpreadsheetMimeType), gapi.QuoteDriveValue(ref))
	list, err := s.api.SearchSpreadsheets(ctx, q, 10, "")
	if err != nil {
		return Reference{}, wrap(err)
	}
	switch len(list.Files) {
	case 1:
		return Reference{ID: list.Files[0].ID, File: list.Files[0]}, nil
	case 0:
		return Reference{}, s.noSuchTitle(ctx, ref)
	}
	return Reference{}, Errorf("ambiguous",
		"%d spreadsheets are called %q; pass one of these ids instead:%s",
		len(list.Files), ref, render.Candidates(hits(list.Files)))
}

// noSuchTitle turns an empty exact-title search into an answer worth
// reading: the closest titles, so the caller can see the typo.
func (s *Service) noSuchTitle(ctx context.Context, title string) error {
	q := fmt.Sprintf("mimeType = %s and name contains %s and trashed = false",
		gapi.QuoteDriveValue(gapi.SpreadsheetMimeType), gapi.QuoteDriveValue(firstWord(title)))
	list, err := s.api.SearchSpreadsheets(ctx, q, 5, "")
	if err != nil || len(list.Files) == 0 {
		return Errorf("not_found", "no spreadsheet is called %q; search_spreadsheets finds one by part of its title", title)
	}
	return Errorf("not_found", "no spreadsheet is exactly called %q. The closest are:%s",
		title, render.Candidates(hits(list.Files)))
}

// firstWord is the widest search that still narrows: the index and the
// slice have to come from the same string, or a leading space shifts one
// against the other and the "did you mean" search looks for a truncated
// word.
func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}

// SheetRef is a resolved sheet: its properties, and the rectangle a
// range named on that sheet.
type SheetRef struct {
	Props *gsheets.SheetProperties
	Rect  a1.Rect
}

// ResolveRange turns the caller's sheet and range into one rectangle on
// one sheet.
//
// Three rules, each of them a bug in a shipped server:
//
//   - No sheet is defaulted. Google names the first sheet in the
//     account's language, so "Sheet1" does not exist on a Portuguese
//     account, and a description that offers it as an example teaches
//     the model to guess.
//   - A missing sheet is refused with the titles that do exist, because
//     the API's own message for it says only "Unable to parse range".
//   - The range is rebuilt from the parsed title rather than
//     concatenated, and the title is always quoted. A title with a
//     space or punctuation needs it, and a whole-sheet reference needs
//     it to mean the sheet: a bare name is resolved as a named range
//     first (verified live), so an unquoted "Data" reads the named
//     range called Data.
func (s *Service) ResolveRange(ctx context.Context, ref Reference, sheet, rangeA1 string) (SheetRef, error) {
	sp, err := s.card(ctx, ref.ID)
	if err != nil {
		return SheetRef{}, err
	}
	parsed, err := a1.Parse(rangeA1)
	if err != nil {
		return SheetRef{}, Errorf("invalid", "%s. A range is A1 notation: a rectangle (B2:D40), whole columns (B:D), "+
			"whole rows (2:5), or empty for the whole sheet", strings.TrimPrefix(err.Error(), "a1: invalid reference: "))
	}
	name := strings.TrimSpace(sheet)
	if parsed.HasSheet {
		if name != "" && name != parsed.Sheet {
			return SheetRef{}, Errorf("invalid",
				"the range names sheet %q and the sheet argument says %q; pass the sheet once", parsed.Sheet, name)
		}
		name = parsed.Sheet
	}
	props, err := s.findSheet(sp, name, ref)
	if err != nil {
		return SheetRef{}, err
	}
	return SheetRef{Props: props, Rect: parsed.Rect}, nil
}

// findSheet resolves a title, a sheet id, or the gid from a URL.
func (s *Service) findSheet(sp *gsheets.Spreadsheet, name string, ref Reference) (*gsheets.SheetProperties, error) {
	titles := make([]string, 0, len(sp.Sheets))
	for _, sh := range sp.Sheets {
		if sh.Properties != nil {
			titles = append(titles, strconv.Quote(sh.Properties.Title))
		}
	}
	if name == "" {
		if ref.HasGid {
			for _, sh := range sp.Sheets {
				if sh.Properties != nil && sh.Properties.SheetID == ref.Gid {
					return sh.Properties, nil
				}
			}
			return nil, Errorf("not_found", "the URL points at sheet id %d, which this spreadsheet does not have; it has %s",
				ref.Gid, join(titles))
		}
		return nil, Errorf("invalid", "name a sheet. This spreadsheet has %s. "+
			"Sheet names are not predictable — Google names the first sheet in the account's language — so nothing is assumed here", join(titles))
	}
	for _, sh := range sp.Sheets {
		if sh.Properties != nil && sh.Properties.Title == name {
			return sh.Properties, nil
		}
	}
	// A number is a sheet id, which is stable across renames.
	if n, err := strconv.Atoi(name); err == nil {
		for _, sh := range sp.Sheets {
			if sh.Properties != nil && sh.Properties.SheetID == n {
				return sh.Properties, nil
			}
		}
	}
	return nil, Errorf("not_found", "no sheet named %q in this spreadsheet; it has %s", name, join(titles))
}

// hits turns Drive files into the renderer's view model.
func hits(files []*gapi.File) []render.Hit {
	out := make([]render.Hit, 0, len(files))
	for _, f := range files {
		h := render.Hit{Title: f.Name, ID: f.ID, Modified: f.ModifiedTime, Link: f.WebViewLink}
		if len(f.Owners) > 0 {
			h.Owner = f.Owners[0].EmailAddress
		}
		out = append(out, h)
	}
	return out
}
