package service

import (
	"context"
	"errors"
	"strings"

	"github.com/mmedum/google-sheets-mcp/v3/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
	"github.com/mmedum/google-sheets-mcp/v3/internal/plan"
	"github.com/mmedum/google-sheets-mcp/v3/internal/render"
)

// ManageCommentRequest is what manage_cell_comment asks for.
type ManageCommentRequest struct {
	Spreadsheet string
	Action      string
	Sheet       string
	Cell        string
	CommentID   string
	PostID      string
	Text        string
	Assignee    string
	DryRun      bool
}

// ManageCommentResult is its answer.
type ManageCommentResult struct {
	Summary     string         `json:"summary"`
	Spreadsheet string         `json:"spreadsheet" jsonschema:"the spreadsheet id"`
	Action      string         `json:"action" jsonschema:"the action taken"`
	Thread      *CommentRecord `json:"thread,omitempty" jsonschema:"the thread after the call, or as it would be after it"`
	PostID      string         `json:"post_id,omitempty" jsonschema:"the post this call wrote or edited"`
	// Emailed and Mentioned are who a comment reaches outside the
	// spreadsheet, which is why the tool is annotated open-world.
	Emailed   string   `json:"emailed,omitempty" jsonschema:"the address Google emails because this call assigns the thread to it"`
	Mentioned []string `json:"mentioned,omitempty" jsonschema:"addresses the text names; Google treats the text as the Sheets editor does, so it may notify them"`
	Changed   bool     `json:"changed" jsonschema:"false when nothing needed doing, such as resolving a thread already resolved"`
	DryRun    bool     `json:"dry_run,omitempty" jsonschema:"true when nothing was sent"`
}

// Render is the text half.
func (r ManageCommentResult) Render() string { return r.Summary }

// ManageComment answers manage_cell_comment.
func (s *Service) ManageComment(ctx context.Context, req ManageCommentRequest) (*ManageCommentResult, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	switch action {
	case CommentAdd, CommentReply, CommentEdit, CommentResolve, CommentReopen:
	default:
		return nil, Errorf("invalid", "action must be add, reply, edit, resolve or reopen")
	}
	if err := checkAssignee(req.Assignee); err != nil {
		return nil, err
	}
	if req.Assignee != "" && action != CommentAdd && action != CommentReply {
		return nil, Errorf("invalid", "assignee goes with add or reply; %s cannot assign a thread", action)
	}
	ref, err := s.Resolve(ctx, req.Spreadsheet)
	if err != nil {
		return nil, err
	}
	res := &ManageCommentResult{Spreadsheet: ref.ID, Action: action, DryRun: req.DryRun, Mentioned: mentioned(req.Text)}
	if action == CommentAdd {
		return s.addComment(ctx, ref, req, res)
	}
	rec, err := s.findThread(ctx, ref.ID, req.CommentID)
	if err != nil {
		return nil, err
	}
	switch action {
	case CommentEdit:
		return s.editComment(ctx, ref.ID, req, rec, res)
	case CommentResolve, CommentReopen:
		return s.setCommentStatus(ctx, ref.ID, req.Text, rec, res)
	}
	return s.replyComment(ctx, ref.ID, req, rec, res)
}

// notified is who a result says the write reaches.
func (r *ManageCommentResult) notified() render.Notified {
	return render.Notified{Assignee: r.Emailed, Mentioned: r.Mentioned}
}

// addComment starts a thread on one cell.
func (s *Service) addComment(ctx context.Context, ref Reference, req ManageCommentRequest, res *ManageCommentResult) (*ManageCommentResult, error) {
	if err := checkText(req.Text, true); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Cell) == "" {
		return nil, Errorf("invalid", "add needs the cell to comment on, such as B4, and the sheet it is on")
	}
	at, err := s.ResolveRange(ctx, ref, req.Sheet, req.Cell)
	if err != nil {
		return nil, err
	}
	if !at.Rect.OneCell() {
		return nil, Errorf("invalid", "a comment sits on one cell, and %s is a range; name the one cell, such as B4",
			a1.FormatRect(at.Rect))
	}
	if g := at.Props.GridProperties; g != nil && (at.Rect.FirstRow > g.RowCount || at.Rect.FirstCol > g.ColumnCount) {
		return nil, Errorf("invalid", "%s is outside the sheet %q, which has %d rows and %d columns",
			a1.FormatRect(at.Rect), at.Props.Title, g.RowCount, g.ColumnCount)
	}
	cell := a1.FormatRect(at.Rect)
	rec := &CommentRecord{Sheet: at.Props.Title, Cell: cell, Status: statusOpen, Assignee: req.Assignee,
		Posts: []PostRecord{{Text: req.Text, Mine: true, Assignee: req.Assignee}}}
	res.Thread, res.Emailed, res.Changed = rec, req.Assignee, true
	if req.DryRun {
		res.Summary = render.CommentPreview(CommentAdd, rec.view(), res.notified())
		return res, nil
	}
	reply, err := s.commentBatch(ctx, ref.ID, plan.InsertComment(at.Props.SheetID, at.Rect, req.Text, req.Assignee))
	if err != nil {
		return nil, err
	}
	if reply.InsertComment == nil || reply.InsertComment.CommentThread == nil || reply.InsertComment.CommentThread.HeadPost == nil {
		return nil, Errorf("ambiguous_outcome", "Google answered the comment without the thread it made, so whether it "+
			"was saved is unknown. read_cell_comments on %s shows whether it is there; do not add it again before looking", cell)
	}
	made := threadRecord(reply.InsertComment.CommentThread)
	made.Sheet, made.Cell = at.Props.Title, cell
	res.Thread, res.PostID = &made, made.Posts[0].PostID
	res.Summary = render.CommentDone(CommentAdd, made.view(), res.notified())
	return res, nil
}

