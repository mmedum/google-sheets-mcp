# CLAUDE.md — google-sheets-mcp

A Go MCP server for Google Sheets over stdio: one binary per person,
their own OAuth client, nothing hosted (D-01). It works inside one
spreadsheet, and files, sharing and revisions belong to a Drive server
(D-02). Why the code is the way it is lives in `docs/architecture.md`,
one decision per `D-NN`; read it before changing the tool surface, the
addressing, the write guards or the sign-in.

## Commands

- `make check` runs every gate CI runs; its `check` target is the list.
  `make hooks`, once per clone, adds the pre-commit scan.
- `make live` drives the built binary in scratch spreadsheets on your
  account; read the transcript. `CONTRIBUTING.md` covers it, the spikes
  (`-list` prints their names, `-only` takes them) and `make evals`.
- `make api-diff`, `make schema-refetch`, `make leaks-history` and
  `make schema-baseline` are release-day steps in `docs/release.md`.
- `go run ./scripts/gates` lists the gates; add a name to run one.

## Hard rules

1. **Keep everything internal out of the repository, because it is
   public**: spreadsheet ids or URLs, anything from a real spreadsheet,
   account addresses, Cloud project ids, OAuth client ids, secrets, and
   any reference to another project, account or machine the maintainers
   use. That covers code, docs, fixtures, transcripts, commit messages,
   pull requests and logs. `make leaks` and gitleaks fail on the shapes
   they see (D-09).
2. **Generate fixtures rather than record them (D-10), and keep the live
   driver and the evals to what they wrote.** Both work in spreadsheets
   they made and print only through the redacting helper
   `make transcript` holds (D-11).
3. **Write to stdout only from `main`**, because a stray print corrupts
   the JSON-RPC stream. Stdout carries only MCP JSON-RPC frames; logs go
   to stderr through `slog` (D-06). forbidigo and `make smoke` hold it,
   with `scripts/` excluded.
4. **Log the call, not the payload** of cell values, formulas, titles,
   ranges or search terms, because people paste logs into bug reports;
   a transport error loses its URL (D-07).
   `TestLogsCarryNothingFromTheSpreadsheet` holds it.
5. **Show the model A1 and do its counting for it, rather than showing
   it a `GridRange`.** A1 is the contract and the conversion lives in
   `internal/a1` alone (D-12). A range is built from a parsed title,
   quoted on the wire (D-14). No tool defaults a sheet and no
   description offers `Sheet1` (D-15); `make staleness` refuses the
   second.
6. **Write over only what the caller has seen.** Non-empty cells need
   `overwrite` with `checkpoint`, formulas `overwrite_formulas`
   too, and protected ranges and partial merges are refused (D-24,
   D-25). Formatting and transforms guard what they take (D-33).
7. **Report what Google changed.** The caller picks `typed` or `literal`
   and the server keeps that pick rather than substituting its own;
   every write reports what Google stored, read back (D-27, D-31).
8. **Write a formula that reaches outside the spreadsheet only when
   `GSHEETS_ALLOW_EXTERNAL_FORMULAS` is set**, because one can send the
   spreadsheet somewhere the person cannot see; no argument lifts it
   (D-28).
9. **Repeat a write only when its HTTP method makes that safe.** An
   append or a `batchUpdate` that may have landed is `[ambiguous_outcome]`
   (D-45). `TestUnrepeatableWritesAreNotRepeated` holds it.
10. **Register every tool through `add`**, whose `Kind` decides
    annotations, registration and rendering (D-34). A delete registers
    only with `GSHEETS_ENABLE_DESTRUCTIVE` and needs `confirm: true`
    (D-35). A risky variant is its own tool, not an argument (D-36). The
    server asks the person nothing (D-37), and `requiresUserInteraction`
    stays on `delete_data_source` alone (D-39).
11. **Leave widening the server's reach to the operator, not to a
    tool**: its settings, the credentials, the profile, a file or the
    environment (D-40), because a cell somebody else wrote can persuade
    the model. `TestNoToolCanWriteTheServersSettings` holds it.
12. **Keep to these scopes, even for the live driver's cleanup**:
    `spreadsheets` and `drive.readonly`, or their read-only pair, and
    `bigquery.readonly` only with `GSHEETS_ENABLE_DATA_SOURCES` (D-49,
    D-43). `TestScopes` holds it.
13. **Extend `internal/gsheets` rather than importing
    `google.golang.org/api`** or another project's code (D-75). depguard
    refuses the import. Record a verdict in `testdata/api-coverage.tsv`
    or `testdata/api-fields.tsv` before you call a method or model a
    field (D-76).
14. **Keep every released tool, argument and output field, under its
    released name.** `make schema-diff` fails on a break unless go.mod's
    major version is above the release's (D-57).
15. **Emit every error as `[class] message`.** `make classes` holds the
    vocabulary closed (D-44).
16. **Give every rule a way to fail**: a test, a list derived from the
    code, and a floor on what the check read (D-65).
17. **Commit on a topic branch, not `main`, and leave pushing to the
    owner**; the owner pushes, tags and merges (D-73).
18. **Base a belief about Google on the discovery document or a live
    probe, not on a reference page's prose** (D-60). Record a verified
    Google behavior in the comment at the line that depends on it, with
    the source link and the live step that checks it.

## Definition of done

- `make check` passes. On a pull request CI also runs `schema-ack`,
  which needs a commit footer naming each changed item of the tool
  surface, and `changelog`, which needs an entry under `[Unreleased]`
  or a `Changelog: none` footer (`CONTRIBUTING.md`).
- New behavior has tests, and the schema diff has been read.
- `/simplify` and `/code-review medium` have run. A fixed finding is one
  line in the commit body. A declined one goes in the PR body, or
  becomes a present-tense constraint comment at the line it protects.
  Write findings only in those places, not into `docs/`.
- Anything touching the write path or an API response shape has had a
  `make live` run whose transcript was read (D-61).

## Traps

- **Green gates are not done.** The fakes in `internal/gapi/sheetstest`
  are written from belief about Google; only `make live` tests it.
- **A dry run is each write's own early return.** The API client refuses
  a write under `gapi.WithDryRun` only as a backstop (D-80), so a new
  write still needs its own "sends nothing" test (D-30).
- **The card is cached for `service.CacheWindow`, and only the
  `forgetting` writes drop it**: `BatchUpdate`, `AppendValues` and
  `CopySheetTo`, whatever their outcome. The format, transform and
  table-header guards read it through the cache (D-48), so a new write
  method that changes the card needs a `forgetting` wrapper.
- **A test can redirect the config directory but not the OS keyring.**
  `isolate` in `cmd/google-sheets-mcp` gives each test its own profile;
  a test that skips it can delete your refresh token.
- **Supersede or deprecate a decision rather than deleting it, and give
  each new one a fresh ID.** Take the next ID from the file's `Next ID:`
  line.

## Writing

Plain and short: lead with the outcome, one idea per sentence, facts
rather than narration, and a count only where a gate checks it. History
goes in commit messages. `release.yml` lifts the CHANGELOG section
verbatim into the release notes, so follow its rules in
`CONTRIBUTING.md` exactly.
