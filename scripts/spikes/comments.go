//go:build live

package main

// Spike R: cell-anchored comments, which the Sheets API published in
// 2026-09 as five batchUpdate requests and a commentsViewMode on get.
// The reference says what each request takes; it does not say what a
// read returns under a field mask, where an anchor goes when rows move,
// how an assignee reads back, what a bad id answers, or whether a failed
// comment write is a 400 or a 200 that only commentUpdateState admits.
//
// Every comment here is invented. Ids, names and addresses are printed
// as their length, never their value; the one assignee is the signed-in
// account itself, so the only mail this sends goes to its own inbox.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/mmedum/google-sheets-mcp/v3/internal/a1"
)

// shown are the string fields whose values the outline prints: enums,
// times, and text this probe wrote itself. Everything else prints as its
// length.
var shown = map[string]bool{
	"status": true, "commentAction": true, "commentUpdateState": true, "commentsViewMode": true,
	"title": true, "content": true, "contentHtml": true, "plainTextQuote": true,
	"createTime": true, "updateTime": true, "message": true,
}

// outline prints a JSON body with every unlisted string replaced by its
// length, keys sorted, so the transcript shows the shape and nothing
// that identifies the account.
func outline(body string) string {
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return first120(body)
	}
	var b strings.Builder
	var walk func(key string, v any)
	walk = func(key string, v any) {
		switch t := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			b.WriteString("{")
			for i, k := range keys {
				if i > 0 {
					b.WriteString(" ")
				}
				b.WriteString(k + ":")
				walk(k, t[k])
			}
			b.WriteString("}")
		case []any:
			b.WriteString("[")
			for i, e := range t {
				if i > 0 {
					b.WriteString(" ")
				}
				walk(key, e)
			}
			b.WriteString("]")
		case string:
			if shown[key] {
				fmt.Fprintf(&b, "%q", t)
			} else {
				fmt.Fprintf(&b, "<str %d>", len(t))
			}
		default:
			fmt.Fprintf(&b, "%v", t)
		}
	}
	walk("", v)
	return b.String()
}

// say prints one probe's status and the outline of its body.
func say(what string, status int, body string) {
	line("  %s -> HTTP %d", what, status)
	line("    %s", outline(body))
}

// commentsMask is the field mask the read tool would send.
const commentsMask = "comments,sheets(properties(sheetId,title),commentAnchors)"

// readComments reads the scratch spreadsheet's comments under a mask and
// a view mode; an empty mode leaves the parameter off.
func readComments(ctx context.Context, mask, mode string) (int, string) {
	v := url.Values{}
	v.Set("fields", mask)
	if mode != "" {
		v.Set("commentsViewMode", mode)
	}
	return call(ctx, http.MethodGet, sheetsBase+"/spreadsheets/"+scratchID+"?"+v.Encode(), nil)
}

// thread is what the probe keeps from an insertComment reply.
type thread struct {
	CommentID string `json:"commentId"`
	AnchorID  string `json:"anchorId"`
	HeadPost  struct {
		PostID string `json:"postId"`
	} `json:"headPost"`
}

// insert adds a comment at a zero-based cell and returns its thread.
func insert(ctx context.Context, what string, sheetID, row, col int, content, assignee string) (thread, bool) {
	req := map[string]any{
		"coordinate": map[string]any{"sheetId": sheetID, "rowIndex": row, "columnIndex": col},
		"content":    content,
	}
	if assignee != "" {
		req["assigneeEmailAddress"] = assignee
	}
	status, body := batchOne(ctx, map[string]any{"insertComment": req})
	say(what, status, body)
	var out struct {
		Replies []struct {
			InsertComment struct {
				CommentThread thread `json:"commentThread"`
			} `json:"insertComment"`
		} `json:"replies"`
	}
	if status != 200 || json.Unmarshal([]byte(body), &out) != nil || len(out.Replies) == 0 {
		return thread{}, false
	}
	return out.Replies[0].InsertComment.CommentThread, true
}

// reply adds a post to a thread and returns its post id.
func reply(ctx context.Context, what, commentID string, post map[string]any) string {
	status, body := batchOne(ctx, map[string]any{"addCommentReply": map[string]any{"commentId": commentID, "post": post}})
	say(what, status, body)
	var out struct {
		Replies []struct {
			AddCommentReply struct {
				Post struct {
					PostID string `json:"postId"`
				} `json:"post"`
			} `json:"addCommentReply"`
		} `json:"replies"`
	}
	if status != 200 || json.Unmarshal([]byte(body), &out) != nil || len(out.Replies) == 0 {
		return ""
	}
	return out.Replies[0].AddCommentReply.Post.PostID
}

