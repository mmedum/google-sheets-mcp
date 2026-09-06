package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// ReadFormattingInput is what read_formatting takes.
type ReadFormattingInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Sheet       string `json:"sheet,omitempty" jsonschema:"the sheet title exactly as get_spreadsheet reports it, or its numeric sheet id"`
	Range       string `json:"range,omitempty" jsonschema:"the range to describe, or omitted for the whole sheet"`
	MaxCells    int    `json:"max_cells,omitempty" jsonschema:"how many cells to read, default 5000 and at most 50000. The answer is per block rather than per cell, so this bounds the read rather than the reply"`
}

// FormatCellsInput is what format_cells takes.
//
// Every field is optional and each one set is one op, applied in a
// single atomic batch. A header row that is bold, centred and shaded is
// one call.
type FormatCellsInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Sheet       string `json:"sheet,omitempty" jsonschema:"the sheet title exactly as get_spreadsheet reports it, or its numeric sheet id"`
	Range       string `json:"range" jsonschema:"the range to format, such as A1:D1, B:B for a whole column, or 2:2 for a whole row"`

	NumberFormat string `json:"number_format,omitempty" jsonschema:"how values are displayed: one of text, number, percent, currency, date, time, date_time, scientific, optionally followed by a colon and a pattern, as in date:yyyy-mm-dd or currency:\"£\"#,##0.00. The type is asked for rather than guessed from the pattern, because a wrong guess formats a column as the wrong kind of thing and looks like it worked"`

	Bold          *bool  `json:"bold,omitempty" jsonschema:"true turns bold on, false turns it off, and leaving it out leaves it as it is"`
	Italic        *bool  `json:"italic,omitempty" jsonschema:"as bold"`
	Underline     *bool  `json:"underline,omitempty" jsonschema:"as bold"`
	Strikethrough *bool  `json:"strikethrough,omitempty" jsonschema:"as bold"`
	FontSize      int    `json:"font_size,omitempty" jsonschema:"the point size"`
	FontFamily    string `json:"font_family,omitempty" jsonschema:"the typeface, such as Roboto"`
	TextColour    string `json:"text_colour,omitempty" jsonschema:"a hex colour such as #b7472a, or none to clear it"`
	Background    string `json:"background,omitempty" jsonschema:"the cell fill, as a hex colour such as #d9e2f3, or none to clear it"`

	Borders     string `json:"borders,omitempty" jsonschema:"a border in the spelling a person writes: \"1pt solid #cccccc\", \"2pt dashed\", or \"none\" to remove one. Widths are 1pt, 2pt or 3pt and styles are solid, dotted, dashed or double"`
	BorderSides string `json:"border_sides,omitempty" jsonschema:"which edges the border goes on: all (the default), outer, inner, or a list such as top,bottom,left,right,inner_horizontal,inner_vertical"`
	Horizontal  string `json:"horizontal,omitempty" jsonschema:"left, centre or right"`
	Vertical    string `json:"vertical,omitempty" jsonschema:"top, middle or bottom"`
	Wrap        string `json:"wrap,omitempty" jsonschema:"what happens to text too long for its cell: overflow, clip or wrap"`

	Merge       string `json:"merge,omitempty" jsonschema:"join the cells: all makes one cell, rows makes one per row, columns one per column. Sheets keeps the top-left value of each merged block and discards the rest, so this is refused unless overwrite is passed when anything else in it holds a value"`
	Unmerge     bool   `json:"unmerge,omitempty" jsonschema:"split every merge the range covers"`
	ClearFormat bool   `json:"clear_format,omitempty" jsonschema:"remove all formatting and keep the values. Applied before anything else in the same call, so clearing and then setting is one call"`
	Note        string `json:"note,omitempty" jsonschema:"the note beside every cell in the range. A note is invisible in a values read, so replacing one needs overwrite"`
	ClearNote   bool   `json:"clear_note,omitempty" jsonschema:"remove the note instead of setting one"`

	Overwrite bool `json:"overwrite,omitempty" jsonschema:"allow a merge that discards values, a clear that removes formatting, or a note that replaces one"`
	DryRun    bool `json:"dry_run,omitempty" jsonschema:"say what would change and send nothing"`
}

