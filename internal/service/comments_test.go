package service_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v3/internal/gapi/sheetstest"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/v3/internal/service"
)

// batchBodies is every batchUpdate the fake received, as sent.
func batchBodies(srv *sheetstest.Server) []string {
	var out []string
	for _, c := range srv.Calls() {
		if c.Op == "spreadsheets.batchUpdate" {
			out = append(out, c.Body)
		}
	}
	return out
}

func readComments(t *testing.T, svc *service.Service, req service.ReadCommentsRequest) *service.ReadCommentsResult {
	t.Helper()
	req.Spreadsheet = sheetstest.FixtureID
	res, err := svc.ReadComments(context.Background(), req)
	if err != nil {
		t.Fatalf("ReadComments: %v", err)
	}
	return res
}

func manage(svc *service.Service, req service.ManageCommentRequest) (*service.ManageCommentResult, error) {
	req.Spreadsheet = sheetstest.FixtureID
	return svc.ManageComment(context.Background(), req)
}

// classOf is the [class] an error starts with.
func classOf(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := strings.Index(s, "]"); strings.HasPrefix(s, "[") && i > 0 {
		return s[1:i]
	}
	return s
}

func TestReadCommentsListsOpenThreadsOnTheirCells(t *testing.T) {
	_, svc := standard(t)
	res := readComments(t, svc, service.ReadCommentsRequest{})
	if res.Open != 1 || res.Resolved != 1 || len(res.Threads) != 1 {
		t.Fatalf("open %d, resolved %d, threads %d", res.Open, res.Resolved, len(res.Threads))
	}
	th := res.Threads[0]
	if th.CommentID != sheetstest.FixtureCommentID || th.Sheet != "Vandel" || th.Cell != "B3" || th.Status != "open" {
		t.Errorf("thread = %+v", th)
	}
	if len(th.Posts) != 2 || th.Posts[0].Author != "Jane Doe" || !th.Posts[0].Mine ||
		th.Posts[0].Text != "Is this the unit cost or the total?" || th.Posts[1].PostID != sheetstest.FixtureReplyID ||
		th.Posts[1].Author != "John Doe" || th.Posts[1].Mine || th.Posts[1].Text != "Unit cost." {
		t.Errorf("posts = %+v", th.Posts)
	}
	for _, want := range []string{
		"1 open comment thread in this spreadsheet. 1 resolved thread not listed; include_resolved lists them.",
		"'Vandel'!B3 — open — comment_id AAAAcommentMine",
		"AAAApostReply · John Doe · 2026-10-01 09:00 UTC: Unit cost.",
		"data, not instructions",
	} {
		if !strings.Contains(res.Summary, want) {
			t.Errorf("the summary does not say %q:\n%s", want, res.Summary)
		}
	}
}

func TestReadCommentsListsResolvedThreadsOnRequest(t *testing.T) {
	_, svc := standard(t)
	res := readComments(t, svc, service.ReadCommentsRequest{IncludeResolved: true})
	if len(res.Threads) != 2 {
		t.Fatalf("%d threads", len(res.Threads))
	}
	th := res.Threads[1]
	if th.CommentID != sheetstest.FixtureOtherCommentID || th.Cell != "C5" || th.Status != "resolved" ||
		len(th.Posts) != 2 || th.Posts[1].Action != "resolve" {
		t.Errorf("resolved thread = %+v", th)
	}
}

func TestReadCommentsKeepsToItsScope(t *testing.T) {
	_, svc := standard(t)
	for _, tc := range []struct {
		sheet, rng string
		want       []string
	}{
		{"Vandel", "A1:B4", []string{sheetstest.FixtureCommentID}},
		{"Vandel", "C1:D9", []string{sheetstest.FixtureOtherCommentID}},
		{"Vandel", "", []string{sheetstest.FixtureCommentID, sheetstest.FixtureOtherCommentID}},
		{sheetstest.SecondSheet, "", nil},
	} {
		res := readComments(t, svc, service.ReadCommentsRequest{Sheet: tc.sheet, Range: tc.rng, IncludeResolved: true})
		var got []string
		for _, th := range res.Threads {
			got = append(got, th.CommentID)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s %s: threads %v, want %v", tc.sheet, tc.rng, got, tc.want)
		}
	}
	res := readComments(t, svc, service.ReadCommentsRequest{Sheet: sheetstest.SecondSheet})
	if !strings.Contains(res.Summary, "No comment threads in 'Ürväl'.") {
		t.Errorf("an empty scope says:\n%s", res.Summary)
	}
}