// selfAddress is the signed-in account's address, from Drive's about.
func selfAddress(ctx context.Context) string {
	status, body := call(ctx, http.MethodGet, driveBase+"/about?fields=user(emailAddress)", nil)
	if status != 200 {
		return ""
	}
	var out struct {
		User struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"user"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	return out.User.EmailAddress
}

func spikeR(ctx context.Context) {
	sec("Spike R: cell-anchored comments")
	const sheet = "SpikeComments"
	sheetID, err := addSheet(ctx, sheet)
	if err != nil {
		line("  setup failed: %v", err)
		return
	}
	q := a1.QuoteSheet(sheet)
	if err := put(ctx, q+"!A1:B4", [][]any{
		{"Item", "Cost"}, {"Widget", 12}, {"Gadget", 30}, {"Sprocket", 7},
	}); err != nil {
		line("  setup failed: %v", err)
		return
	}

	line("R1 insertComment on B2, no assignee")
	t1, ok := insert(ctx, "insert", sheetID, 1, 1, "Is this the unit cost or the total?", "")
	if !ok {
		line("  nothing else here means anything without a comment; stopping.")
		return
	}

	line("R2 reading comments back")
	for _, mode := range []string{"", "COMMENTS_VIEW_MODE_INCLUDED", "COMMENTS_VIEW_MODE_DEFAULT_FOR_CURRENT_ACCESS", "COMMENTS_VIEW_MODE_OMITTED"} {
		status, body := readComments(ctx, commentsMask, mode)
		say("mask "+commentsMask+", mode "+orNone(mode), status, body)
	}
	narrow := "comments(commentId,status,anchorId,headPost(postId,content,author(me))),sheets(properties(sheetId),commentAnchors(anchorId,range))"
	status, body := readComments(ctx, narrow, "COMMENTS_VIEW_MODE_INCLUDED")
	say("nested mask", status, body)
	status, body = readComments(ctx, "sheets(properties(sheetId),commentAnchors)", "COMMENTS_VIEW_MODE_INCLUDED")
	say("anchors alone", status, body)

	line("R3 reply, resolve, reopen")
	r1 := reply(ctx, "reply with text", t1.CommentID, map[string]any{"content": "Unit cost."})
	reply(ctx, "resolve, no text", t1.CommentID, map[string]any{"commentAction": "RESOLVE"})
	status, body = readComments(ctx, "comments(commentId,status,replies(postId,content,commentAction,deleted))", "COMMENTS_VIEW_MODE_INCLUDED")
	say("after resolve", status, body)
	reply(ctx, "reopen with text", t1.CommentID, map[string]any{"commentAction": "REOPEN", "content": "Reopening: the total is in C."})
	reply(ctx, "resolve twice", t1.CommentID, map[string]any{"commentAction": "RESOLVE"})
	reply(ctx, "resolve an already resolved thread", t1.CommentID, map[string]any{"commentAction": "RESOLVE"})
	reply(ctx, "reopen", t1.CommentID, map[string]any{"commentAction": "REOPEN"})
	reply(ctx, "empty content, no action", t1.CommentID, map[string]any{})

	line("R4 edit the head post and a reply")
	for _, p := range []struct{ what, postID string }{{"head post", t1.HeadPost.PostID}, {"reply", r1}} {
		status, body := batchOne(ctx, map[string]any{"updateCommentPost": map[string]any{
			"commentId": t1.CommentID, "postId": p.postID, "content": "Edited: " + p.what,
		}})
		say("updateCommentPost "+p.what, status, body)
	}
	status, body = readComments(ctx, "comments(commentId,headPost(content,updateTime),replies(postId,content,updateTime))", "COMMENTS_VIEW_MODE_INCLUDED")
	say("after the edits", status, body)

	line("R5 where an anchor goes when rows move")
	status, body = batchOne(ctx, map[string]any{"insertDimension": map[string]any{
		"range": map[string]any{"sheetId": sheetID, "dimension": "ROWS", "startIndex": 0, "endIndex": 1},
	}})
	line("  insert a row above everything -> HTTP %d", status)
	status, body = readComments(ctx, "sheets(properties(sheetId),commentAnchors)", "COMMENTS_VIEW_MODE_INCLUDED")
	say("anchors after the insert (B2 should now be B3)", status, body)
	t2, ok := insert(ctx, "insert a second comment on A5 (Sprocket)", sheetID, 4, 0, "Discontinued?", "")
	if ok {
		status, _ = batchOne(ctx, map[string]any{"deleteDimension": map[string]any{
			"range": map[string]any{"sheetId": sheetID, "dimension": "ROWS", "startIndex": 4, "endIndex": 5},
		}})
		line("  delete row 5, which holds the second comment -> HTTP %d", status)
		status, body = readComments(ctx, "comments(commentId,status,anchorId),sheets(properties(sheetId),commentAnchors)", "COMMENTS_VIEW_MODE_INCLUDED")
		say("after the row went", status, body)
		line("    the second thread's anchor id is %d characters", len(t2.AnchorID))
	}

	line("R6 assignee")
	self := selfAddress(ctx)
	if self == "" {
		line("  the signed-in address could not be read; skipping the assignee probes")
	} else {
		t3, ok := insert(ctx, "insert on B4 assigned to the signed-in account", sheetID, 3, 1, "Please check this figure.", self)
		if ok {
			status, body = readComments(ctx, "comments(commentId,headPost(assigneeEmail,author(me)),replies(assigneeEmail))", "COMMENTS_VIEW_MODE_INCLUDED")
			say("how the assignee reads back", status, body)
			reply(ctx, "reply that reassigns to the same account", t3.CommentID, map[string]any{"content": "Reassigning.", "assigneeEmail": self})
		}
		reply(ctx, "reply with an assignee on a thread that has none", t1.CommentID, map[string]any{"content": "Assigning.", "assigneeEmail": self})
	}
	insert(ctx, "insert assigned to an address that cannot exist", sheetID, 2, 1, "Nobody gets this.", "nobody@example.invalid")

	line("R7 what a bad id or a bad place answers")
	for _, p := range []struct {
		what string
		req  map[string]any
	}{
		{"reply to a thread that does not exist", map[string]any{"addCommentReply": map[string]any{"commentId": "AAAAnotacomment", "post": map[string]any{"content": "x"}}}},
		{"edit a post that does not exist", map[string]any{"updateCommentPost": map[string]any{"commentId": t1.CommentID, "postId": "AAAAnotapost", "content": "x"}}},
		{"delete a thread that does not exist", map[string]any{"deleteComment": map[string]any{"commentId": "AAAAnotacomment"}}},
		{"insert beyond the grid", map[string]any{"insertComment": map[string]any{"coordinate": map[string]any{"sheetId": sheetID, "rowIndex": 5000, "columnIndex": 0}, "content": "x"}}},
		{"insert on a sheet that does not exist", map[string]any{"insertComment": map[string]any{"coordinate": map[string]any{"sheetId": 987654, "rowIndex": 0, "columnIndex": 0}, "content": "x"}}},
		{"insert with empty content", map[string]any{"insertComment": map[string]any{"coordinate": map[string]any{"sheetId": sheetID, "rowIndex": 0, "columnIndex": 0}, "content": ""}}},
	} {
		status, body := batchOne(ctx, p.req)
		say(p.what, status, body)
	}
	status, body = call(ctx, http.MethodPost, sheetsBase+"/spreadsheets/"+scratchID+":batchUpdate", map[string]any{"requests": []any{
		map[string]any{"repeatCell": map[string]any{
			"range":  map[string]any{"sheetId": sheetID, "startRowIndex": 0, "endRowIndex": 1, "startColumnIndex": 0, "endColumnIndex": 1},
			"cell":   map[string]any{"userEnteredFormat": map[string]any{"textFormat": map[string]any{"bold": true}}},
			"fields": "userEnteredFormat.textFormat.bold",
		}},
		map[string]any{"updateCommentPost": map[string]any{"commentId": t1.CommentID, "postId": "AAAAnotapost", "content": "x"}},
	}})
	say("a format and a bad comment edit in one batch", status, body)

	line("R8 delete a reply, then the thread, then the thread again")
	if r1 != "" {
		status, body = batchOne(ctx, map[string]any{"deleteCommentReply": map[string]any{"commentId": t1.CommentID, "postId": r1}})
		say("deleteCommentReply", status, body)
		status, body = batchOne(ctx, map[string]any{"deleteCommentReply": map[string]any{"commentId": t1.CommentID, "postId": r1}})
		say("deleteCommentReply again", status, body)
	}
	status, body = readComments(ctx, "comments(commentId,replies(postId,deleted,content))", "COMMENTS_VIEW_MODE_INCLUDED")
	say("after the reply went", status, body)
	status, body = batchOne(ctx, map[string]any{"deleteComment": map[string]any{"commentId": t1.CommentID}})
	say("deleteComment", status, body)
	status, body = batchOne(ctx, map[string]any{"deleteComment": map[string]any{"commentId": t1.CommentID}})
	say("deleteComment again", status, body)
	status, body = readComments(ctx, "comments(commentId,status),sheets(properties(sheetId),commentAnchors)", "COMMENTS_VIEW_MODE_INCLUDED")
	say("after the thread went", status, body)

	line("R9 the same spreadsheet through Drive's comments")
	status, body = call(ctx, http.MethodGet, driveBase+"/files/"+scratchID+"/comments?fields=comments(id,anchor,resolved,deleted,quotedFileContent,replies(id,action,deleted))&includeDeleted=true", nil)
	say("files.comments.list", status, body)
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
