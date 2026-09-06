# Changelog

The format is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [semantic versioning](https://semver.org).

## [Unreleased]

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
