package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// SpreadsheetInput names a spreadsheet and nothing else.
type SpreadsheetInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title to look up through Drive"`
}

// ReadInput scopes a read of cells.
type ReadInput struct {
	Spreadsheet       string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Sheet             string `json:"sheet,omitempty" jsonschema:"the sheet title exactly as get_spreadsheet reports it, or its numeric sheet id. Required unless the range carries the sheet or the URL carried a gid; nothing is defaulted, because Google names the first sheet in the account's language"`
	Range             string `json:"range,omitempty" jsonschema:"an A1 range on that sheet: a rectangle (B2:D40), whole columns (B:D), whole rows (2:5), or omitted for the whole sheet"`
	Show              string `json:"show,omitempty" jsonschema:"values (default), formulas, or both. both prints the formula on a line under the value it produced, which is the only way to see that a number is computed"`
	Format            string `json:"format,omitempty" jsonschema:"grid (default, an addressed grid for reading), or json, csv or tsv for feeding elsewhere"`
	Formatted         bool   `json:"formatted,omitempty" jsonschema:"apply Google's display formatting. Off by default: a currency symbol inside a number you may want to compute with is a trap"`
	MaxCells          int    `json:"max_cells,omitempty" jsonschema:"cell budget, default 5000, maximum 50000. Applied before the call, so an open-ended range never becomes an unbounded fetch"`
	MaxChars          int    `json:"max_chars,omitempty" jsonschema:"character budget for the rendering, default 20000, maximum 400000"`
	ContinueFrom      int    `json:"continue_from,omitempty" jsonschema:"the continue_from row a truncated read returned; resumes there"`
	IncludeNotes      bool   `json:"include_notes,omitempty" jsonschema:"list cell notes under the grid; a values read cannot show them"`
	IncludeValidation bool   `json:"include_validation,omitempty" jsonschema:"list data validation rules under the grid"`
	IncludeMerges     bool   `json:"include_merges,omitempty" jsonschema:"list merged ranges under the grid"`
}

// SearchInput scopes a Drive search.
type SearchInput struct {
	Name          string `json:"name,omitempty" jsonschema:"part of the title, matched case-insensitively"`
	Text          string `json:"text,omitempty" jsonschema:"a word inside the spreadsheet, matched by Drive's full-text index. It reaches cell values, but it tokenises and lags: a hyphenated compound such as Quorbin-01 does not match while Quorbin does, and a spreadsheet changed moments ago may not be found yet. To search inside a spreadsheet you already have, use find_in_spreadsheet, which reads the cells"`
	Owner         string `json:"owner,omitempty" jsonschema:"the owner's email address"`
	ModifiedAfter string `json:"modified_after,omitempty" jsonschema:"an RFC 3339 timestamp such as 2026-01-31T00:00:00Z"`
	Limit         int    `json:"limit,omitempty" jsonschema:"how many to return, default 20, maximum 100"`
	PageToken     string `json:"page_token,omitempty" jsonschema:"the next_page_token from a previous call"`
}

// FindInput scopes a search inside one spreadsheet.
type FindInput struct {
	Spreadsheet    string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Query          string `json:"query,omitempty" jsonschema:"plain text to look for. Give this or regex, not both"`
	Regex          string `json:"regex,omitempty" jsonschema:"an RE2 pattern: no backreferences and no lookaround. Give this or query, not both"`
	Sheet          string `json:"sheet,omitempty" jsonschema:"limit the search to one sheet, by title or numeric sheet id. Default searches every grid sheet"`
	MatchCase      bool   `json:"match_case,omitempty" jsonschema:"make query case-sensitive. A regex says so itself with (?i)"`
	SearchFormulas bool   `json:"search_formulas,omitempty" jsonschema:"also look at the formula under a value, not only at the value"`
	SearchNotes    bool   `json:"search_notes,omitempty" jsonschema:"also look at cell notes"`
	MaxCells       int    `json:"max_cells,omitempty" jsonschema:"how many cells may be read, default 5000, maximum 50000. There is no server-side search in the Sheets API, so this reads and matches here and the budget is what bounds the cost"`
	MaxMatches     int    `json:"max_matches,omitempty" jsonschema:"stop after this many matches, default 200, maximum 5000"`
}