// ManageRangeInput is what manage_range takes.
type ManageRangeInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Sheet       string `json:"sheet,omitempty" jsonschema:"the sheet title exactly as get_spreadsheet reports it, or its numeric sheet id"`
	Range       string `json:"range" jsonschema:"the range the thing is attached to. For update and delete this is also how it is named: the range has to match the one it covers exactly, which get_spreadsheet and read_formatting both report"`
	Kind        string `json:"kind" jsonschema:"named_range, protected_range, data_validation, table, banding or conditional_format"`
	Action      string `json:"action" jsonschema:"add, update or delete"`

	Name        string `json:"name,omitempty" jsonschema:"the name, for a named range or a table"`
	Description string `json:"description,omitempty" jsonschema:"what a protected range is for, which is shown to anyone who tries to edit it"`
	WarningOnly *bool  `json:"warning_only,omitempty" jsonschema:"for a protected range: true warns in the interface and refuses nothing over the API, false refuses edits from anyone but the owner. Leaving it out on an update leaves it as it is"`

	Condition string   `json:"condition,omitempty" jsonschema:"the test, for data_validation and conditional_format: number_greater, number_between, text_contains, text_eq, one_of_list, date_after, blank, not_blank, custom_formula and the rest of that family"`
	Values    []string `json:"values,omitempty" jsonschema:"what the condition tests against: one value for number_greater, two for number_between, the whole list for one_of_list, the formula for custom_formula"`
	Strict    *bool    `json:"strict,omitempty" jsonschema:"for data_validation: true (the default) rejects a value the rule refuses, false only flags it"`
	Message   string   `json:"message,omitempty" jsonschema:"for data_validation: the message shown when someone selects the cell"`

	Colour     string `json:"colour,omitempty" jsonschema:"a hex colour: the base shade for a banding, or the background a conditional_format rule applies"`
	TextColour string `json:"text_colour,omitempty" jsonschema:"for conditional_format: the text colour the rule applies"`
	Bold       *bool  `json:"bold,omitempty" jsonschema:"for conditional_format: whether the rule makes the text bold"`
	Header     bool   `json:"header,omitempty" jsonschema:"for banding: give the first row a darker shade of the colour"`
	Index      int    `json:"index,omitempty" jsonschema:"for conditional_format: which rule, counted from zero in the order they are evaluated. read_formatting lists the rules with their indexes. On add it is where the new rule goes, so 0 makes it the first to be tried"`

	DryRun bool `json:"dry_run,omitempty" jsonschema:"say what would change and send nothing"`
}

// TransformRangeInput is what transform_range takes.
type TransformRangeInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Sheet       string `json:"sheet,omitempty" jsonschema:"the sheet title exactly as get_spreadsheet reports it, or its numeric sheet id"`
	Range       string `json:"range" jsonschema:"the range to read from. For copy_paste and cut_paste this is the source; destination is where it lands"`
	Action      string `json:"action" jsonschema:"sort, find_replace, trim_whitespace, remove_duplicates, text_to_columns, randomize, auto_fill, copy_paste or cut_paste"`

	SortBy string `json:"sort_by,omitempty" jsonschema:"for sort: the columns and their order, as \"B asc\" or \"B asc, C desc\". Each column has to be inside the range"`

	Find            string `json:"find,omitempty" jsonschema:"for find_replace: the text to look for"`
	Replace         string `json:"replace,omitempty" jsonschema:"for find_replace: what to put in its place. An empty string removes the text"`
	MatchCase       bool   `json:"match_case,omitempty" jsonschema:"for find_replace: match upper and lower case exactly"`
	MatchEntireCell bool   `json:"match_entire_cell,omitempty" jsonschema:"for find_replace: match only cells that are exactly the search text"`
	Regex           bool   `json:"regex,omitempty" jsonschema:"for find_replace: read find as a regular expression"`
	InFormulas      bool   `json:"in_formulas,omitempty" jsonschema:"for find_replace: replace inside formula text as well, which changes what those cells compute. Needs overwrite_formulas when the range holds any"`

	Columns   string `json:"columns,omitempty" jsonschema:"for remove_duplicates: which columns decide whether two rows are the same, as \"B,D\". Empty compares every column in the range"`
	Delimiter string `json:"delimiter,omitempty" jsonschema:"for text_to_columns: comma, semicolon, period, space, or a single character. Empty asks Google to detect it, and then how far right the split lands cannot be known before the call"`

	FillRows   bool `json:"fill_rows,omitempty" jsonschema:"for auto_fill: fill downwards rather than to the right"`
	FillLength int  `json:"fill_length,omitempty" jsonschema:"for auto_fill: how many rows or columns to fill past the range"`

	Destination string `json:"destination,omitempty" jsonschema:"for copy_paste and cut_paste: the cell the top-left corner lands on. It may name another sheet in the same spreadsheet, as 'Other sheet'!A1"`
	Paste       string `json:"paste,omitempty" jsonschema:"what to carry across: normal (the default), values, format, formula, validation or conditional"`
	Transpose   bool   `json:"transpose,omitempty" jsonschema:"for copy_paste: turn rows into columns"`

	Overwrite         bool `json:"overwrite,omitempty" jsonschema:"allow the write over cells that are not empty, wherever this lands"`
	OverwriteFormulas bool `json:"overwrite_formulas,omitempty" jsonschema:"allow it over cells holding formulas. Needs overwrite as well"`
	DryRun            bool `json:"dry_run,omitempty" jsonschema:"say what would change and send nothing"`
}

