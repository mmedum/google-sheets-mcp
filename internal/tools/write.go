package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// CreateInput is what create_spreadsheet takes.
type CreateInput struct {
	Title    string   `json:"title" jsonschema:"the new spreadsheet's title"`
	Sheets   []string `json:"sheets,omitempty" jsonschema:"the sheets to create, in order. Giving this replaces the single sheet Google would otherwise make rather than adding to it, so name every sheet you want"`
	Values   [][]any  `json:"values,omitempty" jsonschema:"seed values for the first sheet, as rows of scalars starting at A1. Every row must be the same length"`
	TSV      string   `json:"tsv,omitempty" jsonschema:"the seed values as tab-separated text, one row per line. Give this or values, not both"`
	Input    string   `json:"input,omitempty" jsonschema:"typed (default) parses the seed values as a person typing, so 007 becomes 7; literal stores them exactly"`
	Locale   string   `json:"locale,omitempty" jsonschema:"a locale such as en_GB, which decides date and number parsing"`
	TimeZone string   `json:"time_zone,omitempty" jsonschema:"a time zone such as Europe/Copenhagen"`
	DryRun   bool     `json:"dry_run,omitempty" jsonschema:"describe what would be created and send nothing"`
}

// WriteInput is what write_values takes.
type WriteInput struct {
	Spreadsheet string  `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Sheet       string  `json:"sheet,omitempty" jsonschema:"the sheet title exactly as get_spreadsheet reports it, or its numeric sheet id. Required unless the range carries the sheet"`
	Range       string  `json:"range" jsonschema:"where the write starts and how far it may reach: a single cell (B2) is enough, and a rectangle (B2:D40) also caps the size. The values decide the rectangle that is actually written"`
	Values      [][]any `json:"values,omitempty" jsonschema:"rows of scalars: strings, numbers, booleans. Every row must be the same length, because a short row leaves the cells beyond it holding what they held"`
	TSV         string  `json:"tsv,omitempty" jsonschema:"the same values as tab-separated text, one row per line. Give this or values, not both"`
	Input       string  `json:"input,omitempty" jsonschema:"typed (default) parses as a person typing: 007 becomes 7, 1-2 becomes a date, =A1+1 becomes a formula. literal stores exactly what you send, which is what a product code or a leading-zero identifier needs. Every value Google changes is named in the result"`

	Overwrite             bool `json:"overwrite,omitempty" jsonschema:"allow the write over cells that are not empty. Without it a write into occupied cells is refused with their addresses"`
	OverwriteFormulas     bool `json:"overwrite_formulas,omitempty" jsonschema:"allow the write over cells holding formulas, which a value write replaces with plain values. Needs overwrite as well: a formula and its result look identical in a read, so this one is asked for separately"`
	AllowExternalFormulas bool `json:"allow_external_formulas,omitempty" jsonschema:"allow writing IMPORTXML, IMPORTDATA, IMPORTHTML, IMPORTFEED, IMAGE, HYPERLINK or IMPORTRANGE. The first six take an arbitrary URL that Google fetches from its own servers; IMPORTRANGE pulls another spreadsheet's data into this one"`

	ExpectCheckpoint string `json:"expect_checkpoint,omitempty" jsonschema:"a checkpoint from a read_range of this same range; the write is refused if the cells changed since. Best effort — Sheets has no atomic guard — so a protected range is the real one"`
	DryRun           bool   `json:"dry_run,omitempty" jsonschema:"report what the write would find and change, and send nothing"`
}

// AppendInput is what append_rows takes.
type AppendInput struct {
	Spreadsheet string  `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Sheet       string  `json:"sheet,omitempty" jsonschema:"the sheet title exactly as get_spreadsheet reports it, or its numeric sheet id"`
	Range       string  `json:"range" jsonschema:"which block of data to append after — not where the rows land. Google finds the contiguous block your range falls in and writes below that one, so a range inside the first block appends after the first block while the whole sheet appends after the last. The result says where the rows actually went"`
	Values      [][]any `json:"values,omitempty" jsonschema:"rows of scalars, all the same length"`
	TSV         string  `json:"tsv,omitempty" jsonschema:"the same rows as tab-separated text. Give this or values, not both"`
	Input       string  `json:"input,omitempty" jsonschema:"typed (default) or literal, as in write_values"`
	Insert      string  `json:"insert,omitempty" jsonschema:"rows (default) inserts, so nothing is overwritten and everything below moves down. overwrite writes over whatever follows the block and needs overwrite as well"`

	Overwrite             bool `json:"overwrite,omitempty" jsonschema:"required for insert=overwrite. The destination is chosen during the call, so this server cannot read it first and tell you what is there"`
	AllowExternalFormulas bool `json:"allow_external_formulas,omitempty" jsonschema:"allow appending formulas that reach outside the spreadsheet, as in write_values"`
	DryRun                bool `json:"dry_run,omitempty" jsonschema:"report what would be appended and send nothing"`
}