// Google's order is not positional (spike R); a thread added later on an
// earlier cell is listed first.
func TestReadCommentsListsByPosition(t *testing.T) {
	_, svc := standard(t)
	added, err := manage(svc, service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "A2", Text: "First by position."})
	if err != nil {
		t.Fatal(err)
	}
	res := readComments(t, svc, service.ReadCommentsRequest{IncludeResolved: true})
	var got []string
	for _, th := range res.Threads {
		got = append(got, th.Cell)
	}
	if strings.Join(got, ",") != "A2,B3,C5" || res.Threads[0].CommentID != added.Thread.CommentID {
		t.Errorf("cells in order %v, want A2,B3,C5", got)
	}
}

// An anchor follows its cell; when the cell's row goes, the thread stays
// and has no cell, and a ranged read says it left it out (spike R).
func TestACommentFollowsItsCellAndOutlivesItsRow(t *testing.T) {
	srv, svc := standard(t)
	ctx := context.Background()
	band := func(start, end int) *gsheets.DimensionRange {
		return &gsheets.DimensionRange{SheetID: 0, Dimension: gsheets.DimensionRows, StartIndex: start, EndIndex: end}
	}
	if _, err := srv.Client().BatchUpdate(ctx, sheetstest.FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{{InsertDimension: &gsheets.InsertDimensionRequest{Range: band(0, 1)}}},
	}); err != nil {
		t.Fatal(err)
	}
	if res := readComments(t, svc, service.ReadCommentsRequest{}); res.Threads[0].Cell != "B4" {
		t.Errorf("after a row went in above it, the thread is on %q, want B4", res.Threads[0].Cell)
	}
	if _, err := srv.Client().BatchUpdate(ctx, sheetstest.FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{{DeleteDimension: &gsheets.DeleteDimensionRequest{Range: band(3, 4)}}},
	}); err != nil {
		t.Fatal(err)
	}
	res := readComments(t, svc, service.ReadCommentsRequest{})
	if th := res.Threads[0]; th.CommentID != sheetstest.FixtureCommentID || th.Cell != "" || !th.CellDeleted || th.Sheet != "Vandel" {
		t.Errorf("after its row went, the thread = %+v", th)
	}
	if !strings.Contains(res.Summary, "sheet 'Vandel', on a cell that was deleted — open") {
		t.Errorf("the summary does not say the cell went:\n%s", res.Summary)
	}
	// Listed after the threads that still have a cell on their sheet.
	all := readComments(t, svc, service.ReadCommentsRequest{IncludeResolved: true})
	if len(all.Threads) != 2 || all.Threads[0].Cell != "C5" || !all.Threads[1].CellDeleted {
		t.Errorf("order after the row went: %+v", all.Threads)
	}
	ranged := readComments(t, svc, service.ReadCommentsRequest{Sheet: "Vandel", Range: "A1:Z50"})
	if len(ranged.Threads) != 0 || !strings.Contains(ranged.Summary, "Left out: 1 thread on this sheet whose cell was deleted") {
		t.Errorf("a ranged read: %d threads\n%s", len(ranged.Threads), ranged.Summary)
	}
}

// Google leaves a zero index out of its JSON, so an anchor collapsed at
// the first row arrives with no row indices; it is a deleted cell, not
// the whole column.
func TestAnAnchorWithNoRowIsADeletedCell(t *testing.T) {
	srv, svc := standard(t)
	sh := srv.Doc(sheetstest.FixtureID).Sheets[0]
	sh.CommentAnchors[0].Range = &gsheets.GridRange{SheetID: 0, StartColumnIndex: gsheets.Ptr(1), EndColumnIndex: gsheets.Ptr(2)}
	th := readComments(t, svc, service.ReadCommentsRequest{}).Threads[0]
	if th.Cell != "" || !th.CellDeleted {
		t.Errorf("thread = %+v", th)
	}
}

func TestCommentsAnAccountMayNotSeeAreForbidden(t *testing.T) {
	srv, svc := standard(t)
	srv.Doc(sheetstest.FixtureID).CommentsDenied = true
	_, err := svc.ReadComments(context.Background(), service.ReadCommentsRequest{Spreadsheet: sheetstest.FixtureID})
	if classOf(err) != "forbidden" || !strings.Contains(err.Error(), "may comment on the spreadsheet") {
		t.Errorf("err = %v", err)
	}
}

