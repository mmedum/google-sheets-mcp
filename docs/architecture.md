# Architecture — google-sheets-mcp

**Status: design (2026-09-05). There is no code yet.** Phase 0 (§16)
starts on an explicit go.

Everything here was checked against the Sheets API v4 discovery document
(`sheets.googleapis.com/$discovery/rest?version=v4`, revision 20260831),
the Sheets guides, and the issue trackers of the servers named in §1.
§18 is the evidence log, and about a third of it refutes something this
document said in an earlier draft. §2 is what the platform forces, §16
the phase plan, §17 the decisions that are not to be reopened.

This document is the plan. It is written so that whoever picks the work
up can start from the repository alone: read the status line, §16 for the
phase, §17 for anything still open, and begin.

## 1. Mission and scope

A production-grade, Go, stdio MCP server that lets Claude work **inside a
Google Sheet** the way a careful colleague does: read a range and know
where every value sits, write without destroying the formula underneath,
add and reshape sheets, format, sort and validate, and say afterwards
exactly what changed. Single binary, per-user OAuth against the user's
own Google account, Workspace or consumer.

**The repository is self-contained and meant to be distributed.** Every
deployer creates their own Google Cloud project and OAuth client; nothing
deployer-specific is baked into the code, the repository or the release
artifacts (§9, §12).

**Scope is one spreadsheet, every capability.** In: locating a
spreadsheet by title, creating one, and everything that happens inside
it — values, formulas, sheets, dimensions, formatting, named and
protected ranges, data validation, tables, conditional formatting,
sorting, and later charts, pivot tables and data sources. Out
(**decided**): file management — folders, moving, sharing, trashing,
copying files, revisions — which belongs to a server built on the Drive
API, and comment threads, which live in that API and belong there too.
A cell **note** is a Sheets field and is in scope; a **comment** is not
(§17.5).

Tool names are chosen so a client can run this server beside the Drive
and Docs ones without a collision: `search_spreadsheets` and `read_range`
here, where those say `search_files`, `search_documents`, `read_file`.

### Why build it (research summary, verified 2026-09-05)

- **Google's official Sheets MCP** (`sheetsmcp.googleapis.com/mcp/v1`,
  Developer Preview) exposes six tools: `get_spreadsheet`, `get_values`,
  `update_values`, `update_formulas`, `update_spreadsheet` and
  `insert_dimension`. `update_spreadsheet` is the raw `batchUpdate`
  passed straight through — Google's own page calls it "75+ update
  types", where the discovery document has 69 — so the model composes
  `GridRange` objects, with their zero-based half-open indices, by hand. It is remote HTTP, needs a
  **Web application** OAuth client, asks for four scopes
  (`spreadsheets`, `spreadsheets.readonly`, `drive.file`,
  `drive.readonly`), and needs Developer Preview enrolment. It solves the
  plumbing and none of the hard part: nothing guards an overwrite,
  nothing reports what Google coerced, and a read gives values without
  addresses.