func registerRead(s *mcp.Server, d Deps) {
	add(s, d, Def[SpreadsheetInput, *service.CardResult]{
		Name: "get_spreadsheet",
		Description: "Describe a Google Sheets spreadsheet without reading any cells: title, id, link, locale, time zone, " +
			"recalculation setting, and for every sheet its exact title, numeric sheet id, index, grid size, frozen rows " +
			"and columns, hidden state, tab colour and what it holds. Plus named ranges, tables, protected ranges and " +
			"filter views with their A1 ranges. " +
			"Call this first when handed a spreadsheet: every other tool needs a sheet title, and sheet titles cannot be " +
			"guessed — Google names the first sheet in the account's language, so it is not necessarily an English name. " +
			"Cheap on a spreadsheet of any size. Then read_range for cells, or find_in_spreadsheet to locate something " +
			"when you do not know where it is.",
		Kind: Read,
		Handle: func(ctx context.Context, in SpreadsheetInput) (*service.CardResult, error) {
			return d.Service.Card(ctx, in.Spreadsheet)
		},
	})

	add(s, d, Def[ReadInput, *service.ReadResult]{
		Name: "read_range",
		Description: "Read cells as an addressed grid: column letters across the top, row numbers down the side, so you " +
			"can write back to what you just read without counting. " +
			"Give the sheet exactly as get_spreadsheet reports it. show=both prints each formula under the value it " +
			"produced, which is the only way to tell a computed number from a typed one. include_notes, " +
			"include_validation and include_merges add what a values read cannot show. " +
			"The read is budgeted in cells and characters and the window is resolved before the call, so an open-ended " +
			"range is safe; when it is cut short the footer says where to continue and continue_from resumes there. " +
			"Every read returns a checkpoint to hand to a later write. " +
			"Use get_spreadsheet first for sheet names and sizes, and find_in_spreadsheet when you do not yet know which " +
			"cells you want.",
		Kind: Read,
		Handle: func(ctx context.Context, in ReadInput) (*service.ReadResult, error) {
			if in.ContinueFrom < 0 {
				return nil, service.Errorf("invalid", "continue_from is a row number, so it cannot be negative")
			}
			return d.Service.Read(ctx, service.ReadRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Range: in.Range,
				Show: in.Show, Format: in.Format, Formatted: in.Formatted,
				Budget:       service.Budget{Cells: in.MaxCells, Chars: in.MaxChars},
				ContinueFrom: in.ContinueFrom,
				IncludeNotes: in.IncludeNotes, IncludeValidation: in.IncludeValidation, IncludeMerges: in.IncludeMerges,
			})
		},
	})

	add(s, d, Def[SearchInput, *service.SearchResult]{
		Name: "search_spreadsheets",
		Description: "Find a spreadsheet by part of its title, by text inside it, by owner or by when it changed. " +
			"Returns each match's title, id, owner and modification time, newest first. " +
			"Use it when you have a name rather than an id; then pass the id to get_spreadsheet. " +
			"This searches across the account's spreadsheets. To search inside one spreadsheet, use find_in_spreadsheet.",
		Kind: Read,
		Handle: func(ctx context.Context, in SearchInput) (*service.SearchResult, error) {
			if in.Limit < 0 || in.Limit > 100 {
				return nil, service.Errorf("invalid", "limit must be between 1 and 100")
			}
			return d.Service.Search(ctx, service.SearchRequest{
				Name: in.Name, Text: in.Text, Owner: in.Owner, ModifiedAfter: in.ModifiedAfter,
				Limit: in.Limit, PageToken: in.PageToken,
			})
		},
	})

	add(s, d, Def[FindInput, *service.FindResult]{
		Name: "find_in_spreadsheet",
		Description: "Search inside one spreadsheet for text or an RE2 pattern and get back A1 addresses. " +
			"Each hit says which sheet, which cell, and whether the match was in the value, in the formula under it or " +
			"in a note beside it. " +
			"The Sheets API has no server-side search, so this reads cells and matches them here: it is bounded by " +
			"max_cells and says so when the budget stopped it, rather than reporting no matches for a spreadsheet it " +
			"only partly read. Narrow it with sheet when you can. " +
			"Use search_spreadsheets to find a spreadsheet by name, and read_range once you know which cells you want.",
		Kind: Read,
		Handle: func(ctx context.Context, in FindInput) (*service.FindResult, error) {
			return d.Service.Find(ctx, service.FindRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Query: in.Query, Regex: in.Regex,
				MatchCase: in.MatchCase, SearchFormulas: in.SearchFormulas, SearchNotes: in.SearchNotes,
				Budget: service.Budget{Cells: in.MaxCells, Matches: in.MaxMatches},
			})
		},
	})
}
