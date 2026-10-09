package gsheets

// Cell comments, which Sheets published in 2026-09. A thread is tied to a
// cell through an anchor: the thread names an anchorId, and the sheet
// lists its anchors with the range each covers now. Spike R is what this
// file is built from.

// CommentThread is one comment and its replies.
type CommentThread struct {
	CommentID string `json:"commentId,omitempty"`
	AnchorID  string `json:"anchorId,omitempty"`
	// Status is OPEN or RESOLVED.
	Status string `json:"status,omitempty"`
	// PlainTextQuote is the cell's displayed value when the comment was
	// made, not now.
	PlainTextQuote string  `json:"plainTextQuote,omitempty"`
	HeadPost       *Post   `json:"headPost,omitempty"`
	Replies        []*Post `json:"replies,omitempty"`
}

// Comment thread statuses.
const (
	CommentOpen     = "OPEN"
	CommentResolved = "RESOLVED"
)

// Post is one entry in a thread: the comment itself, or a reply. A reply
// that resolves or reopens the thread carries the action, and may carry
// no text.
type Post struct {
	PostID        string `json:"postId,omitempty"`
	Content       string `json:"content,omitempty"`
	CommentAction string `json:"commentAction,omitempty"`
	// AssigneeEmail is who the post assigns the thread to. On a request
	// it reassigns, which Google refuses on a thread whose head post has
	// no assignee.
	AssigneeEmail string      `json:"assigneeEmail,omitempty"`
	Author        *PostAuthor `json:"author,omitempty"`
	CreateTime    string      `json:"createTime,omitempty"`
	UpdateTime    string      `json:"updateTime,omitempty"`
	Deleted       bool        `json:"deleted,omitempty"`
}

// Comment actions a reply can carry.
const (
	CommentResolve = "RESOLVE"
	CommentReopen  = "REOPEN"
)

// PostAuthor is who wrote a post.
type PostAuthor struct {
	DisplayName string `json:"displayName,omitempty"`
	// Me is whether the signed-in account wrote it. Google lets only the
	// author edit or delete a post.
	Me        bool `json:"me,omitempty"`
	Anonymous bool `json:"anonymous,omitempty"`
}

// CommentAnchor is where a thread sits now. Its range is one cell, and
// it follows the cell as rows and columns move. When the cell's row or
// column is deleted the range collapses to nothing and the thread stays.
type CommentAnchor struct {
	AnchorID string     `json:"anchorId,omitempty"`
	Range    *GridRange `json:"range,omitempty"`
}

// InsertCommentRequest starts a thread on one cell.
type InsertCommentRequest struct {
	Coordinate           *GridCoordinate `json:"coordinate"`
	Content              string          `json:"content"`
	AssigneeEmailAddress string          `json:"assigneeEmailAddress,omitempty"`
}

// AddCommentReplyRequest adds a post to a thread: a reply, a resolve or
// a reopen.
type AddCommentReplyRequest struct {
	CommentID string `json:"commentId"`
	Post      *Post  `json:"post"`
}

// UpdateCommentPostRequest rewrites one post's text.
type UpdateCommentPostRequest struct {
	CommentID string `json:"commentId"`
	PostID    string `json:"postId"`
	Content   string `json:"content"`
}

// DeleteCommentRequest deletes a thread and its replies.
type DeleteCommentRequest struct {
	CommentID string `json:"commentId"`
}

// DeleteCommentReplyRequest deletes one reply.
type DeleteCommentReplyRequest struct {
	CommentID string `json:"commentId"`
	PostID    string `json:"postId"`
}

// InsertCommentReply is the thread an insertComment made.
type InsertCommentReply struct {
	CommentThread *CommentThread `json:"commentThread,omitempty"`
}

// AddCommentReplyReply is the post an addCommentReply made.
type AddCommentReplyReply struct {
	Post *Post `json:"post,omitempty"`
}

// CommentUpdateAllFailed is the commentUpdateState of a batch whose
// comment requests all failed. Spike R never saw it, and every write that
// succeeded said ALL_SAVED; a 200 carrying it is still not a saved
// comment.
const CommentUpdateAllFailed = "ALL_FAILED_UNKNOWN_REASON"
