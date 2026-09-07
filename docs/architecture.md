# Architecture — google-sheets-mcp

**Status: v0.3.1, with phase 4 code complete and unreleased
(2026-09-07).** Reading, writing, formatting, the objects attached to a
range, `gsheets://` resources, durable anchors and now charts, pivot
tables and Connected Sheets all work, and `make check` is green. Spikes
L, M, N and P ran against a real account and §18 carries what they
found.

**Phase 4 added three tools and found four things the reference does not
mention.** `updateChartSpec` replaces a chart's spec whole and refuses a
partial one, so an update reads before it writes and a caller's rename
cannot silently drop the series. A `basicChart` with neither domains nor
series comes back **HTTP 500**, which §6.5 classes retryable — the only
defence is never sending it. Deleting a charted column leaves the chart
in place with nothing to draw, and deleting a pivot's source rows leaves
a header and a grand total; both are silent, and they are the third and
fourth silent destroy this project has found. A pivot table is not a
request in the union at all: it is a field of one cell, and its output's
size is in no request and no reply, so every result reads it back.

**§17.6 was reopened and amended.** `addDataSource` refuses under this
server's two scopes, naming `bigquery.readonly`, so Connected Sheets
cannot be reached without a third scope. `manage_data_source` and that
scope are both behind `GSHEETS_ENABLE_DATA_SOURCES`, off by default;
§17.6a has the reasoning, including the recommendation that was made
against it and overruled.

**Phase 4's live driver has run three times and every transcript was
read: 206 steps, none failed, one undetermined.** The first run failed
three steps and the second passed all of them — and reading the second
still found two defects, a refusal whose clause did not compose into its
sentence and a guard message §7.6 had promised more of than the read
makes possible. §16 has the detail.

**Phase 3's live driver run and its transcript: 169 steps,
none failed, one undetermined.** Reading it found two defects the count
could not — a refusal telling a `read_range` caller to pass an A1 *band*,
and a delete quoting an anchor's name in its refusal and not in its
result. Both are §18 rows and both are fixed.

**The evals have run**, four times, and 15 of 16 tasks pass. Every
failure but the last was in the harness rather than the server: a check
the fixture satisfied by itself, a scorer reading past the data into
another task's work, an unanswerable prompt, and a task that demanded a
refusal a well-behaved model avoids. The sixteenth is a real finding
about the read window and is §17a.25.

Getting there needed a detour: the refresh token vanished five times and
`go test ./...` was deleting it. `cmd`'s test isolation redirected the
config directory and the environment override and could not redirect the
OS keyring, so a logout test removed the real one under the default
profile. Fixed and watched both ways with a decoy. §18 carries it,
including the confident wrong diagnosis that came first.

v0.3.1 followed the same day, from comparing this server against its
siblings rather than from anything failing: the README's setup step told
people to add "the two scopes below" and listed neither, and the profile
was missing the `account_email` the other three record. §17c has the
conventions and the destructive line this server draws.

Phase 2 is what that rule is made of. It took three runs of the driver to
reach 143 steps with none failed, and the first failed five — three of
them the server: a merge spanning a frozen edge, a named range with a
space in it, and a conditional format rule that `deleteTable` had taken
silently. None of the three is in any reference read for this project.
Phase 3 found the same shape twice more, both by reading rather than
counting: `deleteDimension` takes the anchors on the band and says
nothing, and spike K's own first run answered four of eleven questions
against rows the anchor had already left.

**Phase 4 is code complete and not released.** What it still owes is the
maintainer's decision on the version, the release commit and the tag.
§17a carries sixteen open cleanups; the three that wanted a live probe
were probed in this phase and are closed.

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
| **A1 quoting has three traps, and the third is narrower than the guide's wording but still a correctness bug.** Single quotes are required for sheet names with spaces or special characters (`'My Custom Sheet'!A:A`); `A1` without a sheet means cell A1 of the **first visible sheet** while `'A1'` means the whole sheet *named* A1; and the concepts guide says "if there's a named range titled `Sheet1`, then `Sheet1` refers to the named range and `'Sheet1'` refers to the sheet". **Measured (spike C, §18): that last rule applies only to a reference with no `!`.** A bare name resolves as a named range first; anything with a `!` reads its left side as a sheet title regardless. | `internal/a1` builds every range from a parsed sheet title and **always quotes it**, never by string concatenation, and a range is never sent with the sheet omitted. Quoting is load-bearing for the whole-sheet form this server sends (`'Data'` is the sheet, `Data` is the named range) and inert for `'Data'!A1:B2` — kept everywhere so no call site has to know which case it is in. |
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
internal/redact/          the one redactor every live driver prints through: ids, links, addresses
internal/livecover/       what "the driver covers the tool surface" means, so the static gate and the
                          driver itself cannot disagree about it
internal/version/
testdata/                 synthetic fixtures and golden outputs
docs/
scripts/gates/            the repository's own checks, as Go: coverage floor, schema diff, stdio smoke,
                          staleness, leak scan, workflow pins, pre-commit; never shipped
scripts/livesheet/        drives the built binary against a real account, redacting ids and addresses
scripts/spikes/           the probes §15 lists, each answering what the reference does not pin down
scripts/evals/            drives a model through the tools alone and scores the end state and the trace
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
More than one match is `[ambiguous]` with each candidate's id, owner and
modification time, and the instruction to pass an id. No match names the
closest titles. A `gid` lifted from a URL is used as the sheet unless the
call names one.

### 6.2 Referring to a range

`sheet` takes a title or a `sheetId`; titles are unique inside a
spreadsheet, so a title is unambiguous once it is read rather than
guessed. `range` takes A1 (`B2:D40`), a bare column or row range
(`B:B`, `2:5`), or a whole sheet. The server always sends a range with
its sheet resolved **and quoted**; it never concatenates a title into a
string. A title with a space or punctuation needs the quotes, and a
whole-sheet reference needs them to mean the sheet rather than a
same-named named range (§2, measured in spike C).

Two things the reference was quieter about than this design would like,
both now measured (spike C, §18). `values.get` documents its `range` as
"The A1 notation or R1C1 notation of the range to retrieve values from"
and says nothing about **named ranges**; in fact it accepts one, as a
bare name with no `!`, and a name with a `!` after it is read as a sheet
title and fails if no such sheet exists. A **table** reference is not
accepted in any form — neither `Table1` nor `Table1[Column]`. No tool
offers either: a named range is reachable only in the one spelling that
is also the spelling a whole-sheet reference uses, which is too sharp an
edge to hand a model. **R1C1 is accepted by the API and is not offered
here**: two ways to say the same thing is one more thing for a model to
get wrong, and A1 is what the person sees.

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

### 6.4 Developer metadata (phase 3, built)

Developer metadata is the only anchor Google keeps attached to a location
while the sheet is edited around it. It reaches a spreadsheet, a sheet or
a dimension range, not an arbitrary rectangle, and reading it needs a
write-capable scope, so it can never be the only way to find something.
It is offered as an optional durable label — "remember this row as
`invoice-totals`" — and every tool that accepts one also accepts A1.

**Verified as spike K (§18), and the premise held.** An anchored row
follows its row through an insert above it, a delete above it, a
`moveDimension` and a `sortRange` — and after a sort it lands where that
row's *values* landed, which is the one thing here a reading of the
reference would have got backwards. What it does not do is notice its
contents being replaced: an anchor names a row, not the data on it, so it
survives the sheet being reorganised and says nothing when the row is
rewritten. The two are easy to hear as one promise, so the tool says
both.

**The shape follows from three findings.** A key is not unique, so
uniqueness is enforced here or `anchor:totals` is a question with two
answers. Deleting the row deletes the anchor with nothing in the reply
saying so, so `delete_dimensions` names them first. And there is no
"everything in this spreadsheet" location query — a spreadsheet location
with `INTERSECTING` is refused — so listing goes through an empty lookup,
which the reference does not describe and which makes it one request.

`anchor:<name>` is a prefix on the existing range argument rather than an
argument of its own, so every tool that takes a range takes an anchor
from one change in `ResolveRange`, and none of them grows an eighteenth
argument (§17a.12 is already that complaint). Nothing in A1 can collide
with it: a sheet-qualified range needs a `!`, and no column name is six
letters. Visibility is always `DOCUMENT`: `PROJECT` scopes an entry to
the OAuth client that created it, and this server is distributed for
people to run under a client id of their own, so a `PROJECT` anchor would
vanish on a re-install with nothing to say why.

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
  hit shows title, id, modified time and owner — not the folder, which
  is a raw id nothing here can use (§18). It is the fix for #76 and the
  only Drive call this server makes.
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

`read_formatting` answers the other question: for a range, the number
format, font, colours, borders, alignment and wrapping, summarised per
block of identically formatted cells rather than per cell — a format
repeated down a column is one fact, and a thousand lines of it is none.

A cell with no format of its own is counted rather than listed, so what
comes back is what somebody set rather than the sheet's defaults
repeated. The read carries both of the API's formats for that reason:
`userEnteredFormat` is what the cell was given and decides the blocks,
`effectiveFormat` is what applies once Google has merged the defaults in,
and the difference between them is what tells a caller whether clearing
would take anything away.

It also lists what is attached to the range and decides how a cell looks
without being on the cell — merges, conditional format rules with their
index, banding, validation rules, notes and protected ranges — because an
answer built only from cell formats would describe a green column as
plain. The cell budget bounds the read; there is no character budget,
because the answer is per block and the cell budget already bounds how
many blocks there can be.

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
  `auto_resize`, `group`, `ungroup`, over rows or columns. `move`'s `to`
  is where the band ends up: the API reads its index against the sheet
  *before* the move, verified live, and the conversion is this server's.
- `delete_dimensions`: gated, and needs `confirm: true`. Deleting a
  column takes its data with it and nothing in Sheets brings it back.

  It is a **tool** rather than an action on `edit_dimensions`, which is
  what an earlier draft of this section specified. That version said the
  action would be "absent from the schema" when destructive tools are
  off, and hiding it from the description does not achieve that — with no
  enum on `action` there is nothing structural to omit, and `confirm`'s
  own description still named it. Worse, `edit_dimensions` is registered
  as a plain write, so it advertised `destructiveHint: false` while
  offering an irreversible action. `Kind` decides the annotations, the
  registration gate and the interaction mark, and a tool whose
  destructiveness depends on an argument is exactly the matrix `Kind`
  exists to avoid — so the destructive act got its own tool and inherits
  all three.

### 7.5 Formatting and structure (phase 2)

- `format_cells`: ops over a range — `number_format`, the font
  switches, `text_colour`, `background`, `borders`, `horizontal`,
  `vertical`, `wrap`, `merge`, `unmerge`, `clear_format` and `note`.
  Every field set is one op and they travel together in one atomic
  batch, so a header row that is bold, centred and shaded is one call.
  Borders take a shorthand (`1pt solid #cccccc`), as in the Docs server,
  rather than fifteen flat fields, and a number format names its type
  (`date:yyyy-mm-dd`) rather than leaving the type to be guessed from the
  pattern. Clearing is applied before setting, so "clear this and then
  make it bold" is one call rather than a clear that undoes the bold.

  **Three of these destroy something, and each is refused until
  acknowledged**: a merge keeps the top-left value of every merged block
  and discards the rest with nothing in the response saying so;
  `clear_format` removes formatting Sheets cannot bring back; and a note
  replaces one that no values read would have shown the caller. The cells
  are read only when one of the three is asked for, so an ordinary "make
  it bold" costs one request and not two.
- `manage_range`: things attached to a range — `named_range`,
  `protected_range`, `data_validation`, `table`, `banding` and
  `conditional_format`, each with `add`, `update` and `delete`. A table's
  dropdown column carries its `ONE_OF_LIST` rule, which the API requires.

  **The conditional-format ops are here rather than on `format_cells`,
  which is a change from an earlier draft of this section.** A rule is
  attached to a range and has add, update and delete, which is exactly
  this tool's shape; and it needs a format of its own, so on
  `format_cells` the style fields would have meant two different things
  depending on whether a condition was also given. One argument that
  means two things is how a caller sets the wrong one.

  **An existing object is named by the range it covers, not by an id.**
  A caller who can say "the protection on B2:B10" has not had to fetch
  an id first, and A1 is this server's contract everywhere else (§17.1).
  The range has to match exactly — a protection over a column and one
  over a cell inside it are different objects — and a range matching
  several is refused with the list rather than picked from. A
  conditional format rule is the one exception: the API identifies those
  by position in the sheet's list, so they take an `index`, which
  `read_formatting` reports beside each rule.

  A protection never blocks the request that lifts it. Anything else
  would be a trap rather than a guard.
- `transform_range`: `sort`, `find_replace`, `trim_whitespace`,
  `remove_duplicates`, `text_to_columns`, `randomize`, `auto_fill`,
  `copy_paste`, `cut_paste`. Each reports what the API said it did.
  These are the operations that move data without the caller naming its
  new address, so each one runs the guard over its destination — and
  there are three shapes of destination, each read only when the action
  has one:

  - A paste and an auto-fill land on cells the caller never named, so
    the landing rectangle is read and guarded like any other write
    target.
  - A `text_to_columns` spills into the columns to the right of its
    source. How far is not knowable before the call unless the delimiter
    is given, so with Google detecting it the guard reads a window to the
    right and treats all of it as at risk, and says that is what it is
    doing.
  - A `find_replace` with `in_formulas` rewrites what a cell computes
    rather than what it shows, which is the same invisible loss
    `overwrite_formulas` exists for, so it asks for the same
    acknowledgement.

  `sort`, `randomize`, `remove_duplicates` and `cut_paste` all move rows,
  so each result says that an address or a checkpoint from before the
  call no longer points where it did.

  A sort key is a column of the sheet, not an offset into the range: the
  API's `dimensionIndex` is absolute, and the live driver sorts a range
  starting at B by its second column so that a relative reading would
  leave the rows where they are and be caught.

### 7.6 Charts, pivot tables and data sources (phase 4)

Designed after spikes L, M and N ran, and shaped by what they found
rather than by the reference (§18 carries every row).

**`manage_chart`** — `add`, `update`, `move`, `delete`, `list`, over
charts and slicers alike, since both are embedded objects with one
delete between them.

- `add` takes a chart kind, a title, one domain range and one or more
  series ranges, all in A1, and either an anchor cell or `new_sheet`.
  **Domain and series are checked before the request is built**: a
  `basicChart` with neither returns HTTP 500 from Google, and a 500 is
  retryable by §6.5, so a client that trusts the status sends it again.
  This is the one place in the project where the API's own error class
  is wrong for the request that caused it, and the request is what
  changes.
- `update` **reads the spec, edits it and sends it back whole.**
  `UpdateChartSpecRequest` carries no field mask and refuses a partial
  spec outright, so there is no merge to do; the alternative — filling
  in a chart kind to make the request valid — would rebuild somebody's
  pie chart as a column chart and report success.
- `move` sends `updateEmbeddedObjectPosition`, which **requires** its
  own `fields` mask; the tool sets it from the arguments given.
- `delete` sends `deleteEmbeddedObject`, whose reply is `{}`. The result
  is composed from the card read before the delete, so it can say what
  went. Deleting an id twice is a clean 400 and maps to `[not_found]`.
- `list` reads titles and positions. The card's own mask widens only to
  `charts(chartId,spec(title))` — 72 bytes a chart, so a name is
  affordable on every `get_spreadsheet` — and the fuller read with
  positions stays inside `manage_chart list`, at about 370 bytes a
  chart.

A chart on its own sheet is `sheetType: OBJECT` **with no
`gridProperties`**, and it goes when that sheet goes. Nothing reading a
card may assume a sheet has a grid; that hole predates phase 4, since a
chart sheet made in the Sheets interface reaches every tool already
shipped.

**`manage_pivot_table`** — `add`, `update`, `delete`, `list`. A pivot is
not a request in the `batchUpdate` union: it is a `pivotTable` field on
one `CellData`, written through `updateCells` at its anchor and deleted
by an `updateCells` naming that field with an empty cell, which takes
the whole output with it.

- **Columns are named in A1, never as offsets.** The API groups and
  summarises by `sourceColumnOffset` into the source rectangle. A caller
  says `B` or the header text; the server converts against the source,
  the way hard rule 4 requires. It also **checks the offset against the
  source's width**, because an offset past the end is accepted with a
  200 and produces a pivot that means nothing.
- **An anchor inside the source is refused** before the request is
  built. The API accepts it and evaluates to `Circular dependency
  detected`.
- The output's size is not in the request: it is computed from the data,
  and a group added inside the source makes it taller. So every result
  reads the anchor's rectangle back rather than describing what was
  asked for.
- `list` takes a range to look in rather than scanning a sheet. There is
  no pivot index in the API, and a whole-sheet grid read for a field
  that nothing else needs would cost a read of the sheet to find at most
  a handful of anchors.

**The read path gains one field.** `gapi.GridFields` asks for
`pivotTable(source)`, which costs nothing on a cell that has no pivot.
Without it the guard sees a pivot's output as ten anonymous non-empty
cells — it already refuses the write, which is the important half, but
it cannot say what it is refusing.

**It names the anchor, and only the anchor**, and the first live run is
why that sentence is here. A pivot's *output* cells carry no
`pivotTable` field: on the wire they are ordinary computed values, and
the definition sits on the top-left cell alone. So a write over the
anchor is refused with what it would cost — "F1 anchors a pivot table,
and a write there replaces it and clears everything it draws" — and a
write into the middle of the output is refused as a non-empty cell,
which is true and less useful. Naming the pivot from one of its output
cells would mean reading up and left of every guarded write on the
chance that one is there. §17a.27 carries that with its cost.

**What a write into a pivot does is worth stating exactly**, because the
refusal has to be true. A `values.update` over one output cell returns
200 and collapses the entire pivot to `#REF!` at the anchor — and
clearing that one cell brings all of it back. The damage is total and
completely reversible, so the refusal says the pivot will stop drawing
until the cell is cleared. An `updateCells` over the **anchor** is a
different act, and that one replaces the pivot outright and silently.

**`manage_data_source`** — `list`, `add`, `refresh`, `cancel_refresh`,
and **registered only with `GSHEETS_ENABLE_DATA_SOURCES=true`** (§17.6a).