- **The open-source servers** fail in the same places, per their own
  trackers. `xing5/mcp-google-sheets` (996 stars, Python): no way to find
  a spreadsheet by name at all, so the model asks the person to paste an
  id (#76); the docs and tests offer `Sheet1` as the default sheet name,
  which does not exist on a non-English account — Google names the first
  sheet in the owner's language, and the call fails with `Unable to parse
  range: Sheet1!A1:D3` (#94); caller text is interpolated into a Drive
  query unescaped, so an apostrophe detaches the `mimeType` filter and
  the search returns arbitrary files (#93); `list_sheets` returned a
  Python list that the framework serialised down to its first element, so
  every spreadsheet appeared to have one tab (#53); `print()` on stdout
  broke the JSON-RPC framing (#72, #73, #80); a storage-quota refusal was
  reported to the model as something the person must go and fix, when the
  account had 7 GB free (#75).
  `freema/mcp-gsheets` (TypeScript) shipped five tools that assigned a
  validator's internal `.shape` to `inputSchema.properties` instead of
  JSON Schema. A client validating against draft 2020-12 then rejects the
  **entire request**, so all forty-four tools died at once and the
  session stayed dead, reporting only `tools.15.custom.input_schema:
  JSON schema is invalid` — an array index that names no server (#138,
  #139).
  `taylorwilsdon/google_workspace_mcp` silently displayed only the first
  50 rows of a read (#955); the fix for that exposed the worse half, that
  an open-ended range like `A:Z` was **fetched whole into memory** before
  formatting and could take the process out, "even when older releases
  only *displayed* the first 50 rows" (#986). And `_column_to_index`
  rejected only the empty string, so `"A1"` returned index 10 and `"B2"`
  returned 37 — a plausible-looking answer pointing at column K and
  column AL (#959). Its open requests are for the things a spreadsheet
  server is actually for — formatting reads (#758), data validation
  (#892), named ranges (#893), native tables (#977), tab lifecycle
  (#1091), cell notes (#954).
- **No server in either list guards a write.** Writing values over a
  formula destroys the formula, and in every one of these servers it
  succeeds silently. That is the gap this server exists to close (§4.3).

### Non-goals

- Not a Drive client. Finding, sharing, moving, trashing and version
  history belong to a server built on the Drive API. This server calls
  Drive for one thing only: locating a spreadsheet by name (§7.1).
- Not a sync client. No `watch` or push channels: they need a public
  HTTPS endpoint. There is nothing to poll either — the Sheets API has no
  changes feed.
- Not multi-tenant hosted. Stdio only (**decided**); the composition root
  stays transport-agnostic so this can change later.
- Not Apps Script, not the Sheets UI add-on surface, not Connected Sheets
  administration.
- Not a calculation engine. Formulas are written and read as text;
  Google computes them.

## 2. Hard constraints from the platform

Verified against the discovery document (revision 20260831) and the
Sheets guides on 2026-09-05.

| Constraint | Consequence |
|---|---|
| **There is no write guard.** Sheets v4 has no `writeControl`, no `requiredRevisionId` and no ETag: the string does not occur in the discovery document. Docs has one; Sheets does not. | Concurrency is the server's problem. A read hands out a **checkpoint** over the values it read; a write may require it and re-reads to compare (§6.3). It narrows the window and cannot close it, and the tool descriptions say so. |
| **The whole surface is 17 methods.** One `spreadsheets.batchUpdate` carries a union of **69 request kinds**; `spreadsheets.values.*` carries the value paths. Power is in the union, not in the method list. | `internal/plan` compiles typed ops into union members. The model never writes a union member itself. |
| **A batch is atomic, and Google still will not promise the result.** The discovery document: "If any request is not valid then the entire request will fail and nothing will be applied … it is guaranteed that the updates in the request will be applied together atomically", followed by "Due to the collaborative nature of spreadsheets, it is not guaranteed that the spreadsheet will reflect exactly your changes after this completes … Your changes may be altered with respect to collaborator changes." | Ops are never split across batches: splitting costs quota and gives up atomicity. And the platform states in its own words why the checkpoint (§4.7) is best effort — Google does not claim a write survives a collaborator. |
| **`GridRange` is zero-based and half-open; A1 is one-based and inclusive.** The discovery document is explicit: "All indexes are zero-based. Indexes are half open … Missing indexes indicate the range is unbounded on that side." `Sheet1!A1:A1` is `startRowIndex: 0, endRowIndex: 1`. | Index arithmetic lives in `internal/a1`. The model sees A1 notation only, which is also what the person sees on screen. |
| **A1 quoting has three traps, and the third is a correctness bug rather than a nicety.** Single quotes are required for sheet names with spaces or special characters (`'My Custom Sheet'!A:A`); `A1` without a sheet means cell A1 of the **first visible sheet** while `'A1'` means the whole sheet *named* A1; and, verbatim from the concepts guide, "if there's a named range titled `Sheet1`, then `Sheet1` refers to the named range and `'Sheet1'` refers to the sheet". So an unquoted title is not merely untidy — **a named range silently shadows the sheet of the same name**. | `internal/a1` builds every range from a parsed sheet title and **always quotes it**, never by string concatenation, and a range is never sent with the sheet omitted. The shadowing case is a table test from the first commit, because it is invisible until somebody's spreadsheet has a named range called `Data`. |
| **Sheet names are not predictable.** Google names the first sheet in the account's language, so `Sheet1` is `Página1` elsewhere (§1, #94). Sheet titles are unique within a spreadsheet; `sheetId` is stable across renames. | `get_spreadsheet` is the first call and lists titles and ids. No tool defaults a sheet name, and no description offers `Sheet1` as an example. A URL's `#gid=` is parsed as the sheet (§6.1). |
| **Quota is per request, not per unit**, and refills every minute: **300 read and 300 write requests per minute per project, 60 of each per minute per user**. Over it, 429. "Each batch request, including any subrequest, is counted as one API request toward your usage limit." No daily cap. Charging for excess is "planned … later in 2026". | Batching is close to free and is therefore mandatory: one tool call is one API request wherever the API allows it. The limiters are set to the documented numbers rather than guessed (§11). |
| **A request may run for 180 seconds** before Sheets returns a timeout error. Google's own 503 advice is to combine updates with `batchUpdate`, "limit concurrent requests to 1 per second", prefer A1 notation on large spreadsheets, and use field masks. | Read deadline 60 s, write deadline 180 s to match the platform's own limit; one request per second in flight. |
| **`values.update` is a PUT; `values.append` and `batchUpdate` are POSTs.** Update is repeatable by construction. Append inserts rows, so repeating it duplicates data. `batchUpdate` repeated makes two sheets, two named ranges, two charts. | Repeatability is derived from the HTTP method (§11). Append and `batchUpdate` are never repeated after an ambiguous failure; the result is `[ambiguous_outcome]` naming the read that settles it. |
| **`values.update` does not clear.** "Values with no data are skipped": a shorter array leaves the old cells alone. Clearing is `""` per cell or `values.clear`. | `write_values` reports the region it actually covered, and a shrinking write says what it left behind. Clearing is its own tool. |
| **`USER_ENTERED` parses as if a person typed it**: "The input is parsed exactly as if it were entered into the Sheets UI", so `1-2` becomes a date, `007` becomes 7, `$100.15` becomes formatted currency, `=1+2` becomes a formula. `RAW` "isn't parsed and is inserted as a string". Only strings are affected either way: "non-string values like booleans or numbers are always handled as `RAW`". | The caller chooses (`input: typed \| literal`), the server never substitutes, and every write reports the values Google changed (§4.4). The coercion report has one job, so it compares only what was sent as a string. |
| **`values.append` writes after a *detected table*, not at the end of the sheet.** It searches the given range for a block of contiguous filled cells and writes after it; `OVERWRITE` (the API default) writes over whatever sits below that block. | `append_rows` defaults to `INSERT_ROWS`, reports the range Google actually wrote (`tableRange` and `updates.updatedRange`), and its description says how detection works. |
| **Empty trailing rows and columns are omitted** from a `values.get` response; interior gaps come back as empty strings. A ragged response is normal. | The renderer pads to the requested rectangle so a grid's addresses stay true, and says how far the data actually reached. |
| **Read-only scopes reach only three methods.** `spreadsheets.get`, `values.get` and `values.batchGet` accept `spreadsheets.readonly`/`drive.readonly`. Every other method — including `getByDataFilter`, `values.batchGetByDataFilter` and both `developerMetadata` reads — requires a **write-capable** scope. | In read-only mode, A1 is the only addressing there is: data filters and developer-metadata lookups are unavailable, and the design may not depend on them for reading (§6.4, §10). |
| **Limits**: 10 million cells or 18 278 columns (column ZZZ) per spreadsheet; a cell over 50 000 characters is dropped on import from Excel. | Budgets in §7.2 sit far below these. The column ceiling is what `internal/a1` validates against. |
| **Cells carry more than a value**: `userEnteredValue`, `effectiveValue`, `formattedValue`, `userEnteredFormat`, `effectiveFormat`, `dataValidation`, `note`, `hyperlink`, `textFormatRuns`, `chipRuns`, `pivotTable`, `dataSourceFormula`. An `ExtendedValue` is one of `numberValue`, `stringValue`, `boolValue`, `formulaValue`, `errorValue`; an error cell has a type such as `REF`, `NAME`, `DIVIDE_BY_ZERO`, `N_A`. | A read says which of these a cell has, because a formula and a number render identically. The overwrite guard is built on exactly this (§4.3). |
| **Developer metadata survives edits.** The discovery document: metadata "will remain associated at those locations as they move around and the spreadsheet is edited". It attaches to the spreadsheet, a sheet or a dimension range — **not to an arbitrary rectangle** — and reading it needs a write-capable scope. | The one durable anchor the API offers, and the answer to "come back to this row later" (§6.4). Phase 3, and never the only way to reach something. |
| **Tables are a first-class object** (`addTable`, `updateTable`, `deleteTable`; `tableId`, `name`, `range`, column types including dropdown, which requires a `ONE_OF_LIST` data validation rule). | `manage_range` covers them in phase 2. Whether `values.get` accepts a `Table1[Column]` reference is unverified and is spike C (§15). |
| **Protected ranges and merges are structure a write can violate.** A protected range refuses an edit from an account without permission; a partially covered merge is an error. | Both are read before a write and refused up front with the reason, rather than sent and rejected (§7.3). |
| Claude Code truncates tool results above 25 000 tokens and warns at 10 000. | Every read is budgeted in cells and characters, with a continuation (§7.2). |

### What the API cannot do (so we don't promise it)

Undo anything — there is no trash for a deleted sheet, cleared cells or a
dropped column, and version history is a UI feature the API cannot
restore from; guard a write against a concurrent edit; anchor a comment
to a cell (comments are Drive's, and a cell **note** is the Sheets
equivalent); read or set a cell's *displayed* value without also reading
its format; tell you why a formula produced an error beyond its error
type; search a spreadsheet server-side (there is no query endpoint —
`find_in_spreadsheet` reads and matches here); watch for changes.

## 3. Requirements distilled from other servers' failures

1. A spreadsheet is found by name, not only by id (#76). Ambiguity is
   listed, never resolved by taking the first match.
2. Sheet names are read from the spreadsheet, never assumed, and no
   description teaches the model a default that only exists in English
   (#94).
3. Values interpolated into a Drive query are escaped, and a test asserts
   an injected apostrophe cannot detach the `mimeType` filter (#93).
4. A result carries every row it says it carries; the shape is asserted
   in tests, not trusted to a framework (#53).
5. Reads are budgeted with an explicit continuation, and a truncated read
   says so. A silent 50-row ceiling is a wrong answer, not a small one
   (#955).
6. **The budget bounds the request, not the rendering.** Clamping what is
   displayed while fetching an open-ended range whole is the bug that
   survived the first fix (#986): the memory was already unbounded. Every
   read here resolves its window *before* the call and sends a finite
   range (§7.2).
7. Column-letter parsing is total and rejects: nonsense must produce an
   error, never a plausible index (#959).
8. Stdout carries only JSON-RPC, held by a test that fails on a direct
   `print`-equivalent rather than by discipline (#72, #73, #80).
9. Tool input schemas are valid JSON Schema, and a gate dumps and diffs
   them every build. One invalid schema is not one broken tool: the
   client rejects the whole request and every tool in the session goes
   with it (#138, #139).
10. An error keeps Google's own meaning: a quota refusal says it is a
    quota refusal and what it applies to (#75).
11. Every write says what changed, in what range, and what Google altered
    on the way in (taylorwilsdon #1031; the same class here).
12. Conventions are checked against the MCP specification, Anthropic's
    tool guidance and observed client behaviour (§18): flat strict
    schemas, snake_case verb_noun names, `[class] message` errors, dry
    run on writes, destructive tools unregistered unless enabled.

## 4. Core design bets

### 4.1 A1 notation is the contract

Every range the model sees or sends is A1, because that is what the
person sees on screen and what the formula bar speaks. `GridRange`
arithmetic, the zero-based half-open kind, stays in `internal/a1`. It is
the bet a document server has to make when it keeps UTF-16 offsets away
from the model, made easier by the platform: Sheets already has a public
address for every cell.

### 4.2 Every read shows addresses

A read renders a grid with column letters across the top and row numbers
down the side, so the model can write back to what it just read without
counting. Addresses are the handles; there is nothing to remember between
calls and nothing to go stale.

```
        A            B        C
   1 |  Region     | Q3      | Q4
   2 |  North      | 1200    | =B2*1.1
   3 |  South      | 980     | =B3*1.1
     rows 1-3 of 41; formulas shown; continue at row 4
```

### 4.3 A write never destroys what it cannot see

The guard is the reason this server exists. Before writing values the
server reads the target rectangle and refuses when it holds something a
value would silently destroy:

- **a formula** — refused unless `overwrite_formulas: true`, because a
  formula and its result render identically and losing one loses the
  computation;
- **anything at all**, when `overwrite: true` was not passed and the
  cells are not empty;
- **a protected range** this account may not edit — refused before the
  request is built, with the protection named;
- **a partially covered merge** — refused with the merged range named;
- a cell carrying a **note**, **data validation** or a **chip** is
  reported in the refusal, since those are invisible in a values read.

`suggest`-style modes do not exist here: Sheets has no tracked changes.
What stands in their place is `dry_run`, which reports the guard's
findings and the region that would change without sending anything.

### 4.4 Google's coercion is reported, never hidden

`USER_ENTERED` is how a person types, and it rewrites input: `1-2` is a
date, `007` is 7, `=A1+1` is a formula. The caller chooses `typed` or
`literal` and the server never substitutes one for the other. Every write
asks Google for the stored values back (`includeValuesInResponse` with
`responseValueRenderOption: FORMULA`) and diffs them against what was
sent, so the result names every cell Google changed. No other server
does this, and it costs nothing: the API returns the data in the same
response.

### 4.5 One tool call is one API request

Quota is counted per request and a batch counts once, so an ops-shaped
tool that compiles to a single `batchUpdate` is both faster and cheaper
than several small ones. Where a guard needs a prior read, that is one
read, taken with a field mask over the target range only.

### 4.6 Reads are budgeted in cells

`includeGridData` on a whole spreadsheet is never sent. A read names its
range, its cell budget and its character budget, and returns a
continuation. A 10-million-cell spreadsheet is readable one window at a
time and never crashes the process or the context window.

### 4.7 Checkpoints stand in for the missing revision guard

A read hands back `checkpoint: ck_7f3a91b2`, a hash over the values it
read. A write may pass `expect_checkpoint`; the server re-reads and
refuses with a diff on a mismatch. It is honest about what it is: a
narrow window, not an atomic guard, since Sheets offers none. Where a
person needs a real one, `manage_range` can protect the range.

### 4.8 Raw REST with our own wire types

`internal/gsheets` holds hand-written structs for the fields this server
reads. `google.golang.org/api` is not a dependency: it drags in gRPC,
OpenTelemetry and the cloud auth stack for a binary that needs JSON and
HTTP. The 69-member request union is written as typed builders, not as
free-form maps.

### 4.9 Results say what changed

Clients disagree about which half of a result they show the model: Claude
Code forwards only `structuredContent` when both are present, while
claude.ai and ChatGPT show the text. The usual answers are to send text
only, or to send both and hope. Neither is right here, because the
substance of a read is the *addressed grid* — the column letters and row
numbers that let the model write back without arithmetic — and a design
that puts the addresses in one half is a design that loses them on some
client, silently, until a task goes strangely wrong.

So no tool picks. **Every tool declares an output schema and returns both
halves, and the structured half carries the rendering in a `grid`
field.** A client showing structure gets the addressed grid; a client
showing text gets the same grid; neither is missing anything. They are
not the same bytes — the structured half also carries `range`,
`checkpoint`, `truncated` and `continue_from`, and the text half carries
those in its footer — and neither is a stringified copy of the other,
which is what the specification asks for and what SEP-1624 argues the
two halves should mean.

The raw arrays are **not** carried beside the grid by default: that would
be the same data twice inside one half. `format: json` (or `csv`, `tsv`)
puts the machine form in `rows` for a caller that wants it, and the
rendering is then the smaller field. Reads therefore have a schema for
the diff to hold, which a text-only read would not.

## 5. Module layout

```
cmd/google-sheets-mcp/    main: login / logout / status / doctor subcommands, server default,
                          --version, --dump-schemas
internal/config/          GSHEETS_* env with flags bound to the same names; typed enums; validated at start
internal/credentials/     refresh token: OS keyring → 0600 file under os.UserConfigDir() with a logged
                          warning → GSHEETS_REFRESH_TOKEN env override
internal/userconfig/      non-secret profile file: client_secret path, account email, token location, scopes
internal/auth/            loopback OAuth (127.0.0.1:<random>, PKCE), scope sets (full / read-only)
internal/gsheets/         Sheets API wire types: Spreadsheet, Sheet, GridData, CellData, ExtendedValue,
                          the 69 Request union members and their responses. No dependencies
internal/gapi/            raw REST client: client.go (retry, limiters, slog, host allowlist), sheets.go,
                          values.go, batch.go, drive.go (locate only), errors.go. No MCP imports
internal/gapi/sheetstest/ an in-memory Sheets behind httptest: sheets, cells with formulas and errors,
                          merges, protected ranges, validation, notes, batchUpdate over the union,
                          append's table detection, recorded coercion outcomes, error injection
internal/a1/              A1 notation: parse, format, quote, column letters ↔ indices, GridRange
                          conversion, bounds. Parsing only, no network
internal/grid/            the server's view of a rectangle: values, formulas, formats, notes, validation,
                          merges; checkpoints; the diff a write reports
internal/render/          text output: the spreadsheet card, the addressed grid, formatting summaries,
                          write reports, guard refusals; budgets and continuation
internal/plan/            typed ops → batchUpdate request union members; the write guards; dry-run reports
internal/service/         orchestration: resolve references, read, write, structure, formatting, policy
internal/server/          SDK wiring; schema dump through an in-memory client session
internal/tools/           one file per area: read.go, values.go, sheets.go, format.go, ranges.go,
                          transform.go, resources.go, tools.go
internal/version/
testdata/                 synthetic fixtures and golden outputs
docs/
scripts/gates/            the repository's own checks, as Go: coverage floor, schema diff, stdio smoke,
                          staleness, leak scan, workflow pins, pre-commit; never shipped
scripts/livesheet/        drives the built binary against a real account, redacting ids and addresses
```

Dependency direction runs one way: `tools` → `service` → `plan`/`grid` →
`gapi` → `gsheets`. Nothing under `internal/gapi` imports MCP, and
nothing imports `google.golang.org/api`.

**One language.** Everything the repository runs on itself is Go,
including the gates and the live driver, so a contributor needs one
toolchain and the code holding the gates shut is built, vetted, linted
and tested like the rest.

Dependencies, all pinned: `modelcontextprotocol/go-sdk` v1.7.0 (with
`google/jsonschema-go`), `golang.org/x/oauth2` v0.36.0,
`zalando/go-keyring` v0.2.8, `golang.org/x/time` v0.15.0. Nothing else.

Toolchain, current as of 2026-09-05 and matching the sibling servers:
Go 1.27.1 (`go 1.27.1` in go.mod), go-sdk v1.7.0 (v1.8.0-pre.2 exists;
Dependabot proposes it when final), golangci-lint v2.13.2, govulncheck
v1.7.0, go-licenses v1.6.0, gitleaks v8.30.1, GoReleaser v2.18.

**Scaffolding first.** The Makefile, the gates, `.golangci.yml`, the CI,
CodeQL and release workflows with pinned actions and scoped tokens,
`.goreleaser.yaml`, `.gitleaks.toml`, Dependabot, issue templates,
`.gitattributes`, `SECURITY.md`, `CONTRIBUTING.md` and
`docs/development.md` are written in Phase 0, before the first tool, so
every later commit passes the same gates.

`.gitignore` is the exception and ships **with this design commit**,
before there is anything to build. An editor writes its own settings into
the working tree while a session runs, so the first wildcard `git add`
takes whatever exists at that moment — which is how a stray file reached
a sibling project's public history, and this repository had such a file
in it before a line of code existed (§18).

## 6. Addressing

### 6.1 Referring to a spreadsheet

```
1AbC…                                                      an id
https://docs.google.com/spreadsheets/d/<id>/edit#gid=123    a URL; the gid names the sheet
https://docs.google.com/spreadsheets/d/<id>/edit?gid=123    the other spelling Sheets uses
"Q3 budget"                                                 a title, resolved through Drive
```

A title is resolved with a Drive query whose values are escaped (#93).
More than one match is `[ambiguous]` with each candidate's id, folder and
modification time, and the instruction to pass an id. No match names the
closest titles. A `gid` lifted from a URL is used as the sheet unless the
call names one.

### 6.2 Referring to a range

`sheet` takes a title or a `sheetId`; titles are unique inside a
spreadsheet, so a title is unambiguous once it is read rather than
guessed. `range` takes A1 (`B2:D40`), a bare column or row range
(`B:B`, `2:5`), or a whole sheet. The server always sends a range with
its sheet resolved **and quoted**; it never concatenates a title into a
string, because an unquoted title is shadowed by a named range of the
same name (§2).

Two things the reference is quieter about than this design would like.
`values.get` documents its `range` as "The A1 notation or R1C1 notation
of the range to retrieve values from" and says nothing about **named
ranges** — though the concepts guide's shadowing rule implies a bare name
resolves to one — so named ranges as a `range` argument are spike C
(§15), and until it runs no tool promises them. **R1C1 is accepted by the
API and is not offered here**: two ways to say the same thing is one more
thing for a model to get wrong, and A1 is what the person sees.

Errors carry the fix: `[not_found] no sheet named "Sheet1" in this
spreadsheet; it has "Página1", "Dados"` — the exact failure #94
describes.

### 6.3 Checkpoints

A read returns `checkpoint: ck_<12 hex>`, a hash over the spreadsheet id,
the resolved range and the canonical form of the values it read. Passing
`expect_checkpoint` on a write makes the server re-read that range and
compare before writing; a mismatch is `[conflict]` with a cell-level
diff. This is best effort by construction — the platform offers nothing
atomic — and the tool description says so rather than implying a
guarantee.

### 6.4 Developer metadata (phase 3)

Developer metadata is the only anchor Google keeps attached to a location
while the sheet is edited around it. It reaches a spreadsheet, a sheet or
a dimension range, not an arbitrary rectangle, and reading it needs a
write-capable scope, so it can never be the only way to find something.
It is offered as an optional durable label — "remember this row as
`invoice-totals`" — and every tool that accepts one also accepts A1.

### 6.5 Error classes

Errors are tool results, not protocol errors, in the form
`[class] actionable message`. The vocabulary is closed: `auth`,
`forbidden`, `not_found`, `ambiguous`, `invalid`, `blocked`, `conflict`,
`stale`, `unsupported`, `rate_limited`, `unavailable`,
`ambiguous_outcome`.

A gate holds it shut **from day one**, because this is the difference
between a vocabulary and a habit: it reads the declared classes and every
error literal in `internal/`, and fails on a class that is emitted
without being declared, on a class declared and never emitted, and on a
scan that read too few files to be looking at anything. In a sibling
repository the equivalent check found four classes that appeared in no
document at all, and the vocabulary had been living in comments in two
packages where nothing could disagree with it.

Two of these words are shared deliberately with the sibling servers and
must keep the same meaning across all of them, because a model should not
have to learn a class twice: `ambiguous` is a reference matching several
candidates, and `ambiguous_outcome` is a write whose result is unknown.
One asks the caller to choose, the other to go and look. `blocked` and
`forbidden` are likewise distinct: "nothing you do here will work" versus
"your role is wrong".
`blocked` is the write guard refusing something the API would have
allowed. `ambiguous` means a reference matched several things and the
caller must choose; `ambiguous_outcome` means a write may or may not have
landed and the caller must go and look. Spike E (§15) observes what
Sheets actually returns before the mapping onto these is finalised.

## 7. Reading and writing

### 7.1 Locating and describing

- `search_spreadsheets` finds spreadsheets by title or content through
  Drive (`mimeType = 'application/vnd.google-apps.spreadsheet'`), with
  `name`, `text`, `owner`, `modified_after`, `limit`, `page_token`. Each
  hit shows title, id, folder, modified time and owner. It is the fix for
  #76 and the only Drive call this server makes.
- `get_spreadsheet` is the **spreadsheet card** and the first call:
  title, id, link, locale, time zone, recalculation setting, and for
  every sheet its title, `sheetId`, index, type, grid size, frozen rows
  and columns, hidden state, tab colour and whether it holds a table,
  chart, pivot table or data source. Plus named ranges, tables, protected
  ranges and filter views with their A1 ranges. One `spreadsheets.get`
  with a field mask and **no grid data**, so it is cheap on a spreadsheet
  of any size.

### 7.2 Reading values

`read_range` takes `spreadsheet`, `sheet`, `range`, and returns an
addressed grid (§4.2).

- `show`: `values` (default), `formulas`, or `both` — `both` renders the
  formula under the value where they differ, which is the only way to see
  that a number is computed.
- `format`: `grid` (default), `json`, `csv`, `tsv`. `grid` is for
  reading, the rest for feeding elsewhere; all four go in the text block.
- `formatted`: whether Google's display formatting is applied
  (`FORMATTED_VALUE`) or raw values are returned (`UNFORMATTED_VALUE`,
  the default, because a currency symbol in a number the model may want
  to compute with is a trap).
- `max_cells` (default 5 000, maximum 50 000) and `max_chars` (default
  20 000, maximum 400 000) bound the answer. They are applied **before
  the call**: the window is resolved against the sheet's real extent and
  a finite range is sent, so an open-ended `A:Z` never becomes a whole
  column in memory. Clamping the rendering while fetching everything is
  the bug this design is avoiding, not repeating (#986). The footer names
  the range shown, the range that exists, and `continue_from`.
- `include_notes`, `include_validation` and `include_merges` annotate
  what a values read cannot show.
- Every read returns the `checkpoint` (§6.3).

`read_formatting` (phase 2) answers the other question: for a range, the
number format, font, colours, borders, alignment, wrapping, conditional
format rules and banding that apply, summarised per contiguous block
rather than per cell.

`find_in_spreadsheet` searches text or an RE2 regex across sheets and
returns A1 addresses with context and the matching cell's kind (value,
formula, note). There is no server-side search in the Sheets API, so this
reads with a field mask and matches here; its description says what it
costs and it takes the same budgets.

### 7.3 Writing values

Every write takes `dry_run`, optional `expect_checkpoint`, and the
acknowledgements the guard requires. The path is fixed:

1. Resolve the spreadsheet, the sheet and the range (§6).
2. Read the target rectangle with a field mask covering
   `userEnteredValue`, `effectiveValue`, `formattedValue`, `note`,
   `dataValidation`, merges and protected ranges. One request.
3. Run the guard (§4.3). A refusal names what is in the way, in A1
   addresses, and which acknowledgement would allow it.
4. Compare `expect_checkpoint` if given; mismatch is `[conflict]`.
5. Send one request: `values.update` (PUT) for one range,
   `values.batchUpdate` for several, `values.append` for an append,
   `batchUpdate` for anything structural. A structural batch carries
   `includeSpreadsheetInResponse` with `responseRanges` scoped to what it
   touched, so the state after the write comes back in the same request
   rather than costing another read.
6. Ask for the stored values back and diff them against what was sent
   (§4.4).
7. Return: the range written, cells changed, every coerced value, the
   formulas created, the new checkpoint, and a rendered view of the
   region.

- `write_values`: `values` as rows of scalars, or `tsv` for bulk text;
  `input: typed | literal`; `overwrite`, `overwrite_formulas`. A write
  shorter than the previous contents leaves the tail alone and the result
  says so (§2, `values.update` does not clear).
- `append_rows`: appends after the table detected in the range,
  `insert: rows` (default) or `overwrite`, and reports the range Google
  actually chose.
- `clear_values`: clears values and keeps formatting; needs `confirm`
  and reports the cell count and how many held formulas. Registered only
  with `GSHEETS_ENABLE_DESTRUCTIVE=true`, because nothing in Sheets can
  undo it (§9).

**Formulas that reach outside the spreadsheet.** Two different risks,
which an earlier draft of this document ran together:

- `IMPORTXML`, `IMPORTDATA`, `IMPORTHTML`, `IMPORTFEED`, `IMAGE` and
  `HYPERLINK` take **an arbitrary URL** — the reference for `IMPORTXML`
  is explicit that `url` is "The URL of the page to examine, including
  protocol". Google fetches it from its own servers, and a URL can carry
  the sheet's own data in its query string, which `ENCODEURL` makes
  convenient. Writing one is an outbound request with the spreadsheet's
  contents attached, made by a machine the person cannot see.
- `IMPORTRANGE` takes a **spreadsheet** URL, not an arbitrary one, so it
  exfiltrates nothing. Its risk is the other direction: it pulls data out
  of any spreadsheet the signed-in account can read into this one, which
  aggregates access rather than widening it, and it embeds that other
  spreadsheet's id in a string.

Both need `allow_external_formulas: true` on the call, for the two
reasons above rather than one, and every formula a write creates is named
in the result. Reads show formulas for the
same reason: an injected one should be visible, not hidden behind its
value.

### 7.4 Sheets and dimensions

- `create_spreadsheet`: `title`, optional `sheets` (titles), optional
  seed `values` for the first sheet, `locale`, `time_zone`. Returns the
  card. A spreadsheet is created in My Drive's root; moving it elsewhere
  is a Drive API operation this server does not offer, and the
  description says so.
- `manage_sheet`: `action` — `add`, `rename`, `duplicate`, `copy_to`
  (another spreadsheet, through `sheets.copyTo`), `hide`, `unhide`,
  `reorder`, `resize` (grid rows and columns), `freeze`, `tab_color`.
- `delete_sheet`: gated and needs `confirm`; the dry run reports how many
  non-empty cells, formulas and charts go with it, and the description
  points at `duplicate` first.
- `edit_dimensions`: `action` — `insert`, `move`, `resize`,
  `auto_resize`, `group`, `ungroup`, over rows or columns. The `delete`
  action is **absent from the schema** unless
  `GSHEETS_ENABLE_DESTRUCTIVE=true`, and needs `confirm: true` when it is
  there: deleting a column takes its data with it and nothing in Sheets
  brings it back. An action a model cannot see is one it cannot reach,
  which is the same rule as an unregistered tool, applied one level down.

### 7.5 Formatting and structure (phase 2)

- `format_cells`: ops over a range — `number_format`, `text_style`,
  `background`, `borders`, `alignment`, `wrap`, `merge`, `unmerge`,
  `clear_format`, `note`, and the conditional-format ops `rule_add`,
  `rule_update`, `rule_delete`. Borders take a shorthand
  (`1pt solid #cccccc`), as in the Docs server, rather than fifteen flat
  fields.
- `manage_range`: things attached to a range — `named_range`,
  `protected_range`, `data_validation`, `table`, `banding`, each with
  `add`, `update` and `delete`. A table's dropdown column carries its
  `ONE_OF_LIST` rule, which the API requires.
- `transform_range`: `sort`, `find_replace`, `trim_whitespace`,
  `remove_duplicates`, `text_to_columns`, `randomize`, `auto_fill`,
  `copy_paste`, `cut_paste`. Each reports the rows affected. These are
  the operations that move data without the caller naming its new
  address, so each one runs the guard over its destination.

### 7.6 Charts, pivot tables and data sources (phase 4)

`manage_chart` (`add`, `update`, `delete`, `move`), `manage_pivot_table`
and `manage_data_source` for Connected Sheets, each verified live before
it is designed in detail (§16). Slicers and embedded-object positioning
go with the charts.

## 8. Tool surface

snake_case verb_noun, no dots. Claude Code prefixes `mcp__<server>__`.
"Gated" means registered only with `GSHEETS_ENABLE_DESTRUCTIVE=true`, and
each gated tool also requires `confirm: true` on the call and sets
`_meta["anthropic/requiresUserInteraction"]`. One *action* is gated the
same way — `edit_dimensions delete` — and is absent from the schema
rather than refused at runtime (§7.4). `GSHEETS_READ_ONLY=true` registers
only the readOnly rows and requests read-only scopes.

| Tool | Purpose | Annotations | Phase |
|---|---|---|---|
| `get_spreadsheet` | The spreadsheet card: sheets, sizes, named and protected ranges, tables (§7.1) | readOnly | 0 |
| `read_range` | An addressed grid of values or formulas, budgeted, with a checkpoint | readOnly | 0 |
| `search_spreadsheets` | Find a spreadsheet by title or content through Drive | readOnly | 0 |
| `find_in_spreadsheet` | Text or regex search across sheets → A1 addresses with context | readOnly | 0 |
| `create_spreadsheet` | A new spreadsheet, optionally with sheets and seed values | — | 1 |
| `write_values` | Write a grid; guard, coercion report, checkpoint | — | 1 |
| `append_rows` | Append after the detected table; reports where it landed | — | 1 |
| `manage_sheet` | add, rename, duplicate, copy_to, hide, unhide, reorder, resize, freeze, tab_color | idempotent | 1 |
| `edit_dimensions` | insert, move, resize, auto_resize, group, ungroup rows or columns; `delete` only when destructive is enabled | — | 1 |
| `read_formatting` | Number formats, styles, borders, conditional rules over a range | readOnly | 2 |
| `format_cells` | Number format, styles, borders, alignment, merges, notes, conditional rules | — | 2 |
| `manage_range` | Named ranges, protected ranges, data validation, tables, banding | — | 2 |
| `transform_range` | sort, find_replace, trim, dedupe, text_to_columns, fill, copy/cut-paste | — | 2 |
| `clear_values` | Gated: clear a range's values, keeping formatting | destructive | 1 |
| `delete_sheet` | Gated: delete a sheet and everything on it | destructive | 1 |
| `manage_chart` | Charts and slicers: add, update, delete, move | — | 4 |
| `manage_pivot_table` | Pivot tables on a range | — | 4 |
| `manage_data_source` | Connected Sheets data sources: add, refresh, delete | — | 4 |

There is deliberately no bulk tool that spans spreadsheets: one
spreadsheet per call, so a wrong id costs one refusal rather than a
sweep.

**Resources** (phase 3). `gsheets://{spreadsheet}` is the card;
`gsheets://{spreadsheet}/{sheet}` is that sheet's used range as csv under
one 400 000-character budget. No static list (that would be a Drive
listing) and no subscriptions (the API has no push and no changes feed).

**Registration.** One `Kind` per tool — read, write, idempotent write,
destructive — decides the annotations, whether read-only mode leaves the
tool registered, whether the client is asked to involve a person, and
that the reply is rendered. Four rules kept by hand at twenty call sites
is four ways to be quietly wrong. `Kind` is an enum over *which world a
tool touches*, so it grows an axis the first time a tool touches a second
one; this server has no disk axis at all, since nothing downloads or
uploads and all content is inline.

Two things the registration layer enforces so a tool cannot forget them:
the output type is constrained to a renderer interface, so a tool with no
rendering does not compile; and `dry_run` is found by reflection rather
than declared per tool, so a tool that offers the flag cannot fail to
honour it. **Registration gates the tool; the service gates the act** —
`confirm` and the one gated action live in the service, where the caller
can be told why, which is what keeps `Kind` from becoming a matrix.

**Results.** Every tool declares an output schema and returns both
halves. The structured half carries the rendering in `grid` (reads) or
`summary` (writes), so the two can never disagree and no client can be
shown the half without the addresses in it (§4.9).

## 9. Confidentiality, security, safety

**Nothing internal leaves the user's machine or enters the repository**
(**decided**).

- Nothing deployer-specific enters the repository, and §9.1 says exactly
  what that means here and how it is enforced rather than trusted.
- The server talks only to Google: `sheets.googleapis.com`,
  `www.googleapis.com`, `oauth2.googleapis.com`, `accounts.google.com`,
  over HTTPS, checked against an allowlist **with the port** before any
  credential is attached. No telemetry, no update checks.
- Logs carry method, tool, outcome, duration and a truncated spreadsheet
  id. They never carry cell values, formulas, sheet or spreadsheet
  titles, ranges, search terms or addresses. Two routes make that harder
  than it sounds, and both are closed deliberately: a search term reaches
  a log inside a request URL, so transport errors are stripped of it; and
  **this server's own error messages quote the data back** — a
  `[not_found]` that lists the sheet titles which do exist is the right
  answer to give a model and the wrong thing to write to a log. A tool
  error is returned, never logged as its text; the log line carries its
  class. A test drives every registered tool at debug level against
  fixtures whose every value is unmistakable — a nonsense word, never
  "test" — and fails if any of it appears, refusals included, because a
  refusal is where a message is most tempted to quote what it refused.
  Its argument table is kept complete by a second test, and it asserts
  the **HTTP methods that reached the fake** rather than the list of
  calls somebody remembered to write: a guarantee stated over a surface
  decays every time the surface grows, and in a sibling repository this
  same test had quietly stopped covering eight newer tools, including the
  only ones taking an email address.
- Destructive tools are unregistered unless enabled **and** need
  `confirm: true`; annotations are hints a client may not trust, so every
  gate is server-side.
- The refresh token lives in the OS keyring, or a 0600 file with a
  warning on every use; `logout` revokes and deletes it.

| Risk | What limits it |
|---|---|
| Destroying a formula with a value | The guard refuses a write over any formula without `overwrite_formulas`, and `both` shows formulas under values so the model can see them first (§4.3). |
| Destroying data with no way back | Sheets has no trash and the API cannot restore version history. Clearing and deleting are gated **and** need `confirm`; the dry run counts what would be lost; `duplicate` is offered first. |
| A silent, wrong conversion | Every write reports what Google coerced, read back from the write's own response (§4.4). |
| Overwriting someone else's concurrent edit | Checkpoints (§6.3), stated as best effort; `manage_range` can protect a range, which is the only real guarantee. |
| Data leaving through a formula | `IMPORTXML`, `IMPORTDATA`, `IMPORTHTML`, `IMPORTFEED`, `IMAGE` and `HYPERLINK` take an arbitrary URL, which Google fetches from its own servers with whatever the sheet puts in the query string. Creating one needs `allow_external_formulas: true`, and every formula a write creates is named in the result (§7.3). |
| Data arriving from a spreadsheet the person did not name | `IMPORTRANGE` reads any spreadsheet the signed-in account can open, into this one. Same acknowledgement, different reason: it aggregates the account's access rather than widening the file's (§7.3). |
| Instructions hidden in cells | Reads return content as data and the server never acts on it. Formulas are shown rather than resolved, so an injected instruction is visible. |
| Acting on the wrong spreadsheet or sheet | Ids are the contract; a title matching several spreadsheets is `[ambiguous]` with candidates; sheet names are read, never assumed (#94); no tool defaults a sheet. |
| Quota exhaustion | One request per tool call, limiters set to the documented 60/minute per user, batch compilation, and budgeted reads (§11). |
| A stalled request hanging the server | Every attempt, token refresh and batch runs under a deadline; the token source carries its own client, because the oauth2 refresh otherwise runs on `http.DefaultClient`, which has none. |
| Secrets or someone's data in the repository | §9.1: gitleaks and the leak gate in pre-commit and CI, synthetic fixtures, and a live driver that works only in a spreadsheet it filled itself. |

### 9.1 What may never enter the repository, and what stops it

This server is built by running it against a real spreadsheet, so every
one of the following reaches a terminal during ordinary development — a
`doctor` run, a failing test, a pasted error — and from there it is one
careless copy into a fixture, a golden file, an issue or a commit
message. A rule kept by remembering to look is not a rule.

Never, in code, docs, fixtures, goldens, transcripts, issues, pull
requests, commit and tag messages, or logs:

- **Spreadsheet ids and the URLs carrying them** —
  `docs.google.com/spreadsheets/d/<id>`, with or without `#gid=`.
- **Sheet and spreadsheet titles**, named range and table names, and
  developer metadata keys and values. These are business vocabulary:
  a tab called "Q3 pipeline — <customer>" names a customer.
- **Cell values of any kind**, and the notes beside them. This is the
  payload, and it is the thing this server exists to touch.
- **Formulas**, which are worse than values: `IMPORTRANGE` embeds
  *another* spreadsheet's id inside a string, so a formula copied into a
  fixture leaks a second document nobody was thinking about.
- **Account addresses, Cloud project ids, OAuth client ids and secrets,
  refresh tokens**, and the 21-digit Google account id.
- Any reference to another project, repository, account or machine the
  maintainers use.

Two gates, because they catch different things. **gitleaks** covers
credentials, in the pre-commit hook and in CI, with rules for Google
client ids, client secrets and refresh tokens on top of its defaults.
Two things about it are known in advance rather than discovered: an
exception goes in `.gitleaks.toml` scoped to the literal value, **not**
as an inline `gitleaks:allow` comment, because the CI scan walks a pull
request's commits and a comment added later never clears the commit that
introduced the line; and the two scanners will flag each other's
fixtures, since the allowlist has to quote the fabricated credential it
permits, which is then a credential-shaped string sitting in a tracked
file. Both need a per-line marker, and each exception says which scanner
it is for.
**`scripts/gates leaks`** covers identifiers and data, which are not
secrets and therefore pass gitleaks untouched — a spreadsheet id in a
fixture leaks what somebody works on rather than a password. Its design
is fixed here:

- **Every rule is an allow-list.** A deny-list naming the domain, the
  organisation or the account to watch for would itself be the
  disclosure.
- **A synthetic fixture says so in its own text.** Ids carry a marker
  (`…Fixture…`) that a base64url id issued from random bytes cannot
  contain by chance, so the exception list does not grow with every test.
  The marker is the rule that scales; the allowlist is the part that
  rots.
- **Exceptions are listed by value, each with its reason**, and a test
  asserts every entry *has* one. An entry without a reason is how a gate
  quietly stops working.
- **The shape rule covers every identifier the APIs use, not the one that
  came to mind.** A spreadsheet id is base64url with letters and digits,
  so a rule demanding a capital and a digit catches it — and misses the
  purely numeric ones beside it: the 21-digit Google account id, and a
  Drive permission id, which is twenty digits with no letter. A sibling
  shipped exactly that hole and a numeric id reached a transcript before
  it was found. Sheets' own small integers (`sheetId`, `tableId`,
  `metadataId`) identify nothing outside their spreadsheet and are not in
  scope; the two above are.
- **A committed binary is a finding**, not something to scan: its symbol
  table buries the line that matters.
- **Findings are abbreviated** in the gate's own output, so CI logs and
  terminals do not reprint the leak.
- **"Found nothing" and "looked at nothing" must not print the same
  sentence.** A scan that reads zero files fails — and so does every
  other gate here that counts what it read. A guard that has caught
  nothing is working; a guard that looked at nothing reports the same
  thing and is not.
- **`leaks history` walks every blob and every commit and tag message**,
  from the message down — the author and tagger lines above it are git's
  own, and an identity is public in every repository by construction. It
  runs before the repository is made public and after anything is removed
  from it in a hurry, because a leak deleted from the tip is still in the
  log.

**Fixtures are generated, never recorded.** `testdata/` and the goldens
are built from `sheetstest`, whose content is invented — column headings
from a lorem vocabulary, numbers from a seeded generator. No fixture is
ever a spreadsheet exported, copied or trimmed down. The rule is
structural rather than diligent: there is no path by which a real value
reaches `testdata/`.

**The live driver never reads a spreadsheet it did not write.** It
creates a scratch spreadsheet, fills it with its own synthetic data,
exercises every tool inside it, and trashes it. So the values in a
transcript are the driver's own and are safe to paste into a commit
message. Redaction of ids, links and addresses is a second line of
defence and lives in the **print helper only** — a redaction on the read
path corrupts a value the driver feeds back into the next call, which is
a mistake a sibling project made and had to undo. Pointing the driver at
an existing spreadsheet is not offered.

**The evals seed their own data too** (phase 3), for the same reason:
a task is scored against a spreadsheet the harness built.

## 10. Auth, config, process model

- **Setup**, in the order `doctor` checks it: create a Cloud project →
  enable the **Google Sheets API** and the **Google Drive API** (the
  latter only for `search_spreadsheets`) → consent screen (**Internal**
  for Workspace; **External + Testing** for consumer accounts, which
  means re-running `login` weekly) → add the scopes → create a **Desktop
  app** OAuth client → download `client_secret.json` → `login` →
  `doctor`.
- **No Developer Preview enrolment is needed.** Everything this server
  uses is GA. Google's own Sheets MCP is in the preview programme and is
  a comparison (spike D), not a dependency.
- **Scopes.** Full: `spreadsheets` plus `drive.readonly`. Read-only:
  `spreadsheets.readonly` plus `drive.readonly`. `drive.file` is not
  requested: it reaches only files the app created or the person opened
  through the Picker, which a stdio server cannot show. Full `drive` is
  not requested either — this server does not manage files. A
  missing-scope 403 becomes `[forbidden] missing scope …; re-run
  google-sheets-mcp login`.
- **Read-only mode is a real restriction, not a label**: with
  `spreadsheets.readonly` the API itself refuses `getByDataFilter`,
  the data-filter value reads and both developer-metadata reads (§2), so
  those paths are unavailable and the tools that would use them are not
  registered.
- **OAuth flow**: Google's documented desktop flow, loopback
  `127.0.0.1:<random port>` with PKCE. **Token storage**: OS keyring →
  0600 file with a stderr warning → `GSHEETS_REFRESH_TOKEN` override.
  **Profiles** (`GSHEETS_PROFILE`) keep separate secrets, tokens and
  accounts under `GSHEETS_CONFIG_DIR`.
- **Settings** (`GSHEETS_*` env, each with a bound flag): `PROFILE`,
  `CLIENT_SECRET`, `REFRESH_TOKEN`, `CONFIG_DIR`, `LOG_LEVEL`,
  `LOG_FORMAT`, `READ_ONLY`, `ENABLE_DESTRUCTIVE`, `MAX_CELLS`,
  `MAX_CHARS`, `HTTP_TIMEOUT` (per read attempt, default `60s`),
  `WRITE_TIMEOUT` (default `180s`, matching Sheets' own processing
  limit). Operational flags: `--version`, `--dump-schemas`.
- **Startup**: warm the token off the startup path; on failure log and
  **keep serving**, with `[auth]` on every tool, because a server that
  exits shows the person "failed to connect" and the model never learns
  why. `doctor` checks credentials, the token exchange, granted scopes,
  the Sheets API, the Drive API, and — given a spreadsheet reference —
  `spreadsheets.get` and a one-cell read.
- **Transport**: stdio (**decided**).

## 11. Reliability

- **Retries.** Reads retry on 429, 5xx and network errors with
  exponential backoff and full jitter, capped at 30 s, five attempts,
  honouring `Retry-After`; Google's own guidance for both 429 and 503 is
  truncated exponential backoff. Repeatability is **derived from the HTTP
  method**: GET and PUT may be repeated, a POST only when the call site
  says why. So `values.update` retries and `values.append` and
  `batchUpdate` do not — appending twice duplicates rows and a repeated
  `addSheet` makes two sheets. A refusal to *begin* (429, or a 503 before
  any response) proves nothing was applied and is repeatable; a 5xx after
  the request began is `[ambiguous_outcome]` naming the read that settles
  it.
- **POSTs that only read.** Three of them: `spreadsheets.getByDataFilter`,
  `values.batchGetByDataFilter` and `developerMetadata.search`. Beside
  them sit two POSTs that do write — `values.batchClearByDataFilter` and
  `values.batchUpdateByDataFilter` — so the method alone is wrong in one
  direction and a name pattern is wrong in both: "get" reads like a read
  on a method that is a POST only because a filter is too long for a
  query string. Deriving write-ness from the method would put a read on
  the write limiter, refuse to retry it, and block it under the dry-run
  context — three wrong answers from one inference. So the method decides
  by default, an explicit `readOnly` flag on the request marks the
  exceptions, and a test over this package's syntax tree allows that flag
  only on those three methods **by name**. The flag is the one field here
  that can be set *wrongly* rather than merely forgotten: absent from a
  search it costs speed, present on a real write it lets that write run
  during a preview.
- **Limiters.** Two buckets at the documented per-user quota: 60 reads
  and 60 writes per minute, with a small burst, and at most one request
  in flight at a time, which is Google's own advice for avoiding 503s.
  Taken inside the retry loop, once per attempt.
- **Batching.** Every ops tool compiles to one `batchUpdate`; a batch and
  all its subrequests count as one request against quota, so the design
  never splits work that could travel together.
- **Deadlines.** `GSHEETS_HTTP_TIMEOUT` (60 s) per read attempt;
  `GSHEETS_WRITE_TIMEOUT` (180 s) for a batch, because Sheets itself
  allows a request to run that long. The token refresh runs under a
  bounded client of its own.
- **Memory.** Reads are windowed by cells with field masks; nothing holds
  a whole spreadsheet.
- **Caching.** Spreadsheet metadata coalesced for 5 s by id; the sheet
  title → id map for the same window; writes invalidate. Never for
  correctness.
- **Targets, measured by benchmarks against the fake in phase 3.** The
  card in one request; a 5 000-cell read in one request rendered under
  20 ms; a guarded write in two requests; a 10 000-row grid rendered with
  a flat memory profile.

## 12. Distribution and setup

- **Artifacts.** GoReleaser builds linux, darwin and windows on amd64 and
  arm64 from a `v*` tag; `checksums.txt` signed with a keyless Sigstore
  certificate (a cosign 3 bundle); an SBOM per archive; a build
  provenance attestation; `-trimpath` and `mod_timestamp` so a rebuilt
  tag is byte-identical; never a draft. `go install …@latest` is the
  second path, with the version from `debug.ReadBuildInfo()` since
  `go install` applies no ldflags.
- **Branches and pull requests.** `main` is released code and is never
  pushed to directly, release commits included. Work on a short topic
  branch; the maintainer pushes, opens the pull request and merges once
  CI is green on three platforms. Tags are pushed one at a time — GitHub
  drops tag events past the third in one push.
- **Workflow hardening.** Every action pinned to a full commit SHA with
  the version in a trailing comment — GitHub's hardening guide is
  explicit that "pinning an action to a full-length commit SHA is
  currently the only way to use an action as an immutable release", and
  it now also offers repository and organisation policies that *require*
  it. **And every tool an action installs pinned beside it**, with a
  comment saying which half is load-bearing: `cosign-release`,
  `syft-version`, and the GoReleaser version as an exact string. Never a
  `~>` range. `goreleaser-action`'s own default is `~> v2`, which floats
  across an entire major line, and narrowing one to `~> v2.18.0` is still
  a range: a sibling once recorded that narrowing *as the fix* and was
  still floating afterwards, which is why this is a gate and not a habit. `permissions: contents: read` at the top of every workflow,
  raised per job; `persist-credentials: false` on checkouts; CodeQL on
  push, pull request and weekly; Dependabot weekly for gomod and actions.
  A gate asserts all of it, because a comment cannot hold a version shut.
- **CI shape, from four failures that cost sibling projects weeks.**
  `shell: bash` on every job, because PowerShell read `-coverprofile=cov.out`
  as a file named `cov` and the suite carried on. No gate conditioned on
  `runner.os`: a platform in the matrix that no gate reads is a platform
  nobody is testing, which is how the previous bug survived — the
  coverage step was Linux-only. `cancel-in-progress` excludes `main`
  (`github.ref != 'refs/heads/main'`), or a merged commit carries a
  killed run and a later bisect walks a green history with a hole in it.
- **Documentation set.** README, this file, `docs/configuration.md`,
  `docs/security.md`, `docs/development.md`, `CONTRIBUTING.md`,
  `SECURITY.md`, `CHANGELOG.md` (Keep a Changelog; the release workflow
  lifts the section verbatim). Apache-2.0.

## 13. Testing

- **Unit, table-driven, no network.** `internal/gapi/sheetstest` is an
  in-memory Sheets behind `httptest`: sheets with grid data, formulas,
  error values, merges, protected ranges, data validation and notes; the
  `batchUpdate` union; append's table detection; `values.update`'s
  skip-don't-clear semantics; ragged responses with trailing empties
  omitted; error injection (429, 5xx, a cut connection, a 180-second
  timeout).

  **The fake does not invent Google's parser.** `USER_ENTERED` coercion
  is *recorded* from spike A and replayed, never simulated. A fake built
  from the documentation inherits the documentation's errors, and a
  sibling project shipped a search bug that its entire test suite agreed
  with (§18).
- Specific tables: every A1 shape and every malformed one, including
  column letters past ZZZ and the `'A1'`-versus-`A1` trap; GridRange
  conversion round trips; the guard matrix (empty / value / formula /
  merge / protected / validated × acknowledgements); checkpoint
  mismatch; the coercion diff; append with and without a gap below the
  table; budgets and continuation; Drive query escaping (#93).
- **Fixtures and goldens are generated from `sheetstest`, never
  recorded** from a real spreadsheet (§9.1). The vocabulary is invented
  and the numbers come from a seeded generator, so there is no path by
  which somebody's data reaches `testdata/`.
- **Coverage floor** 80% per package, with the list **derived** from
  `go list ./internal/...` and exemptions named with reasons. A
  hand-written list silently stops covering new packages.
- **Schema dump and diff** in CI against the last tag; a removed tool or
  field, or a new required field, is breaking.
- **Stdio smoke** without credentials, closing stdin the moment the last
  message is written, asserting a clean exit code — the SDK reports a
  closed session as JSON-RPC -32004 with the EOF only in the message
  text, so `errors.Is(err, io.EOF)` does not catch it and a sleeping
  smoke test never sees the bug.
- **Identifier scan** (`scripts/gates leaks`, in the pre-commit hook and
  in CI): the tree, and on demand every blob and message in the history.
  §9.1 is its specification, including why every rule is an allow-list.
- **Live driver** `scripts/livesheet`: every tool and every op against
  one scratch spreadsheet it **creates, fills with its own synthetic
  data**, and trashes, so a transcript carries nobody's values (§9.1);
  ids, links and addresses are replaced by placeholders in the print
  helper. A phase is not done until it has run **and its transcript has
  been read** — a sibling's driver twice reported "all calls behaved as
  expected" while three results were wrong, because it checked whether
  calls succeeded, not whether they told the truth. The mechanised half
  of that fix is inherited rather than rediscovered:

  - **Every step carries `expect_error` and a `why`.** An expected
    refusal proves as much as a success, and a success where a refusal
    was expected is a failure. That alone caught two of the three.
  - **A result that asserts state is read back and compared.** All three
    wrong results were a result describing the state from *before* the
    write.
  - **Anything eventually consistent is polled, and says which it saw.**
    Here that is Drive's index: a spreadsheet created a second ago may
    not be findable by `search_spreadsheets` yet, and one read cannot
    tell indexing lag from a broken search. The driver retries, and if it
    never sees the new file it prints a loud line saying this run cannot
    tell the two apart, rather than printing an empty result and moving
    on.
  - What no driver catches is a result that is internally consistent and
    wrong. That is what the evals are for, which is why they score the
    trace as well as the end state: a task can be completed by a model
    that guessed a range and was lucky.
- **Agent evals** (phase 3), about fifteen tasks through `claude -p` with
  only this server's tools: find a spreadsheet by name; total a column
  and write the total; add a sheet and copy a filtered subset into it;
  append a row without disturbing what is below; fix a formula; format a
  header row; sort by a column; add a dropdown; refuse to overwrite a
  formula and say why; refuse an unasked external formula; handle a
  non-English sheet name; read the tail of a 40 000-row sheet. Scored on
  the end state read back through the server and on the trace: no
  invented ranges, no guessed sheet names, no acknowledgement passed
  unasked.

  One of them is an **A/B rather than a task**: the same reads with the
  rendering inside the structured half, and with rows alone, counting the
  tool calls needed to complete something that writes an address back.
  §4.9 is reasoning, not measurement — the sibling number behind the
  original rule was measured on *prose* reads on Claude Code 2.1.259 and
  has not been re-measured, and a grid is a different claim from a
  paragraph. The harness counts tool calls already, so this costs one
  run and settles what the reasoning currently assumes.
- **Benchmarks** (`make bench`): A1 parsing, grid rendering of 10 000
  rows, the guard over a large rectangle.
- **CI on Linux, macOS and Windows** from the first commit, with every
  gate reading every platform (§12): a check that runs on one OS while
  the matrix claims three is worse than no check, because the matrix is
  what people trust. `.gitattributes` pins LF so goldens compare byte for
  byte.

## 14. Confirmed decisions and their consequences

| Decision | Consequence in the design |
|---|---|
| Deployer-owned Cloud project and OAuth client; nothing internal in the repository | §9, §10, §12 |
| Inside the spreadsheet only; files, sharing, revisions and comments are the Drive server's | §1, §7.4, §17.5 |
| A1 is the contract; GridRange math is server-side | §4.1, §6.2, `internal/a1` |
| Every read shows addresses | §4.2; the grid renderer, and no handle memory to go stale |
| A write never destroys what it cannot see | §4.3; the guard, its acknowledgements, and the dry run |
| Google's coercion is reported, never hidden | §4.4; `includeValuesInResponse` on every write |
| Checkpoints stand in for the absent revision guard, and say they are best effort | §4.7, §6.3 |
| One tool call is one API request; ops compile to one batch | §4.5, §11 |
| Destructive tools gated **and** confirmed, because Sheets has no undo | §8, §9 |
| External-fetch formulas need an explicit acknowledgement | §7.3, §9 |
| Raw REST, own wire types, no generated client | §4.8, §5 |
| Every tool returns both halves, and the structured half carries the rendering, so no client is shown the half without the addresses | §4.9, §8, §18 |
| Stdio only | No HTTP auth design |
| Phases end in a tagged release and wait for an explicit "go" | §16 |

## 15. What must be verified live

Everything in §2 comes from the discovery document or a guide. Seven
things have behaviour the reference does not pin down, and each runs at
the start of the phase that builds the code depending on it — a spike
whose subject does not exist yet is a spike that gets skipped and then
forgotten. Results go into §18.

- **A. The coercion matrix** (phase 1, before `write_values`): what
  `USER_ENTERED` does to `1-2`, `007`, `=1+2`, `$100.15`, `TRUE`,
  `'0123`, `2026-09-05`, a 60 000-character string, and what `RAW` does
  to the same. The recorded answers become the fake's replay table.
- **B. Append's table detection** (phase 1): where `append_rows` writes
  on a sheet with a gap below the block, with `OVERWRITE` and with
  `INSERT_ROWS`, and what `tableRange` reports.
- **C. Range syntax** (phase 0): whether `values.get` accepts a named
  range (its reference documents only A1 and R1C1) and a table reference
  (`Table1[Column]`); that a quoted title behaves as documented; that a
  named range really does shadow a same-named sheet when the title is
  unquoted, which is the one trap here that fails silently rather than
  loudly; and what a bad range's error actually says.
- **D. Google's Sheets MCP** (any phase, needs preview enrolment):
  connect once, dump its tools and schemas, record what `get_values`
  returns for a formula and an error cell. Keeps §1 honest. Nothing
  depends on it.
- **E. Error shapes** (phase 0): the status, status string and message
  for a missing spreadsheet, a missing sheet, a bad range, a protected
  range, a read-only scope attempting a write, and a 429. The
  troubleshooting page documents only 400, 500 and 503, so the class
  mapping in §6.5 is otherwise built from the Drive API's documented
  error vocabulary rather than from Sheets itself.
- **F. Checkpoint economics** (phase 1): whether Drive's `files.get`
  `version` field changes on every Sheets edit, which would give a
  cheaper spreadsheet-wide staleness check than re-reading a range.
- **G. Size behaviour** (phase 2): what a write past 10 million cells or
  column ZZZ returns, and how a 50 000-character cell round-trips.

## 16. Delivery phases

Each phase ends in a tagged release and waits for an explicit "go". Each
phase is one session: the next one starts from this repository alone.

**Phase 0 — skeleton, gates and reading (v0.0.1).** Scaffolding (§5,
§12): Makefile, golangci, govulncheck, go-licenses, gitleaks, goreleaser,
CI, CodeQL and release workflows, Dependabot, templates, `CONTRIBUTING.md`.
`login/logout/status/doctor`; `config`, `credentials`, `userconfig`,
`auth`; `gapi` with `spreadsheets.get`, `values.get`, `values.batchGet`
and the Drive locate call; `internal/a1` complete with its table tests;
`grid` and `render` for the card and the addressed grid; `sheetstest`
with sheets, cells and ranges; tools `get_spreadsheet`, `read_range`,
`search_spreadsheets`, `find_in_spreadsheet`; the gates green on three
platforms — smoke, schema dump and diff, staleness, leak scan, workflow
pins, coverage floor, and the closed error-class gate (§6.5), which is
built now rather than as a later cleanup because it costs an afternoon
and is the difference between a vocabulary and a habit; spikes C and E;
a live run whose transcript is read.

**Phase 1 — writing values and sheets (v0.1.0).** Spikes A, B and F
first. Then `create_spreadsheet`, `write_values`, `append_rows`,
`manage_sheet`, `edit_dimensions`, and the gated `clear_values` and
`delete_sheet`; the guard, the coercion diff, checkpoints and `dry_run`;
`sheetstest` grows the write paths and the recorded coercion table;
`scripts/livesheet` covers every tool.

**Phase 2 — formatting and structure (v0.2.0).** Spike G first. Then
`read_formatting`, `format_cells`, `manage_range`, `transform_range`;
the union builders for formatting, validation, protection, tables and
conditional formatting; the guard extended over the transforms'
destinations.

**Phase 3 — resources, anchors, evals, performance (v0.3.0).**
`gsheets://` resources; developer metadata as durable anchors (§6.4); the
agent evals and the fixes they force; `make bench` and the numbers in
§11; `/simplify` and `/code-review high` over the tree, with findings
resolved or recorded in §17a.

**Phase 4 — charts, pivots, data sources (v0.4.0).** `manage_chart`,
`manage_pivot_table`, `manage_data_source`, each verified live before it
is designed in detail. Then the discovery document is diffed against what
the client calls, and every one of the 69 union members is either used or
listed here as deliberately out.

**v1.0.0** waits for use in anger and a further eval round with a second
client.

### Closing a phase

1. `make check` green on three platforms; the live driver run **and its
   transcript read**; in phase 3 the evals; the review passes done, with
   findings resolved or recorded in §17a with the reason.
2. The status line updated, the phase marked done in §16 with the date,
   what was verified added to §18, the `CHANGELOG.md` entry written.
3. Released per `docs/development.md`: a "Release N.N.N" commit on a
   topic branch, a pull request, CI green, merge, then the tag pushed on
   its own.
4. Stop and wait for "go".

## 17. Open decisions

None. The seven below were decided on 2026-09-05 and are kept here so
they are not reopened.

1. **A1 everywhere.** The model never sees a `GridRange`. Where the API
   needs one, `internal/a1` builds it.
2. **The guard is on by default.** Overwriting anything non-empty needs
   `overwrite: true`; overwriting a formula needs `overwrite_formulas:
   true` as well. Two acknowledgements rather than one, because the
   formula case is the one that loses work invisibly.
3. **`input` is explicit and never substituted.** `typed` is
   `USER_ENTERED`, `literal` is `RAW`. The default is `typed`, because
   that is what a person typing would get, and the coercion report is
   what makes it safe.
4. **Checkpoints, not locks.** Best-effort staleness detection, stated as
   such. Protected ranges are offered as the real guarantee.
5. **Comments are out; notes are in.** A cell note is a Sheets field. A
   comment thread is a Drive resource, reachable for any file through a
   server built on the Drive API. Revisit in phase 4 only if
   cell-anchored comments turn out to be reachable and useful.
6. **No `drive.file`, no full `drive`.** `spreadsheets` plus
   `drive.readonly`; file management belongs to the Drive server.
7. **Go directive `go 1.27.1`**, matching the sibling servers.

## 17a. Deferred cleanups

Nothing yet. The phase-0 review passes fill this in.

## 17b. Deviations from the shared Go MCP server standard

The shared standard the sibling Go MCP servers run on was adopted here on
2026-09-05, and re-read on 2026-09-06 after it gained a preamble and a
confidentiality paragraph. Where this server differs, the difference is a
decision rather than a drift.

**Two deviations, and one that was never one.** An earlier version of
this table claimed a third: that the standard forbids any identifier in a
log while this server keeps a truncated spreadsheet id at debug level. It
does not forbid it. The standard's §4 says the rule is that "a log must
not identify or reconstruct the subject, not that identifiers are banned
outright", and names a truncated id that cannot be looked up as meeting
the intent. Writing down a deviation that is really compliance is the
same failure as missing a real one — it invites somebody to "fix" a
choice nobody made — so the row is gone, and §9's logging rule is the
standard's rule, held by a test.

| The standard says | Here | Why |
|---|---|---|
| Errors use the classes `invalid`, `not_found`, `auth`, `conflict`, `unavailable`, `unsupported` | Twelve classes, adding `ambiguous`, `blocked`, `forbidden`, `rate_limited`, `ambiguous_outcome` and `stale` | `blocked` is the write guard refusing something the API would allow, which is this server's whole point; `ambiguous` (a title matching several spreadsheets) and `ambiguous_outcome` (an append that may have landed) ask the caller to do opposite things, and collapsing either into `invalid` loses what the model needs to decide. The vocabulary is closed, derived from the code, and asserted in both directions (§6.5) |
| Never overwrite: compute a minimal diff | A minimal diff is not possible over a rectangle of values, so the guard refuses instead | A document server can diff prose because text has structure a diff can follow. A grid write is a rectangle of independent cells; the equivalent protection is to refuse what would be destroyed and name it (§4.3) |

Everything else is adopted as written, including the two paragraphs added
to the standard on 2026-09-06: the preamble's three obligations for any
rule adopted — make it a test, derive the list from the code, assert a
floor on how much the checker read — and the rule in its §5 that a leak
gate cannot be patterns alone when the payload is ordinary words. §9.1
here is this server's instance of the second, written before the standard
carried it; the two say the same thing.

## 18. Evidence log: conventions checked, changed, or rejected

Sources: the Sheets API v4 discovery document (revision 20260831), the
Sheets guides and function reference, the MCP specification, the Go
module proxy, `go.dev`, GitHub's own documentation, and the issue
trackers named in §1. Checked 2026-09-05 and 2026-09-06.

**Not every row here carries the same weight, and the plan says which is
which rather than letting them blur.** Three tiers:

1. **Verified here, against a primary source.** Everything about the
   Sheets API — the absent write guard, the quota model, the per-method
   scopes, GridRange semantics, A1 quoting, `values.update` skipping,
   append's table detection, `USER_ENTERED`, batch atomicity, the size
   limits — was read out of the discovery document or quoted verbatim
   from the guide, not recalled. So was the official Sheets MCP's tool
   list, and the OSS failures in §1, which were read from the issues
   themselves rather than from their titles, except the four marked
   below.
2. **Inherited from a sibling server and re-checked here on 2026-09-06**,
   because they decide code this repository has not written yet:
   claude-code#55677, the go-sdk's disconnect codes and its public
   `jsonrpc.Error`, and the oauth2 refresh client. Each has its own row.
3. **Inherited and *not* re-checked.** Taken on the sibling
   repositories' evidence, and second-hand until this server hits them:
   Claude Code's 25 000-token truncation and 10 000-token warning; cosign
   3 requiring `--bundle` where v2 wrote a signature and a certificate
   separately, which its release notes describe as a move to the bundle
   format without stating the requirement in the words the sibling logs
   use; and the gate bugs (a hand-written coverage list going stale, a
   staleness gate blocking its own release pull request). They are cheap
   to hold and expensive to rediscover, and nothing here depends on one
   being exactly right. The agent-eval numbers have moved out of this
   tier and into a row of their own, because they turned out to carry a
   provenance that matters.

Beyond those, seven things are **unverified by design** and wait for a
live probe: §15's spikes A-G, of which E — what Sheets actually returns
for a missing sheet, a bad range, a protected range and a throttled call
— is the one the error mapping in §6.5 rests on.

| Convention | Verdict | Effect |
|---|---|---|
| Sheets has a write guard like the Docs API's `writeControl.requiredRevisionId` (my assumption, and the design was built on it for an hour) | **Refuted**: neither `writeControl` nor `requiredRevisionId` nor an ETag occurs anywhere in the discovery document. Sheets offers nothing atomic | Checkpoints (§4.7, §6.3), stated as best effort, and protected ranges named as the only real guarantee. An earlier draft promised a conflict check it could not deliver |
| Sheets quota is unit-based per method, as Drive's is (inherited from the Drive API's model, where a list costs 100 units and a read 5) | **Refuted**: Sheets counts requests. 300 reads and 300 writes per minute per project, 60 of each per user, refilled every minute, no daily cap. Verbatim: "Each batch request, including any subrequest, is counted as one API request toward your usage limit" | Batching is close to free, so §4.5 makes one tool call one request and every ops tool compiles to a single `batchUpdate`. Limiters use the documented numbers rather than a guess |
| Google has no official Sheets MCP (my assumption) | **Refuted**: `sheetsmcp.googleapis.com/mcp/v1`, Developer Preview, six tools, remote HTTP, a **Web application** OAuth client, four scopes including `drive.file`. `update_spreadsheet` passes the raw `batchUpdate` union through | §1 states what it does and does not do; spike D records its schemas. This server stays stdio, per-user, and guards its writes |
| `GridRange` and A1 describe ranges the same way (my assumption) | **Refuted** by the discovery document: GridRange is zero-based and half-open, A1 is one-based and inclusive; a missing GridRange index means unbounded, where A1 spells it `A5:A` | All conversion lives in `internal/a1`, with round-trip tests. This is the single easiest off-by-one in the project |
| A range string can be built by joining a sheet name and an A1 range (what every server in §1 does) | **Refuted**: names with spaces or punctuation need single quotes, an apostrophe inside a name is doubled, and `'A1'` means the sheet named A1 while `A1` means a cell of the first **visible** sheet | `internal/a1` builds every range from a parsed title. A test covers the `'A1'` trap |
| The first sheet is called `Sheet1` (assumed by every server in §1, and by their documentation) | **Refuted** by xing5#94: Google names it in the account's language, so a Portuguese account has `Página1` and the call fails with `Unable to parse range: Sheet1!A1:D3`. The bug was in the docs and tests, which taught the model to guess | `get_spreadsheet` first; no tool defaults a sheet; no description uses `Sheet1` as an example; `[not_found]` lists the titles that do exist |
| `values.update` replaces the range it names (my assumption) | **Refuted**: "values with no data are skipped", so a shorter write leaves the previous tail in place. Clearing needs explicit `""` or `values.clear` | `write_values` reports the covered region and says what it left; `clear_values` is its own gated tool |
| `values.append` appends to the end of the sheet (the name, and what three of the servers in §1 assume) | **Refuted**: it searches the given range for a contiguous block, writes after **that**, and with the API's default `OVERWRITE` writes over whatever follows | `append_rows` defaults to `INSERT_ROWS` and reports `tableRange`; spike B measures the gap case |
| `USER_ENTERED` is the friendly default and needs no explanation | **Refined**: it parses as if typed, turning `1-2` into a date and `007` into 7. It is right for a person and destructive for a product code | `input: typed \| literal`, never substituted, plus the coercion diff read back from the write's own response (§4.4) |
| Read-only mode is a scope swap and nothing else (inherited) | **Refuted** against the per-method scopes in the discovery document: only `spreadsheets.get`, `values.get` and `values.batchGet` accept a read-only scope. `getByDataFilter`, `values.batchGetByDataFilter` and both `developerMetadata` reads require a **write-capable** scope | Read-only mode registers no tool that needs a data filter, and §6.4 may not make developer metadata the only way to reach anything |
| Developer metadata is a general-purpose anchor for any range (my assumption when looking for a Docs `named_range` equivalent) | **Refined**: it survives edits — "will remain associated at those locations as they move around" — but attaches only to the spreadsheet, a sheet, or a **dimension** range, and reading it needs a write scope | Offered in phase 3 as an optional label beside A1, never as the only address |
| The Sheets error surface mirrors Drive's, with 403 reasons for throttling | **Unverified, and recorded as such**: the troubleshooting page documents only 400, 500 and 503, and says 429 for quota. Drive's four 403 quota reasons are a Drive fact and may not hold here | Spike E observes the real shapes before the class mapping is finalised. Until then the mapping treats 429 as rate limiting and does not invent 403 reasons |
| A batch of updates should be split so a failure is small | **Rejected**, and the discovery document says why in its own words: "If any request is not valid then the entire request will fail and nothing will be applied … the updates in the request will be applied together atomically." A batch also counts once against quota, so splitting costs quota *and* gives up atomicity | Ops compile into one batch; a partial application cannot happen |
| A write, once applied, is what the spreadsheet holds (my assumption, and the premise of an earlier checkpoint design that claimed to detect conflicts) | **Refuted by the platform itself**: "Due to the collaborative nature of spreadsheets, it is not guaranteed that the spreadsheet will reflect exactly your changes after this completes … Your changes may be altered with respect to collaborator changes" | The checkpoint is documented as narrowing a window rather than closing it, and `manage_range`'s protected ranges are named as the only real guarantee (§4.7) |
| A structural write needs a second call to report the state it produced | Refuted: `batchUpdate` takes `includeSpreadsheetInResponse` with `responseRanges`, so the after state arrives in the same response | One request per write, including its report (§7.3) |
| Retrying a write is safe when the API is idempotent (inherited, and stated as "writes may retry") | **Refined** from the sibling projects' own refutation: repeatability is a property of the method. `values.update` is a PUT and repeatable; `values.append` and `batchUpdate` are POSTs that duplicate rows and sheets | `retryable` reads the method, so a call added later without thought fails closed. `[ambiguous_outcome]` names the read that settles it |
| An external-fetch formula is ordinary content | **Rejected**: `IMPORTXML`, `IMPORTRANGE`, `IMPORTDATA`, `IMPORTHTML`, `IMPORTFEED`, `IMAGE` and `HYPERLINK` make Google fetch a URL that can carry the sheet's own data. Writing one is an outbound request the person never saw | `allow_external_formulas: true` per call, and every formula a write creates is named in the result (§7.3, §9) |
| Reads return text only, because Claude Code shows the model only `structuredContent` (inherited, and what this plan decided at first) | **Refined twice by review from two sibling sessions, and the second answer is better than the choice I was making.** The first correction: text-only is safe *only* because such a tool declares no output schema — Claude Code shows the text when there is no structured half to prefer, so a tool declaring a schema and returning text alone goes blank. The second dissolved the question: do not choose which half carries the substance, put the rendering **inside** the structured half. Both clients then see the addressed grid | Every tool declares an output schema and returns both halves; the structured half carries the rendering in `grid` (reads) or `summary` (writes). Reads keep a schema for the diff to hold, which text-only would have given up (§4.9) |
| "In JSON the addressing is gone, so a grid read must be text" (my argument for text-only, stated as a property of JSON) | **Refuted in review**: it is a modelling choice, not a fact. Rows as `{ref, values}`, or a range plus a header row, keep the addressing perfectly well in a structured half | The premise is gone, and with it the fork. Recorded because the reasoning was wrong even where the conclusion was nearly right |
| Repeatability is derived from the HTTP method, full stop (§11 as first written, inherited from a sibling's refutation of its own kind-based rule) | **Refined, and it would have been a bug here**: Sheets has three POSTs that only read — `spreadsheets.getByDataFilter`, `values.batchGetByDataFilter`, `developerMetadata.search` — beside two POSTs that write. Deriving write-ness from the method puts a read on the write limiter, refuses to retry it, and blocks it under the dry-run guard: three wrong answers from one inference. A name pattern is worse, since "get" reads like a read on a method that is a POST only because a filter is long | The method decides by default, so an unconsidered POST still fails closed; an explicit `readOnly` flag marks the exceptions; and a syntax-tree test allows that flag only on those three methods **by name** (§11) |
| `IMPORTRANGE` exfiltrates data like the rest of the `IMPORT` family (§7.3 as first written) | **Refuted** against the function reference: `IMPORTXML` takes "The URL of the page to examine, including protocol" and `IMPORTDATA` "a given url", so those do reach an arbitrary host — but `IMPORTRANGE` takes a **spreadsheet** URL and cannot. Its risk is the opposite direction: it pulls any spreadsheet the account can read into this one | Two risks, two rows in §9, one acknowledgement. Lumping them together would have justified the gate with a claim that is false for the function most people write |
| A display budget is a memory budget (implied by "reads are budgeted") | **Refuted by somebody else's fix**: a sibling project clamped what it *displayed* to 50 rows while still fetching `A:Z` whole, and the maintainer's later note is explicit that "memory was already unbounded even when older releases only displayed the first 50 rows" (taylorwilsdon #986) | The window is resolved before the call and a finite range is sent (§7.2). The budget bounds the request, never only the rendering |
| A column-letter parser may be permissive at the edges | **Refuted with a table**: `_column_to_index("A1")` returned 10 and `"B2"` returned 37 in a shipped server, silently naming columns K and AL, because only the empty string was rejected (#959) | `internal/a1` is total and rejects; the malformed cases are table tests from the first commit, and `A1`-as-a-column is one of them |
| One invalid tool schema breaks one tool | **Refuted**: a client validating against draft 2020-12 rejects the whole request, so five bad schemas killed all forty-four tools in the session, reporting only an array index that names no server (freema #138) | The schema dump and diff run in CI from phase 0, and the smoke test drives a real session rather than trusting the schemas to be well-formed |
| "Non-string" input is affected by `valueInputOption` (my assumption when designing the coercion report) | **Refuted** by the values guide: "non-string values like booleans or numbers are always handled as `RAW`" | The coercion report compares only what was sent as a string, which is also the only thing that can be coerced |
| An id-shaped rule that wants a capital and a digit catches the identifiers that matter (inherited from a sibling's leak gate) | **Refuted by that sibling's own slip**: a Google account id is 21 digits and a Drive permission id is 20, neither with a letter, and one reached a transcript before the rule was fixed | §9.1's shape rule names the numeric identifiers explicitly. Sheets' own small integers identify nothing outside their spreadsheet and are deliberately out of scope |
| claude-code#55677 says what the sibling logs say it says (inherited) | **Re-checked 2026-09-06**: real, closed 2026-05-06 with five comments, titled "MCP: tool result `content[].text` dropped from model when `structuredContent` is also present", reproduced on Claude Code v2.1.126 over streamable HTTP, and the reporter states the same response shape works on claude.ai and ChatGPT | Believed, and §4.9's design is the one that does not depend on it either way |
| The go-sdk reports a closed session as JSON-RPC -32004, and `jsonrpc.Error` is public (inherited) | **Re-checked 2026-09-06 at v1.7.0**: `internal/jsonrpc2/wire.go` defines `ErrServerClosing = NewError(-32004, …)` and `ErrClientClosing = NewError(-32003, …)`, and `jsonrpc/jsonrpc.go` exports `Error = jsonrpc2.WireError` | The clean-exit check matches those codes through `errors.As`, with no message sniffing (§13) |
| An unquoted sheet name in a range is untidy but harmless (my assumption, and what §2 said in the first draft) | **Refuted 2026-09-06** by the concepts guide, verbatim: "if there's a named range titled `Sheet1`, then `Sheet1` refers to the named range and `'Sheet1'` refers to the sheet". A named range **shadows** a sheet of the same name, so an unquoted title reads a different rectangle without failing | `internal/a1` always quotes the title, and the shadowing case is a table test from the first commit. This is the trap here that fails silently rather than loudly, and it appears in no server surveyed in §1 |
| `values.get` accepts a named range as its `range` (assumed, and it is what every server in §1 implies) | **Not confirmed**: the method's own reference documents `range` as "The A1 notation or R1C1 notation of the range to retrieve values from" and says nothing about named ranges, though the shadowing rule above implies a bare name resolves to one | Named ranges are spike C (§15) and no tool promises them until it runs. R1C1 is accepted by the API and deliberately not offered: two spellings of one address is one more thing to get wrong |
| The eval finding behind "reads return text only" is current (inherited, and cited in my first draft as though it were) | **Given its provenance 2026-09-06 by the session that measured it**: the number is from Claude Code 2.1.259, that machine now runs 2.1.260, it has not been re-measured, and **every read measured was prose**. Their claim was "a client may show only `structuredContent`"; mine was "when it does, the addressing is the content" — a different claim wearing the same evidence | §4.9 no longer rests on it, since both halves now carry the rendering. What remains is measured rather than assumed: one eval is an A/B on exactly this (§13) |
| "Pinning an action to a full-length commit SHA is the only immutable form" is a sibling's paraphrase (inherited) | **Re-checked 2026-09-06**, and it is GitHub's own sentence verbatim: "Pinning an action to a full-length commit SHA is currently the only way to use an action as an immutable release." The page has since grown repository- and organisation-level **policies that require** it, which is stronger than a convention and stronger than our gate | §12; the gate stays because a policy is the deployer's to set and this repository cannot assume one |
| `~> v2.18.0` narrows `goreleaser-action` enough to count as a pin (recorded as a fix in a sibling's log at the time, and since corrected there) | **Refuted first-hand 2026-09-06**: the action's own `action.yml` at v7.2.3 declares `version` with `default: '~> v2'`. A `~>` value is a constraint whatever follows it, so the tool that decides what the artifacts are floats — the narrowing changed the width of the range, not its kind. Every shipping sibling now pins an exact version; what survives is the lesson, not the defect | Exact versions only, asserted by a gate, with a comment beside each naming which half of the pin is load-bearing (§12) |
| A CI matrix of three platforms means three platforms are tested (assumed) | **Refuted by a sibling's experience**: a gate conditioned on `runner.os` ran on Linux only while the matrix advertised three, and that blind spot hid the next bug for weeks — PowerShell read `-coverprofile=cov.out` as a file named `cov`, and the suite carried on. A third: `cancel-in-progress` without an exclusion for `main` leaves a merged commit carrying a killed run, so a later bisect walks a green history with a hole in it | §12: `shell: bash` on every job, no gate conditioned on `runner.os`, and `cancel-in-progress` excluded on `main` |
| An inline `gitleaks:allow` comment clears a false positive (assumed) | **Refuted**: the CI scan walks a pull request's *commits*, so a comment added later never clears the commit that introduced the line. And the two scanners flag each other's fixtures, because an allowlist has to quote the fabricated credential it permits | Exceptions live in `.gitleaks.toml` scoped to the literal, each saying which scanner it is for, with per-line markers (§9.1) |
| A rule written in a document is a rule (implicit in keeping this document at all) | **Rejected, with five examples from one sibling repository**: a pin that was a range, a security page promising logs carried nothing while the code logged part of an id, an error vocabulary documented as ten closed classes while the code emitted fifteen with four named nowhere, a `CLAUDE.md` forbidding direct pushes to a repository that had no branch protection, and a live-driver coverage rule that survived a whole language port as a comment. Not one was caught by reading; each was caught by something that could fail | The fix is the same every time, is this plan's default, and as of 2026-09-06 heads the shared standard as an obligation on anything adopted from it: make the rule a test, **derive** the list from the code rather than typing it out, and have the checker assert a floor on how much it read, since zero findings and zero inputs are otherwise the same output. §6.5, §9.1, §12 and §13 each name the check that holds them |
| A wildcard `git add` is safe in a repository whose ignore rules come later | **Refuted in this session, before any code existed**: `.claude/settings.local.json` was written into this working tree while the design was being drafted, and was kept out of the commit only by a *global* ignore on this machine — which nobody cloning a public repository has. A sibling put exactly such a file into a public history this way | `.gitignore` is committed with the design rather than with phase 0's scaffolding, and it carries the editor's temp files, the credential file names and the build outputs |
| An oauth2 refresh runs on the client in its context, and there is none by default (inherited) | **Re-checked 2026-09-06** in `golang.org/x/oauth2/internal/transport.go`: `ContextClient` returns the context's `*http.Client` if one was put there and `http.DefaultClient` otherwise, which has no timeout | The token source and the login exchange both put a bounded client in the context, with a test that hangs a listener (§9) |
| A leak gate made of patterns is enough, as it is for a file server (inherited) | **Refined, and it is the difference this server has to design around**: an id, a link, an address and a client id all have shapes a regex catches, but a **cell value, a sheet title, a named range and a note do not** — they are ordinary words, and they are the payload. A pattern cannot separate an invented column heading from somebody's customer list | The pattern gate stays for what has a shape, and everything else is made **structurally** impossible: fixtures are generated rather than recorded, and the live driver reads only a spreadsheet it created and filled itself (§9.1). A control that depends on noticing is not a control |
| A formula in a fixture is just text (my assumption when listing what the gate scans) | **Refuted**: `IMPORTRANGE("<spreadsheet id>", …)` carries another spreadsheet's id inside a string literal, so one copied formula leaks a second document nobody was thinking about — and it is the kind of cell most likely to be copied, because it is the interesting one | Formulas are named in §9.1's never-list, and the id pattern scans inside string literals like any other text |
| A live transcript can be made safe by redacting it (inherited from a file server, where a transcript is names and ids) | **Refined**: there, redaction removes the identifying part and leaves the shape. Here the transcript *is* the data — a grid of values — so redacting it would leave nothing worth reading, and forgetting to redact one line leaks a row | The driver never reads a spreadsheet it did not write, so the values are its own; redaction of ids and addresses is a second line of defence, in the print helper only |
| Redaction can be applied where a value is read (inherited) | **Refuted** by a sibling project on its driver's first run: scrubbing on the read path meant a step parsed a placeholder out of one result and fed it back into the next call, which the API then rejected | Redaction lives in the one helper every transcript line passes through; a step reads the untouched value |
| A fake built from the documentation is a sound test oracle (inherited, and refuted twice in the sibling projects) | **Rejected in advance**: a sibling's in-memory fake implemented its API's search operator exactly as the reference described it, the reference was wrong, and the entire suite agreed with the bug. Simulating `USER_ENTERED` here would repeat that exactly | `sheetstest` replays coercion outcomes **recorded** in spike A; unrecorded input is an explicit fake error, not a guess |
| A live driver reporting "all calls behaved as expected" verifies the surface (inherited) | **Refuted** in the Drive project: two runs said so while three results were wrong, because the driver checked whether calls succeeded, not whether they told the truth | A phase is not closed until a transcript has been read (§13, §16) |
| `errors.Is(err, io.EOF)` catches the end of a stdio session (inherited) | **Refuted** in two sibling projects: the SDK reports it as JSON-RPC -32004 with the EOF only in the message text, so the process exits non-zero on an ordinary disconnect and hosts log a crash | Match the code through `jsonrpc.Error`; the smoke test closes stdin abruptly and is verified against the broken behaviour |
| The client's `Timeout` bounds every HTTP call (inherited) | **Refuted** in two sibling projects: an oauth2 refresh runs on the client in its context, and with none supplied that is `http.DefaultClient`, which has no timeout | The token source and the login code exchange both carry a bounded client, with a test that hangs a listener |
| Pinning an action to a full commit SHA pins what the step does (inherited) | **Refuted** three times across the siblings: an action that installs a tool needs the tool pinned too, and `~> v2.18.0` is a constraint, not a pin | Exact versions beside every SHA, held by a gate rather than a comment |
| A hand-written list of packages under the coverage floor stays current (inherited) | **Refuted**: a sibling added a package that was under no floor at all, silently | The list is derived from `go list ./internal/...`, with exemptions named and justified |
| go-sdk latest is v1.7.0; Go latest is 1.27.1; golangci-lint latest is v2.13.2 | Confirmed 2026-09-05 against `proxy.golang.org`, `go.dev/dl` and the GitHub release. v1.8.0-pre.2 exists and is not final | Pinned as in the sibling servers, so all four repositories move together |
| `[class] message` errors, flat schemas, snake_case verb_noun names, server-side gating, keep serving without credentials, keyring → file → env, loopback PKCE, env-first config with bound flags (inherited) | Confirmed against the MCP specification and Google's native-app guide, and re-confirmed by the siblings' evals: the spec requires only `isError` and treats annotations as untrusted, so gates must be server-side; Claude Code, Claude Desktop and Cursor pass only `command`, `args` and `env` | Kept unchanged |