// replyComment adds a reply, which may reassign the thread.
func (s *Service) replyComment(ctx context.Context, id string, req ManageCommentRequest, rec CommentRecord,
	res *ManageCommentResult) (*ManageCommentResult, error) {
	if err := checkText(req.Text, true); err != nil {
		return nil, err
	}
	// Google refuses to reassign a thread whose first comment has no
	// assignee (spike R); saying so here names the way that works.
	if req.Assignee != "" && rec.Posts[0].Assignee == "" {
		return nil, Errorf("invalid", "this thread was never assigned, and Google assigns only from the first comment. "+
			"Reply without an assignee, or add a new comment with one")
	}
	post := PostRecord{Text: req.Text, Mine: true, Assignee: req.Assignee}
	return s.post(ctx, id, rec, res, post, plan.AddCommentReply(rec.CommentID, req.Text, "", req.Assignee))
}

// setCommentStatus resolves or reopens a thread. A thread already there
// is left alone: Google would post a second resolve (spike R).
func (s *Service) setCommentStatus(ctx context.Context, id, text string, rec CommentRecord,
	res *ManageCommentResult) (*ManageCommentResult, error) {
	if err := checkText(text, false); err != nil {
		return nil, err
	}
	want, action := statusResolved, gsheets.CommentResolve
	if res.Action == CommentReopen {
		want, action = statusOpen, gsheets.CommentReopen
	}
	if rec.Status == want {
		res.Thread, res.Changed, res.Mentioned = &rec, false, nil
		res.Summary = render.CommentUnchanged(rec.view())
		return res, nil
	}
	post := PostRecord{Text: text, Mine: true, Action: res.Action}
	return s.post(ctx, id, rec, res, post, plan.AddCommentReply(rec.CommentID, text, action, ""))
}

// post sends one addCommentReply and reports the thread with it.
func (s *Service) post(ctx context.Context, id string, rec CommentRecord, res *ManageCommentResult, preview PostRecord,
	wire *gsheets.Request) (*ManageCommentResult, error) {
	after := rec
	after.Posts = append(append([]PostRecord(nil), rec.Posts...), preview)
	applyPost(&after, preview)
	res.Thread, res.Emailed, res.Changed = &after, preview.Assignee, true
	if res.DryRun {
		res.Summary = render.CommentPreview(res.Action, after.view(), res.notified())
		return res, nil
	}
	reply, err := s.commentBatch(ctx, id, wire)
	if err != nil {
		return nil, err
	}
	if reply.AddCommentReply == nil || reply.AddCommentReply.Post == nil {
		return nil, Errorf("ambiguous_outcome", "Google answered the reply without the post it made, so whether it was "+
			"saved is unknown. read_cell_comments shows thread %s; do not post again before looking", rec.CommentID)
	}
	posted := postRecord(reply.AddCommentReply.Post)
	after.Posts[len(after.Posts)-1] = posted
	res.PostID = posted.PostID
	res.Summary = render.CommentDone(res.Action, after.view(), res.notified())
	return res, nil
}

// applyPost is what a post does to its thread's status and assignee.
func applyPost(rec *CommentRecord, p PostRecord) {
	switch p.Action {
	case CommentResolve:
		rec.Status = statusResolved
	case CommentReopen:
		rec.Status = statusOpen
	}
	if p.Assignee != "" {
		rec.Assignee = p.Assignee
	}
}

// editComment rewrites one post, the comment itself when post_id is
// empty.
func (s *Service) editComment(ctx context.Context, id string, req ManageCommentRequest, rec CommentRecord,
	res *ManageCommentResult) (*ManageCommentResult, error) {
	if err := checkText(req.Text, true); err != nil {
		return nil, err
	}
	postID := strings.TrimSpace(req.PostID)
	if postID == "" {
		postID = rec.Posts[0].PostID
	}
	i := rec.postIndex(postID)
	// Only the author may edit a post (the discovery document on
	// updateCommentPost).
	switch {
	case i < 0:
		return nil, Errorf("not_found", "thread %s has no post %q; read_cell_comments lists each post's id", rec.CommentID, postID)
	case !rec.Posts[i].Mine:
		return nil, Errorf("forbidden", "Google lets only a post's author edit it, and %s wrote this one",
			render.AuthorName(rec.Posts[i].Author))
	case rec.Posts[i].Text == "" && rec.Posts[i].Action != "":
		return nil, Errorf("invalid", "that post only %s the thread and has no text to edit", render.ActionPast(rec.Posts[i].Action))
	}
	after := rec
	after.Posts = append([]PostRecord(nil), rec.Posts...)
	after.Posts[i].Text = req.Text
	res.Thread, res.PostID, res.Changed = &after, postID, rec.Posts[i].Text != req.Text
	if !res.Changed {
		res.Mentioned = nil
		res.Summary = render.CommentUnchanged(after.view())
		return res, nil
	}
	if req.DryRun {
		res.Summary = render.CommentPreview(CommentEdit, after.view(), res.notified())
		return res, nil
	}
	if _, err := s.commentBatch(ctx, id, plan.UpdateCommentPost(rec.CommentID, postID, req.Text)); err != nil {
		return nil, err
	}
	res.Summary = render.CommentDone(CommentEdit, after.view(), res.notified())
	return res, nil
}

