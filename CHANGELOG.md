# Changelog

The format is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [semantic versioning](https://semver.org).

## [Unreleased]

### Added

- Four tools for formatting and structure: `read_formatting`,
  `format_cells`, `manage_range` and `transform_range`.
- **`read_formatting`** is the other half of a read: what the cells look
  like, summarised per block of identically formatted cells rather than
  per cell. A cell with no format of its own is counted, not listed, so
  what comes back is what somebody set. It also reports what is attached
  to the range and decides how a cell looks without being on the cell —
  merges, conditional format rules with the index `manage_range` needs,
  banding, validation rules, notes and protected ranges.
- **`format_cells`** applies everything in one call as one atomic batch:
  number format, font, colours, borders, alignment, wrapping, merges and
  notes. A header row that is bold, centred and shaded is one request.
  Clearing is applied before setting, so "clear this and then make it
  bold" is one call rather than a clear that undoes the bold.
- **The guard extended over what formatting can destroy.** Three ops
  take something away and each is refused until acknowledged: a merge
  keeps the top-left value of every merged block and discards the rest,
  `clear_format` removes formatting Sheets cannot bring back, and a note
  replaces one no values read would have shown the caller. The cells are
  read only when one of the three is asked for, so an ordinary "make it
  bold" still costs one request.
- **`manage_range`** adds, updates and deletes what is attached to a
  range: a named range, a protected range, a validation rule, a table,
  banding, or a conditional format rule. An existing one is named by the
  range it covers rather than by an id, so nothing has to be fetched
  first; a range matching several is refused with the list. A protection
  never blocks the request that lifts it.
- **`transform_range`** sorts, replaces, trims, de-duplicates, splits,
  shuffles, fills, copies and moves. These are the operations that move
  data without the caller naming its new address, so each reads what it
  would land on and refuses first: a paste lands on cells nobody named, a
  split spills into the columns to its right, and a replacement inside
  formulas rewrites what a cell computes rather than what it shows.
- `internal/plan` grows the union builders for formatting, validation,
  protection, tables, banding, conditional formats and the transforms —
  typed, like the rest, so no request is sent that no code here has read.
- The colour, border, number-format, alignment, condition and sort-key
  parsers, each taking the spelling a person has in their hand: `1pt
  solid #cccccc`, `date:yyyy-mm-dd`, `B asc, C desc`.
- `make parity` (`gates parity`): `make check` and `ci.yml` have to run
  the same things, and the gate fails when either side has something the
  other lacks. The Makefile has said "everything CI runs" since phase 0
  and has been wrong three times.
- CI vets the build-tagged code, so the live driver and the spikes are
  compiled by something other than a maintainer's laptop. Nothing in CI
  compiled them before: `go list ./scripts/livesheet` returns one stub
  file, and a break would have surfaced at the step that closes a phase.
- `make tidy` and a CI step: `go mod tidy -diff` fails when go.mod is not
  what tidy would write. It ran nowhere before — only inside a release,
  in the form that writes, where a rewrite fails the tag on a dirty tree
  while every rehearsal stays green.
- `make secrets` runs gitleaks at the version CI uses. CI scanned and the
  Makefile did not, so the drift ran both ways.
- `cmd/` is under the coverage floor, at 40% against 48% reached. It was
  outside the profile entirely, so 571 lines of `login`, `logout`,
  `status` and `doctor` had no tests and nothing could report that.

### Changed

- **The structural tools' English moved into the renderer** (§17a.9).
  `manage_sheet` and `edit_dimensions` used to compose a sentence
  fragment that the renderer then capitalised and wrapped, which is
  phrasing the goldens could not cover and had to agree with itself
  across a refusal, a preview and a result. They now return the parts and
  the renderer owns the template. `plan.Band` lost its words with it.

### Fixed

- **A field mask could ask for the whole grid.** `includeGridData` is
  ignored when a field mask is set, so a mask naming `data(...)` is a
  grid read whatever the option says — and `manage_range`'s rule count
  was using the formatting mask with no range, which is the entered and
  effective format of every cell of every sheet. `GetSpreadsheet` now
  refuses a mask naming cell data with no range, and the rule count has
  a mask of its own. The fake could not have caught it: it returns grid
  data only when the option is set.
- **Clearing a note was not guarded.** Setting one is refused until
  acknowledged because a note is invisible in a values read; removing one
  takes the same unseen thing, and `clear_note` went through with no
  read, no blocker and nothing in the result. Asking for `note` and
  `clear_note` together is now refused rather than silently doing the
  second.
- Two of the three transform guard reads had no cell cap, so an
  unbounded range — which clamps to the whole sheet — meant reading
  every cell on it to decide what to refuse.
- An `auto_fill` destination was checked against a spreadsheet's limits
  rather than the sheet's own size, so filling past the last row gave
  Google's "exceeds grid limits" instead of the refusal that names the
  sheet and the tool that grows it.
- A protection, table or banding stored with an unbounded range — what
  the Sheets interface writes for a whole sheet — could never be named,
  because the caller's range is clamped and the stored one was not. The
  refusal also formatted that range as the empty string.
- A banding update always wrote `rowProperties`, so updating a column
  banding left it carrying both sets, which the API rejects.
- `delimiter: " "` became autodetect, because the string was trimmed
  before it was read.
- An explicitly white background read as "no formatting of its own"
  while `clear_format` still refused over it and named the cell — the
  read and the guard describing the same cell differently.
- **A package's coverage number was its subtree's.** The floor gate
  matched a package by prefix, so `internal/gapi` was scored on
  `internal/gapi/sheetstest` as well — and the number it printed
  described neither. It held for two phases because the only subpackage
  was small and well covered. `internal/gapi` is 90% on its own and was
  reported as 69%.

### Notes

- **None of this has been run against a real account.** Spike G and the
  live driver both need credentials the machine this was written on does
  not have. `make check` is green and the driver has a step for every
  option of all four new tools, but green gates are not done in this
  project: `make live` and `go run -tags=live ./scripts/spikes -only G`
  close the phase, and their transcripts have to be read rather than
  counted.

## [0.1.0] - 2026-09-06

Writing, with a guard in front of it.

### Added

- Eight write tools: `create_spreadsheet`, `write_values`,
  `append_rows`, `manage_sheet`, `edit_dimensions`, and the gated
  `clear_values`, `delete_dimensions` and `delete_sheet`. Removing rows
  or columns is its own tool rather than an action, so it carries the
  destructive annotation and the registration gate rather than depending
  on an argument for them.
- **The write guard.** Before a value write is sent, the target is read
  and the write is refused if it holds a formula, if it is not empty, if
  it is protected, or if it cuts across a merged range. Each refusal
  names the cells and the argument that would allow it. Sheets has no
  undo, so this is the only guard there is.
- **The coercion report.** Every write asks Google for the stored values
  back in the same response and names each one it changed, with both
  kinds: `text "007" -> number 7`. When something was coerced it reads
  the display back too, so a date shows as `number 46270, displayed
  "2026-09-05"` rather than as data loss.
- **Checkpoints and `dry_run`.** A read's checkpoint can be handed to a
  write, which re-reads and refuses on a mismatch; the refusal says it
  may also be a checkpoint from a different range. `dry_run` is found by
  reflection rather than declared per tool, so a write that offers a
  preview cannot fail to honour it.
- Formulas that reach outside the spreadsheet need
  `allow_external_formulas`, and the two risks are named separately: six
  functions take an arbitrary URL Google fetches from its own servers,
  and `IMPORTRANGE` pulls another spreadsheet's data in.
- `internal/plan`: the write guard and the typed builders for the
  `batchUpdate` union, with no free-form maps.
- `sheetstest` grew the write paths, and the `USER_ENTERED` coercion
  table is replayed from a live probe rather than simulated.

### Changed

- One `service.Budget` defaults and clamps every read limit in one
  place, and `max_matches` has a maximum for the first time. An
  out-of-range budget is refused rather than quietly reduced.
- The spreadsheet card builds its own link. Google's `spreadsheetUrl`
  carries the signed-in account's obfuscated id, which nothing needs to
  open a spreadsheet.
- `spreadsheets.get` no longer asks for `spreadsheetUrl`.

### Fixed

- A formula that evaluated to an error was not treated as a formula, so
  `=IMPORTRANGE(...)` showing `#REF!` could be replaced with `overwrite`
  alone. It now needs `overwrite_formulas` like any other formula.
- `manage_sheet reorder` was off by one when moving a sheet later: the
  API reads the index against the order before the move.

- A dry run answers even when the write it previews would be refused,
  and says what would stop it. The guard used to run first, so a caller
  following the refusal's own advice — "dry_run shows what would change
  without sending anything" — got the same refusal back.
- A checkpoint hashes what the cells store rather than what they show. A
  read with `formatted: true` produced one no write could match, so
  every date or currency in the range made `expect_checkpoint` report a
  conflict that had not happened.
- `values.update` keeps a cell's note and its validation rule, verified
  live. The result said they were removed, and a cell holding only a
  note refused a write for a loss that never happened; both are
  corrected, and a surviving validation rule is now reported as
  surviving, because it still applies to the value that replaced the
  old one.
- `edit_dimensions move` places the band where `to` says. The API reads
  its index against the sheet before the move, the same trap as a
  sheet's index.
- `tail_left` names both parts of the range a write did not reach, not
  whichever it checked first.
- A clear larger than one read is refused rather than guarded in part.
- `create_spreadsheet`'s preview no longer promises a sheet the call
  will not make, and its seed values get the same coercion report as
  any other write.

### Notes

- Spikes A, B and F ran, and H was added and ran. `USER_ENTERED` turns
  `$100.15` into a number *and* a currency format; `values.append`
  writes after the block the given range falls in, so the same sheet
  appends six rows apart depending on the range; neither Drive `version`
  nor `modifiedTime` moves within fourteen seconds of an edit, so
  checkpoints stay as designed.
- The live driver ran repeatedly, and every transcript held a defect the
  green count did not — including the guard hole above.
  `docs/architecture.md` §18 has them. Spikes H and I were added during
  the phase to answer questions the code had assumed: what a `sheets`
  list does to `spreadsheets.create`, and what a values write keeps.
- `/simplify` and `/code-review high` both ran. What they found is
  fixed or recorded in §17a with the reason.
- A run leaves two spreadsheets behind: `drive.readonly` cannot trash
  them. They share a title prefix and the driver prints the search that
  finds them.
- Not tagged. `main` is the maintainer's to push.

## [0.0.1] - 2026-09-06

The first release: the skeleton, the gates, and reading.

### Added

- Four read tools: `get_spreadsheet` (the spreadsheet card, no cell data
  at all), `read_range` (an addressed grid with a checkpoint),
  `search_spreadsheets` (find one through Drive) and
  `find_in_spreadsheet` (text or an RE2 pattern to A1 addresses).
- `login`, `logout`, `status` and `doctor`, plus `--version` and
  `--dump-schemas`. The server keeps serving without credentials and
  answers every tool with `[auth]`, so a client shows a working server
  with an actionable message rather than a failed connection.
- `internal/a1`: the only place that knows how A1 and the API's
  zero-based half-open `GridRange` relate. Total parsing, always-quoted
  sheet titles, and table tests for the traps — a column parser that
  accepts `A1`, and a named range that shadows a same-named sheet.
- A raw REST client with its own wire types, per-user rate limiters at
  Google's documented quota, one request in flight, retries derived from
  the HTTP method, and a host allowlist checked with the port.
- `internal/gapi/sheetstest`: an in-memory Sheets and the one Drive
  endpoint this server calls, with formulas, error cells, notes,
  validation, merges, protected ranges, ragged responses and injected
  failures.
- The repository's own gates, as Go: the coverage floor derived from
  `go list`, the closed error-class vocabulary checked from both sides,
  the identifier and data scan, the workflow pin and shell check, the
  stdio smoke test, the schema dump and diff, and the staleness gate.
- CI on Linux, macOS and Windows with every gate reading every platform;
  CodeQL; Dependabot; a signed, attested GoReleaser release.

### Notes

- Every read is bounded before the request is built, and the bound
  applies to all four output formats: the grid, the rows and the footer
  always describe the same window.
- A range that misses the sheet entirely is refused with the sheet's
  size rather than clamped to its last row, and a search says which
  limit stopped it so the advice names a dial that would help.
- Everything ran against a real account: spikes C and E, the live driver
  (32 steps), and the configuration surface — profiles, read-only scopes
  and `logout`.
- `logout` revokes the OAuth *grant*, not one token, so it signs the
  account out of every profile sharing that client. It says so and names
  them; `logout -local` deletes the stored token without revoking.
- Not tagged. CI has never run on macOS or Windows, and `main` is the
  maintainer's to push.

[Unreleased]: https://github.com/mmedum/google-sheets-mcp/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/mmedum/google-sheets-mcp/compare/v0.0.1...v0.1.0
[0.0.1]: https://github.com/mmedum/google-sheets-mcp/releases/tag/v0.0.1