// SheetInput is what manage_sheet takes.
type SheetInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Action      string `json:"action" jsonschema:"add, rename, duplicate, copy_to, hide, unhide, reorder, resize, freeze or tab_color"`
	Sheet       string `json:"sheet,omitempty" jsonschema:"the sheet to act on, by title or numeric sheet id. Not needed by add"`
	Title       string `json:"title,omitempty" jsonschema:"the new title: the sheet to create for add, the new name for rename, the copy's name for duplicate"`
	Index       *int   `json:"index,omitempty" jsonschema:"a zero-based position, for add, duplicate and reorder. For reorder it is where the sheet ends up, which the API's own index is not"`
	Rows        int    `json:"rows,omitempty" jsonschema:"for resize, how many rows the sheet has room for; for freeze, how many rows to pin at the top, where zero unfreezes"`
	Cols        int    `json:"cols,omitempty" jsonschema:"for resize, how many columns the sheet has room for; for freeze, how many columns to pin at the left"`
	Destination string `json:"destination,omitempty" jsonschema:"for copy_to, the other spreadsheet: an id, a URL or an exact title"`
	Colour      string `json:"colour,omitempty" jsonschema:"for tab_color, a hex colour such as #4a90d9, or none to clear it"`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"say what would change and send nothing"`
}

// DimensionInput is what edit_dimensions takes.
type DimensionInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Sheet       string `json:"sheet,omitempty" jsonschema:"the sheet to act on, by title or numeric sheet id"`
	Action      string `json:"action" jsonschema:"insert, move, resize, auto_resize, group or ungroup"`
	Dimension   string `json:"dimension" jsonschema:"rows or columns"`
	Band        string `json:"band" jsonschema:"which ones, in A1: 2:5 for rows, B:D for columns. It must agree with dimension"`
	To          int    `json:"to,omitempty" jsonschema:"for move, the one-based row or column the band should start at afterwards"`
	Pixels      int    `json:"pixels,omitempty" jsonschema:"for resize, the new size in pixels"`
	Inherit     bool   `json:"inherit,omitempty" jsonschema:"for insert, take the formatting of the band before rather than the one after"`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"say what would change and send nothing"`
}

// DeleteDimensionsInput is what delete_dimensions takes.
type DeleteDimensionsInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Sheet       string `json:"sheet,omitempty" jsonschema:"the sheet to act on, by title or numeric sheet id"`
	Dimension   string `json:"dimension" jsonschema:"rows or columns"`
	Band        string `json:"band" jsonschema:"which ones, in A1: 2:5 for rows, B:D for columns. It must agree with dimension"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"required: Sheets cannot undo this"`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"report what would go with them and delete nothing"`
}

// ClearInput is what clear_values takes.
type ClearInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Sheet       string `json:"sheet,omitempty" jsonschema:"the sheet title exactly as get_spreadsheet reports it, or its numeric sheet id"`
	Range       string `json:"range,omitempty" jsonschema:"the range to clear, or omitted for the whole sheet"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"required: Sheets cannot undo a clear"`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"report what is there and clear nothing"`
}

