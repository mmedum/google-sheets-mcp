//go:build live

package main

import (
	"fmt"
	"strings"
)

// The cell comment steps, on a sheet of their own, because they insert
// and delete rows to watch where a thread goes (spike R). The one
// assignee is the signed-in account, so the only mail this sends goes to
// its own inbox.
const commentSheet = "Livesheet comments"

func (d *driver) commentAll() {
	sec("cell comments")
	d.run(d.commentSetupSteps()...)
	d.run(d.commentAddSteps()...)
	d.run(d.commentReplySteps()...)
	d.run(d.commentThreadSteps()...)
	d.run(d.commentReadSteps()...)
	d.run(d.commentPlaceSteps()...)
	d.run(d.commentDeleteSteps()...)
}

// threadOf is the thread a comment result carries.
func threadOf(s map[string]any) map[string]any {
	t, _ := s["thread"].(map[string]any)
	return t
}

func (d *driver) commentSetupSteps() []step {
	return []step{
		{
			name: "add a sheet for the comment steps",
			why:  "these steps insert and delete rows, which would move what every other step asserts about",
			tool: "manage_sheet",
			args: map[string]any{"spreadsheet": d.spreadsheet, "action": "add", "title": commentSheet, "rows": 20, "cols": 4},
		},
		{
			name: "fill it with something to comment on",
			why:  "a comment records the value its cell showed, so the cells need values",
			tool: "write_values",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": commentSheet, "range": "A1:B4",
				"values": [][]any{{"Item", "Cost"}, {"Widget", 12}, {"Gadget", 30}, {"Sprocket", 7}},
			},
		},
	}
}

func (d *driver) commentAddSteps() []step {
	return []step{
		{
			name: "a dry run comments nothing, and says who would be emailed",
			why:  "a preview that posted would be a preview that wrote, and an assignee is the one write that reaches a person",
			tool: "manage_cell_comment",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "add", "sheet": commentSheet, "cell": "B4",
				"text": "Preview only.", "assignee": d.owner, "dry_run": true,
			},
			check: func(text string, s map[string]any) error {
				if dry, _ := s["dry_run"].(bool); !dry || !strings.Contains(text, "Nothing was sent") ||
					!strings.Contains(text, "Google would email") {
					return fmt.Errorf("the preview does not say it sent nothing and who would be emailed:\n%s", text)
				}
				return nil
			},
		},
		{
			name: "and the sheet has no comments after it",
			why:  "the dry run's claim is about what Google holds, which only a read can answer",
			tool: "read_cell_comments",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": commentSheet},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "No comment threads in") {
					return fmt.Errorf("the dry run left a comment:\n%s", text)
				}
				return nil
			},
		},
		{
			name: "comment on one cell",
			why:  "the thread every later step names, made the way a caller would",
			tool: "manage_cell_comment",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "add", "sheet": commentSheet, "cell": "B2",
				"text": "Is this the unit cost or the total?",
			},
			check: func(_ string, s map[string]any) error {
				t := threadOf(s)
				// The author is the signed-in account's display name, which
				// the redactor does not know; it is registered before
				// anything else here prints it.
				if posts, _ := t["posts"].([]any); len(posts) > 0 {
					if p, _ := posts[0].(map[string]any); p != nil {
						name, _ := p["author"].(string)
						reg(name, "<account name>")
					}
				}
				id, _ := t["comment_id"].(string)
				if id == "" || t["cell"] != "B2" || t["quote"] != "12" {
					return fmt.Errorf("the thread is %v; want an id, cell B2 and the value 12", t)
				}
				d.commentID = id
				return nil
			},
		},
		{
			name: "comment on another, assigned to the signed-in account",
			why:  "the assignee is emailed, and this is the one address the driver may send to",
			tool: "manage_cell_comment",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "add", "sheet": commentSheet, "cell": "B4",
				"text": "Please check this figure.", "assignee": d.owner,
			},
			check: func(_ string, s map[string]any) error {
				t := threadOf(s)
				id, _ := t["comment_id"].(string)
				if id == "" || s["emailed"] == "" || t["assignee"] == "" {
					return fmt.Errorf("the assigned thread is %v, emailed %v", t, s["emailed"])
				}
				d.assignedID = id
				return nil
			},
		},
		{
			name: "a cell off the grid is refused here",
			why:  "Google refuses it too, and saying the sheet's size names the way out",
			tool: "manage_cell_comment",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "add", "sheet": commentSheet, "cell": "B300", "text": "x",
			},
			expectError: "invalid",
		},
		{
			name: "an assignee that is not an address is refused here",
			why:  "spike R: Google accepts any string, so a typo would assign the thread to nobody",
			tool: "manage_cell_comment",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "add", "sheet": commentSheet, "cell": "B3", "text": "x",
				"assignee": "not an address",
			},
			expectError: "invalid",
		},
	}
}

