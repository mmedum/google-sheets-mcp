// Package redact masks the few things this server prints to a person
// that must not survive being pasted somewhere else.
//
// The logging rule (§9) is about what the server writes while it runs,
// and a test holds it. This is the other half: `status` and `doctor`
// print for a human, the issue form asks for their output, and both were
// printing an OAuth client id and an account address — two entries on
// §9.1's never-list — until a live run showed it.
//
// The client id arrives by an unlucky route: the Cloud console names the
// file it gives you after the client id, so printing the *path* to the
// client secret prints the client id. Nobody types that path; it is
// whatever the download was called.
package redact

import (
	"regexp"
	"strings"
)

// clientID is the shape Google issues, and the same one the repository's
// leak gate looks for.
var clientID = regexp.MustCompile(`[0-9]{6,}-[a-z0-9]{20,}\.apps\.googleusercontent\.com`)

// Path masks a client id anywhere in a filename.
//
// The rest of the path is kept: "it looked in the wrong directory" is
// most of what a first-run report is about, and a directory identifies
// nobody.
func Path(p string) string { return clientID.ReplaceAllString(p, "<client-id>") }

// ClientID masks the identifier wherever it appears in free text.
func ClientID(s string) string { return clientID.ReplaceAllString(s, "<client-id>") }

// Email keeps enough of an address for the person who owns it to
// recognise it, and not enough for anyone else to use it.
//
// A domain is an organisation name, so it goes too: "which account am I
// signed in as" is answered by the first letter and the shape, and a
// maintainer reading a pasted bug report needs none of it.
func Email(addr string) string {
	local, domain, ok := strings.Cut(addr, "@")
	if !ok || local == "" || domain == "" {
		return mask(addr)
	}
	tld := ""
	if i := strings.LastIndexByte(domain, '.'); i >= 0 {
		tld, domain = domain[i:], domain[:i]
	}
	return mask(local) + "@" + mask(domain) + tld
}

// address matches an email address anywhere in free text.
var address = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

// opaqueID matches the identifiers Google hands out for files, folders
// and shared drives. They are base64url and long; a file id starts with
// 1 and a folder or shared-drive id with 0A, which between them is every
// one this server can encounter.
//
// A 40- or 64-character hex string is a commit SHA, not an id, and is
// left alone.
var (
	opaqueID = regexp.MustCompile(`\b[01][A-Za-z0-9_-]{17,}\b`)
	hexOnly  = regexp.MustCompile(`^[0-9a-f]+$`)
)

// Line masks everything in one line of output that identifies a person,
// an account or a document.
//
// This is for text assembled by somebody else — an API response, an
// error, a rendered result — where the values are not known in advance
// and so cannot be registered for substitution. A live driver's
// transcript is the case it exists for: §9.1 says that transcript is
// safe to paste into a commit message, and it was not, because the
// driver only masked the ids it had created itself. The owner's address
// and the folder id came from Drive and went straight through.
func Line(s string) string {
	s = address.ReplaceAllStringFunc(s, Email)
	s = ClientID(s)
	return opaqueID.ReplaceAllStringFunc(s, func(id string) string {
		if len(id) >= 40 && hexOnly.MatchString(id) {
			return id // a commit SHA identifies a change, not a person
		}
		return "<id>"
	})
}

// mask keeps the first rune and replaces the rest with an ellipsis, so
// the result cannot be mistaken for a short address.
func mask(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	return string(r[0]) + "…"
}
