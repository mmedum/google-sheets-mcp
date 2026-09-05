# CLAUDE.md — google-sheets-mcp project instructions

Project-specific rules for Claude Code in this repository. The user's
global instructions still apply; this file adds to them.

## Mission

A production-grade Go MCP server for Google Sheets, distributed to other
people. One binary, stdio, per-user OAuth, no hosted deployment. The
design, its evidence log, the decided constraints and the phase plan live
in `docs/architecture.md`. Read it before changing the tool surface, the
addressing model or the write path. The server works inside a
spreadsheet: files, folders, sharing, revisions and comment threads
belong to a server built on the Drive API.

## Hard rules

1. **Nothing internal, ever.** No organisation names, spreadsheet ids or
   URLs, account emails, Cloud project ids, OAuth client ids or secrets;
   no cell values, formulas, notes, sheet or spreadsheet titles, named
   ranges or metadata from a real spreadsheet; and no reference to any
   other project, repository, account or machine the maintainers use.
   This holds for code, docs, fixtures, goldens, transcripts, commit and
   tag messages, pull requests and logs. A formula is the worst of them:
   `IMPORTRANGE` carries another spreadsheet's id inside a string.

   Two of these are structural rather than a matter of care, and must
   stay that way: **fixtures are generated, never recorded**, and the
   **live driver reads only a spreadsheet it created and filled itself**.
   `docs/architecture.md` §9.1 is the full specification, including why
   every rule in the leak gate is an allow-list.
2. **Stdout carries only MCP JSON-RPC frames.** Logs use `slog` to
   stderr. Never `fmt.Println` on the server path.
3. **Logs never carry the payload.** Method, tool, outcome, duration and
   a truncated spreadsheet id are fine. Cell values, formulas, sheet or
   spreadsheet titles, ranges and search terms are not — a search term
   reaches a log through a request URL, so transport errors are stripped
   of it.
4. **A1 is the contract.** The model never sees a `GridRange` and never
   does index arithmetic. Zero-based half-open conversion lives in
   `internal/a1` and nowhere else.
5. **Sheet names are read, never assumed.** `Sheet1` does not exist on a
   non-English account. No tool defaults a sheet, and no description uses
   `Sheet1` as an example.
6. **A write never destroys what it cannot see.** Non-empty cells need
   `overwrite`; formulas need `overwrite_formulas` as well; protected
   ranges and partial merges are refused before the request is built.
   Sheets has no undo, so a gate here is the only one there is.
7. **Coercion is reported, never hidden.** The caller chooses `typed` or
   `literal` and the server never substitutes; every write reads back
   what Google stored and names each value it changed.
8. **Repeatability comes from the HTTP method.** GET and PUT may be
   retried; a POST only where the call site says why. `values.append` and
   `batchUpdate` duplicate data when repeated and are never retried on an
   ambiguous failure.
9. **Own wire types, raw REST.** Do not import `google.golang.org/api`;
   extend `internal/gsheets`. No code is imported from any other project.
10. **Destructive tools are unregistered** unless
    `GSHEETS_ENABLE_DESTRUCTIVE=true`, and each still needs
    `confirm: true` on the call.
11. **Branches and commits.** `main` is released code and is never pushed
    to directly, release commits included. Work on a short topic branch.
    Commit at the end of every phase with a message that says what and
    why. Pushing, opening the pull request and merging are the
    maintainer's.
12. **Verify against the discovery document or a live probe** before
    adopting a convention, and record the verdict in the evidence log in
    `docs/architecture.md` §18. A reference page's prose is not evidence.

## Where things go

- `cmd/google-sheets-mcp/` — subcommands and server wiring.
- `internal/config/` env plus bound flags; `internal/credentials/`
  keyring → file → env; `internal/userconfig/` non-secret profile state;
  `internal/auth/` loopback OAuth and the token source.
- `internal/gsheets/` wire types; `internal/gapi/` the raw REST client,
  with `sheetstest/` the in-memory Sheets used by tests.
- `internal/a1/` A1 notation and range conversion, no network;
  `internal/grid/` the server's view of a rectangle and its checkpoints;
  `internal/render/` text output; `internal/plan/` typed ops into the
  `batchUpdate` union, and the write guards; `internal/service/`
  orchestration and policy; `internal/tools/` the MCP tools;
  `internal/server/` SDK wiring and the schema dump.
- `scripts/gates/` the repository's own checks, as Go; `scripts/livesheet/`
  the live driver.
- `testdata/` synthetic fixtures and renderer goldens.

## Definition of done

`make check`, which is what CI runs: gofmt, `go vet` including the tagged
tests, golangci-lint, race tests with an 80% floor per package,
govulncheck, the licence allow-list, the leak scan, the workflow pin
check, a stdio smoke test, the schema diff, and the staleness gate over
README, `docs/` and CHANGELOG. Plus tests for new behaviour, `/simplify`
and `/code-review high` with findings resolved or written down, and a
look at the schema diff for anything breaking.

Green gates are not done. Anything touching the write path or an API
response shape gets a live run before it counts, and **the transcript is
read** — a sibling's driver twice reported success while its results were
wrong.

## Working across sessions

Each phase is one session, and the session is cleared between phases. On
a fresh session: read this file, the status line and §16, §17 and §17a of
`docs/architecture.md`, `CHANGELOG.md` under `[Unreleased]`,
`git log --oneline -20` and `git status`; run `make check`; then continue
the phase §16 names, on a topic branch. Commit at the end of the phase,
say which version was tagged, and stop.

## Writing

Plain and short, everywhere it lands — code comments, commit messages,
CHANGELOG, docs, tool descriptions. Lead with the outcome. One idea per
sentence. No narration of the investigation.