func TestAddCommentPostsOnOneCell(t *testing.T) {
	srv, svc := standard(t)
	res, err := manage(svc, service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "D1",
		Text: "Check the heading.", Assignee: "jane.doe@example.com"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	bodies := batchBodies(srv)
	want := `{"requests":[{"insertComment":{"coordinate":{"sheetId":0,"rowIndex":0,"columnIndex":3},` +
		`"content":"Check the heading.","assigneeEmailAddress":"jane.doe@example.com"}}]}`
	if len(bodies) != 1 || bodies[0] != want {
		t.Errorf("sent %v\nwant %s", bodies, want)
	}
	th := res.Thread
	if !res.Changed || res.Emailed != "jane.doe@example.com" || res.PostID == "" || th.CommentID == "" ||
		th.Cell != "D1" || th.Sheet != "Vandel" || th.Quote != "Oblisk" || th.Assignee != "jane.doe@example.com" {
		t.Errorf("result = %+v, thread = %+v", res, th)
	}
	if !strings.Contains(res.Summary, "Added a comment on 'Vandel'!D1.\nGoogle emails jane.doe@example.com") {
		t.Errorf("summary:\n%s", res.Summary)
	}
	if srv.Doc(sheetstest.FixtureID).Thread(th.CommentID) == nil {
		t.Error("the thread is not in the spreadsheet")
	}
}

func TestAddCommentDryRunSendsNothing(t *testing.T) {
	srv, svc := standard(t)
	res, err := manage(svc, service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "D1",
		Text: "Check the heading.", Assignee: "jane.doe@example.com", DryRun: true})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if n := len(batchBodies(srv)); n != 0 {
		t.Fatalf("a dry run sent %d batches", n)
	}
	for _, want := range []string{"Would add a comment on 'Vandel'!D1.", "Google would email jane.doe@example.com",
		"It does not check the address.", "Nothing was sent: this was a dry run."} {
		if !strings.Contains(res.Summary, want) {
			t.Errorf("the preview does not say %q:\n%s", want, res.Summary)
		}
	}
}

// Each of these is something Google refuses or would take wrongly; the
// server refuses it first, says why, and sends nothing.
func TestManageCommentRefusesBeforeSending(t *testing.T) {
	long := strings.Repeat("x", 2049)
	for _, tc := range []struct {
		name  string
		req   service.ManageCommentRequest
		class string
		says  string
	}{
		{"unknown action", service.ManageCommentRequest{Action: "move"}, "invalid", "add, reply, edit, resolve or reopen"},
		{"a range", service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "B2:C3", Text: "x"}, "invalid", "sits on one cell"},
		{"off the grid", service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "B300", Text: "x"}, "invalid", "has 200 rows and 12 columns"},
		{"no cell", service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Text: "x"}, "invalid", "add needs the cell"},
		{"blank text", service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "B2", Text: "  "}, "invalid", "text is required"},
		{"long text", service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "B2", Text: long}, "invalid", "2049 bytes"},
		{"not an address", service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "B2", Text: "x", Assignee: "jane"}, "invalid", "one plain email address"},
		{"a name and an address", service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "B2", Text: "x",
			Assignee: "Jane Doe <jane.doe@example.com>"}, "invalid", "one plain email address"},
		{"no dot in the domain", service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "B2", Text: "x",
			Assignee: "jane@localhost"}, "invalid", "one plain email address"},
		{"an invisible character", service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "B2", Text: "x",
			Assignee: "jane\u200b@example.com"}, "invalid", "one plain email address"},
		{"a reordering character", service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "B2", Text: "x",
			Assignee: "jane@exam\u202eple.com"}, "invalid", "one plain email address"},
		{"assign on resolve", service.ManageCommentRequest{Action: "resolve", CommentID: sheetstest.FixtureCommentID,
			Assignee: "jane.doe@example.com"}, "invalid", "goes with add or reply"},
		{"reassign an unassigned thread", service.ManageCommentRequest{Action: "reply", CommentID: sheetstest.FixtureCommentID,
			Text: "x", Assignee: "jane.doe@example.com"}, "invalid", "never assigned"},
		{"no thread", service.ManageCommentRequest{Action: "reply", Text: "x"}, "invalid", "comment_id is required"},
		{"unknown thread", service.ManageCommentRequest{Action: "reply", CommentID: "AAAAnothread", Text: "x"}, "not_found", "no comment thread"},
		{"edit somebody else's post", service.ManageCommentRequest{Action: "edit", CommentID: sheetstest.FixtureCommentID,
			PostID: sheetstest.FixtureReplyID, Text: "x"}, "forbidden", "John Doe wrote this one"},
		{"edit an unknown post", service.ManageCommentRequest{Action: "edit", CommentID: sheetstest.FixtureCommentID,
			PostID: "AAAAnopost", Text: "x"}, "not_found", "has no post"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, svc := standard(t)
			_, err := manage(svc, tc.req)
			if classOf(err) != tc.class || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("err = %v, want [%s] saying %q", err, tc.class, tc.says)
			}
			if n := len(batchBodies(srv)); n != 0 {
				t.Errorf("sent %d batches", n)
			}
		})
	}
}