func registerFormat(s *mcp.Server, d Deps) {
	add(s, d, Def[ReadFormattingInput, *service.FormattingResult]{
		Name: "read_formatting",
		Description: "Describe what a range looks like: number formats, fonts, colours, borders, alignment and wrapping, " +
			"summarised per block of identically formatted cells rather than per cell. " +
			"It is the other half of read_range, which says what the cells hold. A cell with no format of its own is " +
			"counted rather than listed, so what comes back is what somebody set. " +
			"It also lists what is attached to the range and decides how a cell looks without being on the cell: " +
			"merges, conditional format rules with the index manage_range needs, banding, validation rules, notes and " +
			"protected ranges.",
		Kind: Read,
		Handle: func(ctx context.Context, in ReadFormattingInput) (*service.FormattingResult, error) {
			return d.Service.Formatting(ctx, service.FormattingRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Range: in.Range,
				Budget: service.Budget{Cells: in.MaxCells},
			})
		},
	})

	add(s, d, Def[FormatCellsInput, *service.FormatResult]{
		Name: "format_cells",
		Description: "Set how a range looks: number format, font, colours, borders, alignment, wrapping, merges and notes. " +
			"Everything you set in one call is applied in one atomic batch, so a header row that is bold, centred and " +
			"shaded is one call rather than three. " +
			"Formatting destroys nothing, with three exceptions, and each is refused unless overwrite is passed: a merge " +
			"keeps the top-left value of every merged block and discards the rest, clear_format removes formatting " +
			"Sheets cannot bring back, and a note replaces one that no values read would have shown you. Those three " +
			"read the range first; everything else costs one request. " +
			"Conditional format rules are manage_range, because a rule is attached to a range rather than written into it.",
		Kind: IdempotentWrite,
		Handle: func(ctx context.Context, in FormatCellsInput) (*service.FormatResult, error) {
			return d.Service.FormatCells(ctx, service.FormatRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Range: in.Range,
				NumberFormat: in.NumberFormat,
				Bold:         in.Bold, Italic: in.Italic, Underline: in.Underline, Strikethrough: in.Strikethrough,
				FontSize: in.FontSize, FontFamily: in.FontFamily,
				TextColour: in.TextColour, Background: in.Background,
				Borders: in.Borders, BorderSides: in.BorderSides,
				Horizontal: in.Horizontal, Vertical: in.Vertical, Wrap: in.Wrap,
				Merge: in.Merge, Unmerge: in.Unmerge, ClearFormat: in.ClearFormat,
				Note: in.Note, ClearNote: in.ClearNote,
				Overwrite: in.Overwrite, DryRun: in.DryRun,
			})
		},
	})

	add(s, d, Def[ManageRangeInput, *service.RangeResult]{
		Name: "manage_range",
		Description: "Add, update or delete the things attached to a range rather than written into it: a named range, " +
			"a protected range, a data validation rule, a table, banding, or a conditional format rule. " +
			"An existing one is named by the range it covers, which has to match exactly — so you never need to fetch " +
			"an id first, and a range matching several is refused with the list rather than picked from. A conditional " +
			"format rule is the exception: the API identifies those by position, so they take index, which " +
			"read_formatting reports. " +
			"A protected range is the real guarantee this server can offer against a concurrent edit; a checkpoint is " +
			"only best effort.",
		Kind: Write,
		Handle: func(ctx context.Context, in ManageRangeInput) (*service.RangeResult, error) {
			return d.Service.ManageRange(ctx, service.RangeRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Range: in.Range,
				Kind: in.Kind, Action: in.Action,
				Name: in.Name, Description: in.Description, WarningOnly: in.WarningOnly,
				Condition: in.Condition, Values: in.Values, Strict: in.Strict, Message: in.Message,
				Colour: in.Colour, TextColour: in.TextColour, Bold: in.Bold, Header: in.Header,
				Index: in.Index, DryRun: in.DryRun,
			})
		},
	})

	add(s, d, Def[TransformRangeInput, *service.TransformResult]{
		Name: "transform_range",
		Description: "Sort, replace, trim, de-duplicate, split, shuffle, fill, copy or move a range. " +
			"These are the operations that move data without you naming its new address, so each one reads what it " +
			"would land on and refuses first: a paste lands on cells you did not name, a split spills into the columns " +
			"to its right, and a replacement inside formulas rewrites what a cell computes rather than what it shows. " +
			"Each refusal names the cells and the argument that would allow it. " +
			"sort, randomize, remove_duplicates and cut_paste all move rows, so an address or a checkpoint from before " +
			"the call no longer points where it did, and the result says so.",
		Kind: Write,
		Handle: func(ctx context.Context, in TransformRangeInput) (*service.TransformResult, error) {
			return d.Service.Transform(ctx, service.TransformRequest{
				Spreadsheet: in.Spreadsheet, Sheet: in.Sheet, Range: in.Range, Action: in.Action,
				SortBy: in.SortBy,
				Find:   in.Find, Replace: in.Replace, MatchCase: in.MatchCase,
				MatchEntireCell: in.MatchEntireCell, Regex: in.Regex, InFormulas: in.InFormulas,
				Columns: in.Columns, Delimiter: in.Delimiter,
				FillRows: in.FillRows, FillLength: in.FillLength,
				Destination: in.Destination, Paste: in.Paste, Transpose: in.Transpose,
				Overwrite: in.Overwrite, OverwriteFormulas: in.OverwriteFormulas, DryRun: in.DryRun,
			})
		},
	})
}