// DeleteSheetInput is what delete_sheet takes.
type DeleteSheetInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Sheet       string `json:"sheet" jsonschema:"the sheet to delete, by title or numeric sheet id"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"required: Sheets cannot undo a sheet deletion"`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"report what would go with it and delete nothing"`
}

func registerWrite(s *mcp.Server, d Deps) {
	add(s, d, Def[CreateInput, *service.CreateResult]{
		Name: "create_spreadsheet",
		Description: "Make a new Google Sheets spreadsheet and return its card, including the id every later call needs. " +
			"It is created in My Drive's root; moving it into a folder is a Drive operation this server does not offer. " +
			"Give sheets to name every tab yourself, in order; leave it out and Google makes one sheet and names it in " +
			"the account's language, which is not necessarily an English word. Either way, read the returned sheet " +
			"titles rather than assuming one. " +
			"Seed values go to the first sheet, written as a separate request with the same rules as write_values, so " +
			"input means the same thing here and coercions are reported the same way.",
		Kind: Write,
		Handle: func(ctx context.Context, in CreateInput) (*service.CreateResult, error) {
			return d.Service.Create(ctx, service.CreateRequest{
				Title: in.Title, Sheets: in.Sheets, Values: in.Values, TSV: in.TSV,
				Input: in.Input, Locale: in.Locale, TimeZone: in.TimeZone, DryRun: in.DryRun,
			})
		},
	})

	add(s, d, Def[WriteInput, *service.WriteResult]{
		Name: "write_values",
		Description: "Write a rectangle of values, refusing first anything the write would destroy that you cannot see. " +
			"Before sending, this reads the target and refuses if it holds formulas (a formula and its result look " +
			"identical in a read), if it is not empty, if it is protected, or if the write cuts across a merged range. " +
			"Each refusal names the cells and the argument that would allow it. Sheets has no undo, so this is the only " +
			"guard there is. " +
			"After writing it asks Google for the stored values back and names every one it changed: with input=typed, " +
			"007 becomes 7 and 2026-09-05 becomes a date serial that still displays as a date. " +
			"The range says where to start; the values decide the rectangle. A range larger than the values leaves the " +
			"rest as it was, and the result says so. Use dry_run first when you are not sure what is there.",
		Kind: Write,
		Handle: func(ctx context.Context, in WriteInput) (*service.WriteResult, error) {
			return d.Service.Write(ctx, service.WriteRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Range: in.Range,
				Values: in.Values, TSV: in.TSV, Input: in.Input,
				Overwrite: in.Overwrite, OverwriteFormulas: in.OverwriteFormulas,
				AllowExternalFormulas: in.AllowExternalFormulas,
				ExpectCheckpoint:      in.ExpectCheckpoint, DryRun: in.DryRun,
			})
		},
	})

	add(s, d, Def[AppendInput, *service.AppendResult]{
		Name: "append_rows",
		Description: "Add rows after a block of data, and report where they actually landed. " +
			"Google decides the destination: it finds the contiguous block your range falls in and writes below that " +
			"one, so the same sheet given a cell in the first block and given the whole sheet appends in different " +
			"places. The result names the block it found and the range it wrote. " +
			"insert=rows is the default and destroys nothing — it inserts, and everything below moves down, so an " +
			"address or a checkpoint from before the call no longer points where it did. insert=overwrite writes over " +
			"whatever follows and needs overwrite, because the destination is not knowable before the call. " +
			"Use write_values when you know the addresses you want.",
		Kind: Write,
		Handle: func(ctx context.Context, in AppendInput) (*service.AppendResult, error) {
			return d.Service.Append(ctx, service.AppendRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Range: in.Range,
				Values: in.Values, TSV: in.TSV, Input: in.Input, Insert: in.Insert,
				Overwrite: in.Overwrite, AllowExternalFormulas: in.AllowExternalFormulas,
				DryRun: in.DryRun,
			})
		},
	})

	add(s, d, Def[SheetInput, *service.SheetResult]{
		Name: "manage_sheet",
		Description: "Add, rename, duplicate, copy, hide, unhide, reorder, resize, freeze or colour a sheet. " +
			"One request, whichever action. resize changes how many rows and columns the sheet has room for and only " +
			"grows: shrinking would take whatever is on the rows it removes, and edit_dimensions is where removing " +
			"lives, because it says what is on them first. freeze pins rows at the top and columns at the left, and " +
			"zero unfreezes. copy_to copies into another spreadsheet; duplicate copies inside this one. " +
			"The result lists the sheets afterwards, so the next call can name one exactly.",
		Kind: IdempotentWrite,
		Handle: func(ctx context.Context, in SheetInput) (*service.SheetResult, error) {
			return d.Service.ManageSheet(ctx, service.SheetRequest{
				Spreadsheet: in.Spreadsheet, Action: in.Action, Sheet: in.Sheet, Title: in.Title,
				Index: in.Index, Rows: in.Rows, Cols: in.Cols,
				Destination: in.Destination, Colour: in.Colour, DryRun: in.DryRun,
			})
		},
	})

	add(s, d, Def[DimensionInput, *service.DimensionResult]{
		Name: "edit_dimensions",
		Description: "Insert, move, resize, auto-size, group or ungroup rows or columns. " +
			"dimension and band must agree — rows with 2:5, columns with B:D — and a mismatch is refused rather than " +
			"guessed at, because either reading would move somebody's data somewhere they did not ask for. " +
			"insert and move change the addresses of everything after the band, so a checkpoint or an address from " +
			"before the call no longer points where it did; move's to is where the band ends up. " +
			"auto_resize sizes columns to their contents, which is the one people usually want after a write. " +
			"Removing rows or columns is delete_dimensions, which is a separate tool because it cannot be undone.",
		Kind: Write,
		Handle: func(ctx context.Context, in DimensionInput) (*service.DimensionResult, error) {
			return d.Service.EditDimensions(ctx, service.DimensionRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Action: in.Action,
				Dimension: in.Dimension, Band: in.Band, To: in.To, Pixels: in.Pixels,
				Inherit: in.Inherit, DryRun: in.DryRun,
			})
		},
	})

	add(s, d, Def[DeleteDimensionsInput, *service.DimensionResult]{
		Name: "delete_dimensions",
		Description: "Delete rows or columns and the data on them. " +
			"Counts what is there first and says so, because nothing in Sheets brings it back. " +
			"Everything after the band moves up, so a checkpoint or an address from before the call no longer points " +
			"where it did. " +
			"To make room rather than remove it, or to hide a band, use edit_dimensions. confirm is required.",
		Kind: Destructive,
		Handle: func(ctx context.Context, in DeleteDimensionsInput) (*service.DimensionResult, error) {
			return d.Service.EditDimensions(ctx, service.DimensionRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Action: service.DimDelete,
				Dimension: in.Dimension, Band: in.Band, Confirm: in.Confirm, DryRun: in.DryRun,
			})
		},
	})

	add(s, d, Def[ClearInput, *service.ClearResult]{
		Name: "clear_values",
		Description: "Clear a range's values and keep its formatting, notes and validation rules. " +
			"Reads the range first and says how many cells hold something and how many are formulas, so the " +
			"confirmation is informed. Sheets cannot undo it, so confirm is required and dry_run shows what is there. " +
			"To replace values rather than remove them, use write_values.",
		Kind: Destructive,
		Handle: func(ctx context.Context, in ClearInput) (*service.ClearResult, error) {
			return d.Service.Clear(ctx, service.ClearRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Range: in.Range,
				Confirm: in.Confirm, DryRun: in.DryRun,
			})
		},
	})

	add(s, d, Def[DeleteSheetInput, *service.DeleteSheetResult]{
		Name: "delete_sheet",
		Description: "Delete a sheet and everything on it: values, formulas, charts, formatting. " +
			"Counts what would go with it first and says so, because Sheets cannot bring any of it back. " +
			"Consider manage_sheet duplicate first, or manage_sheet hide, which keeps the sheet and takes it out of " +
			"the way. confirm is required.",
		Kind: Destructive,
		Handle: func(ctx context.Context, in DeleteSheetInput) (*service.DeleteSheetResult, error) {
			return d.Service.DeleteSheet(ctx, service.DeleteSheetRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Confirm: in.Confirm, DryRun: in.DryRun,
			})
		},
	})
}