// DeleteCommentRequest is what delete_cell_comment asks for.
type DeleteCommentRequest struct {
	Spreadsheet string
	CommentID   string
	PostID      string
	Confirm     bool
	DryRun      bool
}

// DeleteCommentResult is its answer.
type DeleteCommentResult struct {
	Summary     string         `json:"summary"`
	Spreadsheet string         `json:"spreadsheet" jsonschema:"the spreadsheet id"`
	CommentID   string         `json:"comment_id" jsonschema:"the thread"`
	PostID      string         `json:"post_id,omitempty" jsonschema:"the reply deleted, when only a reply was"`
	Thread      *CommentRecord `json:"thread" jsonschema:"the thread as it was before the delete"`
	Deleted     bool           `json:"deleted" jsonschema:"true when it is gone now"`
	Gone        bool           `json:"gone,omitempty" jsonschema:"Google answered that it was already gone"`
	DryRun      bool           `json:"dry_run,omitempty" jsonschema:"true when nothing was sent"`
}

// Render is the text half.
func (r DeleteCommentResult) Render() string { return r.Summary }

// DeleteComment answers delete_cell_comment: a whole thread, or one
// reply. Registered only with GSHEETS_ENABLE_DESTRUCTIVE=true, it needs
// confirm and asks the person, because Sheets cannot bring a deleted
// comment back. Resolving keeps it, and is usually what is wanted.
func (s *Service) DeleteComment(ctx context.Context, req DeleteCommentRequest) (*DeleteCommentResult, error) {
	ref, err := s.Resolve(ctx, req.Spreadsheet)
	if err != nil {
		return nil, err
	}
	rec, err := s.findThread(ctx, ref.ID, req.CommentID)
	if err != nil {
		return nil, err
	}
	res := &DeleteCommentResult{Spreadsheet: ref.ID, CommentID: rec.CommentID, Thread: &rec}
	postID := strings.TrimSpace(req.PostID)
	target := &rec.Posts[0]
	switch {
	case postID == rec.Posts[0].PostID:
		return nil, Errorf("invalid", "%s is the comment that starts the thread; deleting it deletes the thread, so "+
			"leave post_id empty to do that", postID)
	case postID != "":
		i := rec.postIndex(postID)
		if i < 0 {
			return nil, Errorf("not_found", "thread %s has no reply %q; read_cell_comments lists each post's id", rec.CommentID, postID)
		}
		target, res.PostID = &rec.Posts[i], postID
	}
	// Google's own refusals, said before the person is asked, and with
	// the way out: only the author deletes, and a reply that changed the
	// thread's status or assignee stays (the discovery document on
	// deleteComment and deleteCommentReply).
	switch {
	case !target.Mine:
		return nil, Errorf("forbidden", "Google lets only the author delete this, and %s wrote it. Resolving the thread "+
			"with manage_cell_comment keeps it out of the way instead", render.AuthorName(target.Author))
	case postID != "" && target.Action != "":
		return nil, Errorf("invalid", "that reply %s the thread, and Google does not delete a reply that changed the "+
			"thread's status", render.ActionPast(target.Action))
	case postID != "" && target.Assignee != "":
		return nil, Errorf("invalid", "that reply assigned the thread, and Google does not delete a reply that carries an assignee")
	}
	view := rec.view()
	if req.DryRun {
		res.DryRun = true
		res.Summary = render.CommentDeletePreview(view, postID)
		return res, nil
	}
	if !req.Confirm {
		return nil, Errorf("blocked", "deleting %s cannot be undone. Pass confirm to go ahead, or resolve the thread with "+
			"manage_cell_comment, which keeps it", render.CommentTarget(view, postID))
	}
	sp, err := s.card(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	if err := ask(ctx, func() (render.Question, error) {
		return render.AskDeleteComment(ref.ID, titleOf(sp), view, postID), nil
	}); err != nil {
		return nil, err
	}
	if _, err := s.commentBatch(ctx, ref.ID, plan.DeleteComment(rec.CommentID, postID)); err != nil {
		// A repeat finds it gone (spike R): what was asked for is true.
		var se *Error
		if !errors.As(err, &se) || se.Class != "not_found" {
			return nil, err
		}
		res.Gone = true
	}
	res.Deleted = true
	res.Summary = render.CommentDeleted(view, postID, res.Gone)
	return res, nil
}
