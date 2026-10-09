package plan

import (
	"github.com/mmedum/google-sheets-mcp/v3/internal/a1"
	"github.com/mmedum/google-sheets-mcp/v3/internal/gsheets"
)

// InsertComment starts a comment thread on one cell. cell is the cell as
// A1 reads it, on the sheet sheetID names; assignee may be empty.
func InsertComment(sheetID int, cell a1.Rect, text, assignee string) *gsheets.Request {
	return &gsheets.Request{InsertComment: &gsheets.InsertCommentRequest{
		Coordinate: &gsheets.GridCoordinate{SheetID: sheetID, RowIndex: a1.ZeroBased(cell.FirstRow),
			ColumnIndex: a1.ZeroBased(cell.FirstCol)},
		Content: text, AssigneeEmailAddress: assignee,
	}}
}

// AddCommentReply adds a post to a thread: a reply, or a resolve or
// reopen when action is gsheets.CommentResolve or gsheets.CommentReopen.
func AddCommentReply(commentID, text, action, assignee string) *gsheets.Request {
	return &gsheets.Request{AddCommentReply: &gsheets.AddCommentReplyRequest{
		CommentID: commentID,
		Post:      &gsheets.Post{Content: text, CommentAction: action, AssigneeEmail: assignee},
	}}
}

// UpdateCommentPost rewrites one post's text.
func UpdateCommentPost(commentID, postID, text string) *gsheets.Request {
	return &gsheets.Request{UpdateCommentPost: &gsheets.UpdateCommentPostRequest{
		CommentID: commentID, PostID: postID, Content: text,
	}}
}

// DeleteComment deletes a whole thread, or one reply when postID is set.
func DeleteComment(commentID, postID string) *gsheets.Request {
	if postID != "" {
		return &gsheets.Request{DeleteCommentReply: &gsheets.DeleteCommentReplyRequest{CommentID: commentID, PostID: postID}}
	}
	return &gsheets.Request{DeleteComment: &gsheets.DeleteCommentRequest{CommentID: commentID}}
}