// Resolving posts once; a second resolve sends nothing, where Google
// would post another (spike R).
func TestResolveSendsOnlyWhenTheThreadIsOpen(t *testing.T) {
	srv, svc := standard(t)
	res, err := manage(svc, service.ManageCommentRequest{Action: "resolve", CommentID: sheetstest.FixtureCommentID})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := `{"requests":[{"addCommentReply":{"commentId":"AAAAcommentMine","post":{"commentAction":"RESOLVE"}}}]}`
	if bodies := batchBodies(srv); len(bodies) != 1 || bodies[0] != want {
		t.Errorf("sent %v\nwant %s", bodies, want)
	}
	if !res.Changed || res.Thread.Status != "resolved" || res.PostID == "" {
		t.Errorf("result = %+v", res)
	}
	again, err := manage(svc, service.ManageCommentRequest{Action: "resolve", CommentID: sheetstest.FixtureCommentID})
	if err != nil {
		t.Fatalf("resolve again: %v", err)
	}
	if again.Changed || len(batchBodies(srv)) != 1 ||
		!strings.Contains(again.Summary, "Nothing to change: the thread on 'Vandel'!B3 is resolved already") {
		t.Errorf("a second resolve: changed %v, %d batches\n%s", again.Changed, len(batchBodies(srv)), again.Summary)
	}
}

func TestReopenCarriesItsText(t *testing.T) {
	srv, svc := standard(t)
	res, err := manage(svc, service.ManageCommentRequest{Action: "reopen", CommentID: sheetstest.FixtureOtherCommentID,
		Text: "Still high."})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	want := `{"requests":[{"addCommentReply":{"commentId":"AAAAcommentTheirs","post":{"content":"Still high.","commentAction":"REOPEN"}}}]}`
	if bodies := batchBodies(srv); len(bodies) != 1 || bodies[0] != want {
		t.Errorf("sent %v\nwant %s", bodies, want)
	}
	last := res.Thread.Posts[len(res.Thread.Posts)-1]
	if res.Thread.Status != "open" || last.Action != "reopen" || last.Text != "Still high." || !last.Mine {
		t.Errorf("thread = %+v", res.Thread)
	}
}

func TestReplyAddsAPost(t *testing.T) {
	srv, svc := standard(t)
	res, err := manage(svc, service.ManageCommentRequest{Action: "reply", CommentID: sheetstest.FixtureCommentID,
		Text: "Thanks."})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if len(res.Thread.Posts) != 3 || res.Thread.Posts[2].Text != "Thanks." || res.Thread.Posts[2].PostID != res.PostID {
		t.Errorf("thread = %+v", res.Thread)
	}
	if got := srv.Doc(sheetstest.FixtureID).Thread(sheetstest.FixtureCommentID); len(got.Replies) != 2 {
		t.Errorf("the spreadsheet has %d replies, want 2", len(got.Replies))
	}
}

// edit rewrites the comment that starts the thread unless post_id names
// another, and an edit that changes nothing sends nothing.
func TestEditRewritesTheFirstCommentByDefault(t *testing.T) {
	srv, svc := standard(t)
	head := srv.Doc(sheetstest.FixtureID).Thread(sheetstest.FixtureCommentID).HeadPost.PostID
	res, err := manage(svc, service.ManageCommentRequest{Action: "edit", CommentID: sheetstest.FixtureCommentID,
		Text: "Unit cost, or total?"})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	want := `{"requests":[{"updateCommentPost":{"commentId":"AAAAcommentMine","postId":"` + head + `","content":"Unit cost, or total?"}}]}`
	if bodies := batchBodies(srv); len(bodies) != 1 || bodies[0] != want {
		t.Errorf("sent %v\nwant %s", bodies, want)
	}
	if res.PostID != head || res.Thread.Posts[0].Text != "Unit cost, or total?" {
		t.Errorf("result = %+v", res)
	}
	same, err := manage(svc, service.ManageCommentRequest{Action: "edit", CommentID: sheetstest.FixtureCommentID,
		Text: "Unit cost, or total?"})
	if err != nil || same.Changed || len(batchBodies(srv)) != 1 {
		t.Errorf("an edit to the same text: %v, changed %v, %d batches", err, same.Changed, len(batchBodies(srv)))
	}
}

