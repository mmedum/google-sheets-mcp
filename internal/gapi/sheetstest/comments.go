package sheetstest

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
)

// The fake's cell comments, written from spike R: what each request
// answers, live, when the thread, the post or the place is wrong, and
// where an anchor goes when its row moves or is deleted.

// AddComment puts a thread on a zero-based cell of sh, as a fixture.
// mine says whether the signed-in account wrote it, which decides what
// it may edit or delete.
func (d *Doc) AddComment(sh *Sheet, row, col int, content string, mine bool) *gsheets.CommentThread {
	t := newThread(d, sh, row, col, content, "")
	t.HeadPost.Author = author(mine)
	return t
}

// Reply adds a reply to a fixture thread.
func (d *Doc) Reply(t *gsheets.CommentThread, content string, mine bool) *gsheets.Post {
	p := d.post(content)
	p.Author = author(mine)
	t.Replies = append(t.Replies, p)
	return p
}

// Thread finds a thread by id, or nil.
func (d *Doc) Thread(id string) *gsheets.CommentThread {
	for _, t := range d.Comments {
		if t.CommentID == id {
			return t
		}
	}
	return nil
}

func author(mine bool) *gsheets.PostAuthor {
	if mine {
		return &gsheets.PostAuthor{DisplayName: "Jane Doe", Me: true}
	}
	return &gsheets.PostAuthor{DisplayName: "John Doe"}
}

// post is a new post, with an id and a time from the doc's counter.
func (d *Doc) post(content string) *gsheets.Post {
	at := d.stamp(9)
	return &gsheets.Post{PostID: fmt.Sprintf("AAAApost%d", d.commentSeq), Content: content,
		CommentAction: "NO_COMMENT_ACTION_CHANGE", CreateTime: at, UpdateTime: at}
}

// stamp advances the doc's counter and is a time in that hour of the
// fixture's day, one second per tick.
func (d *Doc) stamp(hour int) string {
	d.commentSeq++
	return fmt.Sprintf("2026-10-01T%02d:%02d:%02d.000Z", hour, d.commentSeq/60, d.commentSeq%60)
}

// maxCommentBytes is Google's limit on a post's text.
const maxCommentBytes = 2048

func newThread(d *Doc, sh *Sheet, row, col int, content, assignee string) *gsheets.CommentThread {
	head := d.post(content)
	head.Author = author(true)
	head.AssigneeEmail = assignee
	quote := ""
	if c := sh.Cells[[2]int{row, col}]; c != nil {
		quote = c.FormattedValue
	}
	t := &gsheets.CommentThread{
		CommentID: fmt.Sprintf("AAAAcomment%d", d.commentSeq), AnchorID: fmt.Sprintf("AAAAanchor%d", d.commentSeq),
		Status: gsheets.CommentOpen, PlainTextQuote: quote, HeadPost: head,
	}
	d.Comments = append(d.Comments, t)
	sh.CommentAnchors = append(sh.CommentAnchors, &gsheets.CommentAnchor{AnchorID: t.AnchorID, Range: &gsheets.GridRange{
		SheetID: sh.Props.SheetID, StartRowIndex: gsheets.Ptr(row), EndRowIndex: gsheets.Ptr(row + 1),
		StartColumnIndex: gsheets.Ptr(col), EndColumnIndex: gsheets.Ptr(col + 1),
	}})
	return t
}

func notFound(format string, args ...any) error {
	return &statusError{status: http.StatusNotFound, code: "NOT_FOUND", message: fmt.Sprintf(format, args...)}
}

// badRequest is Google's 400, in Google's words. A statusError rather
// than a plain error, which the batch handler would also send as a 400,
// because Google's messages are capitalized and end in a period, and a
// plain error may not be.
func badRequest(format string, args ...any) error {
	return &statusError{status: http.StatusBadRequest, code: "INVALID_ARGUMENT", message: fmt.Sprintf(format, args...)}
}

// isComment says a request is one of the five comment members.
func isComment(req *gsheets.Request) bool {
	return req != nil && (req.InsertComment != nil || req.AddCommentReply != nil || req.UpdateCommentPost != nil ||
		req.DeleteComment != nil || req.DeleteCommentReply != nil)
}

// applyComment is the fake's half of the comment union.
func applyComment(d *Doc, req *gsheets.Request) (*gsheets.Reply, bool, error) {
	switch {
	case req.InsertComment != nil:
		reply, err := insertComment(d, req.InsertComment)
		return reply, true, err
	case req.AddCommentReply != nil:
		reply, err := addCommentReply(d, req.AddCommentReply)
		return reply, true, err
	case req.UpdateCommentPost != nil:
		return &gsheets.Reply{}, true, updateCommentPost(d, req.UpdateCommentPost)
	case req.DeleteComment != nil:
		return &gsheets.Reply{}, true, deleteComment(d, req.DeleteComment.CommentID)
	case req.DeleteCommentReply != nil:
		return &gsheets.Reply{}, true, deleteCommentReply(d, req.DeleteCommentReply)
	}
	return nil, false, nil
}

