package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-sheets-mcp/internal/service"
)

// AnchorInput scopes a call to manage_anchor.
type AnchorInput struct {
	Spreadsheet string `json:"spreadsheet" jsonschema:"a spreadsheet id, any docs.google.com/spreadsheets URL, or an exact title"`
	Action      string `json:"action" jsonschema:"add, list, move or remove"`
	Name        string `json:"name,omitempty" jsonschema:"the label, as a person would say it: letters, digits, spaces and . _ - are allowed. Required for every action but list"`
	Note        string `json:"note,omitempty" jsonschema:"anything worth recording with the label, such as what the row is for. Returned whenever the anchor is"`
	Sheet       string `json:"sheet,omitempty" jsonschema:"the sheet title exactly as get_spreadsheet reports it, or its numeric sheet id"`
	Range       string `json:"range,omitempty" jsonschema:"what to anchor, for add and move: one row (5:5), one column (B:B), or empty for the whole sheet. A single cell is taken as its row, and the result says which. A rectangle is refused — an anchor attaches to a row, a column or a sheet, never to an area"`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"describe what would happen and send nothing"`
}

func registerAnchor(s *mcp.Server, d Deps) {
	add(s, d, Def[AnchorInput, *service.AnchorResult]{
		Name: "manage_anchor",
		Description: "Label a row, a column or a sheet so it can be found again after the spreadsheet has been " +
			"edited around it. " +
			"An A1 address goes stale the moment somebody inserts a row: what was row 40 is row 41 and nothing says " +
			"so. An anchor does not — it follows its row through inserts, deletes, moves and sorts, so " +
			"anchor:invoice totals still reads the same row a week later. Deleting the row itself takes the anchor " +
			"with it. " +
			"Pass anchor:<name> anywhere a range or band argument is taken, in this tool and every other one; A1 " +
			"keeps working everywhere, so an anchor is a convenience and never a requirement. " +
			"action=add creates one and refuses a name already in use, move points an existing one somewhere else, " +
			"remove deletes the label and leaves the data alone, and list shows every anchor in the spreadsheet. " +
			"Anchors are stored in the spreadsheet itself, so they outlive this session and are visible to anything " +
			"else that reads it.",
		Kind: Write,
		Handle: func(ctx context.Context, in AnchorInput) (*service.AnchorResult, error) {
			return d.Service.Anchors(ctx, service.AnchorRequest{
				Spreadsheet: in.Spreadsheet, Action: in.Action, Name: in.Name, Note: in.Note,
				Sheet: in.Sheet, Range: in.Range, DryRun: in.DryRun,
			})
		},
	})
}