// post_id reaches a reply, and the edit leaves the first comment alone.
func TestEditReachesAReplyByItsID(t *testing.T) {
	srv, svc := standard(t)
	mine, err := manage(svc, service.ManageCommentRequest{Action: "reply", CommentID: sheetstest.FixtureCommentID, Text: "Unit cost."})
	if err != nil {
		t.Fatal(err)
	}
	res, err := manage(svc, service.ManageCommentRequest{Action: "edit", CommentID: sheetstest.FixtureCommentID,
		PostID: mine.PostID, Text: "Edited: unit cost."})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	want := `{"requests":[{"updateCommentPost":{"commentId":"AAAAcommentMine","postId":"` + mine.PostID + `","content":"Edited: unit cost."}}]}`
	if bodies := batchBodies(srv); bodies[len(bodies)-1] != want {
		t.Errorf("sent %s\nwant %s", bodies[len(bodies)-1], want)
	}
	got := srv.Doc(sheetstest.FixtureID).Thread(sheetstest.FixtureCommentID)
	if got.HeadPost.Content != "Is this the unit cost or the total?" || got.Replies[1].Content != "Edited: unit cost." ||
		res.Thread.Posts[0].Text != "Is this the unit cost or the total?" || res.Thread.Posts[2].Text != "Edited: unit cost." {
		t.Errorf("head %q, reply %q; reported %+v", got.HeadPost.Content, got.Replies[1].Content, res.Thread.Posts)
	}
}

// A 200 whose state says no comment was saved is not a saved comment.
func TestACommentGoogleDidNotSaveIsAnError(t *testing.T) {
	srv, svc := standard(t)
	srv.Doc(sheetstest.FixtureID).CommentsFail = true
	_, err := manage(svc, service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "D1", Text: "x"})
	if classOf(err) != "unavailable" || !strings.Contains(err.Error(), "was not saved") {
		t.Errorf("err = %v", err)
	}
}

func deleteComment(svc *service.Service, req service.DeleteCommentRequest) (*service.DeleteCommentResult, error) {
	req.Spreadsheet = sheetstest.FixtureID
	return svc.DeleteComment(accepted(), req)
}

func TestDeleteCommentNeedsConfirm(t *testing.T) {
	srv, svc := destructive(t)
	_, err := deleteComment(svc, service.DeleteCommentRequest{CommentID: sheetstest.FixtureCommentID})
	if classOf(err) != "blocked" || !strings.Contains(err.Error(), "the comment thread on 'Vandel'!B3 and its 1 reply") {
		t.Errorf("err = %v", err)
	}
	if n := len(batchBodies(srv)); n != 0 {
		t.Fatalf("an unconfirmed delete sent %d batches", n)
	}
	res, err := deleteComment(svc, service.DeleteCommentRequest{CommentID: sheetstest.FixtureCommentID, Confirm: true})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	want := `{"requests":[{"deleteComment":{"commentId":"AAAAcommentMine"}}]}`
	if bodies := batchBodies(srv); len(bodies) != 1 || bodies[0] != want {
		t.Errorf("sent %v\nwant %s", bodies, want)
	}
	if !res.Deleted || res.Gone || res.Thread.Cell != "B3" || srv.Doc(sheetstest.FixtureID).Thread(sheetstest.FixtureCommentID) != nil {
		t.Errorf("result = %+v", res)
	}
}

func TestDeleteCommentDryRunSendsNothing(t *testing.T) {
	srv, svc := destructive(t)
	res, err := deleteComment(svc, service.DeleteCommentRequest{CommentID: sheetstest.FixtureCommentID, DryRun: true})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !res.DryRun || res.Deleted || len(batchBodies(srv)) != 0 ||
		!strings.Contains(res.Summary, "Would delete the comment thread on 'Vandel'!B3 and its 1 reply.") {
		t.Errorf("result = %+v\n%s", res, res.Summary)
	}
}

func TestDeleteAReplyLeavesTheThread(t *testing.T) {
	srv, svc := destructive(t)
	mine, err := manage(svc, service.ManageCommentRequest{Action: "reply", CommentID: sheetstest.FixtureCommentID, Text: "Ignore this."})
	if err != nil {
		t.Fatal(err)
	}
	res, err := deleteComment(svc, service.DeleteCommentRequest{CommentID: sheetstest.FixtureCommentID, PostID: mine.PostID, Confirm: true})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !res.Deleted || res.PostID != mine.PostID || !strings.Contains(res.Summary, "Deleted the reply "+mine.PostID) {
		t.Errorf("result = %+v", res)
	}
	got := srv.Doc(sheetstest.FixtureID).Thread(sheetstest.FixtureCommentID)
	if got == nil || len(got.Replies) != 1 || got.Replies[0].PostID != sheetstest.FixtureReplyID {
		t.Errorf("the thread after the reply went: %+v", got)
	}
}