func insertComment(d *Doc, req *gsheets.InsertCommentRequest) (*gsheets.Reply, error) {
	at := req.Coordinate
	if at == nil {
		return nil, badRequest("Invalid requests[0].insertComment: a coordinate is required.")
	}
	sh := d.FindByID(at.SheetID)
	if sh == nil {
		return nil, badRequest("Invalid requests[0].insertComment: No grid with id: %d", at.SheetID)
	}
	g := sh.Props.GridProperties
	if g != nil && at.RowIndex >= g.RowCount {
		return nil, badRequest("Invalid requests[0].insertComment: GridCoordinate.rowIndex[%d] is after last row in grid[%d]",
			at.RowIndex, g.RowCount-1)
	}
	if g != nil && at.ColumnIndex >= g.ColumnCount {
		return nil, badRequest("Invalid requests[0].insertComment: GridCoordinate.columnIndex[%d] is after last column in grid[%d]",
			at.ColumnIndex, g.ColumnCount-1)
	}
	if req.Content == "" {
		return nil, badRequest("Invalid requests[0].insertComment: Insert comment requests must specify non-empty post text.")
	}
	if len(req.Content) > maxCommentBytes {
		return nil, badRequest("Invalid requests[0].insertComment: The content may not exceed %d UTF-8 code units.", maxCommentBytes)
	}
	t := newThread(d, sh, at.RowIndex, at.ColumnIndex, req.Content, req.AssigneeEmailAddress)
	return &gsheets.Reply{InsertComment: &gsheets.InsertCommentReply{CommentThread: cloneThread(t)}}, nil
}

func addCommentReply(d *Doc, req *gsheets.AddCommentReplyRequest) (*gsheets.Reply, error) {
	t := d.Thread(req.CommentID)
	if t == nil {
		return nil, notFound("Comment with ID %s does not exist.", req.CommentID)
	}
	in := req.Post
	if in == nil {
		in = &gsheets.Post{}
	}
	action := in.CommentAction
	if in.Content == "" && action != gsheets.CommentResolve && action != gsheets.CommentReopen {
		return nil, badRequest("Invalid requests[0].addCommentReply: Add comment reply requests must specify a non-empty " +
			"content if comment action is not RESOLVE or REOPEN.")
	}
	if len(in.Content) > maxCommentBytes {
		return nil, badRequest("Invalid requests[0].addCommentReply: The content may not exceed %d UTF-8 code units.", maxCommentBytes)
	}
	// The discovery document: an assignee with a resolve or reopen is a
	// 400.
	if in.AssigneeEmail != "" && (action == gsheets.CommentResolve || action == gsheets.CommentReopen) {
		return nil, badRequest("Invalid requests[0].addCommentReply: An assignee cannot be set when resolving or reopening.")
	}
	if in.AssigneeEmail != "" && (t.HeadPost == nil || t.HeadPost.AssigneeEmail == "") {
		return nil, badRequest("Cannot reassign comment thread with ID %s whose head post does not have an assignment.", t.CommentID)
	}
	p := d.post(in.Content)
	p.Author = author(true)
	p.AssigneeEmail = in.AssigneeEmail
	switch action {
	case gsheets.CommentResolve:
		// A second resolve is accepted and posted again (spike R).
		p.CommentAction = action
		t.Status = gsheets.CommentResolved
	case gsheets.CommentReopen:
		p.CommentAction = action
		t.Status = gsheets.CommentOpen
	}
	t.Replies = append(t.Replies, p)
	copied := *p
	return &gsheets.Reply{AddCommentReply: &gsheets.AddCommentReplyReply{Post: &copied}}, nil
}

func updateCommentPost(d *Doc, req *gsheets.UpdateCommentPostRequest) error {
	t := d.Thread(req.CommentID)
	if t == nil {
		return notFound("Comment with ID %s does not exist.", req.CommentID)
	}
	p := findPost(t, req.PostID)
	if p == nil {
		return notFound("Reply with ID %s does not exist.", req.PostID)
	}
	if req.Content == "" {
		return badRequest("Invalid requests[0].updateCommentPost: Update comment post requests must specify non-empty content.")
	}
	if len(req.Content) > maxCommentBytes {
		return badRequest("Invalid requests[0].updateCommentPost: The content may not exceed %d UTF-8 code units.", maxCommentBytes)
	}
	if p.Author == nil || !p.Author.Me {
		return badRequest("The requesting user is not the author of the post.")
	}
	p.Content = req.Content
	p.UpdateTime = d.stamp(10)
	return nil
}