// commentReplySteps read d.commentID, so they are built after the adds
// ran; the reply's id is learned here.
func (d *driver) commentReplySteps() []step {
	return []step{
		{
			name: "reply in the thread",
			why:  "a reply is a post with its own id, which an edit and a delete name",
			tool: "manage_cell_comment",
			args: map[string]any{"spreadsheet": d.spreadsheet, "action": "reply", "comment_id": d.commentID, "text": "Unit cost."},
			check: func(_ string, s map[string]any) error {
				id, _ := s["post_id"].(string)
				if id == "" {
					return fmt.Errorf("the reply carries no post id")
				}
				d.replyID = id
				return nil
			},
		},
	}
}

// commentThreadSteps read d.replyID as well, so they are built after the
// reply ran. Built in one group with it, the edit by post_id carried an
// empty id and rewrote the first comment instead.
func (d *driver) commentThreadSteps() []step {
	return []step{
		{
			name: "reassigning a thread nobody was assigned is refused here",
			why:  "spike R: Google answers it with a 400, and the refusal says what works instead",
			tool: "manage_cell_comment",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "reply", "comment_id": d.commentID, "text": "x", "assignee": d.owner,
			},
			expectError: "invalid",
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "never assigned") {
					return fmt.Errorf("refused for another reason: %s", firstLine(text))
				}
				return nil
			},
		},
		{
			name: "reassigning an assigned thread goes through",
			why:  "Google takes a reassignment on a thread whose first post has an assignee",
			tool: "manage_cell_comment",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "reply", "comment_id": d.assignedID, "text": "Reassigning.",
				"assignee": d.owner,
			},
		},
		{
			name: "edit the first comment",
			why:  "edit with no post_id rewrites the comment that starts the thread",
			tool: "manage_cell_comment",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "edit", "comment_id": d.commentID, "text": "Edited: unit cost or total?",
			},
		},
		{
			name: "edit the reply by its id",
			why:  "post_id reaches a reply, the post an edit of the first comment never touches",
			tool: "manage_cell_comment",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "edit", "comment_id": d.commentID, "post_id": d.replyID,
				"text": "Edited: unit cost.",
			},
		},
		{
			name: "resolve the thread",
			why:  "a resolve is a post that changes the thread's status",
			tool: "manage_cell_comment",
			args: map[string]any{"spreadsheet": d.spreadsheet, "action": "resolve", "comment_id": d.commentID},
			check: func(_ string, s map[string]any) error {
				if threadOf(s)["status"] != "resolved" {
					return fmt.Errorf("the thread is %v after a resolve", threadOf(s)["status"])
				}
				return nil
			},
		},
		{
			name: "resolving it again sends nothing",
			why:  "spike R: Google posts a second resolve on a resolved thread",
			tool: "manage_cell_comment",
			args: map[string]any{"spreadsheet": d.spreadsheet, "action": "resolve", "comment_id": d.commentID},
			check: func(text string, s map[string]any) error {
				if changed, _ := s["changed"].(bool); changed || !strings.Contains(text, "Nothing to change") {
					return fmt.Errorf("a second resolve changed something:\n%s", text)
				}
				return nil
			},
		},
	}
}

func (d *driver) commentReadSteps() []step {
	return []step{
		{
			name: "a read counts the resolved thread and does not list it",
			why:  "resolved threads are listed only on request",
			tool: "read_cell_comments",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": commentSheet},
			check: func(text string, s map[string]any) error {
				if s["open"] != float64(1) || s["resolved"] != float64(1) || !strings.Contains(text, "1 resolved thread not listed") {
					return fmt.Errorf("open %v, resolved %v:\n%s", s["open"], s["resolved"], text)
				}
				return nil
			},
		},
		{
			name: "include_resolved lists it, with both edits and the reassignment",
			why:  "the edits and the reassignment are claims about what Google stored, which only a read can check",
			tool: "read_cell_comments",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": commentSheet, "range": "A1:D10", "include_resolved": true},
			check: func(text string, _ map[string]any) error {
				for _, want := range []string{"Edited: unit cost or total?", "Edited: unit cost.", "resolved the thread",
					"'" + commentSheet + "'!B2 — resolved", "Reassigning."} {
					if !strings.Contains(text, want) {
						return fmt.Errorf("the read does not show %q:\n%s", want, text)
					}
				}
				return nil
			},
		},
		{
			name: "reopen it with a reason",
			why:  "a reopen may carry text, and the thread is open after it",
			tool: "manage_cell_comment",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "action": "reopen", "comment_id": d.commentID, "text": "Reopening: the total is in C.",
			},
			check: func(_ string, s map[string]any) error {
				if threadOf(s)["status"] != "open" {
					return fmt.Errorf("the thread is %v after a reopen", threadOf(s)["status"])
				}
				return nil
			},
		},
	}
}

