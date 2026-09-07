package tools

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// Scheme is this server's resource scheme.
const Scheme = "gsheets://"

// Resource templates. There is no static resource list: enumerating a
// person's spreadsheets is a Drive listing, which belongs to a server
// built on the Drive API, and a list nobody can complete is worse than
// none. There are no subscriptions either — the Sheets API has no push
// and no changes feed, so a subscription this server offered would be a
// promise it could only keep by polling somebody's quota.
const (
	CardURI  = Scheme + "{spreadsheet}"
	SheetURI = Scheme + "{spreadsheet}/{sheet}"
)

func registerResources(s *mcp.Server, d Deps) {
	// One handler behind both templates. The SDK matches a URI against
	// each template's regexp and hands the handler the raw URI, so which
	// template matched decides nothing; parsing the URI once is what
	// keeps the two answers from disagreeing about what a URI means.
	h := readResource(d)
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "spreadsheet",
		Title:       "Spreadsheet card",
		URITemplate: CardURI,
		MIMEType:    "text/plain",
		Description: "A spreadsheet's card: title, every sheet with its exact title, id and size, plus named ranges, " +
			"tables and protected ranges. Reads no cells, so it costs the same on a spreadsheet of any size. " +
			"The {spreadsheet} is an id, a docs.google.com URL or an exact title.",
	}, h)
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "sheet",
		Title:       "Sheet as CSV",
		URITemplate: SheetURI,
		MIMEType:    "text/csv",
		Description: "One sheet's used range as CSV — the rows and columns that hold something, not the sheet's " +
			"allocated size. Values as stored, without formatting. The {sheet} is the sheet title, percent-encoded. " +
			"Use read_range instead when you need addresses, formulas or a range of your own choosing.",
	}, h)
}

func readResource(d Deps) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := ""
		if req.Params != nil {
			uri = req.Params.URI
		}
		spreadsheet, sheet, err := ParseResourceURI(uri)
		if err != nil {
			return nil, err
		}
		if sheet == "" {
			card, err := d.Service.Card(ctx, spreadsheet)
			if err != nil {
				return nil, resourceError(uri, err)
			}
			return text(uri, "text/plain", card.Card), nil
		}
		csv, err := d.Service.SheetCSV(ctx, spreadsheet, sheet)
		if err != nil {
			return nil, resourceError(uri, err)
		}
		res := text(uri, "text/csv", csv.CSV)
		if csv.Note != "" {
			// A second entry rather than a line inside the CSV. A
			// comment is not a thing CSV has, so a note in the body
			// parses as a row of data — and a resource cut short that
			// says nothing is the silent half-answer this server exists
			// to avoid.
			res.Contents = append(res.Contents, &mcp.ResourceContents{
				URI: uri, MIMEType: "text/plain", Text: csv.Range + ". " + csv.Note + "\n",
			})
		}
		return res, nil
	}
}

func text(uri, mime, body string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
		{URI: uri, MIMEType: mime, Text: body},
	}}
}

// ParseResourceURI splits a gsheets:// URI into a spreadsheet reference
// and a sheet title, both percent-decoded.
//
// Parsed here rather than with net/url. A sheet title is somebody's
// prose: it can hold a slash, a question mark or a hash, and url.Parse
// would read those as a path separator, a query and a fragment and hand
// back a title with the end missing. Splitting the raw string at the
// first slash and decoding each half means only the encoding has to be
// right, and a client that failed to encode gets an error rather than a
// truncated title.
func ParseResourceURI(uri string) (spreadsheet, sheet string, err error) {
	rest, ok := strings.CutPrefix(uri, Scheme)
	if !ok {
		return "", "", fmt.Errorf("%q is not a %s URI", uri, Scheme)
	}
	head, tail, hasSheet := strings.Cut(rest, "/")
	if spreadsheet, err = url.PathUnescape(head); err != nil {
		return "", "", fmt.Errorf("the spreadsheet in %q is not percent-encoded: %w", uri, err)
	}
	if spreadsheet == "" {
		return "", "", fmt.Errorf("%q names no spreadsheet; the form is %s or %s", uri, CardURI, SheetURI)
	}
	if !hasSheet {
		return spreadsheet, "", nil
	}
	if sheet, err = url.PathUnescape(tail); err != nil {
		return "", "", fmt.Errorf("the sheet in %q is not percent-encoded: %w", uri, err)
	}
	if sheet == "" {
		return "", "", fmt.Errorf("%q ends in a slash with no sheet; drop the slash for the spreadsheet's card", uri)
	}
	if strings.Contains(tail, "/") {
		// Decoding would have joined them silently, and a title with a
		// slash in it is exactly the case this would get wrong.
		return "", "", fmt.Errorf("%q has more than two parts; a slash inside a sheet title is percent-encoded as %%2F", uri)
	}
	return spreadsheet, sheet, nil
}

// resourceError keeps the specification's not-found code for the one
// case that means it, and passes everything else through with its class.
//
// A refusal to overwrite and a missing spreadsheet are different answers
// and a client that retries the first has misread the second.
func resourceError(uri string, err error) error {
	var se *service.Error
	if errors.As(err, &se) && se.Class == "not_found" {
		return mcp.ResourceNotFoundError(uri)
	}
	return fail(err)
}