func deleteComment(d *Doc, id string) error {
	i := slices.IndexFunc(d.Comments, func(t *gsheets.CommentThread) bool { return t.CommentID == id })
	if i < 0 {
		return notFound("Comment thread with ID '%s' was not found.", id)
	}
	t := d.Comments[i]
	if t.HeadPost == nil || t.HeadPost.Author == nil || !t.HeadPost.Author.Me {
		return badRequest("The requesting user is not the author of the head post.")
	}
	d.Comments = slices.Delete(d.Comments, i, i+1)
	for _, sh := range d.Sheets {
		sh.CommentAnchors = slices.DeleteFunc(sh.CommentAnchors, func(a *gsheets.CommentAnchor) bool { return a.AnchorID == t.AnchorID })
	}
	return nil
}

func deleteCommentReply(d *Doc, req *gsheets.DeleteCommentReplyRequest) error {
	t := d.Thread(req.CommentID)
	if t == nil {
		return notFound("Comment with ID %s does not exist.", req.CommentID)
	}
	i := slices.IndexFunc(t.Replies, func(p *gsheets.Post) bool { return p.PostID == req.PostID })
	if i < 0 {
		return notFound("Reply with ID %s does not exist.", req.PostID)
	}
	p := t.Replies[i]
	switch {
	case p.Author == nil || !p.Author.Me:
		return badRequest("The requesting user is not the author of the post.")
	case p.CommentAction == gsheets.CommentResolve || p.CommentAction == gsheets.CommentReopen:
		return badRequest("The reply post contains a comment action.")
	case p.AssigneeEmail != "":
		return badRequest("The reply post contains an assignee.")
	}
	// A deleted reply leaves the Sheets read entirely (spike R).
	t.Replies = slices.Delete(t.Replies, i, i+1)
	return nil
}

func findPost(t *gsheets.CommentThread, id string) *gsheets.Post {
	if t.HeadPost != nil && t.HeadPost.PostID == id {
		return t.HeadPost
	}
	for _, p := range t.Replies {
		if p.PostID == id {
			return p
		}
	}
	return nil
}

// shiftCommentAnchors follows an insert (n > 0) or a delete (n < 0) of a
// band on one axis. An anchor inside a deleted band collapses to an
// empty range at the band's start and its thread stays (spike R).
func shiftCommentAnchors(sh *Sheet, dim string, start, n int) {
	for _, a := range sh.CommentAnchors {
		lo, hi := axisOf(a.Range, dim)
		switch {
		case *lo < start:
		case n < 0 && *lo < start-n:
			*lo, *hi = start, start
		default:
			*lo += n
			*hi += n
		}
	}
}

// moveCommentAnchors follows a moveDimension: an anchor travels with its
// cell.
func moveCommentAnchors(sh *Sheet, dim string, start, end, destination int) {
	move := moveMapping(start, end, destination)
	for _, a := range sh.CommentAnchors {
		lo, hi := axisOf(a.Range, dim)
		if *hi <= *lo {
			continue
		}
		to := move(*lo)
		*lo, *hi = to, to+1
	}
}

// axisOf is the start and end index of an anchor's range on one axis.
// The fake always writes all four.
func axisOf(g *gsheets.GridRange, dim string) (lo, hi *int) {
	if dim == gsheets.DimensionColumns {
		return g.StartColumnIndex, g.EndColumnIndex
	}
	return g.StartRowIndex, g.EndRowIndex
}

// cloneThread copies a thread and its posts, so a reply never shares
// what the fake keeps.
func cloneThread(t *gsheets.CommentThread) *gsheets.CommentThread {
	c := *t
	if t.HeadPost != nil {
		h := *t.HeadPost
		c.HeadPost = &h
	}
	c.Replies = clonePointers(t.Replies)
	return &c
}

// wireAnchors is a sheet's anchors as Google sends them: a zero index is
// left out of the JSON, so it arrives missing rather than zero (spike R).
func wireAnchors(in []*gsheets.CommentAnchor) []*gsheets.CommentAnchor {
	out := cloneAnchors(in)
	for _, a := range out {
		if a == nil || a.Range == nil {
			continue
		}
		for _, p := range []**int{&a.Range.StartRowIndex, &a.Range.EndRowIndex, &a.Range.StartColumnIndex, &a.Range.EndColumnIndex} {
			if *p != nil && **p == 0 {
				*p = nil
			}
		}
	}
	return out
}

// cloneAnchors copies a sheet's anchors and their ranges.
func cloneAnchors(in []*gsheets.CommentAnchor) []*gsheets.CommentAnchor {
	out := clonePointers(in)
	for _, a := range out {
		if a != nil && a.Range != nil {
			a.Range = cloneGridRange(a.Range)
		}
	}
	return out
}

func cloneGridRange(g *gsheets.GridRange) *gsheets.GridRange {
	c := *g
	for _, p := range []**int{&c.StartRowIndex, &c.EndRowIndex, &c.StartColumnIndex, &c.EndColumnIndex} {
		if *p != nil {
			v := **p
			*p = &v
		}
	}
	return &c
}