func (d *driver) commentPlaceSteps() []step {
	return []step{
		{
			name: "insert a row above the comments",
			why:  "a thread's anchor follows its cell, which an A1 address recorded at the add would not",
			tool: "edit_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": commentSheet, "action": "insert", "dimension": "rows", "band": "1:1",
			},
		},
		{
			name: "both threads moved down a row",
			why:  "spike R: the anchor is what says where a thread is now",
			tool: "read_cell_comments",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": commentSheet},
			check: func(text string, _ map[string]any) error {
				for _, want := range []string{"'" + commentSheet + "'!B3 — open", "'" + commentSheet + "'!B5 — open"} {
					if !strings.Contains(text, want) {
						return fmt.Errorf("the read does not show %q:\n%s", want, text)
					}
				}
				return nil
			},
		},
		{
			name: "delete the row the assigned thread is on",
			why:  "spike R: the thread outlives its row, which nothing in Google's reply says",
			tool: "delete_dimensions",
			args: map[string]any{
				"spreadsheet": d.spreadsheet, "sheet": commentSheet, "dimension": "rows", "band": "5:5", "confirm": true,
			},
		},
		{
			name: "the thread is still there, on no cell",
			why:  "a read has to say the cell went, rather than drop the thread or place it on the next row",
			tool: "read_cell_comments",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": commentSheet},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "on a cell that was deleted") {
					return fmt.Errorf("the read does not say the cell went:\n%s", text)
				}
				return nil
			},
		},
		{
			name: "a ranged read leaves it out and says so",
			why:  "no range holds a deleted cell, and a silent omission would read as no thread",
			tool: "read_cell_comments",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": commentSheet, "range": "A1:D10"},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "Left out: 1 thread") {
					return fmt.Errorf("the ranged read does not say it left a thread out:\n%s", text)
				}
				return nil
			},
		},
	}
}

func (d *driver) commentDeleteSteps() []step {
	return []step{
		{
			name: "a dry run deletes nothing",
			why:  "a preview that deleted would be a preview that wrote",
			tool: "delete_cell_comment",
			args: map[string]any{"spreadsheet": d.spreadsheet, "comment_id": d.commentID, "post_id": d.replyID, "dry_run": true},
			check: func(text string, _ map[string]any) error {
				if !strings.Contains(text, "Would delete the reply") {
					return fmt.Errorf("the preview does not say what it would delete:\n%s", text)
				}
				return nil
			},
		},
		{
			name: "delete the reply",
			why:  "a reply goes alone and the thread stays",
			tool: "delete_cell_comment",
			args: map[string]any{"spreadsheet": d.spreadsheet, "comment_id": d.commentID, "post_id": d.replyID, "confirm": true},
		},
		{
			name:        "a delete without confirm is refused",
			why:         "Sheets cannot bring a comment back",
			tool:        "delete_cell_comment",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "comment_id": d.commentID},
			expectError: "blocked",
		},
		{
			name:        "declined, the thread stays",
			why:         "only the person's accept lets a delete through (§9a)",
			tool:        "delete_cell_comment",
			args:        map[string]any{"spreadsheet": d.spreadsheet, "comment_id": d.assignedID, "confirm": true},
			answer:      "decline",
			expectError: "blocked",
		},
		{
			name: "delete the thread",
			why:  "the whole thread goes with its first comment",
			tool: "delete_cell_comment",
			args: map[string]any{"spreadsheet": d.spreadsheet, "comment_id": d.commentID, "confirm": true},
		},
		{
			name: "it is gone",
			why:  "the claim is about what Google holds now",
			tool: "read_cell_comments",
			args: map[string]any{"spreadsheet": d.spreadsheet, "sheet": commentSheet, "include_resolved": true},
			check: func(text string, _ map[string]any) error {
				if strings.Contains(text, d.commentID) || !strings.Contains(text, "Please check this figure.") {
					return fmt.Errorf("the read after the delete:\n%s", text)
				}
				return nil
			},
		},
	}
}