**Deleting one is `delete_data_source`, its own gated tool**, for the
reason `edit_dimensions`' delete became `delete_dimensions` (§7.4,
§17a.10): an action that removes what nobody can read back does not
belong inside a tool whose annotations say it is not destructive, and a
host in an auto-approve mode reads those annotations and asks nobody. It
meets §17c's criterion more severely than an action already gated —
`deleteDataSource` takes the DATA_SOURCE sheet and everything on it,
which is strictly more than `delete_sheet` does, and what goes cannot be
read back without re-running a billed query.

It is `Destructive` rather than `Connected`, and needs no BigQuery
scope: spike N sent `deleteDataSource` under this server's two scopes
and got a 400 about the id rather than a 403 about the scope. So it is
reachable — and therefore worth gating — wherever a data source exists,
including one somebody else connected. The first version of this phase
put the delete inside `manage_data_source` behind `confirm` alone; the
cleanup pass found it, and §18 carries the row. `addDataSource` under this server's two scopes returns 403
naming `bigquery.readonly`, so the tool needs a third scope that most
users of a Sheets server should not be asked to grant. `list` is the
exception and needs no scope at all: `fields=dataSources` is accepted as
it stands and returns `{}` where there are none, so `get_spreadsheet`
reports a connected source whatever the configuration.

**Two shipped tools change.** `delete_dimensions` counts the charts and
pivot tables whose ranges intersect the band and names them before the
confirm gate, beside the anchors it already names — deleting a charted
column leaves the chart alive with zero series, and deleting a pivot's
source rows leaves a header and a grand total, and both are silent. That
is the third and fourth silent destroy this project has found on an API
whose reference mentions none of them.

## 8. Tool surface

snake_case verb_noun, no dots. Claude Code prefixes `mcp__<server>__`.
"Gated" means registered only with `GSHEETS_ENABLE_DESTRUCTIVE=true`, and
each gated tool also requires `confirm: true` on the call and sets
`_meta["anthropic/requiresUserInteraction"]`. `GSHEETS_READ_ONLY=true`
registers only the readOnly rows and requests read-only scopes.

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
| `edit_dimensions` | insert, move, resize, auto_resize, group, ungroup rows or columns | — | 1 |
| `delete_dimensions` | Gated: remove rows or columns and the data on them | destructive | 1 |
| `read_formatting` | Number formats, styles, borders, conditional rules over a range | readOnly | 2 |
| `format_cells` | Number format, styles, borders, alignment, wrapping, merges, notes | idempotent | 2 |
| `manage_range` | Named ranges, protected ranges, data validation, tables, banding, conditional format rules | — | 2 |
| `transform_range` | sort, find_replace, trim, dedupe, text_to_columns, fill, copy/cut-paste | — | 2 |
| `manage_anchor` | Durable labels for a row, column or sheet: add, list, move, remove. `anchor:<name>` works wherever a range does (§6.4) | — | 3 |
| `clear_values` | Gated: clear a range's values, keeping formatting | destructive | 1 |
| `delete_sheet` | Gated: delete a sheet and everything on it | destructive | 1 |
| `manage_chart` | Charts and slicers: add, update, delete, move | — | 4 |
| `manage_pivot_table` | Pivot tables on a range | — | 4 |
| `manage_data_source` | Connected Sheets data sources: add, refresh, cancel a refresh, list | — | 4 |
| `delete_data_source` | Gated: remove a data source, its sheet and everything on it | destructive | 4 |

There is deliberately no bulk tool that spans spreadsheets: one
spreadsheet per call, so a wrong id costs one refusal rather than a
sweep.

**Resources** (phase 3, built). `gsheets://{spreadsheet}` is the card;
`gsheets://{spreadsheet}/{sheet}` is that sheet's used range as csv under
one 400 000-character budget. No static list (that would be a Drive
listing) and no subscriptions (the API has no push and no changes feed).

The used range rather than the sheet: a sheet is 1 000 by 26 whatever is
on it, and a resource of 26 000 commas describes nothing. A read cut
short by the budget says so in a second content block rather than in the
CSV — CSV has no comment syntax, so a note in the body parses as a row of
data, which is the silent half-answer this server exists to avoid. The
sheet title is percent-encoded: a title can hold a slash, a question mark
or a hash, and `net/url` would read each of those as structure and hand
back a title with the end missing.

**Registration.** One `Kind` per tool — read, write, idempotent write,
destructive, connected — decides the annotations, whether read-only mode leaves the
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

**Every line a driver prints goes through one redacting helper, and a
gate reads the syntax tree to say so** (`scripts/gates transcript`). The
promise that a transcript is safe to paste held by luck until this
existed: nothing stopped a step calling `fmt.Println` directly, and a
step that did would look exactly like the ones that do not. It found one
on its first run — the spike probes printed raw, and their first two runs
were redacted by hand afterwards, which is the same job done worse. A
sibling found the matching gap from the other side, where a renderer
began printing a person's name somewhere its redactor had never been
told about.

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
  accounts under `GSHEETS_CONFIG_DIR` — separate *storage*, and not a
  separate **grant**. Google revokes the grant rather than the token, so
  `logout` under one profile signs the account out of every profile
  sharing that OAuth client (measured; §18). `logout` says so and names
  them, and `logout -local` deletes the stored copy without revoking.
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
- **Targets, measured 2026-09-06 by `make bench`** on an AMD Ryzen 7 PRO
  7730U, Go 1.27.1, linux/amd64, against the fake. The request counts
  hold and the timings are all comfortably inside their targets — but
  measuring moved the target, because the cost is not where the target
  was written.

  | What | 5 000 cells (the default budget) | 50 000 cells (the maximum) |
  |---|---|---|
  | Decode the response | **7.2 ms**, 1.7 MB, 33 001 allocs | **83 ms**, 17 MB, 329 886 allocs |
  | Build the grid | 0.8 ms | 7.5 ms |
  | Render it addressed | **0.73 ms**, 490 KB | 6.7 ms |
  | The write guard over it | **0.04 ms**, 536 B, 25 allocs | 0.42 ms, 1 KB, 50 allocs |

  **Rendering was never the expensive half.** §11 has said "rendered
  under 20 ms" since the plan was written, and rendering a 5 000-cell
  read takes 0.73 ms — 27 times inside the target. Decoding the JSON that
  carried those cells takes ten times longer, and at the maximum budget
  it takes 83 ms, which is the same order as the network round trip that
  delivered it. Nothing here can make that much faster; it is the shape
  of the wire, and it is the number to quote when somebody asks what
  raising `max_cells` costs. The target stands, and now it names the
  right half.

  **The guard's flat profile is literal.** 25 allocations over 5 000
  cells and 50 over 50 000 — the cost of a guarded write does not grow
  with the rectangle, only with what it finds. It reached that during
  this measurement rather than before it: the guard formatted an A1
  address for every cell and kept ten, which was 28 000 allocations on a
  5 000-cell write and 298 000 on a 50 000-cell one. **13.7× faster and
  1 121× fewer allocations**, over three passes, and each pass is worth
  more than the number.

  The first was wrong in a way only the benchmark could show: it asked
  whether *any* finding set was full, and on a rectangle of plain values
  three of the four sets stay empty, an empty set is not full, and every
  address got built anyway. The measurement did not move, which is how
  the mistake was found — a reading of the code would have called it
  fixed. The second worked and left the cap in two places: the loop
  named every set a cell could land in, so a set added later without its
  clause would be counted correctly and never named, and a message going
  quiet is not a failure anything catches. The third moved the decision
  into `Cells.AddCell`, which takes the coordinates instead of the
  address and formats one only if it will keep it. Nothing outside the
  type knows the cap now, and it is a further 1.6× faster than the
  version that did.

  The counts are unchanged: the card is one request, a 5 000-cell read is
  one request, a guarded write is two. `a1.Parse` is 61–143 ns and
  allocates nothing except for a quoted sheet title; `CheckDestination`,
  which every formatting call pays, is 14 ns and allocates nothing at
  all.

## 12. Distribution and setup

- **Artifacts.** GoReleaser builds linux, darwin and windows on amd64 and
  arm64 from a `v*` tag; `checksums.txt` signed with a keyless Sigstore
  certificate (a cosign 3 bundle); an SBOM per archive; a build
  provenance attestation; `-trimpath` and `mod_timestamp` so a rebuilt
  tag is byte-identical; never a draft. `go install …@latest` is the
  second path, with the version from `debug.ReadBuildInfo()` since
  `go install` applies no ldflags.
- **The Claude Desktop bundle.** Every release carries a `.mcpb`, which
  is a deflate zip: `manifest.json` at the root, the binaries under
  `server/`, and the licence and README beside them. Opening it installs
  the server and asks for the OAuth client JSON, so the alternative to
  shipping one is asking a person to hand-edit a config file.

  **Packed here, in Go** (`scripts/gates/mcpb.go`), rather than by the
  official Node CLI: an interpreter `make check` needs is a prerequisite
  nobody declared, and the standard library writes a zip. What the CLI
  buys is validation against the published schema, and that is not the
  validation worth having — a manifest whose `entry_point` names a binary
  nobody staged is well formed by any schema and produces a bundle that
  installs and then does nothing. `checkManifest` holds it to the staged
  tree instead: `entry_point`, `mcp_config.command`, **every**
  `platform_overrides.*.command`, and every `${user_config.x}` an env
  value spends. Each is broken on purpose in the tests and watched being
  refused.

  A manifest names a command per *platform* and has no key for the
  architecture, so every platform it claims has to work on both. macOS
  gets a universal binary, kept out of the ordinary archives with `ids`
  so the release page does not offer a fourth macOS download. Windows
  gets amd64, which arm64 runs under emulation. Linux gets both binaries
  and a launcher that reads `uname -m` and **`exec`s** the right one —
  not a call, because the server talks MCP over that process's stdio —
  writing any failure to **stderr**, because a line of English on stdout
  corrupts the JSON-RPC session before the client's first request.

  **The version comes from one place** and is checked in five: the bundle
  filename, the archive filenames, the manifest inside the bundle, the
  binary's own `--version` and `checksums.txt`. The committed manifest
  carries `0.0.0-dev` and the packer refuses any other value, so a
  manifest in the tree cannot claim a stale version; the real one goes in
  through a JSON decode and encode rather than a substitution over text.

  **Packing runs as the universal binary's post hook**, the one point
  where every binary exists and `checksums.txt` has not been written —
  and that is only half of what puts it in that file. The checksum step
  covers goreleaser's own artifacts, and a file a hook dropped into
  `dist/` is not one, so the bundle is also named in `checksum.extra_files`
  and in `release.extra_files`. Being in `checksums.txt` is what gets it
  signed, since the signature is over that file. The first snapshot build
  here agreed about the version in four places and left it out of the
  fifth, which is exactly the shape of shipping unsigned while looking no
  different — found by checking rather than by reading.
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

  - **The driver's coverage of the tool surface is a gate, not a
    claim**, and it is checked twice because the two halves catch
    different things. `scripts/gates live-cover` reads the driver's
    source, so it runs in CI where there are no credentials and fails
    when somebody adds an option without a step. The driver itself
    records what it actually sent and checks the same thing against the
    tool list the server publishes, which is the authoritative half: a
    step can exist in the source and never execute — sitting in a slice
    nobody passes to `run`, or behind a condition that was false — and
    the static half counts it as covered. Demonstrated by removing one
    `run` call, after which the static gate still reported 26 of 28.
    Both read the exemption list in `internal/livecover`, so an option
    excused in one is excused in the other. Together they found the
    promise below already broken: 14 of 28 options had never touched the
    network.
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

  **Two rules for writing those tasks, inherited 2026-09-06 from a
  sibling's first full eval run rather than from its plan.** Two of its
  thirteen tasks could never have passed, and neither failure was a
  server defect: one asserted a file on disk under a name the server
  deliberately does not use, and one asserted a grant to an address at
  IANA's reserved documentation domain, which the platform refuses. Both
  asserted an end state the world does not permit.

  - **Every task answers "what does this check when the world says no?"**
    Where the end state cannot exist, score the trace instead and *print*
    which half went unchecked. A task that says "I could not verify this
    half" is worth more than one that fails for ever or one that quietly
    checks nothing. Here that bites on anything asserting an absence,
    anything needing a second account — the protected-range refusal, and
    sharing, which is not even this server's — and anything resting on
    Drive's indexing lag, which is the `search_spreadsheets` task. A task
    that polls beats a task that fails.
  - **A prompt containing an unsubstituted placeholder is refused, and a
    unit test walks the whole table.** That sibling's first run passed
    two tasks while sending the agent a literal `{folder}`: one found the
    file by name across the account and one wanted a refusal and got one
    for the wrong reason. That is the end-state-versus-trace problem
    happening inside the harness built to catch it, so the guard belongs
    in `go test` where it costs nothing rather than in a live run that
    costs ten minutes and an account.

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

- **A. The coercion matrix** (phase 1, **run 2026-09-06**): what
  `USER_ENTERED` does to `1-2`, `007`, `=1+2`, `$100.15`, `TRUE`,
  `'0123`, `2026-09-05`, a 60 000-character string, and what `RAW` does
  to the same. The recorded answers are the fake's replay table. Run
  with a second half the plan had not asked for and needed: what
  `values.update` does when the range and the array disagree about size.
- **B. Append's table detection** (phase 1, **run 2026-09-06**): where
  `append_rows` writes on a sheet with a gap below the block, with
  `OVERWRITE` and with `INSERT_ROWS`, and what `tableRange` reports.
- **C. Range syntax** (phase 0, **run 2026-09-06**; results in §18, and
  the probe is `scripts/spikes`): whether `values.get` accepts a named
  range (its reference documents only A1 and R1C1) and a table reference
  (`Table1[Column]`); that a quoted title behaves as documented; that a
  named range really does shadow a same-named sheet when the title is
  unquoted, which is the one trap here that fails silently rather than
  loudly; and what a bad range's error actually says.
- **D. Google's Sheets MCP** (any phase, needs preview enrolment):
  connect once, dump its tools and schemas, record what `get_values`
  returns for a formula and an error cell. Keeps §1 honest. Nothing
  depends on it.
- **E. Error shapes** (phase 0, **run 2026-09-06**, partially: three of
  the six shapes need a second account, a read-only token or a
  sustained quota breach and are recorded as unobserved): the status, status string and message
  for a missing spreadsheet, a missing sheet, a bad range, a protected
  range, a read-only scope attempting a write, and a 429. The
  troubleshooting page documents only 400, 500 and 503, so the class
  mapping in §6.5 is otherwise built from the Drive API's documented
  error vocabulary rather than from Sheets itself.

  **The 429 was observed on 2026-09-07**, unplanned and by this
  repository's own doing: four runs of the live driver inside a few
  minutes, on top of the spikes, spent the sixty-reads-per-minute-per-user
  allowance. `spreadsheets.get` came back `HTTP 429 RESOURCE_EXHAUSTED
  (RATE_LIMIT_EXCEEDED): Quota exceeded for quota metric 'Read requests'
  and limit 'Read requests per minute per user'`. The class mapping made
  it `[rate_limited]` and the message said to retry shortly, which was
  true — the next run passed. Two of the six shapes are still
  unobserved: a second account, and a read-only token attempting a
  write.
- **F. Checkpoint economics** (phase 1, **run 2026-09-06**, rejected):
  whether Drive's `files.get` `version` field changes on every Sheets
  edit, which would give a cheaper spreadsheet-wide staleness check than
  re-reading a range. `modifiedTime` was read in the same poll.
- **H. Creating with a sheets list** (phase 1, **run 2026-09-06**): added
  during phase 1 rather than planned. Whether `spreadsheets.create`'s
  `sheets` are added beside the one Google always makes or are the whole
  set — which decides what `create_spreadsheet` promises and where its
  seed values land.
- **I. What a values write keeps, and where a move lands** (phase 1,
  **run 2026-09-06**): also added during the phase, both questions raised
  by a review of code that had already assumed an answer. Whether
  `values.update` leaves a cell's note and validation rule alone, which
  decides whether the guard's "the write removed notes on A1" is true;
  and whether `moveDimension`'s `destinationIndex` is read against the
  sheet before or after the move.
- **G. Size behaviour** (phase 2, **run 2026-09-06**): what a write past
  10 million cells or column ZZZ returns, and how a 50 000-character cell
  round-trips. All three answered, and the first run of the probe
  answered none of them properly — it measured the length of a JSON body
  and called it the length of a cell, and it asked for 18 279 columns on
  a 1 000-row sheet, where 18.3 million cells trips the cell ceiling
  before the column one. Both were visible only by reading the
  transcript. Results in §18.
- **J. What a table does to the rules over its range** (phase 2, **run
  2026-09-06**): added during the phase, and not from the plan — the live
  driver watched a conditional format rule disappear across an `addTable`
  and a `deleteTable` and could only see that it was gone by the end.
  Counted after every step, the probe puts it on the delete.
- **K. Whether developer metadata is a durable anchor** (phase 3, **run
  2026-09-06**): §6.4 offers "remember this row as `invoice-totals`" and
  the reference says only where metadata may attach, never what happens
  when the sheet moves under it. An anchor that follows a row and one
  that holds a row *number* are different features, and the second points
  at somebody else's data after one insert. So the probe mutates the
  sheet — insert, delete, move, sort, a values write — and reads the
  location back out of `developerMetadata.search` after each, alongside
  what is on that row now. Results in §18. Its first run answered four of
  the eleven questions against rows the anchor had already left, which
  looked like a verdict and was a stale constant; every step that names a
  row now asks where the anchor is at that moment.
- **L. What a chart is once it exists, and what it survives** (phase 4,
  **run 2026-09-07**): whether `updateChartSpec` merges or replaces,
  since it carries no field mask; what a chart's source range does when
  the column under it is deleted; what `deleteEmbeddedObject` says it
  did; and what the card's `charts(chartId)` mask leaves unread. Results
  in §18. Its first run answered the invalid-spec question against the
  wrong half of the request: both probes omitted a position, so the API
  refused them on the position and never looked at the spec, under a
  heading that said "spec".
- **M. What a pivot table is on the wire** (phase 4, **run 2026-09-07**):
  a pivot is not a `batchUpdate` request at all but a field of
  `CellData`, and its output "is computed dynamically", so its footprint
  is in no request. What the server's own grid mask sees over that
  output decides whether the write guard can see a pivot at all. Results
  in §18. Its first run asked two invalid-pivot questions that both came
  back "No sort order specified", because the probe left out a
  `sortOrder` and a validation order this server did not know about
  answered first.