// Google's own refusals, said first, with nothing sent.
func TestDeleteCommentRefusesWhatGoogleWould(t *testing.T) {
	for _, tc := range []struct {
		name  string
		req   service.DeleteCommentRequest
		class string
		says  string
	}{
		{"somebody else's thread", service.DeleteCommentRequest{CommentID: sheetstest.FixtureOtherCommentID, Confirm: true},
			"forbidden", "John Doe wrote it"},
		{"somebody else's reply", service.DeleteCommentRequest{CommentID: sheetstest.FixtureCommentID,
			PostID: sheetstest.FixtureReplyID, Confirm: true}, "forbidden", "John Doe wrote it"},
		{"an unknown reply", service.DeleteCommentRequest{CommentID: sheetstest.FixtureCommentID, PostID: "AAAAnopost",
			Confirm: true}, "not_found", "has no reply"},
		{"an unknown thread", service.DeleteCommentRequest{CommentID: "AAAAnothread", Confirm: true}, "not_found", "no comment thread"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, svc := destructive(t)
			_, err := deleteComment(svc, tc.req)
			if classOf(err) != tc.class || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("err = %v, want [%s] saying %q", err, tc.class, tc.says)
			}
			if n := len(batchBodies(srv)); n != 0 {
				t.Errorf("sent %d batches", n)
			}
		})
	}
}

func TestDeleteCommentRefusesTheFirstPostByID(t *testing.T) {
	srv, svc := destructive(t)
	head := srv.Doc(sheetstest.FixtureID).Thread(sheetstest.FixtureCommentID).HeadPost.PostID
	_, err := deleteComment(svc, service.DeleteCommentRequest{CommentID: sheetstest.FixtureCommentID, PostID: head, Confirm: true})
	if classOf(err) != "invalid" || !strings.Contains(err.Error(), "leave post_id empty") {
		t.Errorf("err = %v", err)
	}
}

// A delete Google answers with 404 found it gone, which is what was
// asked for (spike R).
func TestADeleteFoundGoneIsDone(t *testing.T) {
	srv, svc := destructive(t)
	srv.FailOnce("spreadsheets.batchUpdate", sheetstest.Failure{Status: http.StatusNotFound})
	res, err := deleteComment(svc, service.DeleteCommentRequest{CommentID: sheetstest.FixtureCommentID, Confirm: true})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !res.Deleted || !res.Gone || !strings.Contains(res.Summary, "was already gone") {
		t.Errorf("result = %+v", res)
	}
}

// askCounter counts the questions it is asked and accepts each.
type askCounter struct{ n *int }

func (a askCounter) Ask(_ context.Context, build service.Build) error {
	*a.n++
	_, err := build()
	return err
}

