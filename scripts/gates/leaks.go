package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The leak gate covers identifiers and data, which are not secrets and
// therefore pass gitleaks untouched: a spreadsheet id in a fixture leaks
// what somebody works on rather than a password.
//
// Every rule below is an allow-list. A deny-list naming the domain, the
// organisation or the account to watch for would itself be the
// disclosure, and it would be committed here in the clear.
//
// What a pattern cannot do is the other half of §9.1, and it is why this
// gate is not the whole control: a cell value, a sheet title, a named
// range and a note are ordinary words, and no regex separates an
// invented column heading from somebody's customer list. Those are made
// structurally impossible instead — fixtures are generated rather than
// recorded, and the live driver reads only a spreadsheet it created and
// filled itself.
var (
	// An address at a domain somebody could actually own. RFC 2606 and
	// RFC 6761 reserve the rest for documentation and tests.
	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@([A-Za-z0-9.\-]+\.[A-Za-z]{2,})`)
	safeDomains  = map[string]bool{"example.com": true, "example.org": true, "example.net": true}
	safeSuffixes = []string{".test", ".invalid", ".localhost", ".example"}

	// The numeric identifiers. A rule that wants a capital and a digit
	// catches a spreadsheet id and misses both of these, and a sibling
	// shipped exactly that hole: a Google account id is 21 digits and a
	// Drive permission id is 20, neither with a letter.
	accountID    = regexp.MustCompile(`\b[0-9]{21}\b`)
	permissionID = regexp.MustCompile(`\b[0-9]{20}\b`)

	// An OAuth client id, which is not a secret and is still an
	// identifier of somebody's Cloud project.
	clientID = regexp.MustCompile(`[0-9]{6,}-[a-z0-9]{20,}\.apps\.googleusercontent\.com`)

	// A user-content URL carries an account id in its path. The bare
	// host is allowed: the client's allowlist names it, and must.
	userContent = regexp.MustCompile(`\b[a-z0-9\-]+\.googleusercontent\.com/[A-Za-z0-9_\-/]{8,}`)

	// A Drive or Sheets resource id, and the URL that carries one.
	//
	// A file id starts with 1 and a folder or shared-drive id with 0A,
	// and only the first shape was here — so a folder id was a finding
	// the gate could not make. A live run put one in a transcript, which
	// is one careless paste from a commit message.
	//
	// A 40- or 64-character hex string is a commit SHA and is excluded
	// below rather than by the pattern, since some of them start with a
	// digit this has to match.
	resourceID     = regexp.MustCompile(`\b[01][A-Za-z0-9_\-]{17,}\b`)
	commitSHA      = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)
	spreadsheetURL = regexp.MustCompile(`docs\.google\.com/spreadsheets/d/([A-Za-z0-9_\-]{10,})`)

	// What an invented id looks like: a marker word that a base64url id
	// made of random bytes cannot contain by chance, or a run of one
	// letter that no real id has. RE2 has no backreferences, so the run
	// is counted rather than matched.
	inventedWord = regexp.MustCompile(`(?i)synthetic|fixture|nosuch|unknown|example|scratch|placeholder`)
)

// allowedValues are exceptions by value, each with its reason. A test
// asserts every entry has one: an entry without a reason is how a gate
// quietly stops working.
//
// By value rather than by file or by pattern, so an exception excuses
// exactly the string it names and nothing that happens to sit beside it.
var allowedValues = map[string]string{
	// The co-author trailer on every commit here. It is a noreply
	// address at a vendor's domain, it identifies nobody, and it is in
	// the commit messages rather than in a file — which is why the tree
	// scan never saw it and the history scan did.
	"noreply@anthropic.com": "the Co-Authored-By trailer; a noreply address that identifies no account", // leakcheck:allow
}

// skipFiles hold machine-generated hashes rather than prose or code.
var skipFiles = map[string]bool{"go.sum": true}

// marker excuses one line, and only the line it is on. The scanner has
// to contain examples of the shapes it catches, or nobody can tell
// whether its rules match anything at all.
const marker = "leakcheck:" + "allow"

// leakGate scans the working tree, and on request every blob and message
// in the history.
func leakGate(history bool) error {
	if err := checkAllowlistHasReasons(); err != nil {
		return err
	}
	if err := scanTree(); err != nil {
		return err
	}
	if history {
		return scanHistory()
	}
	return nil
}

// treeFiles is everything this gate reads: the tracked files, and the
// untracked ones git would let a wildcard add sweep in.
//
// The untracked half is the half that matters most, and it was missing.
// A phase's new files are invisible to a tracked-only scan until they
// are staged — and a phase's new files are precisely the ones nobody has
// scanned before. Phase 3 ran `make check` green a dozen times over 167
// files while 28 of its own were untracked; the first `git add -A` took
// it to 195 and the gate immediately found a spreadsheet id in a test
// written that afternoon. The gate was doing what it said. "make check
// is green" was the claim that was false.
//
// Ordering is the point, and it is a sibling repository's: refusing a
// file while it is still untracked fails *before* `git add -A` can sweep
// it in, where a tracked-only scan catches it one commit too late.
// `--exclude-standard` honours .gitignore, so a build output with a rule
// of its own is left alone — it cannot be committed either.
func treeFiles() (files []string, tracked int, err error) {
	out, err := git("ls-files", "-z")
	if err != nil {
		return nil, 0, err
	}
	files = split0(out)
	tracked = len(files)

	others, err := git("ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, 0, err
	}
	return append(files, split0(others)...), tracked, nil
}

func split0(out string) []string {
	trimmed := strings.TrimRight(out, "\x00")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\x00")
}

func checkAllowlistHasReasons() error {
	for value, reason := range allowedValues {
		if strings.TrimSpace(reason) == "" {
			return fmt.Errorf("the allowlist entry %q has no reason", value)
		}
	}
	return nil
}

func scanTree() error {
	files, tracked, err := treeFiles()
	if err != nil {
		return err
	}
	if tracked < 20 {
		return fmt.Errorf("only %d tracked files; the scan is not seeing the repository", tracked)
	}

	var findings []string
	scanned, scannedUntracked := 0, 0
	for i, name := range files {
		if name == "" || skipFiles[filepath.Base(name)] {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			continue // a file git knows about and the disk does not is not this gate's business
		}
		if isBinary(data) {
			// A committed binary is a finding rather than something to
			// scan: its symbol table buries the line that matters.
			findings = append(findings, name+": a binary file is in the tree; scanning one hides the line that "+
				"matters, and a compiled artifact swept in by a wildcard add is how two sibling repositories put "+
				"megabytes into a public history")
			continue
		}
		scanned++
		// The untracked count is reported rather than folded in, so a
		// run says how much of what it read is code nothing had scanned
		// before. That number being large is the normal state during a
		// phase, and it is the number this gate used to be blind to.
		if i >= tracked {
			scannedUntracked++
		}
		for _, f := range findLeaks(strip(string(data))) {
			findings = append(findings, name+": "+f)
		}
	}
	if scanned < 15 {
		return fmt.Errorf("read %d text files; the scan is not looking at the tree", scanned)
	}
	fmt.Printf("scanned %d text files, %d of them not yet tracked\n", scanned, scannedUntracked)
	return report(findings)
}

// scanHistory walks every blob and every commit and tag message, from
// the message down: the author and tagger lines above it are git's own,
// and an identity is public in every repository by construction.
//
// A leak deleted from the tip is still in the log, so this runs before
// the repository is made public and after anything is removed from it in
// a hurry.
func scanHistory() error {
	out, err := git("rev-list", "--objects", "--all")
	if err != nil {
		return err
	}
	type blob struct{ sha, path string }
	var blobs []blob
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		sha, path, ok := strings.Cut(line, " ")
		if !ok || path == "" || seen[sha] || skipFiles[filepath.Base(path)] {
			continue
		}
		seen[sha] = true
		blobs = append(blobs, blob{sha, path})
	}

	var findings []string
	scanned := 0
	for _, b := range blobs {
		data, err := exec.Command("git", "cat-file", "blob", b.sha).Output()
		if err != nil || isBinary(data) {
			continue
		}
		scanned++
		for _, f := range findLeaks(strip(string(data))) {
			// The commit is named because a finding in history is not
			// fixed by editing a file: it needs the history rewritten,
			// or a decision that this one is harmless.
			findings = append(findings, fmt.Sprintf("%s (blob %s): %s", b.path, b.sha[:12], f))
		}
	}

	messages, err := git("log", "--all", "--format=%B%n--%n")
	if err != nil {
		return err
	}
	for _, f := range findLeaks(strip(messages)) {
		findings = append(findings, "a commit message: "+f)
	}
	tags, err := git("for-each-ref", "--format=%(contents)", "refs/tags")
	if err == nil {
		for _, f := range findLeaks(strip(tags)) {
			findings = append(findings, "a tag message: "+f)
		}
	}

	// The floor is derived from what git reported rather than guessed at
	// a repository size. A constant would be wrong at both ends: too
	// high on a young history, where it fails for the wrong reason, and
	// too low on an old one, where a walk that broke after ten objects
	// would still pass. What has to be true is that the walk found
	// objects and that the scan actually read them.
	switch {
	case len(blobs) == 0:
		return fmt.Errorf("the object walk found no blobs at all; the scan is not seeing the history")
	case scanned*2 < len(blobs):
		return fmt.Errorf("read %d of %d blobs; most of the history was skipped rather than scanned", scanned, len(blobs))
	}
	fmt.Printf("scanned %d of %d blobs and every commit and tag message\n", scanned, len(blobs))
	return report(findings)
}

func report(findings []string) error {
	if len(findings) == 0 {
		return nil
	}
	sort.Strings(findings)
	return fmt.Errorf("%d finding(s):\n  %s", len(findings), strings.Join(findings, "\n  "))
}

// strip drops lines carrying the marker, and only those lines.
func strip(text string) string {
	var b strings.Builder
	for line := range strings.Lines(text) {
		if strings.Contains(line, marker) {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// findLeaks reports everything in text that looks like it identifies
// somebody or names their spreadsheet.
//
// Findings are abbreviated, so a CI log or a terminal does not reprint
// the leak in full while telling you about it.
func findLeaks(text string) []string {
	var out []string
	for _, m := range emailPattern.FindAllStringSubmatch(text, -1) {
		if !safeDomain(m[1]) && allowedValues[m[0]] == "" {
			out = append(out, "an address at a real domain: "+abbreviate(m[0]))
		}
	}
	for _, rule := range []struct {
		re   *regexp.Regexp
		what string
	}{
		{accountID, "a 21-digit Google account id"},
		{permissionID, "a 20-digit Drive permission id"},
		{clientID, "an OAuth client id"},
		{userContent, "a user-content URL with an id in its path"},
	} {
		for _, m := range rule.re.FindAllString(text, -1) {
			if allowedValues[m] == "" {
				out = append(out, rule.what+": "+abbreviate(m))
			}
		}
	}
	for _, m := range spreadsheetURL.FindAllStringSubmatch(text, -1) {
		if !invented(m[1]) && allowedValues[m[0]] == "" {
			out = append(out, "a spreadsheet URL: "+abbreviate(m[0]))
		}
	}
	for _, m := range resourceID.FindAllString(text, -1) {
		if commitSHA.MatchString(m) {
			continue // identifies a change, not a person
		}
		if !invented(m) && allowedValues[m] == "" {
			out = append(out, "an id that does not look invented: "+abbreviate(m)+
				" (if it is synthetic, say so inside the value: Synthetic, Fixture, NoSuch, or a run of XXXX)")
		}
	}
	return out
}

// abbreviate shortens a finding so the output does not reprint it.
func abbreviate(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:6] + "…" + s[len(s)-4:]
}

func safeDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	// RFC 2606 reserves example.com, .net and .org *and everything under
	// them*, so a subdomain is as safe as the apex. Matching only the
	// apex made sub.example.org a finding, which is a false positive
	// that teaches people to reach for the marker.
	for d := range safeDomains {
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return true
		}
	}
	for _, s := range safeSuffixes {
		if strings.HasSuffix(domain, s) {
			return true
		}
	}
	return false
}

func invented(id string) bool { return inventedWord.MatchString(id) || hasRun(id, 4) }

// hasRun reports whether s repeats one character n times in a row.
func hasRun(s string, n int) bool {
	run := 1
	for i := 1; i < len(s); i++ {
		if s[i] == s[i-1] {
			run++
			if run >= n {
				return true
			}
			continue
		}
		run = 1
	}
	return false
}

func isBinary(data []byte) bool {
	limit := min(len(data), 8000)
	for i := range limit {
		if data[i] == 0 {
			return true
		}
	}
	return false
}