- **N. Whether a data source is reachable at all** (phase 4, **run
  2026-09-07**): `addDataSource` "requires an additional
  `bigquery.readonly` OAuth scope" and a BigQuery source needs a
  billing-attached Cloud project. §17.6 had fixed this server's scopes
  at two. So the spike asks not how the tool should be shaped but
  whether its calls can be made, and what a caller sees when they
  cannot. Results in §18, and they reopened §17.6.
- **P. The three deferred cleanups that wanted a probe** (phase 4, **run
  2026-09-07**): §17a.7, §17a.15 and §17a.20 were each left undecided
  under hard rule 12, because the fake returns whatever a test asks it
  for and so cannot say whether a field mask is accepted. Phase 4 is the
  phase with a live run. All three are answered and all three are now
  closed in §17a. Its first run reported §17a.20 unsupported on a mask
  whose parentheses this spike had got wrong by one; the mask is printed
  and balance-checked before it is sent now.

## 16. Delivery phases

Each phase ends in a tagged release and waits for an explicit "go". Each
phase is one session: the next one starts from this repository alone.

**Phase 0 — skeleton, gates and reading (v0.0.1). Done 2026-09-06,
including the live work: spikes C and E ran, the live driver ran (32
steps, 0 failed, 1 undetermined — Drive's content index, distinguished
from a broken search rather than guessed), and the configuration surface
profiles, read-only scopes and `logout` was exercised against a real
account. Not tagged: `main` is the maintainer's, and CI has never run on
macOS or Windows.** Scaffolding (§5,
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

**Phase 1 — writing values and sheets (v0.1.0). Done 2026-09-06,
including the live work: spikes A, B and F ran, spike H was added and
ran, and the live driver ran five times (83 steps, 0 failed, 1
undetermined — Drive's content index again). Reading each transcript
found something the green count hid: a `sheetId: 0` on every create, a
reorder off by one, three counts that were zero for a reason the step
could not see, a coercion line that read as no change, and a guard hole
where a formula showing `#REF!` needed only `overwrite`. The review
passes then found four more, including a `dry_run` that could not
preview any write the guard refused. Not tagged: `main` is the maintainer's.** Spikes A, B and F
first. Then `create_spreadsheet`, `write_values`, `append_rows`,
`manage_sheet`, `edit_dimensions`, and the gated `clear_values` and
`delete_sheet`; the guard, the coercion diff, checkpoints and `dry_run`;
`sheetstest` grows the write paths and the recorded coercion table;
`scripts/livesheet` covers every tool. Plus the three §17a decisions the
phase owed: one `service.Budget`, the scratch-file cleanup, and the three
error classes that were declared and not yet emitted.

**Phase 2 — formatting and structure (v0.2.0). Done 2026-09-06,
including the live work: spike G ran, spike J was added and ran, and the
live driver ran the whole surface — 143 steps, 0 failed, 1 undetermined
(Drive's content index again), 157 of 159 tool options exercised.** `read_formatting`, `format_cells`,
`manage_range` and `transform_range`, with a step in the driver for every
option of each.

It took three runs of the driver. The first failed five steps: three were
the server and two were the driver's own expectations, and §18 carries
the split. The second failed four, and the reason is worth writing down —
an edit to the driver had never reached disk, because the script making
it aborted on a later assertion and wrote nothing, so two steps that
looked fixed were not. The third failed two: a merge band that had been
assumed to hold values held none, so the merge it was supposed to refuse
simply succeeded. A step that asserts about values now writes them
itself.
The three server findings — a merge across a frozen edge, a named range
with a space in it, and `deleteTable` taking the conditional format rules
over its range — are all guarded now, and none of them appears in any
reference read for this project. Spike G's first run is the sharper
lesson: it answered none of its three questions properly and printed
something that looked like an answer to each.

Spike G was written first in the plan and second in fact. That is a
deviation and it is recorded rather than smoothed over: the questions it
asks — the column ceiling, the ten-million-cell ceiling, a cell at
exactly 50 000 characters — bear on the refusals this server writes
before sending, not on the shape of the four tools. It cost nothing here,
and the ordering in the plan is still the right one.

The union builders for formatting, validation, protection, tables,
banding and conditional formatting are in `internal/plan`, and the guard
now covers what formatting and the transforms destroy: a merge's
discarded values, a cleared format, a replaced note, a paste's landing
rectangle, a split's spill to the right, and a replacement inside
formulas. Plus the §17a decision the phase owed: the structural tools'
English moved into the renderer.

**Phase 3 — resources, anchors, evals, performance (v0.3.0). Done
2026-09-06, including the live work: spike K ran, the live driver ran
(169 steps, 0 failed, 1 undetermined) and the evals ran four times (15 of
16 tasks passing). Every transcript was read and every one of them found
something no count would have: two wording defects in the driver's run,
four harness defects across the eval runs, and one real finding about the
read window (§17a.25). Not tagged: `main` is the maintainer's.**
`gsheets://` resources; developer metadata as durable anchors (§6.4); the
agent evals and the fixes they force; `make bench` and the numbers in
§11; `/simplify` and `/code-review high` over the tree, with findings
resolved or recorded in §17a.

Spike K ran first, which is the ordering §15 asks for and phase 2 got
wrong. It answered the question §6.4 had been assuming since the design:
an anchored row survives an insert above, a delete above, a
`moveDimension` and a `sortRange`, and after a sort it lands where its
row's *values* landed. Deleting the row deletes the anchor and the reply
says nothing — the second silent delete this project has found, after
phase 2's `deleteTable`, and `delete_dimensions` now names them first.

Its first run answered four of eleven questions against rows the anchor
had already left. A probe whose whole subject is that the row moves had
hardcoded row 2 in four steps, so "0 matches" read as a verdict on row
lookups and was a stale constant. Every step that names a row now asks
where the anchor is at that moment. That is the third time in three
phases that reading a transcript found what counting it could not.

`make bench` moved a target rather than confirming one. §11 has said "a
5 000-cell read rendered under 20 ms" since the design; rendering takes
0.73 ms and decoding the response takes ten times longer. It also found
the guard formatting an A1 address for every cell in the target and
keeping ten — 28 000 allocations on a 5 000-cell write, now 25. The
first fix for that was wrong and the benchmark is what said so: the
number did not move.

**Phase 4 — charts, pivots, data sources (v0.4.0). Code complete
2026-09-07, including the live work: spikes L, M, N and P ran, the live
driver ran five times (206 steps, 0 failed, 1 undetermined — Drive's
content index again), and both review passes ran with every finding
fixed. Not tagged: `main` is the maintainer's.** `manage_chart`,
`manage_pivot_table`, `manage_data_source`, each verified live before it
is designed in detail. Then the discovery document is diffed against what
the client calls, and every one of the 69 union members is either used or
listed here as deliberately out.

**The diff, run 2026-09-07 against revision 20260831: 54 of the 69 are
built and 15 are not.** Each of the fifteen is out for a reason, and the
reasons fall into four groups.

**A gate holds this now rather than this paragraph doing it.**
`testdata/api-surface.json` is what Google publishes, written by `gates
api-diff` from the discovery documents; `testdata/api-coverage.tsv` is
one verdict per item, by hand; and `gates api-coverage` runs offline in
`make check` and holds the two files and the code to each other. A
published item with no verdict fails, a verdict for something no longer
published fails, a request some builder constructs while the record
calls it out fails, and a verdict of `used` that no code backs fails.
So a capability Google adds stops the build rather than aging quietly
into a paragraph that used to be true — which is what §17a.26 says about
claims a comment holds. The groups below are why; the file is the
record.

*A filter view is a saved view of somebody else's screen* —
`addFilterView`, `updateFilterView`, `deleteFilterView`,
`duplicateFilterView`, `setBasicFilter`, `clearBasicFilter`. The card
reports the filter views a spreadsheet has, because one changes what a
person sees when they open it. Creating them is a preference rather than
data, and a model that leaves a saved filter behind has changed how the
spreadsheet looks to everyone who opens it next.

*Four are a second way to do something this server already does*, and
the second way is worse. `appendCells` appends without the table
detection `values.append` does, so `append_rows` would lose the answer
to "where did it land". `appendDimension` adds rows at the end, which
`manage_sheet resize` covers with a size the caller states rather than a
count they have to work out. `pasteData` takes a delimited string and
splits it, where `write_values` takes the grid the caller already has.
`insertRange` and `deleteRange` shift cells sideways within a
rectangle — the operation a spreadsheet interface calls "insert cells,
shift right" — and every shape of it this server offers moves whole rows
or columns, where what moves is visible in the address rather than in
the option.

*Two are cosmetic on an object this server manages*:
`updateEmbeddedObjectBorder` puts a border on a chart, and
`updateDimensionGroup` collapses a group that `edit_dimensions group`
creates. Both are worth having and neither is worth a nineteenth
argument yet.

*One is out because §17.6a drew the line where it did*:
`updateDataSource` changes a source's query in place, which is the one
Connected Sheets verb `manage_data_source` does not offer. Delete and
add is two calls and one refresh, and it cannot half-apply a query
change to a sheet somebody is reading.

*And one is a decision rather than an omission*:
`updateSpreadsheetProperties` renames the file, changes its locale and
its recalculation setting. Renaming is Drive's, and §1 says so. The
locale decides how every number in the spreadsheet is parsed and
displayed, and changing it under somebody is a rewrite of the whole
file's meaning that no result could summarise.

**Three runs of the driver, and each of the first two found something
the count could not.** The first failed three steps: a `stackedType` on
a LINE chart, which Google refuses outright and this server now refuses
first; an `updateSlicerSpec` with no field mask, which the code had
never set while a comment three lines above claimed it did; and a step
whose own assumption was wrong, asserting that a two-series chart under
a one-column delete would be left with nothing to draw.

That third one is the finding, because the step was wrong *and* the
server was: the refusal had been saying "nothing to draw" for every
chart the band touched. It counts now. The second run passed 206 steps
and reading it found two more things a pass hides — a refusal whose
clause did not compose into its sentence ("the chart(s) "X", which would
be left with nothing to draw in place"), and the guard refusing a write
into a pivot's *output* as merely "not empty", which is true, less
useful than it looked in §7.6, and now §17a.27.

That is four phases in four where reading a transcript found what
counting it could not, and the first two here were in the fake as much
as the server: a unit test cannot refuse a request the fake accepts, and
the fake accepted both.

**The two review passes found nineteen things between them and every one
was fixed.** `/code-review high` found eleven, of which three matter
beyond their own line: a listing field that could never be populated
against the real API and passed its test only because the fake ignores
field masks (§17a.29); an `add` that replaced an existing pivot table
and its whole output in silence; and a pivot's reported footprint
absorbing anything computed below and right of it, which handed a caller
a rectangle of cells to avoid that were never the pivot's.

`/simplify` found eight, and its best is one no gate could have caught:
`manage_data_source` carried a `delete` that removes a whole sheet under
a `Kind` whose annotations say `destructiveHint: false`, reachable with
`GSHEETS_ENABLE_DESTRUCTIVE` off. §17c states the criterion this
project already applies three times; the delete met it more severely
than an action already gated, and the fix is the split §17a.10 made
once before. The same pass also found a phrasing literal being used as a
control flag, where rewording a preview would have silently switched a
branch.

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
   **Amended 2026-09-07 by §17.6a**, which adds a third scope for one
   gated tool. The rule above still holds for everything else: no scope
   is added to reach a file, and none is added by default.

6a. **`bigquery.readonly`, requested only when
   `GSHEETS_ENABLE_DATA_SOURCES=true`** (decided 2026-09-07, after spike
   N). `addDataSource` refuses under this server's two scopes with `403
   … Please include bigquery.readonly scope`, and refreshing a real
   BigQuery source needs it too; the 200s spike N got from `refresh` and
   `cancel` came from a spreadsheet with nothing to refresh and prove
   nothing. So Connected Sheets cannot be reached at all without it.

   The scope is real and the recommendation against taking it was
   weighed and overruled: a Sheets server whose login screen asks for
   BigQuery is asking most of its users to grant access to a product
   they do not have, for a tool that needs a billing-attached Cloud
   project before it does anything. What settles it is that the cost is
   avoidable — the scope is requested only when the setting is on, so
   the default login is unchanged and a user who wants Connected Sheets
   opts in and logs in again. `doctor` says which scopes the token
   actually carries, so the "I turned it on and it still fails" case
   answers itself.

   `list` is deliberately outside the gate. `fields=dataSources` is
   accepted under the existing scopes, so `get_spreadsheet` reports that
   a spreadsheet is connected to a data source for every user, at no
   cost and no consent.
7. **Go directive `go 1.27.1`**, matching the sibling servers.
8. **A tool result is not masked; the artefacts meant to be pasted are**
   (decided 2026-09-06, after the live run raised it). `read_range`
   returns cells and `search_spreadsheets` returns titles, ids and the
   owner's address, because the caller asked, the server holds their own
   token, and all of it is one click away in the Sheets or Drive
   interface — masking would cost an answer without removing anyone's
   access, and the owner is what makes an `[ambiguous]` refusal usable.
   What the server does instead is not fetch what nothing needs, and
   mask the three things that *are* meant to be pasted: a debug log,
   `doctor` and `status`. §18 carries the reasoning and the sources.

## 17a. Deferred cleanups

Sixteen open, eleven closed. A closed entry keeps its text and the decision that
closed it, so nobody reopens a question that was answered. Five of the
open ones were raised by phase 2's own review passes and are recorded
here rather than fixed in passing; two of those (entries 12 and 14) are
about phase 1's tools as much as phase 2's, and entry 14 is left alone
because changing it would alter behaviour that was verified live and
cannot be verified again yet.

1. **The live driver cannot trash the spreadsheets it creates.**
   **Decided 2026-09-06: accept the manual cleanup, and make it one
   search.** §17.6 asks for `drive.readonly` and trashing needs a
   write-capable Drive scope, so the two options written here were a
   driver-only credential with a wider scope or living with the mess. A
   third was considered and rejected: reusing one scratch spreadsheet
   across runs would leave exactly one file forever, and it would also
   mean a driver whose results can be explained by the last run. A
   correctness driver buys isolation with tidiness, not the other way
   round. So every file it makes carries the prefix `livesheet scratch`,
   and the run ends by printing the Drive search that finds all of them.
   A run leaves two: the scratch spreadsheet, and the one
   `create_spreadsheet` makes, which cannot be tested without making one.
2. **Three error classes are declared and not yet emitted** — `blocked`,
   `conflict` and `unsupported`. **Closed 2026-09-06:** the write guard
   emits `blocked`, the checkpoint emits `conflict`, and
   `edit_dimensions delete` emits `unsupported` when destructive tools
   are off. The gate's `plannedClasses` list is empty, and it still
   reads from both sides, so a class listed there and emitted anyway is
   as much a failure as one declared and never emitted.
3. **`cmd/` is under a lower coverage floor than `internal/`.** It was
   outside the coverage profile entirely until phase 1's audit —
   `-coverpkg=./internal/...` and a gate that derived its package list
   the same way — so 571 lines of `login`, `logout`, `status` and
   `doctor` had no tests and nothing could report that they had none. It
   is now measured, at 48% against a floor of 40. What is left uncovered
   opens a browser, holds a token or serves stdio; the live driver and
   the smoke gate cover those against the real thing. Raising the floor
   to `internal/`'s 80 needs either a fake OAuth endpoint or a way to
   drive `serve` in-process, and neither is phase 2's business.
4. **The fetch window is bounded by cells, the rendering by
   characters.** At the default budgets they land within a few rows of
   each other, but a caller who raises `max_cells` — which the tool
   description invites — fetches rows that no rendering can reach, and
   the next continuation re-reads from where the render stopped. A
   character-aware row cap would fix it and could under-fetch if the
   estimate is wrong, so it waits for phase 3, where `make bench`
   measures rather than guesses. **Still open**, and phase 1 met it
   once: a test asserting the cell default had to raise the character
   budget out of the way to see it.
5. **Budget policy sat at three altitudes.** **Closed 2026-09-06, before
   the five write tools were written rather than after.**
   `service.Budget` defaults and clamps in one place, `max_matches` has
   a maximum for the first time, and an over-range budget is refused
   rather than silently reduced — a caller who asks for 200 000 cells
   and gets 50 000 reads a footer describing a window they never asked
   for. The tools pass the caller's numbers through untouched.
6. **The live driver cannot clean up after itself.** The other end of
   §17a.1, and closed with it.
7. **A structural write costs a card re-read it could avoid.**
   `manage_sheet`, `delete_sheet` and `edit_dimensions` each send a
   `batchUpdate` and then re-read the card, because the write invalidated
   the cached one and the result lists the sheets afterwards.
   `batchUpdate` takes `includeSpreadsheetInResponse` with
   `responseRanges`, which §7.3 already names, and it would fold that
   read into the write — one request saved on every structural change,
   which at sixty writes a minute with one in flight is about a second
   each. Two things have to be settled first: the response's spreadsheet
   arrives unmasked, so it carries the `spreadsheetUrl` the card's field
   mask now deliberately drops, and the reply has to re-prime the cache
   rather than just fill the result. It needs a live probe before it is
   adopted (rule 12), which is why phase 1 recorded it rather than
   guessing. The wire fields are not written until then.
   **Closed 2026-09-07 by spike P:** the standard `fields` parameter does
   apply to the reply, so `updatedSpreadsheet(<CardFields>)` comes back a
   card with no `spreadsheetUrl` in it. The concern was real — the
   unmasked reply carries the URL — and the mask the card already has is
   the whole fix.
8. **A refusal names the tool's argument from inside the guard.**
   `plan.Blocker.Allow` holds `"overwrite"`, `"overwrite_formulas"` and
   `"allow_external_formulas"` — the JSON names `internal/tools`
   publishes — so the bottom layer knows the top layer's schema, and
   renaming an argument would make the guard's refusals lie with nothing
   failing. A typed reason on `Blocker`, mapped to a name by the service,
   is the fix. Phase 1 removed the other half of this (the tool layer no
   longer imports `plan`); this half waits, because the messages it would
   touch were verified live and are worth changing on purpose rather than
   in passing.
9. **The service composes the English that the renderer decorates.**
   `manage_sheet` and `edit_dimensions` build a sentence fragment
   ("insert 2 row(s) before row 2 on %q") that `render` then capitalises
   and wraps. Phrasing decided above the renderer is phrasing the
   renderer's goldens cannot cover. The fix is to return the parts —
   action, band, count, title — and let `render` own the template, which
   is what `render.Write` and `render.Append` already do. Phase 2 touches
   both tools and is the moment to do it. **Closed 2026-09-06:**
   `render.SheetAct`, `render.DimensionAct` and `render.Band` carry the
   parts and own the templates, and `plan.Band` lost its `String`, `Unit`
   and `Start` with it — the words are in one place, which is what stops
   a refusal and a result phrasing the same act differently.
10. **`edit_dimensions delete` was hidden from the tool's description,
   not from its schema.** **Closed 2026-09-06** by making it
   `delete_dimensions`, its own destructive tool. The description hid the
   action while `confirm`'s description still named it and `action` had
   no enum to omit it from — and `edit_dimensions`, registered as a plain
   write, advertised `destructiveHint: false` while offering an
   irreversible action. See §7.4.
11. **A protection cannot name its editors.** `manage_range
   protected_range` sets a description and the warning-only flag, and the
   API's `editors` — the accounts allowed to edit past a protection — is
   not offered. The default with no editors is the strongest form, which
   is the one worth having, and the field takes email addresses: hard
   rule 1 makes an address an awkward thing for the live driver to send
   and for a transcript to carry. **Still open**, and it wants a decision
   about how the driver would exercise it before the argument exists.
12. **`transform_range` takes twenty-one arguments.** Nine actions, each
   reading a different handful, and an argument that means nothing for
   the action given is silently ignored rather than refused: a `sort_by`
   passed with `action: trim_whitespace` does nothing and says nothing.
   The tools that came before it have the same shape and the same hole
   — `manage_sheet` ignores `pixels`, `edit_dimensions` ignores
   `colour` — so the fix belongs at the registration layer rather than in
   one tool: a per-action list of the arguments it reads, checked once,
   the way `dry_run` is found by reflection rather than remembered per
   tool. **Still open**, raised by phase 2 and applying to phase 1's
   tools as much as its own.
13. **`manage_range banding` only makes row bandings.** The API bands
   rows or columns, and this offers rows: an `add` always writes
   `rowProperties`. A column banding made in the Sheets interface can be
   recoloured and deleted through this tool — the update reads which axis
   the banding already has, after a review found it writing
   `rowProperties` onto one and leaving it carrying both sets, which the
   API rejects — but there is no way to create one here. It wants a
   `columns` argument, which is a fifth thing for a caller to get right
   on a tool that already takes eighteen, and it is worth deciding
   alongside entry 12 rather than before it. **Still open.**
14. **`dry_run` is answered by three tools and refused by two.**
   `write_values` and phase 2's three previews come before the guard, for
   the reason `write.go` gives: a dry run sends nothing, so there is
   nothing to guard, and the refusal's own advice is to use one.
   `clear_values` and `manage_sheet` call `refuse` first, so a dry run
   over a protected range is refused rather than answered — the same bug
   phase 1 fixed in `write_values` and left in its neighbours.
   Registration guarantees by reflection that the flag exists and nothing
   guarantees it is honoured the same way, which is the second half of
   the rule the reflection check exists for; the fix is one service
   helper that decides the ordering once. **Still open**, and left alone
   deliberately: it changes the behaviour of two tools that were verified
   live in phase 1, and phase 2 has had no live run to verify them again.

   Phase 3's `manage_anchor` joins the second group, and knowingly. A
   dry-run `add` under a name already in use is refused rather than
   previewed. The refusal carries the same information the preview would
   — the name, where the existing anchor points, and `action=move` as the
   way on — so nothing is lost except the consistency, and the ordering
   is the service helper's to decide when one exists rather than a third
   tool's to guess at now.
15. **`manage_range` reads the conditional format rules in a request of
   their own.** Counting them for an update or a delete costs a
   `spreadsheets.get` that the card could carry: `conditionalFormats` is
   sheet metadata, and the card is already in hand and cached. That would
   make a rule update two requests instead of three, and the model is
   steered into the shape that pays it twice — `read_formatting` reports
   the index, then `manage_range` fetches the rules again to check it.
   It needs a live probe first (rule 12): the fake returns
   `conditionalFormats` whatever the mask says, so no test here can tell
   whether the card mask accepts the field or what it adds to a payload
   every `get_spreadsheet` pays for. **Closed 2026-09-07 by
   spike P:** the mask is accepted, and a sub-mask of
   `conditionalFormats(ranges)` answers the counting question for 294
   bytes where a whole rule costs 651. The card carries the ranges, and
   a rule update is two requests instead of three.
16. **`leaks history` spawns one `git cat-file` per blob.** A single
   `git cat-file --batch` fed the ids on stdin would do it in one
   process. It is a manual gate, so the cost is nobody's per-push
   problem, but it grows monotonically with the history. **Still open.**
17. **A committed binary is invisible to every gate here.** The leak
   scan skips any file containing a NUL byte, which is what keeps it from
   choking on an image, and gitleaks looks for credentials rather than
   for size. So a compiled binary committed by a wildcard `git add` would
   pass `make check` and CI. Not hypothetical: two sibling repositories
   found one in their history on 2026-09-06, one of them pushed to a
   public remote across three commits. This tree is clean — the largest
   tracked file is `docs/architecture.md` at 204K, a NUL scan over every
   tracked file finds nothing, and `.gitignore` has carried `/gates`,
   `/.gates`, `/.gates.exe` and `/livesheet` since phase 0 with a comment
   saying to use `go run` — but that is the ignore rules working, not a
   gate. A tracked-file size ceiling with named exceptions would catch
   it and needs no dependency. **Still open**, and deliberately not built
   in passing: a gate nobody has watched fail is not a gate.
18. **The stdio smoke test has never been watched against a server that
   dies during initialisation.** It returns the child's stderr on its
   error paths, so the shape a sibling got wrong — a bare broken pipe
   with the panic that explained it discarded — should not happen here.
   Should not is not the same as has not: nothing has faked a server that
   panics before its first reply and read what came back. **Still open**,
   and recorded as unverified rather than counted as covered, which is
   the distinction §16 keeps making about reading a transcript.
19. **`Cells.Full` coupled the guard's loop to a cap the type owns.**
   The loop asked each finding set whether it could still name a cell
   before formatting an address, which is what took a guarded write from
   28 000 allocations to 25 (§11) — and it made the loop name every set a
   cell could land in, so a set added later without its clause would be
   counted correctly and never named. A message going quiet is not a
   failure anything catches. **Closed 2026-09-06**, in the same review
   pass that raised it: `Cells.AddCell` takes the grid and the
   coordinates and formats the address only if it will keep it, so
   nothing outside the type knows the cap. It is also 1.6× faster than
   the version that asked.
20. **`delete_dimensions` pays a request to find the anchors it would
   take.** `anchorsOnBand` is a `developerMetadata.search`, so an
   unconfirmed delete went from two requests to three and the real
   blocked-then-confirmed pair from four to six — about two seconds at
   one request in flight. The cheaper shape is to fold it into the grid
   read `countBand` already makes: `spreadsheets.get` with `ranges` can
   return `sheets.data.rowMetadata.developerMetadata`, which is exactly
   the anchors on the band being counted, for no extra request. It needs
   a mask of its own rather than an extension of `gapi.GridFields`, which
   the whole read path shares, and `rowMetadata` emits an object per row
   in the window — so it needs a live probe before adoption (rule 12).
   **Closed 2026-09-07 by spike P**, with the cost measured rather than
   assumed: the mask is accepted, and a four-row window returns four
   `rowMetadata` objects for 713 bytes, one of them carrying the anchor.
   That is about 180 bytes per row of the band — right for the bands a
   person deletes by hand, wrong for a ten-thousand-row one, so the fold
   happens under a row count and the search stays for the rest. The
   probe's own first run reported the mask unsupported, on a mask this
   repository had written with one parenthesis missing.

   The placement before the confirm gate is right and stays: a refusal
   has to name what it is refusing. Phase 4 adds charts and pivot tables
   to what that refusal names.
21. **A resource builds a grid the size of its window, not its data.**
   `SheetCSV` fetches up to `config.MaxMaxCells` and `grid.Build` pads to
   the whole requested rectangle, so a sparse sheet holding sixty cells
   allocates megabytes to describe them — 3.4 MB of a measured 4.2 MB on
   one such read. The rendering is trimmed to the used range now, which
   took the path from 42 ms to 23 ms, but the padding is upstream of
   that. Building over the response's actual extent would fix it and
   changes `grid.Build`'s contract, which the whole read path shares.
   **Still open**, and it costs allocations rather than requests, on a
   path already paying a round trip.
22. **`SheetCSV` walks a pipeline `Read` already owns.** Locate, fit,
   fetch with grid data, build, render — the same seven steps, differing
   only in trimming to the used range and in the note it writes. Reusing
   `Read` would mean rendering an addressed grid nobody asked for on
   every resource read; keeping both means a change to the fetch window
   has two places to land. **Decided rather than deferred: keep them
   separate**, because the resource's whole point is the used range and
   `Read`'s is the window the caller named. Recorded so the duplication
   reads as chosen rather than unnoticed.
23. **The evals and the live driver carry the same redaction helper
   twice.** About sixty lines — the registry, `reg`, `substitute`,
   `redactLine`, `sec`, `line` — differing only in a comment. Both
   already import `internal/redact`, which is where a shared registry
   would go, and the transcript gate now reads both directories. The fix
   that both files' comments describe, masking what registration cannot
   know about, had to be made twice. **Still open**: it is a refactor
   across two build-tagged programs neither of which CI can run, and the
   phase that adds the second copy is the wrong one to do it in.
24. **The leak gate was weakest on the code that most needs it.** It
   scanned tracked files, so a phase's new files were invisible to it
   until they were staged — and a phase's new files are exactly the ones
   nobody has scanned before. Phase 3 ran `make check` green a dozen
   times over 167 files while 28 of its own were untracked; the first
   `git add -A` took the count to 195 and the gate immediately found a
   fixture spreadsheet id that did not declare itself synthetic, in a
   test written that afternoon. Nothing was wrong with the gate: it did
   what it said. The claim "make check is green" was the thing that was
   wrong. **Closed 2026-09-06:** the scan reads `git ls-files --others
   --exclude-standard` beside the tracked list, and the ordering is the
   point — a file refused while it is still untracked fails *before* a
   wildcard add can sweep it in, where a tracked-only scan catches it one
   commit too late. `--exclude-standard` honours `.gitignore`, so a build
   output with a rule of its own is left alone; it cannot be committed
   either. Watched failing on all four cases in a throwaway repository:
   an untracked file carrying an id, an untracked ELF, a gitignored ELF
   that must pass, and a clean tree.

   The binary half was already there and this document said otherwise
   for an hour: a tracked binary has been a finding rather than a skip
   since phase 0, which a probe confirmed by building 8.5 MB to the
   repository root and watching the gate name it. Two sibling
   repositories put a compiled `gates` binary into their history this
   week, one of them public, which is what sent somebody looking.

   **Three of us got that question wrong from the source alone**, on
   three repositories, and one of them had just described the behaviour
   to another. The cause was not the code being unclear. `isBinary` had
   a unit test from phase 0 and the branch that uses it had none, so what
   the scan *did* with a binary was answerable only by reading — and a
   name invites agreement where an unexplained gap invites doubt. There
   is a test on the branch now, watched failing with it disabled, and a
   NUL byte in the first few kilobytes is the whole rule, so it needs no
   real executable and costs a second. Its absence is the entire reason
   the question went to three readers instead of to a test.
25. **A read's window is sized by the sheet's allocated width, not by
   where the data is.** The evals found it: asked for the last non-empty
   row of a 900-row sheet, a model took eleven `read_range` calls and
   then said "the footer even says it plainly: data ends at row 900". It
   does say that — once a read overshoots the data — and getting there
   cost eleven reads because the sheet is allocated 26 columns wide while
   its data occupies two. The cell budget is spent over the allocated
   rectangle, so 5 000 cells buys 192 rows of a 2-column block instead of
   2 500: thirteen times the cost, all of it empty.

   Nothing tells a caller the block is narrow. The card reports the grid
   size, which is the allocated one; the used range is only knowable by
   reading. The options are a used-range field on the card (a grid read
   `get_spreadsheet` deliberately does not do), clamping a whole-sheet
   read to the used width (the same problem one level down), or leaving
   it and teaching the tool description to narrow the range — which is
   what a person does and what the model did not think to do.

   **Still open**, and phase 3 is the wrong place to decide it: it
   touches the card, the budget and possibly a new capability, and
   `make bench` (§11) says the cost of a read is the decode, which this
   multiplies. Recorded with its measurement: four runs of that task took
   2, 4, 5 and 13 calls, so the variance is as much the finding as the
   worst case.
27. **A write into a pivot table's output is refused as "not empty".**
   The anchor is named — it carries the `pivotTable` field and the
   refusal says what a write there would cost — but the cells the pivot
   *draws* carry no such field: on the wire they are ordinary computed
   values, and the definition sits on the top-left cell alone. So a
   write to the middle of a pivot is refused correctly and unhelpfully,
   and a caller who passes `overwrite` stops the pivot drawing until
   they clear the cell again.

   Fixing it means reading up and left of every guarded write on the
   chance a pivot anchor is there — a wider rectangle on every write, to
   improve a message. The cheaper shape is to look only when the write
   is refused, which pays the read once per refusal rather than once per
   write, and it needs the refusal path to be able to fetch. **Still
   open**, found by reading the first live run of phase 4: the count was
   green and the refusal said "I3 is not empty" under a step whose whole
   subject was pivot tables.
29. **The fake ignores the field mask, so no test can catch a mask that
   asks for the wrong thing.** `sheetstest`'s `spreadsheets.get` refuses
   an *empty* `fields` — which is the rule it exists to hold — and then
   returns everything whatever the mask says. Phase 4's review pass
   found what that allows: `manage_data_source list` reported each
   source's kind, a test asserted "BigQuery", and the card's mask had
   never asked for the spec. Live it would have been empty on every
   call. The mask was widened and the test still does not prove it; the
   live driver does, by reading a card on every run.

   Honouring a mask properly means walking the response and pruning it,
   which is a small field-mask parser and a real piece of work. The
   cheaper half is a mask *shape* check — refuse a get whose mask names
   a field the response type has no JSON tag for — which would have
   caught nothing here, since `spec` exists and simply was not asked
   for. **Still open**, and recorded with what it would and would not
   have caught: a fake that answers more generously than the API is the
   shape three findings in this project have now taken.
30. Nothing further. The tool-version problem that was here — a
   distribution `golangci-lint` built with an older Go refusing this
   module, and a stale `go-licenses` failing on the standard library —
   was fixed rather than deferred: the Makefile fetches all three tools
   at the versions CI uses, through `go run <module>@<version>`. That it
   needed fixing a second and a third time, by different routes, is why
   `gates parity` now holds the claim rather than a comment doing it.

### 17c. Where the profile and the guards follow the sibling servers

**The destructive model is two layers, and three of the four servers
arrived at it independently.** A tool that removes something a person
cannot get back by reading is not registered at all without
`GSHEETS_ENABLE_DESTRUCTIVE`, and still refuses the call without
`confirm`. Here that is exactly three: `clear_values`, `delete_sheet` and
`delete_dimensions`. Two sibling servers gate two and five tools the same
way on the same criterion; the fourth gates none, which is how an outside
setup report noticed the family was inconsistent at all — from installing
three of them in one sitting, which is the only vantage point that shows
it.

Everything else that destroys is guarded per call rather than gated,
because it removes something the caller can read first and is named in
the refusal: a merge's discarded values, a cleared format, a replaced
note, a paste's landing rectangle, a table delete's conditional format
rules, the anchors on a deleted band. A named range and an anchor are
labels rather than data — the range each covers is on the card before it
goes — so removing one is a plain write. That is the line, and it is
written down here because it was drawn once and would otherwise be
redrawn differently by whoever adds the next tool.



The four Google MCP servers share a shape for their on-disk state, and it
is worth writing down because it was matched by comparing four config
files field by field rather than by reading a specification.

The OAuth client JSON lives at `~/.config/<server>/client_secret.json`,
which is this server's default for the `default` profile. A path given to
`login -secret` is recorded and wins, which is right for somebody keeping
it elsewhere — and it meant this profile pointed at a download directory
for a session, so `login` with no argument now lands on the convention.

The profile records `account_email`, and this one did not. `tokeninfo`
returns an address only for a token carrying an email scope; one sibling
asks for `openid` and `userinfo.email` and two do not, and adding a third
scope to record a string would be the wrong way to match a convention.
It comes from the Drive `about.get` this server already makes. Stored
whole and printed masked: §17.8 masks what is meant to be pasted — a log,
`doctor`, `status` — and not what is kept locally.

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
trackers named in §1. Checked 2026-09-05 and 2026-09-06; the rows added
while building phase 0 are dated in place.

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

Beyond those, §15's spikes are **unverified by design** until the phase
that depends on each one runs it. A, B, C, E, F, G, H, I, J, K, L, M, N
and P are done; D is not. Every row a spike produced is dated in place
below.

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
| Sheets' 429 is a shape this project would need a second account or a deliberate quota breach to see (spike E, and true for a year of phases) | **Observed 2026-09-07**, by running the live driver four times in a few minutes: `HTTP 429 RESOURCE_EXHAUSTED (RATE_LIMIT_EXCEEDED): Quota exceeded for quota metric 'Read requests' and limit 'Read requests per minute per user'`. It arrived on a `spreadsheets.get` in the middle of a run | The mapping built from Drive's vocabulary was right: `[rate_limited]`, with "this is Google's per-minute quota, which refills, so retry shortly". It refilled and the next run passed. Two of spike E's six shapes are still unobserved |
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
| An unquoted sheet name in a range is untidy but harmless (my assumption, and what §2 said in the first draft) | **Refuted 2026-09-06** by the concepts guide, and then **narrowed by measurement** — see the spike C rows below. The guide's rule is real but applies only where there is no `!` | `internal/a1` always quotes the title. The reason survived the correction; the scope of it did not |
| `values.get` accepts a named range as its `range` (assumed, and it is what every server in §1 implies) | **Confirmed by spike C** — see below. It does, in one spelling | No tool offers it, for the reason in the spike C rows |
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

**Added while building phase 0 (2026-09-06).**

| Convention | Verdict | Effect |
|---|---|---|
| A stdio smoke test can close stdin the instant the last message is written *and* read the replies | **Refuted first-hand 2026-09-06**, building this one: the go-sdk drops an in-flight reply when stdin reaches EOF, so the abrupt close that catches the -32004 exit bug also loses the answers the test wanted to assert. Holding stdin open until the replies arrive hides the exit bug instead — with nothing in flight the exit really is 0 | `scripts/gates smoke` is two runs of the binary. One holds stdin open and checks that stdout carried only JSON-RPC, that the tools are there and that an uncredentialed call answers `[auth]`; the other closes stdin immediately and checks only the exit code. Verified against a deliberately broken build, which exits 1 with "server is closing: EOF" |
| `shell: bash` belongs on the jobs that look shell-ish (what an earlier reading of §12 implied) | **Refined 2026-09-06, from a sibling session that checked GitHub's workflow-syntax reference after a drive project took half the finding.** The runner hands *every* `run` block to a shell, so it is not a property of shell-ish steps and not of matrix-ish jobs either: a job-level `defaults` fixes the jobs that exist and leaves the next one bare. `defaults.run` at workflow level covers the file; a job-level block overrides it; neither reaches inside a composite action. And explicit `bash` is not the implicit default — it runs `bash --noprofile --norc -eo pipefail`, so pinning it turns pipefail on everywhere | Every workflow here carries a workflow-level `defaults: run: shell: bash`, and `scripts/gates pins` requires one in every file. Its table test's load-bearing case is "job level only → false", which is the form that looks like the fix. **Inherited: re-checked by that session against the reference, not by this one** |
| The live driver creates a scratch spreadsheet, uses it and trashes it (§9.1 as written) | **Refuted while writing it 2026-09-06**: `spreadsheets` covers `spreadsheets.create`, and trashing a file is a Drive write, which `drive.readonly` cannot do. The scopes in §17.6 and the cleanup in §9.1 contradict each other | The driver still reads only a spreadsheet it created and filled itself, which is the part that matters. It attempts the trash, and when that fails it says so loudly and names the file to remove by hand. §17a.1 carries the decision to phase 1 |
| A closed error vocabulary can be asserted in both directions from the first phase | **Refined 2026-09-06**: three of the twelve — `blocked`, `conflict`, `unsupported` — belong to write and formatting code that phase 0 does not have, so "declared and never emitted" is a phase-ordering fact rather than a defect. A gate that failed on it would be wrong, and one that ignored the direction would be useless | `scripts/gates classes` carries those three by name with the phase that emits each, and fails if one of them *starts* being emitted without leaving the list. The emitted set is read out of the syntax tree — `service.Errorf` and `service.Error{Class:}`, plus everything `gapi.Class` can return — never grepped, since `fmt.Errorf` would otherwise fill the vocabulary with format strings |
| Action SHAs and tool versions can be re-verified while building this phase | **Not done, and recorded as such**: this session had no network for GitHub, so the SHAs and the `cosign-release`, `syft-version` and GoReleaser versions are taken from a shipping sibling's workflows on 2026-09-06 without independent confirmation | They are pinned in the shape the gate requires, and the gate proves the *shape* rather than the *values*. The first release run is the one to watch, which is also the standard's advice about a step gated on a CI-only credential |
| `golangci-lint` on the path is the version the config asks for | **Refuted 2026-09-06 on this machine**: a distribution package built with go1.26 refuses a module whose directive is `go 1.27.1`, and says so as "can't load config", which names the wrong thing | CI installs it with `install-mode: goinstall`, so it is built with the module's own toolchain. §17a.3 carries the local-path version of the same problem |
| The first sheet's name is worth asserting in the fixtures | **Confirmed by construction 2026-09-06**: no fixture sheet is called `Sheet1`, one title is not ASCII and one carries an apostrophe, and the fixture's named range deliberately shares the first sheet's title | The shadowing trap, the quoting trap and the non-English-name trap each have a test that fails if the behaviour regresses, rather than a comment saying they are handled |

**Found by the phase-0 review passes (2026-09-06), after the gates were
already green.**

| Convention | Verdict | Effect |
|---|---|---|
| A read's budget bounds the read (§4.6, and what the tests asserted) | **Refuted**: `max_chars` was a field of the *grid renderer's* options, so `format: json`, `csv` and `tsv` returned every row while taking `truncated`, `continue_from` and the footer from a rendering that had been cut. One result contradicted itself — 200 rows under a footer saying the read stopped at row 6 — and a caller believing either half was wrong about the other | One window decides everything: whichever budget bit first, the row the grid stopped at is the row every format stops at. Held by a test over all four formats, checked against the old behaviour |
| A registration gate may run before the checks that make a tool well formed | **Refuted**: `checkDryRun` ran *after* `allowed()`, so a destructive tool was never checked in the default configuration and no write tool was checked under `GSHEETS_READ_ONLY`. The rule held only because `--dump-schemas` forces the full surface and CI runs it — a structural invariant propped up by a flag in an unrelated function | The check is pure, so every `Def` is validated before the configuration is consulted. It is the difference between a rule and a coincidence |
| A fake and the code it stands in for may each own a copy of a small table | **Refuted, with the divergence already present**: `sheetstest` and `internal/grid` both mapped a Sheets error type to its display text, with different fallbacks. An error type outside the six the fake knew rendered `#ERROR!` formatted and `#SOMETHING_NEW` raw — and a test asserted each answer, so the suite agreed with both | `ErrorValue.Display()` lives in `internal/gsheets` beside the type, and a test reads the same cell formatted and raw and requires the two to agree |
| The error vocabulary is derived from the code (§6.5, and what the gate reports) | **Refuted for one call site**: `tools.fail` built `"[unavailable] " + err.Error()` by hand, which the AST scan cannot see, so nothing stopped the next one being `[timeout]` | `fail` goes through `service.Errorf` like everything else. The gate's reach is now the whole of the vocabulary rather than all of it but one |
| A five-second metadata cache saves the second call in a read (§11, in its own words) | **Refuted**: no tool calls the card twice in one invocation, so the cache only ever helps *across* tool calls — and the gap between two of those is model latency, not machine latency, so at five seconds it never fired. With one request in flight and sixty a minute, each miss is about a second of somebody's wall clock | Thirty seconds, which spans a turn, with the comment saying what it actually does. Nothing depends on it for correctness: a sheet renamed inside the window fails loudly rather than reading the wrong rectangle. Separately, `get_spreadsheet` on a title no longer re-fetches from Drive what the search that resolved it already returned |
| One "truncated" flag is enough to tell a caller what to do | **Refuted**: `find_in_spreadsheet` used one boolean for three endings — the cell budget ran out, a sheet was cut, `max_matches` was reached — and printed the same sentence for all of them. A search stopped by `max_matches` was told to raise `max_cells`, which would have changed nothing | `StoppedBy` distinguishes them and the rendering picks the sentence, so the advice names the dial that would help |

**Found by `/code-review high` on the same tree (2026-09-06). Two of
these would have failed CI's first run on a platform the matrix already
claimed to cover.**

| Convention | Verdict | Effect |
|---|---|---|
| A Windows build needs a `.exe`, so a gate looking for the binary should add one | **Refuted, and it would have broken the Windows leg on the first run**: `go build -o <name>` writes exactly that name on every platform, and both the Makefile and the workflow pass a name. Three gates take no argument in CI, so all three would have failed on `windows-latest` with "file does not exist" while passing everywhere else | The default is the plain name on every platform, with the `.exe` checked second for somebody who built without `-o`. Held by a test. This is precisely the class of bug §12 says a three-platform matrix is for, caught before the matrix ran rather than by it |
| Filling in a zero-valued option struct when its first field is unset fills in the rest | **Refuted**: `New` replaced the whole `RetryPolicy` only when `MaxAttempts <= 0`, so a caller who set attempts and nothing else kept `MaxDelay` at zero — and a zero delay reaches `rand.Int64N(0)`, which panics the process on the first retryable failure instead of backing off | Each field defaults on its own, with a table test over the partial policies. A half-specified configuration is a common thing to write and should never be a crash |
| A retry loop's early exits are the same as its ordinary one | **Refuted**: a context cancelled while waiting on the rate limiter, or during the backoff, returned a bare `context.Canceled` for a `values.append` that had already been sent once — losing the `ambiguous_outcome` that hard rule 8 exists to preserve | Every path out of the loop goes through one classifier. A read cancelled the same way is still not ambiguous, since nothing changed |
| Clamping a range to the sheet is always the right answer | **Refined**: right for a rectangle that overlaps the sheet, wrong for one that misses it entirely. `A50:B60` on a ten-row sheet came back holding row ten — a plausible answer to a question nobody asked | A rectangle starting past the last row or column is `[not_found]` naming the sheet's size; one that overlaps is still clamped |
| A footnote under a grid describes the grid | **Refuted**: notes and validation rules were listed for every row of the window, so a read cut at row 3 of 500 listed a note at A417 under a grid ending at row 3 — and the continuation listed it again | The annotations cover the rows drawn |
| Two limits stopping a search can be reported as whichever stopped it last | **Refuted**: `max_matches` overwrote a cell budget that had already bitten, so the caller was told to raise `max_matches` — which would never reach the sheets the budget excluded — and the "did not cover the whole spreadsheet" warning was dropped | The first limit to bite is the one reported. Both cases have a test |
| A count of what a call did can be taken from what it asked for | **Refuted**: `cells_read` added a sheet's whole window before any of it was examined, so a search stopping at the first match reported five thousand cells searched | Counted as they are looked at. The number is there to say what the answer cost |
| `strings.IndexByte` on a trimmed string indexes the untrimmed one | **Refuted**: `firstWord(" Quarterly Report")` returned `" Quarterl"`, so the "did you mean" search after a missing title looked for a truncated word. A title with surrounding whitespace also could not match itself, since only the id and URL paths trimmed | Both come from the same trimmed string, and `Resolve` trims once at the top |
| One `Read` returns the whole request body | **Refuted**: `io.Reader` promises no such thing. Nothing sends a body in this phase, but the fake records bodies for phase 1's `batchUpdate` assertions, where a short read would be a flaky test rather than a failing one | `io.ReadAll` under a limit |

**From a sibling session, checked here rather than adopted (2026-09-06).**

| Convention | Verdict | Effect |
|---|---|---|
| A shell-pin check can ask whether `run:` and `shell:` both appear inside the `defaults:` block | **Refuted, in a sibling's implementation of the rule it had just sent us**: `defaults.shell` is a *sibling* of `defaults.run`, not a child of it, and pins nothing — but a check that only looks for both words answers true for it, and true again for a `shell:` under any other key beside `run:`. Ours already answered false, because the case that notices leaving the `run:` block is tested before the case that reads a `shell:` key | The ordering of those two cases *is* the check, so it is now said so in the code and held by a table test: the sibling's two cases are in it, plus four-space and tab indentation so the depth rule cannot decay back into a two-space assumption. Verified by swapping the cases and watching the `defaults.shell` case go true |
| A stdio smoke test's second run can treat a timeout as an inconclusive pass | Already false here, and worth stating: `smokeAbruptClose` fails if the process has not exited within thirty seconds, so "stdin closing ended it" is asserted rather than assumed | The two runs exist for opposite reasons — one holds stdin open to read the replies, one closes it to catch the `-32004` exit — and neither can be inferred from the other. A sibling arrived at the same split from a failure; this one arrived at it from the mechanism, and both were verified against a deliberately broken build |
| GoReleaser v2.18.0 is the current release (inherited from a sibling's workflow on 2026-09-06) | **Superseded, and checked here against a primary source**: `proxy.golang.org` lists `v2.18.1`, tagged 2026-09-05T21:04:09Z at commit `9fa2a65`. Its contents — a `sec(deps)` bump of `golang.org/x/crypto` and `golang.org/x/mod`, Go 1.27.1, and a change to signing behaviour — are a sibling's report and are **not** verified here | Pinned to `v2.18.1`. The reason to take a newer version rather than inherit is that this repository has no release history to disturb, and the reason it was safe to take is that the version's existence is the part a release step cannot recover from getting wrong — and that part is now checked rather than believed. The action's own SHA is unchanged; the tool version is a separate input, which is the whole point of pinning both |

**Spike C, run live 2026-09-06 (`go run -tags=live ./scripts/spikes`).**
A scratch spreadsheet with a first sheet, a second sheet, a named range
deliberately carrying the *first sheet's* title but pointing at the
second, a second named range clashing with nothing, a table and a
protected range. Every row below is a transcript line, not a reading of
the guide.

| Question | What the API did | Effect |
|---|---|---|
| Does a named range titled `Sheet1` shadow the *sheet* `Sheet1` in `Sheet1!A1:B1`, as §2 claimed on the strength of the concepts guide? | **No, and this refutes the strong form of the claim the design was built on.** `Sheet1!A1:B1` returned `Sheet1!A1:B1` and the sheet's values, with the clashing named range present. So did `Sheet1!A2:B2`. A `!` makes the left side a sheet title unconditionally: `Solo!A1:B1`, where `Solo` is only a named range, is `400 Unable to parse range` | §2 and §6.2 corrected, and the code comments with them. The quoting rule is unchanged and still right, for the two reasons that survive: a title with a space or an apostrophe requires it, and the whole-sheet form needs it (next row). What changed is that the design can no longer justify it with a danger that is not there in the form this server sends |
| Then where is the shadowing real? | **In a bare reference with no `!`, which is exactly the whole-sheet form.** `Sheet1` returned `Sidecar!A1:B3` — the *named range's* rectangle, from the other sheet. `'Sheet1'` returned `Sheet1!A1:Z1000`, the sheet. A bare name is resolved as a named range first | `a1.Format` emits `'Title'` for a whole sheet, so this server is on the safe side of the one case where it matters. That is now the stated reason for the rule rather than a side effect of it |
| Does `values.get` accept a named range as its `range`? | **Yes**, as a bare name with no `!`: `Solo` returned the rectangle it names. Quoted, `'Solo'` is `400` — quoting forces the sheet reading, and there is no sheet called Solo | Still not offered. The one spelling that works is the same spelling a whole-sheet reference uses, so a caller who meant a sheet and typed a name that is also a named range gets the other rectangle silently. Too sharp an edge to hand a model; a named range's A1 range is on the card instead |
| Does `values.get` accept a table reference? | **No, in any form.** `SpikeTable` and `SpikeTable[FIRSTSHEET-A1]` are both `400 Unable to parse range` | The phase-2 table work addresses tables through `batchUpdate` and their A1 ranges, never through a `Table[Column]` reference |
| Is R1C1 really accepted? | **Yes**: `R1C1:R1C2` returned `Sheet1!A1:B1` | Confirms §6.2's premise for not offering it. Two spellings of one address is one more thing to get wrong |
| What does a range past the grid do? | **`400`, with the limits in the message**: "Range (Sheet1!A5000:B5001) exceeds grid limits. Max rows: 1000, max columns: 26" | Confirms the fix the code review forced independently: a rectangle that misses the sheet is refused rather than clamped to its last row. The API agrees, and says the size — so this server says it too, before sending |

**Spike E, run live 2026-09-06, partially.** Three of the six shapes need
something one account and one project cannot produce, and are recorded as
unobserved rather than guessed.

| Shape | What the API returned | Effect |
|---|---|---|
| A missing spreadsheet | `404` / `NOT_FOUND` / "Requested entity was not found." — **and on a later run of the identical request, `503` / `UNAVAILABLE` / "The service is currently unavailable."** | The mapping in `gapi.Unwrap` was right: 404 → `not_found`. The second observation is the more useful one: a definite answer is not reliably definite on the first attempt, and the probe saw it only because it is a raw client with no retry. The server's five-attempt backoff on 503 turns that into the 404 it should be — evidence *for* the retry policy rather than a hole in the mapping, and a reminder that a one-shot probe of an error surface can record a transport accident as a fact |
| A missing sheet | `400` / `INVALID_ARGUMENT` / "Unable to parse range: 'NoSuchSheetHere'!A1:B2" | Confirms why this server resolves the sheet from the card *before* calling and answers `[not_found]` with the titles that do exist. Google's own message names nothing that helps, and a caller who saw only it would not learn the sheet was the problem |
| A malformed range | `400` / `INVALID_ARGUMENT` / "Unable to parse range: !!!" | Same class, same uselessness; `internal/a1` refuses these before a request is built |
| A bad `batchUpdate` request kind | `400` / `INVALID_ARGUMENT` / "Invalid JSON payload received. Unknown name \"noSuchRequest\" at 'requests[0]': Cannot find field." with a `google.rpc.BadRequest` detail | Phase 1's union builders make this unreachable from a tool call; recorded because it is the shape a malformed generated request would take |
| A protected range refusing a write | **Not observed.** The owner is always an editor, so a write into this spreadsheet's protected range returned `200`. It needs a second account | The guard refuses a protected range before sending regardless, which is the behaviour that matters; the 403's shape stays unverified and the class mapping for it stays uninvented |
| A read-only scope attempting a write | **Not observed**: this token holds `spreadsheets`. It needs a second login under `GSHEETS_READ_ONLY` | Read-only mode does not register a write tool, so the path is unreachable rather than untested |
| A 429 | **Not observed**: it would need sustained hammering of a shared project quota | The mapping treats 429 as rate limiting and invents no 403 reasons, which is what §18 already said to do until this is seen |

**Found by reading the live driver's transcript, 2026-09-06.** The run
reported 19 steps and 0 failures three times. Every one of these was in
the output while it did so, which is the whole argument for the rule that
a phase is not closed until somebody reads it.

| Convention | Verdict | Effect |
|---|---|---|
| The live driver's transcript is safe to paste into a commit message (§9.1) | **Refuted by the first run**: it printed the account's full address and the scratch spreadsheet's Drive folder id — both on §9.1's never-list. The driver substituted the ids it had *created* and let everything Drive *returned* about them through, because substitution can only cover values known in advance | `internal/redact.Line` masks addresses and Google's opaque ids in any text somebody else assembled, and the driver's print helper runs it after the substitutions. The transcript is now what §9.1 always claimed it was |
| `search_spreadsheets` hits are safe to render in full | Same defect, one level down: `render.Hits` prints the owner and the folder id, and it is the *server's* output, not the driver's. A person pasting a tool result into an issue pastes both | The masking is in the driver's print path rather than the renderer, because a model asking who owns a spreadsheet should get the answer. §17a carries the question of whether the tool surface should mask it too — it is a real tension between a useful answer and a pasteable one, and it wants deciding rather than assuming |
| `doctor` and `status` are safe to paste, which is what the issue form tells people | **Refuted before the driver even ran**: `doctor` printed the OAuth client id and the account address, and `status` printed both as well. The client id arrives by an unlucky route — the Cloud console names the file it gives you *after the client id*, so printing the path to the client secret prints the id | Both masked through `internal/redact`, with the directory kept because "it looked in the wrong place" is most of what a first-run report is about. The issue form's promise is now true rather than aspirational |
| A cell budget spent in sheet order searches the spreadsheet | **Refuted by a match that was missing**: the first sheet's allocated grid is 26 000 cells and the budget is 5 000, so the first sheet swallowed all of it and the second was searched 18 cells deep. A term present on both sheets returned one hit, under a note saying only that the search was "truncated" | The budget is divided max-min fair across the sheets: an equal share each, with a sheet that needs less releasing the surplus. The same search now returns both hits. A table test covers the division and a fixture test covers the behaviour |
| "The cell budget was reached" means there is more to find | **Refined**: a new sheet is allocated 1000 rows long before it holds any, so a search of a spreadsheet with three populated rows reaches the budget having seen every value in it. The note sent a model back for a second look that could only find nothing | When every sheet ran out of data before its window ran out of room, the note says so and says the certainty is available by raising `max_cells`. It does not claim the rows below are empty, because nothing short of reading them would show that |
| A leak gate that catches file ids catches Drive ids | **Refuted by the folder id above**: a file id starts with `1` and a folder or shared-drive id with `0A`, and only the first shape was in the pattern. The transcript carried one for three runs before anyone looked — and the widened pattern then caught this very row quoting the id verbatim, which is the shortest gap between writing a rule and having it hold that this project has managed | The pattern covers both, with commit SHAs excluded by their own rule rather than by hoping they do not start with a matching digit — two of the planted cases in the gate's table now start with `0` and `1` |

**Masking a tool result: researched and decided 2026-09-06.** The live
run asked the question and §17.8 answers it. Recorded because the
published guidance points the other way and the reason it does not apply
here is the whole of the argument.

| Convention | Verdict | Effect |
|---|---|---|
| An MCP server should redact PII from its tool results, as the published guidance says | **Refined, and the guidance turns out to address a different architecture.** The MCP specification's own security document says nothing about PII in tool results — it is OAuth, SSRF, token passthrough and *scope* minimisation. OWASP's MCP cheat sheet redacts PII from **logs** only, and its tool-output rule is about sanitising for injection; it explicitly declines to say whether servers should mask responses or where such filtering belongs. The vendor material that does say "mask tool responses" is written for multi-tenant SaaS servers handing another party's PII to a hosted LLM, and two of those sources put the control at a **gateway or control plane** "rather than inside individual servers where coverage will be uneven" | This server is stdio, single-tenant and local, on the caller's own OAuth token; the data subject is usually the caller, and there is no gateway for the control to live in. So the result is not masked. Protecto's caveat is the line the decision rests on: "a 'block all PII' approach can result in useless systems" |
| The folder on a search hit is worth returning | **Refuted on the guidance's own terms.** It is a raw Drive parent id: no tool here takes a folder, and resolving one to a name needs a Drive call this server deliberately does not make. It was fetched, returned and printed because it was in the response — which is exactly the "return the entire upstream payload" habit the same sources name | `parents` is out of both Drive field masks, out of the wire type, and out of every rendering. Minimisation applied where it is cheap and costs nothing, rather than masking applied where it would cost an answer |
| The risk is that the model sees the data | **Refuted by where the leak actually was.** The model seeing a spreadsheet's owner is not a disclosure — the caller can see it in Drive. The path that publishes is a *transcript or a diagnostic pasted into an issue*, which is why all three fixes landed there: the debug log, `doctor`/`status`, and the live driver's transcript | The issue form now says to describe a tool result rather than paste it, and says which three artefacts are safe and why. `docs/security.md` carries the same split |

| A transcript is safe to paste because every line goes through the redactor | **Refuted structurally, though not yet in fact**: nothing made it true. The live driver happened to print only through one helper, and the spike probes did not — their first two runs were redacted by hand with `sed` afterwards, and one of them carried the scratch spreadsheet id inside a `values.update` response | `scripts/gates transcript` reads the syntax tree of both drivers and fails on a `fmt.Print*` outside the redacting helper. It found the spikes on its first run. The table test's load-bearing case is a print inside an ordinary step function — what somebody adds while debugging and leaves behind, invisible in review because it looks like the ones that are allowed |

| A promise scoped to logs is a promise about the product | **Refuted twice on the same evening, here and in a sibling, and it is the sharper form of the `doctor` finding.** `docs/security.md` guarantees that titles, ranges and cell values never reach a *log*, and a test holds it. `doctor` is a different surface, and the issue form points people at it: given `-spreadsheet` it printed `'<sheet title>'!A1`, and a tab called "Q3 pipeline — <customer>" names a customer. The log guarantee was narrowly true and entirely beside the point | `doctor` reports a count and a fixed string; the read's result is discarded rather than sanitised, because a sheet title has no shape a redactor can catch and removing the value path leaves nothing for a later edit to reintroduce. The lesson generalises past this fix: a guarantee has a surface, and naming the surface is part of making the guarantee |

**A second live-driver reading, 2026-09-06, after the coverage gate was
built.** The first reading found what the driver printed. This one found
what it never called.

| Convention | Verdict | Effect |
|---|---|---|
| The live driver covers every tool and every op (§13, since the first draft) | **Refuted by counting**: it exercised **14 of 28** tool options. Every tool was called, which is what made the claim look true — `search_spreadsheets` was driven on `name` and none of its other five criteria, and `read_range` on six of twelve options. This is the failure §13 itself describes, where a sibling's guarantee had quietly stopped covering eight newer tools | `scripts/gates live-cover` compares the options the driver's steps send against the schema the binary publishes, and fails on a gap. Static, because CI has no credentials and the point is to fail when somebody adds an option, not when somebody next runs the driver. 26 of 28 now, with the two exceptions carrying reasons |
| A step reporting "I could not determine this" is the honest outcome when the world will not settle | **Refined, and the refinement matters**: it is honest only when the check itself is sound. The content-search step asked for `Quorbin-01` and reported "cannot tell indexing lag from a broken search" for two runs. It was neither: Drive tokenises, so a hyphenated compound can never match, and the step was asserting an end state the world does not permit. Undetermined looked like patience | The step polls, and when it fails it runs the same query without expecting the new file: matching something else means the query works and the index is behind, matching nothing anywhere means the query is suspect. It now says which it saw. An undetermined step is also counted separately from a failure — reporting it as a pass hides a broken search, reporting it as a failure makes the driver red for something the server did not do |
| Drive's `fullText contains` searches a spreadsheet's contents | **Confirmed, with a limit worth knowing**: `Plimth`, a column heading in a cell and in no title, matched six spreadsheets, so cell values are indexed. But `Quorbin-01` matched none while `Quorbin` matched six — Drive tokenises, and a hyphenated compound is not a term. Multi-word phrases are fine: `Umberly merged` matched. Content indexing also lags well behind the title index: a title was findable on the first attempt, the contents not within thirty seconds | `search_spreadsheets`' description says the parameter reaches cell values, tokenises, and lags, and points at `find_in_spreadsheet` for a spreadsheet already in hand. A model that searched for a product code with a hyphen would otherwise conclude the spreadsheet does not exist |
| Both search limits biting is an edge case | **Refuted by a step that failed for the right reason**: a fresh sheet is allocated 1000 by 26, so a `max_matches` test on any live sheet has the *cell* budget bite first, and the server correctly reported that. The step had to raise `max_cells` past the allocated grid to test the limit it names | Recorded because the step looked correct and tested the wrong thing. The server's behaviour was right throughout; the check was the defect, which is the pattern a sibling hit the same evening when a checker's pass condition was one field of the thing it was checking |

**The configuration surface, run live 2026-09-06 — the part the tool
coverage gate cannot see.** `live-cover` compares the driver against the
*tool* schema; profiles, read-only scopes and `logout` are cmd-level and
had never touched the network at all.

| Convention | Verdict | Effect |
|---|---|---|
| Profiles keep separate tokens, so signing out of one leaves the others alone (§10, as written) | **Refuted, live and destructively.** A second profile was logged in with read-only scopes, used, and logged out; the *default* profile's token stopped working at the same moment. Google revokes the **grant**, and both profiles shared one OAuth client and one account. `logout` reported "Signed out of profile \"readonly\"" while having signed the account out of everything | §10 corrected: separate storage, one grant. `logout` now says revocation is account-wide for that client, names the profiles it will take with it, and repeats it afterwards. `logout -local` deletes this profile's copy without revoking, which is what somebody with a work and a personal profile actually wants. `userconfig.SharingClient` finds them by comparing the stored client-secret path, with a test |
| Read-only mode is exercised by registering fewer tools | **Refuted for this phase**: every phase-0 tool is a read, so `tools/list` is identical in both modes and the registration half is untestable here. The half that *is* testable is the scope half, and it needed a second login | A profile logged in with `spreadsheets.readonly` plus `drive.readonly` served all four tools against a real spreadsheet: card, an addressed grid with `show=both` and merges, a search across sheets, and a Drive search. So the narrower scopes are sufficient for everything phase 0 offers, which is the claim §10 makes and could not previously support |
| A live run's cleanup is a detail | **Refined**: `logout` was the only destructive command available to test, and testing it cost the working login. That is worth doing once and worth knowing before doing it — the recovery is a browser round trip, and on a machine without an interactive browser it would not be a recovery at all | Recorded so the next person testing `logout` does it on a profile they can afford to lose *and* expects to re-authorise every other profile afterwards |

| A static check of the driver's coverage is enough | **Refined, and the gap shown rather than argued**: reading the source counts a step that exists, not one that runs. Removing the single `d.run(d.searchSteps()...)` line left the static gate reporting 26 of 28 and passing, while four options were no longer being sent by anything | The driver records what it actually put on the wire and checks it against the tool list the server publishes, at the end of every run. The static half stays, because it is the one that runs in CI: it fails when an option is added, which is weeks before anybody next has credentials in their hand. One exemption list in `internal/livecover` feeds both, so they cannot drift apart |

| A live driver's own setup does not need the resilience it is testing | **Refuted by an aborted run**: creating the scratch spreadsheet got a transient `503 UNAVAILABLE` and the run ended before its first step. The driver's setup uses a raw client on purpose — the server's retry loop is what is under test, not the driver's plumbing — but that left the whole run hostage to a transport hiccup on a project that demonstrably produces them (the same `503` appeared in a spike E probe of a *definite* 404) | The setup calls retry on 429 and 5xx, four attempts with a widening wait, and say when they did — a run that needed three attempts must not read like one that needed none. The steps themselves are untouched: what they measure is the server's behaviour, and retrying *that* would be measuring the retry |

| An allowlist of functions permitted to print is the same rule as "everything printed is redacted" | **Refuted, in this repository's own gate, and the argument came from a sibling that hit the shape one level down.** Three functions were allowed to print and only one of them redacted: `sec` wrote the section header directly, which was safe because the titles are literals. That is the wrong kind of safe — the list had quietly become "functions allowed to reach the terminal", so a later `sec(someSheetTitle)` would have been blessed by name and printed raw | `sec` goes through `line`, so exactly one function prints and the allowlist is two entries, the second being a build-tag stub with nothing to redact. The sibling's version of this was an allowlist of safe *expressions*, which they deleted for the same reason after it grew to fifteen entries — two of which read a value off an API response that happens to be harmless today. A list that blesses a name once stops asking about the value |

**Spikes A, B and F, run live 2026-09-06 at the start of phase 1.** Each
answers a question §15 says the reference does not, and each decides code
written immediately after it. The probe is `scripts/spikes`, and the
scratch spreadsheet it reads is one it created and filled itself.

| Convention | Verdict | Effect |
|---|---|---|
| **Spike A.** `USER_ENTERED` rewrites the *value*, and a coercion report that names the value has said everything | **Refined, and the refinement is half the report**: it rewrites the number format too. `$100.15` stores the number `100.15` **and** a `CURRENCY` format, so the cell still displays `$100.15`; `2026-09-05` stores `46270` with a `DATE(yyyy-mm-dd)` format and displays unchanged; `1-2` stores `46024` with `DATE(m-d)`. A diff over stored values alone reports "your text became a number" for all three and cannot say that two of them look identical on screen while the third now reads as a date | The coercion diff compares the value **and** says when Sheets attached a format, so `$100.15 → 100.15 (currency)` reads as what happened rather than as data loss. The recorded table is the fake's replay table: the fake never parses input, it looks the answer up |
| **Spike A, the rest of the matrix** | **Confirmed as designed**: `007` → `7`, `TRUE` → boolean, `=1+2` → a formula whose effective value is `3`, `'0123` → the string `0123` with the apostrophe consumed. `RAW` stores every one of them as the literal string, apostrophe included | `input: typed \| literal` is worth the parameter: the two differ on every input tested, and for a product code or a leading-zero identifier `literal` is the only correct answer. Neither is defaulted silently over the other (§17.3) |
| A cell's character limit is a phase-2 question (§15.G) | **Answered early and for free**: a 60 004-character string is refused with `400 INVALID_ARGUMENT`, "Your input contains more than the maximum of 50000 characters in a single cell", under both input options | The limit is checked before the request is built, so a caller is told which cell is too long rather than having a whole batch refused. §15.G keeps the other half — column ZZZ and the 10-million-cell ceiling |
| **Spike A, the shape half.** `range` and `values` must agree, and a caller has to spell the rectangle out | **Refined by probe**: a single anchor cell is enough — `A1` with a three-by-two array wrote `A1:B3` — and an *unbounded* range works the same way, `A:A` with two rows writing `A1:A2`. A range **larger** than the array writes only the array's rectangle and leaves the rest alone, confirming "values with no data are skipped" from the other direction. A range **smaller** than the array is a `400`, and Google's own message for it names a row number: "Requested writing within range [...], but tried writing to row [3]" | `range` names where the write starts and how far it may reach; `values` decides the rectangle. Too small is refused here, before the request, with the array's shape and the range's — the API's message says neither. The result names the region covered and says when the named range was larger, since the tail is still whatever it was |
| A ragged `values` array is a caller's business | **Refuted by what it does**: rows of length 3, 1 and 3 were accepted, and the short row left the two cells beyond it holding their previous values — "skipped", per row, not per request. A caller who believed they had replaced a rectangle would have replaced most of one | `write_values` refuses a ragged array with the two row lengths, and says the fix: pad with empty strings to clear those cells, or write a narrower range to leave them alone. Neither is guessable from a result, which is why it is not guessed on the caller's behalf. It also keeps the guard's target a rectangle, so what is checked and what is written are the same cells |
| **Spike B.** `values.append` finds the data on the sheet and writes after it | **Refuted, in the direction that matters**: it finds the contiguous block the **given range** falls in and writes after *that*. On a sheet holding rows 1-3, a gap, and rows 6-7, an append at `A1` reported `tableRange` `A1:B3` and landed in rows 4-5 — between the two blocks. An append at `A6` reported `A6:B7` and landed in 8-9 | `append_rows` reports `tableRange` and the range Google chose, always, because the caller cannot predict either. The description says the range picks the table rather than the destination |
| A whole-sheet append and an append at `A1` are the same call | **Refuted**: with no range, `tableRange` came back as `A6:B7` — the **last** block, not the first — and the rows landed at 8-9, where the same sheet given `A1` put them at 4-5. Two calls a model would read as synonyms write six rows apart | The range is required rather than defaulted, and named in the result. A default here would be a coin flip whose outcome is invisible until somebody reads the sheet |
| `INSERT_ROWS` and `OVERWRITE` differ only in tidiness | **Refuted, and both need saying**: `INSERT_ROWS` inserted two rows at 4-5 and pushed the second block from 6-7 down to 8-9, so every address below the insert point moved. `OVERWRITE` consumed the two empty gap rows and left the second block where it was | `INSERT_ROWS` stays the default — it destroys nothing — and the result says the rows below moved, because a checkpoint or an address the caller is holding is now wrong. `OVERWRITE` runs the write guard over the rows it would land on, which is the one case where an append can destroy data |
| **Spike F.** Drive's `files.get` `version` is a cheaper spreadsheet-wide staleness check than re-reading the range | **Rejected**: `version` did not move after a value change, after re-writing the same value, or after a structural `batchUpdate` — three edits, fourteen seconds of polling each, still `"3"`. `modifiedTime` was read in the same poll and did not move either | Checkpoints stay as designed (§4.7): a hash over the values, re-read at write time. A field that reports "unchanged" during exactly the window a checkpoint exists to cover would be worse than the re-read it replaces, not cheaper. §17a's staleness entry is closed |
| Drive's lag is a search problem | **Widened by the same run**: `modifiedTime` had not moved fourteen seconds after an edit, which is the field `search_spreadsheets` sorts and filters on. Phase 0 found the *content* index lagging; this is the metadata | `search_spreadsheets`' `modified_after` description says the timestamp is Drive's and lags an edit, so a spreadsheet changed moments ago is not found by it. The same fact, met at a second surface |

**Phase 1's live runs, 2026-09-06.** Five of them, and the count is the
finding: every run was green or nearly so, and every transcript held
something the count did not. Four of the six rows below are defects the
gates could not have caught, because the fake agreed with the code.

| Convention | Verdict | Effect |
|---|---|---|
| A request that creates a sheet may reuse the type a response returns | **Refuted twice in one run.** `SheetProperties` carries `sheetId`, the zero value serialises as `"sheetId": 0`, and Google reads that as a request for id 0 — the id the first sheet always has. `manage_sheet add` came back "Sheet with id 0 already exists", and `create_spreadsheet` came back with one sheet where two were asked for | `NewSheetProperties`, `NewSheet` and `NewSpreadsheet` have no id field at all, so the mistake is not available. The same shape as `Index` becoming a pointer earlier the same day, and for the same reason: a zero that means something is not a zero value |
| **Spike H.** `spreadsheets.create`'s `sheets` are added beside the one Google always makes | **Refuted**: a create naming two sheets came back with exactly those two, at indices 0 and 1, and no third. With no list, Google makes one and names it in the account's language — observed in the same transcript, which creates its scratch spreadsheet that way | `create_spreadsheet`'s description says giving `sheets` replaces the default rather than adding to it, and that seed values go to the first sheet. A description promising a tab that is not there would send a model looking for it |
| `updateSheetProperties`' `index` is where the sheet ends up | **Refuted by a step written to fail if it was a no-op.** A sheet at index 0 asked for index 3 in a four-sheet spreadsheet landed at 2: the API removes the sheet and then inserts it, reading the index against the order *before* the move. It is the convention `moveDimension` documents for rows and columns, undocumented here | `plan.ReorderSheet` takes the sheet's current index and converts, so the caller's `index` means where it ends up. The fake reads the index the same way Google does, or the unit tests would have agreed with the bug. Found only because the previous version of the step moved a sheet to where it already was and passed |
| A cell holding a formula has `Kind == formula` | **Refuted, and it was a hole in the guard rather than in a count.** A formula that evaluated to an error is `KindError`: `userEnteredValue.formulaValue` is set and `effectiveValue.errorValue` overwrites the kind. So `=IMPORTRANGE(...)` showing `#REF!` was treated as an ordinary value, and `overwrite` alone would have replaced it — losing exactly what `overwrite_formulas` exists to protect | `grid.Cell.HasFormula()` tests the formula, not the kind, and the guard and the counts both use it. Found by reading a delete's count: a sheet this run had filled with formulas reported "0 formula(s)", which was the visible end of an invisible defect |
| A step that passes has tested something | **Refuted three times in one transcript.** `clear_values` reported "0 non-empty cells" three times over a rectangle that had been emptied by an earlier step; `delete_sheet` counted zero on a sheet duplicated before anything was written to it; the reorder step moved a sheet to where it already was. All three passed, and none exercised the thing it named | Each now writes what it will then count, and asserts the number. The count is the whole substance of a clear's or a deletion's result, so a count that is always zero is a check that is always true — the same failure as phase 0's undetermined search step, one level down |
| A coercion report that names the values has said enough | **Refuted by reading one**: `B2  "12" -> "12"` is a line that reports a change and shows none. What changed was the kind — the caller sent text and Google stored a number — and that is the change a product code cares about most | Both kinds are named and only text is quoted: `B2  text "12" -> number 12`. The date keeps its pair, `text "2026-09-05" -> number 46270, displayed "2026-09-05"`, which is what stops a serial reading as data loss |
| A link is a link | **Refined**: Google's `spreadsheetUrl` carries `?ouid=`, the signed-in account's obfuscated id, in every card. The driver's redactor had been quietly catching it since phase 0, so it never reached a transcript, and it was still in every tool result | `spreadsheetUrl` is out of the field mask and the link is built from the id. It opens the same spreadsheet and identifies nobody, which is §9's "do not fetch what nothing needs" applied to a field that arrived free |

**What phase 1's review passes found, 2026-09-06.** `/simplify` and
`/code-review high` ran over the whole phase after the live driver was
green. Between them they found four defects the driver had not, and two
of those turned on API behaviour the code had assumed rather than
checked — which is spike I, run to settle them.

| Convention | Verdict | Effect |
|---|---|---|
| **Spike I.** `values.update` destroys the note and the validation rule on the cells it writes | **Refuted**: both survive. A cell carrying a note, written over, came back with the note and the rule intact — the same guarantee `values.clear` documents, which the fake already modelled for clears and not for writes. So every write result said "The write removed notes on A1", which was false, and a cell holding *only* a note counted as occupied and refused a write for a loss that never happened | `Cell.Empty()` is about the value alone. `Report.Loses()` became `Keeps()`, and a surviving validation rule is reported as surviving — it still applies to the value that just replaced the old one, which is worth knowing and is the opposite of what the result used to say |
| **Spike I, the second half.** `moveDimension`'s `destinationIndex` says where the band ends up | **Refuted**: rows 1-2 of four, sent with `destinationIndex: 3`, came back starting at row 2. The API removes the band and then inserts it, reading the index against the order *before* the move — the same convention a sheet's index follows, found the same way two hours earlier | `plan.MoveDimension` converts, so `to` means the row the band starts at afterwards. A driver step marks the rows before moving them and reads the destination back: the version before it moved rows that earlier inserts had left empty, so it landed anywhere and looked right |
| `dry_run` is available whenever a write is | **Refuted by reading the code path**: the guard ran first, so a write the guard refused could not be previewed — and the refusal's own last sentence is "dry_run shows what would change without sending anything". A caller following that advice got the identical refusal. Every test and every live step passed `dry_run` together with the acknowledgements, so the path had never run | The preview comes before the refusal and lists what would stop the write. A dry run sends nothing, so there is nothing to guard, and the one call that exists to explain a refusal must not be the one call the refusal blocks |
| A checkpoint is a checkpoint | **Refuted**: a read hashes what it renders, and `formatted: true` renders `£1,234.50` where the cell stores `1234.5`. A write reads raw and hashes raw, so a checkpoint from a formatted read could never match — every date or currency in the range made `expect_checkpoint` report a conflict that had not happened. The live driver reads unformatted, so no transcript would ever have shown it | `Cell.Raw` carries the unformatted value whatever `Display` holds, and the checkpoint hashes that. A checkpoint is over what the cells store, not over what they show |
| An action can be gated inside a tool | **Refuted in the schema this repository publishes.** `edit_dimensions delete` was hidden by leaving it out of the description, while `confirm`'s description still said "required by delete" and `action` carried no enum for anything to be absent from. The tool was registered as a plain write, so it advertised `destructiveHint: false` while offering an irreversible action, and got no `requiresUserInteraction` mark | `delete_dimensions` is its own tool with `Kind: Destructive`, and inherits the registration gate, the annotation and the mark from the one place that decides them. §8's rule was already the right one — "Kind is an enum over which world a tool touches" — and a tool whose destructiveness depends on an argument is the matrix that rule exists to prevent |
| A guard that reads part of a range guards the range | **Refuted**: `clear_values` bounded its read by the cell budget and then sent the clear for the whole range, so a protected block in the part it never read was not named, and the count it reported was a floor it did not say was one | A clear larger than one read is refused with the size, the way an oversized write is. The alternative — clearing only what was read — would answer a different question from the one asked |

**An audit of the gates themselves, 2026-09-06.** Prompted by a sibling
server hitting two release failures no rehearsal could catch, and by the
question it raised here: what else does `make check` claim that it does
not do. Four answers, none of them found by running anything.

| Convention | Verdict | Effect |
|---|---|---|
| `make check` is what CI runs, as its first line says | **Refuted three times over.** The Makefile ran `go vet -tags=live` and CI did not, so nothing in CI compiled the live driver or the spikes — `go list ./scripts/livesheet` returns one stub file. CI scanned for secrets with gitleaks and the Makefile had no such target. And `go mod tidy` was in neither, running only inside a release, in the one form that writes | `gates parity` compares the Makefile's `check:` line against `ci.yml` and fails when either side has something the other lacks, with the gate list read from the dispatcher so a new gate is covered from its first commit. It found itself missing from both lists on its first run. §17a's earlier note that this claim had already been broken once, by tool versions, is the third time it has needed fixing — hence a gate rather than a fourth correction |
| A build tag hides code from the compiler, not from CI | **Refuted**: the two things that validate the write path against a real account, the live driver and the spikes, were compiled by nothing CI ran. The breakage would surface at the phase-closing step, which is the worst moment and the one where a maintainer has least appetite for a compile error | `go vet -tags=live ./...` runs in CI, and the parity gate holds it there |
| The coverage floor covers what ships | **Refuted**: `-coverpkg=./internal/...`, and the gate derived its package list the same way, so `cmd/` was outside the profile entirely. 571 lines of `login`, `logout`, `status` and `doctor` had no tests, and nothing could report that they had none — the gate's own comment promises a package is under the floor from its first commit, and that promise only ever covered one of the two trees that ship | Both trees are listed, `cmd/` carries a floor of its own at 40% against 48% reached, and the gate has a test asserting a package from each tree is under a floor. The lower number is a floor rather than an exemption: what remains uncovered opens a browser, holds a token or serves stdio |
| A secret scanner is worth having before it has ever fired | **Confirmed the hard way**: the first local `make secrets` run failed on a test fixture written twenty minutes earlier — a realistic-shaped OAuth client id, put in a test whose whole point is that such ids get masked. The repository already had one blessed fixture allowed by value in `.gitleaks.toml`, and the fix was to use it rather than to add a second exception | The test uses the canonical fixture. Worth recording because the finding was in new code, in a test about redaction, written by someone who had just read the rule — which is the argument for the scanner rather than against the author |

**Phase 2's conventions, 2026-09-06.** Every row here was decided from
the discovery document or from the code path, and **none of it has been
run against a real account** — this machine has no credentials, and §16
says which two commands close that. The last row is the one that most
wants a live answer, and the driver is written to give it.

| Convention | Verdict | Effect |
|---|---|---|
| A cell format can be written as a struct, with the mask beside it | **Refuted by the API's own rule.** A field named in an `updateSheetProperties` or `repeatCell` mask and absent from the body is set back to its default — which is how bold is turned off and a background cleared. A struct carrying `bold: false` says nothing about whether the caller asked for it, and a mask written by hand beside it is a second place to be wrong | `plan.Patch` is built by setters, each of which records its own mask entry. The caller's three-way choice (on, off, leave alone) lives in the tool's input as a `*bool`, and "leave alone" is expressed by never calling the setter, so the field cannot reach the mask |
| A number format can be given as a pattern and its type inferred | **Rejected.** The API's `NumberFormat` needs a type as well as a pattern, and a pattern alone would have to be classified — `yyyy` is a date and `#,##0` is a number. A wrong guess formats somebody's column as the wrong kind of thing and looks like it worked | `number_format` names the type and optionally the pattern after a colon: `currency`, `date:yyyy-mm-dd`. A bare pattern is refused with the list of types |
| A merge is a formatting operation | **Refuted.** Sheets keeps the top-left value of every merged block and discards the rest, and nothing in the response says so. Which cell survives depends on the merge type: by rows it is the leftmost of each row, by columns the top of each column | `plan.CheckMerge` names the cells that would go and the merge is refused without `overwrite`. It is the one op in `format_cells` that loses data the way a value write does, and the reason that tool reads its target at all |
| Every formatting call should read its target first | **Rejected as written.** Three ops can destroy something — a merge, `clear_format`, a note that replaces one — and the rest cannot. Reading for all of them would double the cost of "make this bold", which is the commonest formatting call there is | The protections and merges come from the card, which the range resolution has already fetched; the cells are read only when one of the three is asked for. An ordinary formatting call costs one request, and a test asserts the count rather than the intention |
| Clearing a format takes the cell's formatting | **Refined**: it takes what the cell was *given*, not what applies to it. A cell showing only the sheet's defaults has nothing to lose, and a guard built on `effectiveFormat` would fire on every range in every spreadsheet — which is a guard nobody reads | The read asks for both formats. `userEnteredFormat` decides the blocks `read_formatting` reports and what a clear would remove; `effectiveFormat` is what a caller sees. `grid.Cell` carries the pair for the same reason |
| An attached object is named by an id | **Rejected.** A protected range, a banding and a table all have ids, and a caller would have to fetch one before deleting anything. A1 is this server's contract everywhere else (§17.1) | `manage_range` names an existing object by the range it covers, matched exactly; a range matching several is `[ambiguous]` with the list rather than a guess. The exception is a conditional format rule, which the API identifies by position — so those take an `index`, and `read_formatting` reports it beside each rule |
| A protected range refuses every write into it | **Refined**: it must not refuse the request that lifts it. The destination check runs for every kind `manage_range` handles except `protected_range` itself | Anything else is a trap rather than a guard, and a spreadsheet with a protection nobody can remove through this server |
| `includeGridData: false` is enough to keep a `spreadsheets.get` off the grid | **Refuted, and it was a live bug in this phase's own code.** The parameter is documented as ignored when a field mask is set, so a mask naming `data(...)` is a grid read whatever the option says. `manage_range`'s rule count was reading conditional format rules with the formatting mask and no range, which is the entered and effective format of every cell of every sheet — the whole-spreadsheet read §4.6 forbids, walking straight past the guard in `GetSpreadsheet`, which can only see the option. The fake hid it: it returns grid data only when the option is set, so no test could tell the two apart | A second guard in `GetSpreadsheet`, on the mask rather than the option: a mask containing `data(` with no range is refused before anything is sent. `gapi.RuleFields` names the rules and the sheet ids and nothing else. Found by a review pass reading the reference, not by anything running |
| `sortRange`'s `dimensionIndex` is an offset into the sorted range | **Refuted, live.** It is the sheet's own column. `B31:C34` was seeded with B counting up and C counting down, and sorted by column C ascending; the read-back afterwards is B = 3, 3, 2, 1, which is C in order. A reading relative to the range would have sorted by B and left the rows where they were. This row was a belief with a test waiting for it until 2026-09-06, and is written down that way because it was the plan's own claim that the reference did not settle | `plan.ParseSort` converts a column letter to the sheet's zero-based index, and the service refuses a key outside the range with the column named |

**Spike G, run live 2026-09-06 (`go run -tags=live ./scripts/spikes
-only G`).** The platform's own edges, and what this server says before
reaching them. The first run of this probe answered none of these
properly and looked as though it had, which is the argument for reading a
transcript rather than counting it: it printed the length of a JSON body
as though it were the length of a cell, and it asked for one column past
ZZZ on a 1 000-row sheet, where 18.3 million cells trips a different
ceiling first. Both are fixed and both are why the rows below say what
they say.

| Question | What the API did | Effect |
|---|---|---|
| Where exactly is the 50 000-character cell limit? | **Inclusive.** 50 000 characters is accepted and reads back as 50 000 characters stored; 50 001 is `400 INVALID_ARGUMENT`, "Your input contains more than the maximum of 50000 characters in a single cell" | `plan.MaxCellChars` is 50 000 and the check is `>`, so exactly 50 000 goes through. Confirmed rather than assumed, and the round trip is read rather than inferred from a successful write |
| What does a column past ZZZ do? | **Two different things, and the difference matters.** `AAAA1` is `400 Unable to parse range` — four letters is not a column at all. `ZZZ1` on a 26-column sheet is `400 Range (…!ZZZ1) exceeds grid limits. Max rows: 1000, max columns: 26`: a real column name, past the end of *that sheet* | `a1.ParseColumn` refuses more than three letters, which is the first case, and the service refuses past the sheet's extent with the sheet's own size, which is the second. Neither refusal needs a round trip |
| Is the 10-million-cell ceiling per sheet or per spreadsheet? | **Per spreadsheet**, in Google's own word: "This action would increase the number of cells in the **workbook** above the limit of 10000000 cells." Both 10 000 001 × 1 and 1 000 000 × 100 are refused | `manage_sheet resize` checks the row and column ceilings and forwards Google's message for the cell one, which is the only check that would need summing every sheet. Recorded rather than built: the message names the limit and the action, which is enough to act on |
| Is the column ceiling separate from the cell ceiling? | **Yes, and it is a sheet limit.** On a one-row sheet, 18 278 columns is accepted and 18 279 is "This action would increase the number of columns in **the sheet** above the limit of 18278 columns" | `a1.MaxColumns` is 18 278, exactly. The first run of this probe could not see it: at 1 000 rows the cell ceiling answers first, and the transcript showed the cell-limit message where a column-limit one was expected |

**Spike J, run live 2026-09-06.** Not from the plan. The live driver
watched a conditional format rule vanish somewhere between adding a table
over its range and deleting that table, and a driver can only see that
something is gone by the end. Counted after every step:

| Question | What the API did | Effect |
|---|---|---|
| Does a table take the conditional format rules over its range? | **The delete does; the add does not.** One rule on the sheet, `addTable` over the same range → still one, `deleteTable` → none. Nothing in either reply mentions a rule | `manage_range table delete` reads the rules over the range first and refuses without `overwrite`, naming each one. It is the only act in that tool that destroys anything, and it is invisible in the response — which is the definition of what this server's guard is for. Undocumented anywhere read for this project |

**Spike K, run live 2026-09-06 (`go run -tags=live ./scripts/spikes
-only K`).** Whether developer metadata is the durable anchor §6.4 is
built on. Eleven questions, each answered by mutating the sheet and then
reading the location back out of `developerMetadata.search` rather than
out of this code's idea of where it put it. The first run answered four
of them against rows the anchor had already left — a hardcoded row 2 in a
probe whose whole subject is that the row moves — so those steps now ask
where the anchor is at the moment they run.

| Question | What the API did | Effect |
|---|---|---|
| Does a row anchor survive the rows around it changing? | **Yes, through all four.** Anchored on row 3, then: insert 2 above → row 5; delete 1 above → row 4; `moveDimension` the row itself → row 2; `sortRange` over the block → row 4. Every time, the row it landed on still held the values it was created against | §6.4's premise holds, and this is the only anchor in the API that does. It is what `read_range` and the write tools can accept in place of an A1 row |
| Does a sort move the anchor with the values or leave it on the row number? | **With the values.** The sort put the anchored row's contents on row 4 and the anchor moved from row 2 to row 4 with them. `sortRange` moves rows wholesale, metadata included, rather than rewriting cells in place | `transform_range sort` does not disturb an anchor, so nothing has to be re-anchored after one. The distinction is the reason the probe prints the row's values beside the location: a location alone cannot tell the two behaviours apart |
| Does a values write over the anchored row disturb it? | **No.** The row was rewritten and the anchor stayed on it | An anchor names a row, not the data on it. So it survives the sheet being reorganised and it does *not* notice its contents being replaced — which is what the tool description has to say, because the two are easy to hear as one promise |
| Is a metadata key unique? | **No.** Two entries were created under one key and both came back from a search of it | Uniqueness is this server's to enforce: an anchor name that matches two rows is `[ambiguous]` with both listed, the same refusal `manage_range` gives. Google will not do it |
| What does a search match? | **Key alone works, and so does `locationType: ROW` alone** — neither needs a location. A sheet lookup with `INTERSECTING_LOCATION` returned every row anchor on that sheet plus the sheet's own; with `EXACT_LOCATION`, only the sheet's own. A lookup at the anchored row returned it under either strategy; at an empty row, nothing | One request lists every anchor on a sheet, which is what a `list` action returns. Exact is what a lookup by name resolves through, so a row anchor is never confused with the sheet's |
| Can a spreadsheet-level lookup be intersecting? | **No**: `400 DeveloperMetadataLookup.spreadsheet is true, but locationMatchingStrategy was specified as INTERSECTING` | Refused here before it is sent. A spreadsheet *location* reaches nothing but itself |
| Is there a one-request way to list everything, whatever it is attached to? | **Yes, and it is the empty lookup.** A filter carrying `developerMetadataLookup: {}` returned all four entries — two rows, a sheet and the spreadsheet. `locationType: SPREADSHEET` alone also works, which the reference's own wording had left doubtful, since it forbids that type under an intersecting strategy and calls intersecting the default. Several filters in one request come back deduplicated: four filters, four distinct ids, none repeated | `list` is one request against one empty lookup, which keeps §4.5 — one tool call, one API request — on a listing that would otherwise cost a request per sheet. The three shapes were probed together because the reference settles none of them and the cheapest one is not the one its prose suggests |
| Does `PROJECT` visibility work without a service account? | **Yes**, created and read back through an ordinary per-user OAuth client | Recorded, and **not** used. `DOCUMENT` is what this server writes: `PROJECT` scopes an anchor to the OAuth client that made it, so a user who re-installs under a client id of their own — which is how this server is distributed — would find every anchor they had made gone, with nothing to say why |
| What locations are refused, and in what words? | Two rows at once: `DimensionRange must represent a single row or column`. Unbounded: `DimensionRange must specify both a startIndex and an endIndex` | Both refused before sending, quoting the rule. A caller who asks to anchor a rectangle is told what an anchor can attach to rather than forwarded a message about a type they never named |
| Does `spreadsheets.get` carry anchors, or does every read cost a search? | **Both, split by location.** Sheet-level and spreadsheet-level metadata come back with an ordinary card mask. Row and column anchors appear only under `includeGridData` as `sheets.data.rowMetadata.developerMetadata`, so they arrive only for a range already being read | The card carries sheet-level anchors for free. A lookup by name is a `developerMetadata.search`, which is one request and does not need the grid — §17a.15's shape, decided the other way here because the alternative is a grid read of a range nobody asked for |
| What happens to an anchor when its row is deleted? | **It goes with the row, and nothing in the reply mentions it.** Two anchors under one key, one row deleted: the reply is an ordinary success and the search afterwards returns one | `delete_dimensions` reads the anchors over its range first and names them, and refuses without `overwrite`. Exactly spike J's shape — a delete that silently takes something the response never mentions — and the second one this project has found |

**Not observed, and recorded rather than assumed.** Reading developer
metadata needs a write-capable scope: `developerMetadata.get` and
`.search` list `spreadsheets`, `drive` and `drive.file`, and not
`spreadsheets.readonly`. Whether a card read under read-only scopes still
carries the sheet-level metadata is unanswered, for the same reason three
of spike E's error shapes are: this machine's token is not a read-only
one. `GSHEETS_READ_ONLY=true` therefore does not register the anchor
tool at all, which is true whichever way that question resolves.

**The keyring, 2026-09-06: this repository's own test suite was deleting
the developer's credentials.** Five apparent disappearances in one
session, and the cause was `go test ./...`.

`cmd`'s `isolate` helper redirected the two stores an environment
variable can redirect — `GSHEETS_CONFIG_DIR` for the token file and
`GSHEETS_REFRESH_TOKEN` for the override — and its comment named the
token file as "the other thing that could escape the sandbox". The OS
keyring is the third and no variable redirects it: it is addressed by
service name and profile, and the tests took both from the defaults. So
`TestLogoutWithNothingStored` called
`OSKeyring().Delete("google-sheets-mcp", "default")` and removed the real
refresh token, on every `make check`, for a whole session.

**The diagnosis before this one was wrong, and it is left here rather
than deleted.** It concluded the token was never deleted at all — from a
`secret-tool` search that found the item, 50 lookups that all succeeded,
and one recovery without a login. Every one of those observations was
real. The reasoning was not: the item was found between a login and the
next test run, and the "recovery" was the same. A single sample either
side of an event is not a measurement of the event, and the conclusion
"it never disappears" was drawn from never having watched the moment it
did. What settled it was a decoy secret stored under the real key, a full
test run, and looking again — and then the same with the isolation
removed, watching it go.

| What | Verdict | Effect |
|---|---|---|
| Was the token being deleted? | **Yes, by this repository.** A decoy stored under `google-sheets-mcp/default` survives a full suite with the fix and is gone after one test without it | `isolate` sets `GSHEETS_PROFILE` to a name derived from the test, so the keyring key a test can reach is one no person owns. It is set for every test rather than for the one that reaches the keyring today |
| Why did nothing catch it? | **Nothing had run the check.** The escape route the helper's comment names is the file; the keyring is the store its author did not have a variable for, and no test asserted the profile a test runs under | `TestIsolateKeepsTestsOutOfTheRealKeyring` asserts it, and the cost of it being wrong is paid by whoever runs the tests rather than by CI, where there is no keyring to damage |
| Was `ErrKeyringSilent` wasted? | **No, and it is kept.** A locked collection does read as an absence through `go-keyring`, and "run login" is the wrong advice for it. It was written for the wrong reason and is right for its own | Kept, with its reasoning corrected: it is for a keyring that will not answer, which is a real state, and not for the disappearances that prompted it |

**The agent evals, first run 2026-09-06 (`make evals`).** Sixteen tasks,
one failure, seven carrying a half nothing could check, $1.89. Both
findings were in the harness rather than in the server, which is what a
first eval run is mostly for.

| What the transcript showed | What it was | Effect |
|---|---|---|
| "append without disturbing what is below" passed on two reads and no write at all | **A check the fixture satisfied by itself.** It asked whether "Threnody" was anywhere on the sheet, and `Threnody` is one of the ten words the fixture seeds its rows from — so `Threnody-03` had been there since setup. The task verified nothing and reported a pass | The marker is a word the vocabulary does not contain, and the check reads *below* the seeded block: "add a row to the bottom" is the task, and a write that replaced row 2 would satisfy "is it on the sheet" while doing the opposite. The `sheetContains` helper is deleted rather than fixed — the shape is the problem, not the word |
| "sort by a column" failed with `10930 comes after 100` | **The scorer read past the data into another task's work.** 10930 is the column total that "total a column" wrote to B22 seven tasks earlier; the scorer read B2:B100 and found it below the sorted block. The model had sorted exactly what it was asked to | Scoring reads stop at the seeded block. The tasks share one spreadsheet and run in order, so any fixed range past the data is a range some earlier task may have written to |
| The fixed append task then failed too, on two reads and no write | **The prompt was unanswerable.** It asked for "the name and the value" against a table with four columns, three of them numeric, and the model stopped and asked which one 42 belonged in — correctly. A task a careful reader cannot answer measures how willing a model is to guess, and this server's whole argument is that it should not be | Every column the new row touches is named. It also took a second harness change to see: a failing task now prints what the model *said*, because the calls say what it did and only its answer says why — two reads and no write is a cautious model, a model that could not find the tool, or a model answering in prose, and those are three different findings |

The A/B is recorded as unverified and worth one line anyway: the
addressed-grid arm found the row and wrote to it in three calls; the
bare-rows arm made two calls and wrote nothing. That is one run of two
arms and settles nothing, but the direction is the one §4.9 assumes, and
the harness said so rather than counting an abandoned arm as cheaper.

**The live driver on phase 3's surface, run 2026-09-06 (`make live`).**
169 steps, none failed, one undetermined (Drive's content index again).
Nothing failed and reading it still found two defects, which is the
argument for reading in its purest form so far — the count was perfect.

| What the transcript showed | What it was | Effect |
|---|---|---|
| `read_range` with an anchor nobody had set: "…and any A1 **band** works here instead" | **A wrong instruction**, and one no test could see. A review pass had deduplicated the range path and the band path onto one lookup, and the merged refusal kept the band's noun — so a caller of `read_range` was told to pass a thing `read_range` does not accept. The unit test asserted the message contained "A1", which both wordings do | The lookup takes the noun from its caller, and the two tests now assert the word each path must use *and* that neither carries the other's |
| A delete refusing with `the anchor(s) "third row"` and then succeeding with `the anchor(s) third row` | **The same act described two ways, a moment apart.** The clause was added to the refusal and the sentence to the result, each quoting differently — §17a.9's exact failure, reintroduced by fixing one half of a pair | One `render.AnchorNames`, used by both. The refusal also stopped reading "…with them, and takes the anchor(s) X with them", which is what a clause written for one sentence and pasted into another sounds like |

Everything else in the run held: the anchor followed ten inserted rows to
row 13, a sort to row 14 with its values, a move to row 12 keeping the
note that a field mask could have blanked, and the delete took it and
said so. The band path resolved an anchor and refused the wrong axis.

**The live driver, run 2026-09-06 (`make live`).** Five steps failed on
the first run of phase 2's surface, and the split is worth recording:
three were the server, two were the driver's own expectations left behind
by a review pass.

| What failed | What it turned out to be | Effect |
|---|---|---|
| A merge that had been acknowledged | **The server's.** Sheets answers "You can't merge frozen and non-frozen columns", and the work sheet had frozen its first row and column several steps earlier. The refusal says the rule and not where the edge is | `format_cells` refuses a merge spanning a frozen edge before sending, naming the count, the column the freeze ends at, and `manage_sheet freeze` as the way to undo it. The counts are on the card the call has already read, so it costs nothing |
| A named range called "Livesheet band" | **The server's.** `400 The name given to this range is invalid` — which names neither the rule nor the character that broke it, and the fault was a space | `plan.CheckNamedRange` refuses before sending and says the rule. A name that is also a cell address is refused separately, with an alternative, because "Q1" is a good word for a quarter and a good cell reference and cannot be both |
| A conditional rule that could not be deleted | **The server's, found through spike J**: a `deleteTable` earlier in the run had taken it. See above |
| Removing a note | **The driver's.** The step expected removal to go through unguarded, which was true when it was written and stopped being true when a review pass decided that removing a note takes the same unseen thing replacing one does | The step now expects the refusal and then passes `overwrite`. Worth recording as a driver failure rather than a server one: the expectation was the stale half |
| Deleting a named range that was never created | **A cascade** of the second row, and the only one of the five that told us nothing new |

**Spikes L, M, N and P, run live 2026-09-07.** Four of these rows refute
something §7.6 or §8 assumed. Two of them are silent destroys, which
makes five this project has found on an API whose reference mentions
none of them.

| Convention | Verdict | Effect |
|---|---|---|
| `updateChartSpec` updates the fields it is given, like every other update request in the union | **Refuted**: the request carries no field mask and the API refuses a partial spec outright — `400 One of basicChart, pieChart, bubbleChart, candelstickChart, histogramChart, or orgChart must be set on chartSpec`. The chart was unchanged afterwards, so the refusal is clean rather than half-applied | `manage_chart update` reads the existing spec, edits it and sends it back whole. A tool that took a title and sent a title would fail every call, which is the good case; one that filled in a default chart kind would silently rebuild somebody's chart as a column chart |
| A chart notices when the data under it is deleted | **Refuted, and this is the third silent destroy**: deleting the charted column left the chart alive with one domain and **zero series**, the reply was `{}`, and the card still reports a chart. Nothing anywhere says the chart no longer draws anything | `delete_dimensions` counts the charts whose sources intersect the band and names them before the confirm gate, the way it already names anchors. Sheets has no undo, and a chart that has quietly stopped charting is worse than one that is gone |
| An invalid chart spec is refused with a message a caller can act on | **Refuted for the commonest mistake**: a `basicChart` with neither domains nor series returns **HTTP 500, "Internal error encountered"**. A 500 is classed retryable by §6.5, so the honest behaviour of a client that trusts the status is to send it again and get another 500 | `manage_chart` validates domain-and-series before it builds the request. This is the first case in the project where the API's own error class is wrong for the request that caused it, and the mapping is not what needs changing — the request is |
| `deleteEmbeddedObject` reports what it removed | **Refuted**: the reply is `{}` for a chart and for a slicer alike. Deleting the same id twice is a clean `400 No embedded object with id: …`, which is the only feedback there is | The result is composed from the card read before the delete, not from the reply. The second delete's message is good enough to pass through as `[not_found]` |
| A chart on its own sheet is a sheet like any other | **Refined**: `addChart` with `newSheet: true` makes a sheet of `sheetType: OBJECT` titled `Chart1`, **with no `gridProperties` at all**. Deleting that sheet takes the chart with it | Anything reading `gridProperties` off a card must tolerate its absence, which is a hole that predates phase 4: a spreadsheet with a chart sheet made in the Sheets interface reaches every tool this server already ships |
| A pivot table is written and deleted like the other structures | **Refuted**: there is no pivot request in the union. It is a `pivotTable` field on one `CellData`, written through `updateCells` at the anchor, and deleted by an `updateCells` naming that field with no pivot in the cell — which takes the whole output with it. The write's reply is `{}` | `manage_pivot_table` compiles to `updateCells`, and its result is read back rather than reported from the request |
| A pivot's output is data, so a read sees it and the guard counts it | **Confirmed, and half of it matters**: the output cells carry an `effectiveValue` and **no `userEnteredValue`**, so the server's own grid mask sees ten non-empty cells over a four-group pivot and the guard refuses a write over them. It cannot say what they are: `GridFields` asks for no `pivotTable` field | The guard already fails closed here, which is the important half. `read_range` and the guard's refusal both gain the pivot's anchor, so "10 non-empty cells" becomes "the output of a pivot table anchored at E1" |
| Writing into a pivot's output is refused, or destroys it | **Refuted both ways, and the truth is better**: `values.update` over one output cell returns 200 and collapses the whole pivot to `#REF!` at the anchor — "Array result was not expanded because it would overwrite data in F3" — and clearing that one cell brings the entire output back. The damage is total and completely reversible | The refusal says the pivot will stop drawing until the cell is cleared, which is true, rather than that the pivot will be destroyed, which is not. An `updateCells` over the **anchor** is a different act: it replaces the pivot outright and silently, and is the fourth silent destroy |
| The API validates a pivot table it is given | **Refuted where it matters most**: a `sourceColumnOffset` of 9 against a three-column source is accepted with a 200. A missing `summarizeFunction`, a missing `sortOrder` and a missing source are each a clean 400 | `manage_pivot_table` checks every offset against the source's width before sending. An out-of-range offset is the one mistake a caller makes by counting from one |
| A pivot may be anchored anywhere the caller likes | **Refuted**: anchored inside its own source it is accepted with a 200 and evaluates to `Circular dependency detected`. The API does not refuse it and the reply says nothing | Refused before the request is built, by the same rectangle arithmetic the guard already does |
| A pivot follows its source the way an anchor follows its row | **Refined**: deleting four of five source rows left the pivot alive, its source range shrunk with the delete, and its output reduced to a header and `Grand Total`. Silent, like the chart | `delete_dimensions` names intersecting pivots beside the charts and the anchors |
| The scopes in §17.6 are enough for everything in §8 | **Refuted by the one tool that was never probed**: `addDataSource` under `spreadsheets` + `drive.readonly` returns `403 The request scopes are not sufficient for performing this operation. Please include bigquery.readonly scope`, and the reference says refreshing a BigQuery source needs it too. `refreshDataSource` and `cancelDataSourceRefresh` returned 200 here only because there was nothing to refresh, which proves nothing about a real source | §17.6 is reopened and amended: `bigquery.readonly` is requested, behind `GSHEETS_ENABLE_DATA_SOURCES`, so a user who will never own a BigQuery project is not asked to consent to one. See §17.6a |
| The card cannot report data sources without the BigQuery scope | **Refuted**: `fields=dataSources` is accepted under the existing two scopes and returns `{}` on a spreadsheet that has none. The mask costs nothing | `get_spreadsheet` can say that a spreadsheet is connected to a data source whatever the scopes, which is the half of the feature every caller wants and none of the cost |
| §17a.7's blocker stands: a `batchUpdate` reply cannot be masked, so it carries the `spreadsheetUrl` the card drops | **Refuted**: the standard `fields` parameter applies to `updatedSpreadsheet` like any other reply. Under `updatedSpreadsheet(<CardFields>)` the URL is absent and the reply is a card. The unmasked reply does carry it, so the concern was real and is now removable | §17a.7 closed: the structural writes fold their card re-read into the write |
| §17a.15's rules cannot ride on the card | **Refuted**: `sheets.conditionalFormats` is accepted, and a sub-mask of `(ranges)` alone gives the count for 294 bytes against the 651 a whole rule costs | §17a.15 closed: the card carries the ranges, and a rule update is two requests instead of three |
| A tool that reaches a second world needs one gate, so one Kind covers it | **Refuted by the phase's own cleanup pass**: `manage_data_source` carried a `delete` that removes a whole sheet, under `Kind: Connected`, whose annotations say `destructiveHint: false` and whose gate reads only the data-sources setting. With `GSHEETS_ENABLE_DESTRUCTIVE=false` — the documented-safe configuration — the delete was still reachable, and a client trusting annotations to auto-approve would not have asked | Split into `delete_data_source`, `Kind: Destructive`, exactly as `edit_dimensions`' delete became `delete_dimensions` (§17a.10). The two gates are about different things — one is a scope, the other is what cannot be undone — and a tool needing both is two tools |
| Deleting a data source needs the BigQuery scope, like adding and refreshing one | **Refuted by spike N's own transcript, read again a day later**: `deleteDataSource` came back `400 … The data source ID doesn't exist`, not the `403 … Please include bigquery.readonly scope` that `addDataSource` gave in the same run. The evidence was in the phase's first transcript and its consequence was only seen when the delete needed a home | `delete_data_source` asks for no third scope, so it reaches a data source somebody else connected — which is the case a Sheets server is most likely to meet |
| §17a.20's `rowMetadata` is not reachable through a grid read | **Refuted, after the spike's own mask was refused for being unbalanced by one parenthesis**: `data(…,rowMetadata(developerMetadata))` is accepted and returns one object per row of the *window* — four objects for a four-row read, one carrying the anchor, for 713 bytes | §17a.20 closed with its cost stated: it saves a request and pays about 180 bytes per row of the band. Right for the bands anybody deletes by hand, wrong for a ten-thousand-row band, so the fold happens under a row count and the search stays for the rest |