// A column deleted under a thread leaves it on no cell, as a row does.
// Column A is the case that needs care: its collapsed range has no
// column index at all in Google's JSON, and must not read as a row.
func TestACommentOutlivesItsColumn(t *testing.T) {
	srv, svc := standard(t)
	added, err := manage(svc, service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "A5", Text: "Row five."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Client().BatchUpdate(context.Background(), sheetstest.FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{{DeleteDimension: &gsheets.DeleteDimensionRequest{Range: &gsheets.DimensionRange{
			SheetID: 0, Dimension: gsheets.DimensionColumns, StartIndex: 0, EndIndex: 1}}}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, th := range readComments(t, svc, service.ReadCommentsRequest{}).Threads {
		switch th.CommentID {
		case added.Thread.CommentID:
			if th.Cell != "" || !th.CellDeleted {
				t.Errorf("after column A went, its thread = %+v", th)
			}
		case sheetstest.FixtureCommentID:
			if th.Cell != "A3" {
				t.Errorf("the thread on B3 is on %q after column A went, want A3", th.Cell)
			}
		}
	}
}

// A comment on A1 has every start index left out of Google's JSON, and
// is still A1.
func TestACommentOnTheFirstCellIsA1(t *testing.T) {
	_, svc := standard(t)
	if _, err := manage(svc, service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "A1", Text: "Corner."}); err != nil {
		t.Fatal(err)
	}
	if th := readComments(t, svc, service.ReadCommentsRequest{}).Threads[0]; th.Cell != "A1" || th.CellDeleted {
		t.Errorf("thread = %+v", th)
	}
}

// A reply that reassigns emails the new assignee, and the latest
// assignment is the thread's.
func TestAReplyReassigns(t *testing.T) {
	_, svc := standard(t)
	added, err := manage(svc, service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "D1", Text: "Check.",
		Assignee: "jane.doe@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := manage(svc, service.ManageCommentRequest{Action: "reply", CommentID: added.Thread.CommentID, Text: "Over to you.",
		Assignee: "john.doe@example.com"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if res.Emailed != "john.doe@example.com" || res.Thread.Assignee != "john.doe@example.com" {
		t.Errorf("emailed %q, thread assignee %q", res.Emailed, res.Thread.Assignee)
	}
	for _, th := range readComments(t, svc, service.ReadCommentsRequest{}).Threads {
		if th.CommentID == added.Thread.CommentID && th.Assignee != "john.doe@example.com" {
			t.Errorf("the read says the thread is assigned to %q", th.Assignee)
		}
	}
}

// An address in the text may notify that person, and the result says so.
func TestTheTextsAddressesAreReported(t *testing.T) {
	_, svc := standard(t)
	res, err := manage(svc, service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "D1",
		Text: "Ask +jane.doe@example.com and jane.doe@example.com.", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Mentioned, ",") != "jane.doe@example.com" ||
		!strings.Contains(res.Summary, "The text names jane.doe@example.com; Google treats it as the Sheets editor does") {
		t.Errorf("mentioned %v\n%s", res.Mentioned, res.Summary)
	}
}

// A reply that changed the thread's status or assignee, and a post that
// only resolved, are refused before anything is sent or asked.
func TestRepliesGoogleKeepsAreRefused(t *testing.T) {
	srv, svc := destructive(t)
	added, err := manage(svc, service.ManageCommentRequest{Action: "add", Sheet: "Vandel", Cell: "D1", Text: "Check.",
		Assignee: "jane.doe@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	id := added.Thread.CommentID
	reassign, err := manage(svc, service.ManageCommentRequest{Action: "reply", CommentID: id, Text: "x", Assignee: "john.doe@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	resolve, err := manage(svc, service.ManageCommentRequest{Action: "resolve", CommentID: id})
	if err != nil {
		t.Fatal(err)
	}
	before := len(batchBodies(srv))
	asked := 0
	ctx := service.WithAsker(context.Background(), askCounter{&asked})
	for _, tc := range []struct {
		post string
		says string
	}{
		{resolve.PostID, "resolved the thread, and Google does not delete"},
		{reassign.PostID, "carries an assignee"},
	} {
		_, err := svc.DeleteComment(ctx, service.DeleteCommentRequest{Spreadsheet: sheetstest.FixtureID, CommentID: id,
			PostID: tc.post, Confirm: true})
		if classOf(err) != "invalid" || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("err = %v, want one saying %q", err, tc.says)
		}
	}
	_, err = manage(svc, service.ManageCommentRequest{Action: "edit", CommentID: id, PostID: resolve.PostID, Text: "x"})
	if classOf(err) != "invalid" || !strings.Contains(err.Error(), "has no text to edit") {
		t.Errorf("editing a resolve: %v", err)
	}
	if n := len(batchBodies(srv)) - before; n != 0 || asked != 0 {
		t.Errorf("%d batches sent and %d questions asked for refusals", n, asked)
	}
}

// Google's refusals come before the question, so the person is never
// asked to confirm a delete Google would refuse.
func TestARefusedDeleteAsksNobody(t *testing.T) {
	_, svc := destructive(t)
	asked := 0
	ctx := service.WithAsker(context.Background(), askCounter{&asked})
	_, err := svc.DeleteComment(ctx, service.DeleteCommentRequest{Spreadsheet: sheetstest.FixtureID,
		CommentID: sheetstest.FixtureOtherCommentID, Confirm: true})
	if classOf(err) != "forbidden" || asked != 0 {
		t.Errorf("err = %v, asked %d", err, asked)
	}
	if _, err := svc.DeleteComment(ctx, service.DeleteCommentRequest{Spreadsheet: sheetstest.FixtureID,
		CommentID: sheetstest.FixtureCommentID, Confirm: true}); err != nil || asked != 1 {
		t.Errorf("a delete Google allows: %v, asked %d", err, asked)
	}
}

// The budget cuts the listing between threads, and the structured half
// holds the same threads the text shows.
func TestReadCommentsKeepsToTheCharacterBudget(t *testing.T) {
	srv, svc := standard(t)
	doc := srv.Doc(sheetstest.FixtureID)
	for i := range 40 {
		doc.AddComment(doc.Sheets[1], i, 0, strings.Repeat("Quorbin ", 120), true)
	}
	res := readComments(t, svc, service.ReadCommentsRequest{Sheet: sheetstest.SecondSheet})
	if res.Open != 40 || res.Omitted == 0 || len(res.Threads)+res.Omitted != 40 || len(res.Summary) > 20000 ||
		!strings.Contains(res.Summary, "more not shown, to stay inside the character budget") {
		t.Errorf("open %d, listed %d, omitted %d, %d characters", res.Open, len(res.Threads), res.Omitted, len(res.Summary))
	}
}

// One thread alone past the budget is shown with its posts cut short.
func TestOneLongThreadIsCutShort(t *testing.T) {
	srv, svc := standard(t)
	doc := srv.Doc(sheetstest.FixtureID)
	th := doc.AddComment(doc.Sheets[1], 0, 0, "Start.", true)
	for range 30 {
		doc.Reply(th, strings.Repeat("Nardle ", 290), false)
	}
	res := readComments(t, svc, service.ReadCommentsRequest{Sheet: sheetstest.SecondSheet})
	if len(res.Threads) != 1 || len(res.Summary) > 20000 ||
		!strings.Contains(res.Summary, "This thread alone is past the character budget") {
		t.Errorf("%d threads, %d characters", len(res.Threads), len(res.Summary))
	}
}

// A deleted post is not listed, and neither is a thread whose first
// comment was deleted.
func TestDeletedPostsAreNotListed(t *testing.T) {
	srv, svc := standard(t)
	doc := srv.Doc(sheetstest.FixtureID)
	doc.Thread(sheetstest.FixtureCommentID).Replies[0].Deleted = true
	doc.Thread(sheetstest.FixtureOtherCommentID).HeadPost.Deleted = true
	res := readComments(t, svc, service.ReadCommentsRequest{IncludeResolved: true})
	if len(res.Threads) != 1 || len(res.Threads[0].Posts) != 1 {
		t.Errorf("threads = %+v", res.Threads)
	}
}

// The scope is matched by sheet id, so a sheet renamed since the card
// was cached still finds its threads.
func TestAScopeSurvivesARename(t *testing.T) {
	srv, svc := standard(t)
	readComments(t, svc, service.ReadCommentsRequest{Sheet: "Vandel"})
	srv.Doc(sheetstest.FixtureID).Sheets[0].Props.Title = "Vandel renamed"
	if res := readComments(t, svc, service.ReadCommentsRequest{Sheet: "Vandel"}); len(res.Threads) != 1 {
		t.Errorf("after a rename, %d threads", len(res.Threads))
	}
}

// A deleted cell's thread is counted as left out only when the read
// would list it.
func TestALeftOutResolvedThreadIsNotCounted(t *testing.T) {
	srv, svc := standard(t)
	if _, err := srv.Client().BatchUpdate(context.Background(), sheetstest.FixtureID, &gsheets.BatchUpdateSpreadsheetRequest{
		Requests: []*gsheets.Request{{DeleteDimension: &gsheets.DeleteDimensionRequest{Range: &gsheets.DimensionRange{
			SheetID: 0, Dimension: gsheets.DimensionRows, StartIndex: 4, EndIndex: 5}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if res := readComments(t, svc, service.ReadCommentsRequest{Sheet: "Vandel", Range: "A1:Z50"}); strings.Contains(res.Summary, "Left out") {
		t.Errorf("a resolved thread was counted as left out:\n%s", res.Summary)
	}
	if res := readComments(t, svc, service.ReadCommentsRequest{Sheet: "Vandel", Range: "A1:Z50", IncludeResolved: true}); !strings.Contains(res.Summary, "Left out: 1 thread") {
		t.Errorf("with include_resolved:\n%s", res.Summary)
	}
}

// A scope the login never granted is a re-login, not a viewer's limit.
func TestAMissingScopeSaysToLogIn(t *testing.T) {
	srv, svc := standard(t)
	srv.FailOnce("spreadsheets.get", sheetstest.Failure{Status: http.StatusForbidden,
		Body: `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Request had insufficient authentication scopes."}}`})
	_, err := svc.ReadComments(context.Background(), service.ReadCommentsRequest{Spreadsheet: sheetstest.FixtureID})
	if classOf(err) != "forbidden" || !strings.Contains(err.Error(), "login") || strings.Contains(err.Error(), "may comment") {
		t.Errorf("err = %v", err)
	}
}
