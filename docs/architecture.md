# Architecture

How `google-sheets-mcp` is put together, and the decisions a contributor
should not undo without a better reason than the one that put them here.
Part 1 is the overview. Part 2, from [Decisions](#decisions), is one
entry per decision, cited elsewhere by its D-ID. What was verified about
Google lives in the comment at the code that depends on it, with the
reference linked and the live step or probe that checks it named.

## Context

`google-sheets-mcp` is a Model Context Protocol server for Google
Sheets. One Go binary runs as a subprocess of an MCP client and speaks
MCP over stdio. It holds one person's OAuth refresh token and calls the
Sheets API v4 over raw REST, with wire types of its own (D-01, D-75).
Beside Sheets it calls Drive's `files.list` to search and to resolve a
title, `files.get` for the card's file fields, and `about.get` for the
signed-in account, all under `drive.readonly` (D-17, D-49, D-54).
Nothing is hosted.

The parties around the server:

- **The person** creates their own Cloud project and Desktop OAuth
  client, signs in once with `google-sheets-mcp login`, and every call
  acts as them, with their own access (D-01, D-51).
- **The operator** decides what the server may do, through `GSHEETS_*`
  settings the client passes at start: read-only mode, the destructive
  tools, Connected Sheets and formulas that reach outside the
  spreadsheet (D-28, D-35, D-43, D-50). The operator is usually the
  person, setting up their own client.
- **The client** draws the approval prompt for each call from the tool
  annotations and its own rules. The server asks the person nothing
  itself (D-37).
- **The model** chooses the calls and their arguments. It can be wrong,
  or persuaded by a cell somebody else wrote, so nothing it sends turns
  on a tool, lifts a refusal or writes a formula that reaches outside
  (D-28, D-40).
- **The collaborators** edit the same spreadsheet, and their edits can
  land between a read and a write (D-26).

The server talks to Google through:

- `sheets.googleapis.com`, for everything a tool reads or writes;
- `www.googleapis.com`, for the Drive calls;
- `oauth2.googleapis.com` and `accounts.google.com`, for login, refresh
  and revocation.

Nothing else is reached, and every host is checked with its port before
a credential is attached (D-05).

It works inside one spreadsheet per call (D-03). Everything in that
spreadsheet is in scope: values, formulas, sheets, rows and columns,
formatting, named and protected ranges, data validation, tables,
banding, conditional formats, charts, pivot tables, Connected Sheets
data sources, cell comments and notes (D-02). Files, folders, sharing
and revisions belong to a server built on the Drive API.

The platform has gaps nothing here can close, so nothing here promises
to close them. The API has no undo, and it cannot guard a write against
a concurrent edit (D-26). It cannot give a cell's displayed text without
its format, or say why a formula failed beyond its error type. It has no
search inside a spreadsheet, so `find_in_spreadsheet` reads and matches
here. It cannot push a change (D-78).

It is not a Drive client (D-02), a sync client (D-78) or a hosted
service (D-01). It is not Apps Script or the add-on surface, and it does
not administer Connected Sheets. It is not a calculation engine either:
formulas are written and read as text, and Google computes them.

It exists because a value written over a formula destroys the formula
without a word, and a server that does not look first lets that happen.
This one reads the target first and refuses what it would destroy unseen
(D-24).

## Quality goals

In priority order:

1. **A write never destroys what it did not see.** A values write reads
   its target first. Non-empty cells need `overwrite` with the
   checkpoint of what they replace, and formulas need
   `overwrite_formulas` as well. Protected ranges and partial merges are
   refused, and formatting, transforms and table changes guard what they
   take (D-24, D-25, D-26, D-33).
2. **What Google changes or removes is said, never hidden.** Every write
   reports the values Google coerced, read back from its own response.
   Every delete names what goes with it, including what Google removes
   without saying (D-27, D-31, D-42).
3. **The model never guesses an address.** Ranges are A1 throughout, and
   sheet titles are read from the card and quoted on the wire. A title
   matching several spreadsheets is refused with the candidates, and
   every read is an addressed grid (D-12, D-14, D-15, D-17, D-20).
4. **Irreversible and outward-reaching acts are the operator's.**
   Deletes are unregistered unless enabled, and take `confirm`. A risky
   variant is a tool of its own. A formula that makes Google fetch a URL
   or pulls in another spreadsheet needs a setting, and the model cannot
   widen its own reach (D-28, D-35, D-36, D-40).
5. **The stream, the logs and the repository stay clean, and the surface
   is a contract.** Stdout carries frames only, and logs carry no
   payload. The repository holds nothing real, and a released tool or
   field keeps working (D-06, D-07, D-09, D-57).

## Codemap

- `cmd/google-sheets-mcp` is the program. With no subcommand it serves;
  `login`, `logout`, `status`, `doctor`, `--version` and
  `--dump-schemas` are the rest. It names stdout and stderr once and
  passes them down (D-06). It serves without credentials and answers
  `[auth]` per call (D-55).
- `internal/a1` is A1 notation: parsing, formatting, quoting, column
  letters to indices and back, and the conversion to and from GridRange.
  It is the one place that knows how A1 and GridRange relate, and it
  touches no network (D-12, D-13, D-14).
- `internal/auth` runs Google's desktop flow on a loopback port with
  PKCE, and holds the scope set for each mode. Its token source runs on
  a bounded client of its own. It also inspects and revokes a grant
  (D-47, D-49, D-50, D-51).
- `internal/config` reads the `GSHEETS_*` environment and the bound
  flags through `config.Lookup`, validates them once at start, and
  refuses a removed setting. `config.Vars` is the registry of every
  variable the server reads, which the staleness and `mcpb` gates read
  (D-56, D-72).
- `internal/credentials` resolves the refresh token from the
  environment, the OS keyring, or an owner-only file warned about on
  every use (D-52).
- `internal/gapi` is the raw REST client for Sheets and the Drive calls.
  It holds what every request shares: the host allowlist with the port,
  the per-user limiters and one request in flight, deadlines, retries by
  HTTP method, the flag on the POSTs that only read, and the classes of
  Google's failures. Nothing here imports MCP (D-05, D-45, D-46, D-47).
- `internal/gapi/sheetstest` is the in-memory Sheets, with the Drive
  endpoints, behind `httptest`. It replays recorded coercion and models
  what Google was seen to do (D-59).
- `internal/grid` is the server's view of a rectangle: one cell per
  address with its formula, note and validation, padded to the window,
  the checkpoint over what was stored, and the diff a write reports
  (D-26, D-27).
- `internal/gsheets` holds the hand-written wire types for the fields
  this server reads and sends, and the request union members it builds.
  It imports nothing (D-75).
- `internal/livecover` says what "the live driver covers the tool
  surface" means, so the static gate and the driver read one exemption
  list (D-61).
- `internal/plan` compiles typed ops into `batchUpdate` union members.
  It holds the guards as pure functions over what was read: what a
  write, a format, a transform or a table change would destroy (D-24,
  D-32, D-33).
- `internal/redact` masks client ids, account addresses and Google's ids
  in what `status`, `doctor` and the drivers print (D-08, D-11).
- `internal/render` writes every word a caller reads: the card, the
  addressed grid and its footer, formatting summaries, write reports and
  refusals (D-20, D-21).
- `internal/server` wires the MCP SDK: the server instructions, the
  resources, the per-call log line, and the schema dump through an
  in-memory session (D-07, D-57).
- `internal/service` is where the rules live: resolving spreadsheets,
  sheets, ranges and anchors, budgets, the reads before a write,
  checkpoints, the gated deletes' counts and witnesses, and the metadata
  cache. It is where a failure becomes a `[class]` tool error (D-17,
  D-19, D-22, D-41, D-42, D-44, D-48).
- `internal/tools` defines the MCP tools and resources, each added
  through one function. A tool's `Kind` decides its annotations, its
  registration under read-only mode and the settings, the mark, and a
  rendered reply, and `dry_run` is found by reflection (D-21, D-30,
  D-34, D-35, D-38, D-39, D-58).
- `internal/userconfig` stores non-secret per-profile state: the client
  JSON's path, the account, where the token went and the granted scopes.
  It finds the profiles that share a client (D-53, D-54).
- `internal/version` holds the version the build stamps in, with the
  module's build info as a fallback.

Dependencies run one way: `tools` to `service`, to `plan` and `grid`, to
`gapi`, to `gsheets`. Beside the packages, `scripts/gates` is every
check the repository runs on itself, written in Go behind one dispatcher
(D-65, D-66, D-67). `scripts/livesheet` is the live driver (D-11, D-61),
`scripts/spikes` holds the probes the code's comments name (D-60), and
`scripts/evals` scores a model driving the tools (D-62).
`packaging/mcpb` holds the Claude Desktop bundle's manifest and launcher
(D-71, D-72). `testdata` holds the released surface, the API snapshot
and its verdicts, and the renderers' goldens (D-57, D-76).

## Request flow

A tool call crosses the layers in one direction:

```
MCP client
   │  JSON-RPC over stdio; stdout carries nothing else
   ▼
internal/server     SDK session, instructions, one log line per call
   ▼
internal/tools      decode the input; the Kind decides annotations,
   │                registration, the mark and both halves of the reply
   ▼
internal/service    resolve spreadsheet, sheet, range or anchor; budget;
   │                read before a write; checkpoint; [class] errors
   ├──► internal/a1      A1 and GridRange, no network
   ├──► internal/plan    guards and union builders, no network
   ├──► internal/grid    the rectangle, checkpoints, the write diff
   └──► internal/render  every word a caller reads
   ▼
internal/gapi       one request: host allowlist, limiters, deadline,
   │                retry by method, classify
   ▼
internal/auth       refresh on a bounded client
   ▼
Sheets API v4, Drive, Google OAuth
```

### Start

`main` loads the configuration and refuses a bad one before it announces
itself, a removed setting still turned on included (D-37, D-56). With no
refresh token the server serves anyway, and every call answers `[auth]`
naming `login` (D-55). `tools.Register` adds every tool the
configuration allows. Read-only mode keeps only the reads, and a
Destructive or Connected tool without its setting is left out (D-34,
D-35, D-43, D-50).

### A read

A client calls `get_spreadsheet` first. One `spreadsheets.get` with a
field mask and no grid data gives the sheet titles and ids, and the
named ranges, tables, charts, pivot tables and data sources every later
call needs (D-15, D-22). The service keeps this card for a short window
(D-48).

`read_range` names a sheet and a range. The service resolves the title
from the card and quotes it on the wire (D-14). It resolves the window
against the sheet's extent before the call, under the cell budget, so an
open-ended range is a finite request (D-22).

The grid comes back with column letters and row numbers, formulas under
values with `show=both`, and empty runs folded into one line that names
its rows. Its footer names the range shown, the range that exists and
`continue_from` (D-20, D-23). Both halves of the reply carry the grid
(D-21). The read returns a checkpoint, a hash over what the cells store
(D-26).

### A guarded write

A client calls `write_values` over cells that hold a value and a
formula. The service reads the target with one masked request. The guard
refuses `[blocked]`, naming the cells in A1 and the acknowledgments that
would allow the write, and gives the checkpoint of what it read (D-24,
D-25).

With `dry_run`, the same call reports the guard's findings and the
region it would change, and returns before anything is sent (D-30).

Sent again with `overwrite`, `overwrite_formulas` and
`expect_checkpoint`, the service reads the target again and compares. A
change since the first read is `[conflict]` (D-25, D-26). A formula that
reaches outside the spreadsheet is refused unless the operator allowed
it (D-28).

The write is one `values.update`, a PUT, which asks for the stored
values back. The result names every cell Google coerced and every
formula created (D-27, D-31, D-32). A `values.append` or `batchUpdate`
that fails after it may have landed is `[ambiguous_outcome]`, and is
never sent again (D-45).

### A delete

`delete_dimensions` exists only under `GSHEETS_ENABLE_DESTRUCTIVE`. It
takes its band in A1 and needs `confirm` (D-35, D-41). Before it asks
for `confirm` it counts the cells and formulas on the band, and names
the anchors and charts the band takes, which Google removes or empties
without saying (D-42).

### Login and refresh

`google-sheets-mcp login` opens Google's consent page and listens on a
random `127.0.0.1` port with PKCE (D-51). The refresh token goes to the
OS keyring, or to an owner-only file with a warning on every use, and
`GSHEETS_REFRESH_TOKEN` overrides both (D-52). Every refresh runs on a
bounded client (D-47).

`logout` revokes the grant, which signs out every profile that shares
the OAuth client, and names those profiles first. `logout -local`
deletes this profile's copy only (D-53).

## Invariants

- Stdout carries only MCP JSON-RPC frames (D-06).
- No log line carries a cell value, a formula, a title, a range, an
  address or a search term (D-07).
- Nothing in the repository comes from a real spreadsheet, account or
  deployment (D-09, D-10).
- The live driver and the evals read only spreadsheets they made (D-11).
- A credential goes only to Google's hosts, checked with the port
  (D-05).
- The model never sees a GridRange (D-12), and no tool defaults a sheet
  (D-15).
- No values write replaces a non-empty cell without `overwrite` and the
  checkpoint of what it replaces, or a formula without
  `overwrite_formulas` as well (D-24, D-25).
- No write creates a formula that reaches outside the spreadsheet unless
  the operator allowed it (D-28).
- A tool that removes what cannot be read back exists only under its
  setting, and every call to one takes `confirm` (D-35).
- A risky variant is a tool of its own, never an argument (D-36).
- The server never asks the person a question (D-37), and no tool
  changes its settings (D-40).
- A dry run sends nothing (D-30).
- A write that is not repeatable is never repeated after a failure that
  may have applied it (D-45).
- A read never fetches more than its budget, and says where to continue
  (D-22).
- Every reply carries the rendering in its structured half (D-21).
- A released tool, output field or resource template changes only in a
  major version (D-57).

## Cross-cutting concerns

**Configuration.** Every setting is a `GSHEETS_*` variable with a flag
bound to it. A flag overrides the environment, and the environment
overrides the default. Every reader goes through `config.Lookup`, which
reads a value of spaces alone as unset. Validation runs once at start,
and a removed setting still turned on is refused (D-56). The full list
is in [configuration.md](configuration.md).

**Kinds and approval.** Every tool has one `Kind`. Read changes nothing
and is always registered, while Write and IdempotentWrite change the
spreadsheet and are registered unless the server is read-only.
Destructive removes what Sheets cannot bring back, and is registered
only under `GSHEETS_ENABLE_DESTRUCTIVE`. Connected reaches BigQuery
through Connected Sheets, and is registered only under
`GSHEETS_ENABLE_DATA_SOURCES`. Notifying can reach a person through a
cell comment, and is registered as a Write is (D-34, D-35, D-43).

Registration, `confirm`, the guards, the checkpoint and `dry_run` hold
on every client. The annotations and Claude Code's
`requiresUserInteraction` mark shape the client's prompt and hold
nothing on their own (D-37, D-38, D-39). The README lists the tools.

**Errors.** Every tool error is a tool result in the form
`[class] message`, and the class tells the caller what to do next. The
vocabulary is closed and declared in `service.Classes` in
`internal/service/service.go`, where each class's comment says what it
means. `make classes` fails on a class emitted and not declared, and on
one declared and never emitted (D-44).

**Rate, retries and deadlines.** Requests share a read limiter and a
write limiter at Google's per-user quota, with one request in flight
(D-46). A GET or a PUT retries a transient failure. A POST retries only
when Google refused to begin it, except the POSTs that only read, which
a flag marks by name (D-45). Every read attempt, every write and every
token refresh runs under a deadline (D-47).

**Logging and what is pasted.** `slog` writes to stderr. A line names
the method, the tool, the outcome, the duration and a truncated
spreadsheet id, and nothing from the spreadsheet. Transport errors are
stripped of the URL, since a search term travels in it (D-07). `status`,
`doctor` and the drivers' transcripts mask what they print, and a tool
result does not (D-08). [security.md](security.md) holds the threat
model.

**Scopes.** The full mode asks for `spreadsheets` and `drive.readonly`,
and read-only mode for their read-only pair (D-49, D-50).
`bigquery.readonly` is asked for only when Connected Sheets is turned on
(D-43). A changed scope set needs another `login`. The OAuth client's
setup is in [gcp-setup.md](gcp-setup.md).

**Testing.** The unit suite runs against `internal/gapi/sheetstest`,
which replays what Google was seen to do (D-59). Fixtures and goldens
are generated, never recorded (D-10).
`TestLogsCarryNothingFromTheSpreadsheet` drives every tool with values
nobody would write by accident, and fails if one reaches the log (D-07).

`make live` drives the shipped binary against a real account. It is the
only check that can show a belief about Google wrong, and
`make live-cover` fails on a tool option no live step drives (D-61). The
evals score a model's end state and its trace (D-62). A closed session
ends the process cleanly (D-63), and the coverage floor holds both trees
(D-64). How to run a live check is in
[CONTRIBUTING.md](../CONTRIBUTING.md#the-live-run).

**Documentation.** A decision lives once, in part 2 here. What was
verified about Google lives in the comment at the line that depends on
it: the behavior, the reference link, where the reference is wrong or
silent, and the live step or probe that checks it. A refuted belief goes
at that line or in a decision's `Rejected:` line.

Every published method's and union request's verdict is in
`testdata/api-coverage.tsv`, and every published field the wire types
leave out has one in `testdata/api-fields.tsv` (D-76). The docs gate
holds every D-ID citation to an entry, every "Held by" cell to a gate,
linter or test that exists, and the codemap to the tree. The staleness
gate holds the README, [configuration.md](configuration.md) and the
CHANGELOG to the code. Releasing is in [release.md](release.md).

## Decisions

| ID | Decision | Status | Held by |
| --- | --- | --- | --- |
| D-01 | One stdio binary per person, signed in to their own Google account through their own OAuth client | accepted | smoke, TestDumpSchemasWorksWithoutCredentials, review |
| D-02 | The server works inside one spreadsheet: files, sharing and revisions are a Drive server's, and cell comments and notes are here | accepted | api-coverage, review |
| D-03 | One call works in one spreadsheet | accepted | review |
| D-04 | Tool names are snake_case verb_noun, distinct from the Drive and Docs servers' names | accepted | review |
| D-05 | The server talks only to Google's hosts over HTTPS, checked with the port before a credential is attached | accepted | TestGoogleOnlyAllowlist, TestCredentialsGoNowhereElse |
| D-06 | Stdout carries only MCP JSON-RPC frames | accepted | forbidigo, smoke, TestVersionFlagPrintsToStdoutAndNothingElse, TestTheLinuxLauncherPicksABinaryAndKeepsStdoutClean |
| D-07 | Logs record the call, never the payload | accepted | TestLogsCarryNothingFromTheSpreadsheet, TestEveryToolIsDriven, TestTransportErrorsCarryNoURL, TestACanceledRequestCarriesNoURL |
| D-08 | What is meant to be pasted is masked; a tool result is not, and fetches only what something needs | accepted | TestAccountIsStoredWholeAndPrintedMasked, TestPathMasksTheClientId, TestStatusKeepsTheDomainAndDropsTheRest, TestLineMasksWhatSomebodyElseAssembled |
| D-09 | Nothing from a real spreadsheet, account or deployment enters the repository, and two allow-list scanners hold it | accepted | leaks, secrets, leaks-history, TestRulesCatchPlantedIdentifiers, TestAllowlistEntriesHaveReasons, TestTheMarkerExcusesOneLineOnly, TestScanReadsUntrackedFilesToo, TestATrackedBinaryIsAFinding, TestFindingsAreAbbreviated |
| D-10 | Fixtures and goldens are generated, never recorded | accepted | TestFixtureIsSyntheticThroughout, TestNumbersAreDeterministic, leaks |
| D-11 | The live driver and the evals work only in spreadsheets they made, and print only through one redacting helper | accepted | transcript, TestPrintsOutside, live |
| D-12 | A1 is the contract: the model never sees a GridRange, and the conversion lives in `internal/a1` | accepted | TestGridRangeConversion, TestZeroBased, TestDimensionRangesAreZeroBasedAndHalfOpen, TestPivotAddByHeading, review |
| D-13 | Column parsing is total and refuses nonsense, up to column ZZZ | accepted | TestParseColumn, TestBadRangeSaysWhatARangeIs |
| D-14 | A range is built from a parsed title, always quoted, never concatenated or sent without its sheet | accepted | TestTheTitleIsAlwaysQuotedOnTheWire, TestQuotedSheetTrap, TestSheetTitlesWithApostrophesAndAccents |
| D-15 | Sheet names are read, never assumed | accepted | staleness, TestNoSheetIsEverDefaulted, TestMissingSheetListsTheOnesThatExist |
| D-16 | A range is A1: no named range, table reference or R1C1 | accepted | review |
| D-17 | A spreadsheet is an id, a URL or a title, and a title matching several is refused with the candidates | accepted | TestResolveByTitle, TestResolveRefusesToChooseBetweenTwoSpreadsheets, TestSearchEscapesTheCallersText, TestAnUnescapedApostropheDetachesTheFilter, TestSheetByIdAndByGid |
| D-18 | An object attached to a range is named by the range it covers, matched exactly | accepted | TestARangeThatMatchesSeveralIsAmbiguous, TestARangeThatMatchesNothingIsRefusedWithWhatIsNearby |
| D-19 | An anchor is an optional durable label on developer metadata, never the only way to reach anything | accepted | TestAnchorFollowsItsRowThroughEdits, TestAnchorNameIsUniqueHereEvenThoughGoogleAllowsTwo, TestAnchorListIsOneRequest, TestAnchorRefusesARectangle, TestAnchorWorksWhereABandIsTaken, live |
| D-20 | Every read renders an addressed grid, and addresses are the only handles | accepted | TestReadShowsAddresses, TestGridGolden, TestGridAlignsColumnsUnderTheirLetters |
| D-21 | Every tool returns both halves, and the structured half carries the rendering | accepted | TestBothHalvesCarryTheGrid |
| D-22 | Reads are budgeted in cells and characters, the window resolved before the call | accepted | TestOpenEndedRangeIsResolvedBeforeTheCall, TestClampResolvesTheWindowBeforeTheCall, TestTheBudgetBindsEveryFormat, TestBudgetsPastTheMaximumAreRefused, TestGetSpreadsheetRefusesUnboundedReads, TestUnmaskedGetIsRefused |
| D-23 | Content is data, and a read shows formulas rather than resolving them | accepted | TestReadShowsFormulasUnderValues, review |
| D-24 | A values write reads its target first and refuses what it would destroy unseen | accepted | TestWriteRefusesWhatItWouldDestroy, TestNonEmptyCellsNeedOverwrite, TestFormulasNeedTheirOwnAcknowledgment, TestProtectionAndPartialMergeCannotBeAcknowledged, TestASmartChipIsNamedBeforeAWriteErasesIt, TestAnErroredFormulaStillNeedsOverwriteFormulas, TestAnnotationsSurviveAndAreReportedAsSuch |
| D-25 | An overwrite carries the checkpoint of what it replaces | accepted | TestOverwriteNeedsTheCheckpointOfWhatItReplaces, TestARefusalAndADryRunGiveTheCheckpoint, TestADryRunSaysAnOverwriteNeedsItsWitness, TestATransformOverwriteIsHeldToWhatItRead, TestATransformThatReadsNothingTakesNoCheckpoint |
| D-26 | A checkpoint is a best-effort hash over what a read stored, and says so | accepted | TestCheckpointGuardsTheWrite, TestCheckpointChangesWithTheContent, TestCheckpointIgnoresFormatting, TestCheckpointFollowsTheFormulaNotItsResult |
| D-27 | The caller chooses typed or literal input, and every write reports what Google changed | accepted | TestCoercionIsReported, TestDiffReportsWhatGoogleStored, TestDiffComparesKindAsWellAsText, TestLiteralInputStoresWhatWasSent, TestInputEnumIsClosedBeforeAnythingIsSent |
| D-28 | A formula that reaches outside the spreadsheet is written only when the operator allows it | accepted | TestExternalFormulasAreOffUnlessTheOperatorSaysSo, TestTheOperatorAllowsExternalFormulas, TestAReplacementCannotWriteAnExternalFormula, TestAFormulaRewriteCannotReachOutside, TestACopyCannotMultiplyAnExternalFormula, TestCreateStillGatesExternalFormulas, TestFormulasCreatedAreNamed |
| D-29 | `append_rows` only inserts, takes its range, and reports where Google put the rows | accepted | TestAppendOnlyInserts, TestAppendNeedsARange, TestAppendReportsWhereItLanded |
| D-30 | Every write takes `dry_run`, which sends nothing and previews even a refused write | accepted | TestDryRunIsFoundByReflectionNotByMemory, TestRegisteringAWriteWithoutDryRunPanics, TestDryRunSendsNothing, TestDryRunPreviewsAWriteThatWouldBeRefused |
| D-31 | A write says what changed, read back from Google rather than restated from the request | accepted | TestDiffReportsWhatGoogleStored, TestOpsCarryTheCountsTheAPIReported, TestTailLeftIsReported, TestSourceAddReportsTheRefreshRatherThanClaimingItArrived, TestOpsDoneAndPreviewDescribeTheSameChange |
| D-32 | One tool call is one API request, and ops compile to one atomic batch | accepted | TestFormatCellsSendsOneBatch, TestFindIsOneRequestForEverySheet, TestAnchorListIsOneRequest, TestBatchUpdateAppliesNothingWhenOneRequestFails |
| D-33 | Formatting and transforms guard what they destroy, and read only when an op can destroy | accepted | TestMergingOverValuesIsRefused, TestClearingFormatIsRefusedOverFormattedCells, TestReplacingANoteIsRefused, TestCopyPasteGuardsItsDestination, TestTextToColumnsGuardsWhatIsToTheRight, TestReplacingInsideFormulasNeedsAcknowledging, TestAutoFillGuardsWhereItWouldLand, TestDeletingATableThatTakesRulesIsRefused |
| D-34 | Every tool registers through one function, and its Kind decides annotations, registration and rendering | accepted | TestAnnotationsFollowTheKind, TestAHintOnTheWrongKindPanics, TestFullSurfaceRegistersEverything, TestRegistrationGatesAreServerSide |
| D-35 | A tool that removes what cannot be read back is unregistered unless enabled, and needs `confirm` | accepted | TestDeleteIsOffUnlessDestructiveIsOn, TestSourceDeleteIsOffWithoutTheDestructiveFlag, TestClearNeedsConfirmAndSaysWhatIsThere, TestDeleteCommentNeedsConfirm, TestDeleteCountsThenNeedsConfirm, staleness |
| D-36 | A risky variant is its own tool, never an argument | accepted | TestDeleteIsNotAManageAction, TestManageDataSourceDoesNotConnect, TestSourceRefreshNeedsAnID, TestAppendOnlyInserts |
| D-37 | Per-call approval is the client's, and the server asks the person nothing | accepted | TestRemovedRequirePrompt, TestLogsCarryNothingFromTheSpreadsheet, review |
| D-38 | Annotations are exact: `readOnlyHint` on the reads, `destructiveHint` on every write that is not only additive | accepted | TestTheHintsAreExact, TestAnnotationsFollowTheKind |
| D-39 | The `requiresUserInteraction` mark is on `delete_data_source` alone | accepted | TestOnlyDeleteDataSourcePrompts, TestPermissionRulesAreTools |
| D-40 | The model never widens its own reach | accepted | TestNoToolCanWriteTheServersSettings |
| D-41 | A gated delete takes its target in A1, and names it | accepted | TestAnchorBandRefusalOffersABand, TestAClearNamesItsRange, TestADataSourceDeleteCarriesItsSheet, TestADataSourceWitnessIsReadPastTheCache |
| D-42 | A delete counts and names what it takes, including what Google removes silently | accepted | TestDeleteSheetCountsBeforeItAsks, TestDeletingRowsNamesTheAnchorsItWouldTake, TestARefusalCountsTheSeriesItWouldTake, TestClearOverPivotAnchorSaysWhatItTakes, TestSourceDeleteNamesTheSheetItTakes, TestClearCountsOnlyWhatItCanRemove |
| D-43 | Connected Sheets is opt-in: one setting registers its tools and asks for `bigquery.readonly` | accepted | TestScopesAreOptIn, TestScopes, staleness |
| D-44 | Every error is a tool result, `[class] message`, from a closed vocabulary that says what to do | accepted | classes, TestClassesAreClosedAndDistinct, TestClassesAreTheOnesClassReturns, TestToolErrorsAreResultsNotProtocolErrors, TestFailFormatsTheClass, TestMissingScopeIsForbiddenNotAuth |
| D-45 | Repeatability follows the HTTP method | accepted | TestRepeatabilityComesFromTheMethod, TestUnrepeatableWritesAreNotRepeated, TestAnAppendRetriesOnlyARefusalToBegin, TestAnAppendIsNotRepeatedAfterA5xx, TestReadOnlyFlagOnlyOnTheThreeReadingPOSTs, TestACanceledWriteIsStillAmbiguous, TestRetryAfterIsHonored |
| D-46 | Requests are paced at the documented per-user quota, one in flight | accepted | review |
| D-47 | Every request runs under a deadline, the token refresh included | accepted | TestTokenRefreshIsBounded, TestBoundedClientTakesTheConfiguredTimeout, TestLoginTimesOutWaitingForTheBrowser |
| D-48 | Spreadsheet metadata is cached briefly and never trusted for correctness | accepted | TestCardIsCachedBriefly, TestARenameIsVisibleImmediately |
| D-49 | Scopes are `spreadsheets` plus `drive.readonly`, or their read-only pair | accepted | TestScopes, TestMissingScopes, staleness |
| D-50 | Read-only mode asks for read-only scopes and registers only the reads | accepted | TestReadOnlyModeKeepsTheReads, TestScopes |
| D-51 | Sign-in is Google's desktop flow: a loopback redirect on 127.0.0.1, a random port and PKCE | accepted | TestLoginRoundTrip, TestLoginRefusesAMismatchedState, TestLoginReportsAnAuthorizationDenial |
| D-52 | The refresh token lives in the keyring, or an owner-only file warned about on every use, and the environment overrides both | accepted | TestKeyringThenFile, TestEnvironmentWinsAndIsNotDeletable, TestOSKeyringIsWired, TestASilentKeyringIsNotAMissingLogin, TestIsolateKeepsTestsOutOfTheRealKeyring |
| D-53 | Profiles separate storage, not grants, and `logout` names the profiles it takes | accepted | TestProfilesAndSharingClient |
| D-54 | The account address comes from Drive's `about.get`, stored whole and printed masked | accepted | TestAnAbsentAddressDoesNotEraseTheRecordedOne, TestGrantedScopesDoNotClearTheRecordedAccount, TestStatusSeparatesNoAccountFromNoLogin, TestAccountIsStoredWholeAndPrintedMasked |
| D-55 | The server starts and serves without credentials, answering `[auth]` per call | accepted | TestNoCredentialsKeepsTheServerServing, TestDumpSchemasWorksWithoutCredentials, smoke |
| D-56 | Configuration is `GSHEETS_*` with a bound flag each, read through one registry and validated once | accepted | staleness, TestEnvironmentThenFlag, TestInvalidConfigurationFailsBeforeAnythingRuns, TestEveryVariableTheCodeNamesIsInEnvVars, TestDefineReadsEachVariableOnce, TestASettingOfSpacesIsUnset |
| D-57 | The released tool surface is a contract | accepted | schema-diff, schema-ack, schema-baseline, release-tag, TestABreakFailsWithoutAMajorBump, TestATypeChangeBreaksOneWay, TestSchemaAckNeedsEveryItemNamed, TestTheTagMajorMustBeTheModules |
| D-58 | Resources carry the card and a sheet's used range, never a listing | accepted | TestSheetCSVIsTheUsedRangeNotTheSheet, TestSheetCSVSaysWhenItCutSomething, TestResourcesRouteByTemplate, TestResourceTemplatesMatchTheParser |
| D-59 | Tests run against an in-memory Sheets that replays what Google was seen to do | accepted | TestFormulasDependOnTheInputOption, TestFieldMasksAreBalanced, TestBatchUpdateAppliesNothingWhenOneRequestFails, TestFailureInjection, TestTrailingEmptiesAreOmitted |
| D-60 | Behavior the reference does not pin down is probed live before code depends on it | accepted | review, live |
| D-61 | Green gates are not done: the live driver covers every tool option, and a person reads its transcript | accepted | live-cover, live, TestAnOptionMissingIsAGapNotAnExcuse, TestEveryExemptionHasARealReason |
| D-62 | Evals score the end state and the trace, through this server's tools alone | accepted | evals, TestEveryPromptIsSubstituted, TestTraceChecks, TestEndStateChecksReadBack |
| D-63 | A closed session ends the process cleanly | accepted | smoke, TestCleanDisconnect |
| D-64 | Coverage is floored per package over `internal/` and `cmd/`, from a derived list | accepted | cover, TestFloorPerPackage, TestBothTreesAreUnderAFloor |
| D-65 | A rule is a test, its list derived from the code, and its checker asserts a floor on what it read | accepted | review |
| D-66 | Everything the repository runs on itself is Go | accepted | review |
| D-67 | `make check` and CI run the same set | accepted | parity, TestParityPassesOnThisRepository, TestParityFindsTheDriftsThatHappened |
| D-68 | CI runs every gate on Linux, macOS and Windows under a pinned bash | accepted | pins, TestPinsShell |
| D-69 | Every action is pinned by commit SHA and every tool by exact version | accepted | pins, TestPinPatterns, TestUnpinnedTools |
| D-70 | A release is built from a tag, reproducible, signed and attested | accepted | release-tag, release-notes, registry-publish, TestBundleIsReproducible, TestTheTagMajorMustBeTheModules |
| D-71 | The bundle manifest is held to the staged tree and a pinned schema | accepted | mcpb, mcpb-pack, TestTheSixWaysAManifestBreaks, TestCommittedManifestPacks, TestAFailedWriteLeavesNoBundle, TestTheWaysAManifestMisdeclaresItsVersion, TestTheStampedManifestIsHeldToTheSchema, TestAVendoredSchemaIsHeldToItsRecordedDigest |
| D-72 | The bundle asks for the settings a person sets elsewhere, each defaulting as the server does | accepted | mcpb, TestBundleVarsAreDerivedFromTheSettings, TestTheBundleSettingsRefuseEachBreak, TestVarsDescribeWhatBuildDoes, TestTheClientFileMayBeLeftEmpty |
| D-73 | `main` is released code, and work reaches it through a pull request the maintainer merges | accepted | review |
| D-74 | The CHANGELOG section is the release note | accepted | staleness, release-notes, TestReleaseNotesTakesOnlyItsOwnVersion, TestReleaseNotesRefusesAnEmptySection |
| D-75 | Own wire types over raw REST, and no generated client | accepted | depguard, api-fields |
| D-76 | Every published method, union request and field has a written verdict | accepted | api-coverage, api-fields, TestTheRealRecordIsRead, TestFieldsProblemsCatchesAFieldGoogleAdds, TestAPIFieldsGate |
| D-77 | The server confirms a write by asking the person through elicitation | rejected | — |
| D-78 | Changes arrive by a watch or push channel | rejected | — |
| D-79 | A tool changes the spreadsheet's title, locale or recalculation setting | rejected | — |

### D-01 One stdio binary per person, signed in to their own Google account through their own OAuth client

The server is one Go binary that runs as a subprocess of the MCP client
and speaks MCP over stdio. It holds one person's refresh token and calls
Google as that person. Nothing is hosted, and there is no shared
deployment.

Each person creates their own Cloud project, enables the Sheets and
Drive APIs, and creates a Desktop app OAuth client. Nothing
deployer-specific is built into the code, the repository or a release,
so a copy of the binary carries no one's client or token. Everything the
server calls is generally available, so no preview enrollment is needed.

The reason is reach. A Google account opens every spreadsheet its owner
can open. Per-person sign-in keeps every call inside that person's own
access, and keeps their token in their own keyring rather than in a
service someone else runs.

Rejected: a multi-tenant hosted server; the shape of Google's own Sheets
MCP server, which is remote HTTP, needs a Web application client and
four scopes, and asks for preview enrollment.
Cost: each person creates a Cloud project and a Desktop OAuth client
before the first `login`, and [gcp-setup.md](gcp-setup.md) says how. An
External app in Testing gets a refresh token that expires after seven
days, so a consumer account signs in again each week
([token expiration](https://developers.google.com/identity/protocols/oauth2#expiration)).

### D-02 The server works inside one spreadsheet: files, sharing and revisions are a Drive server's, and cell comments and notes are here

In scope: finding a spreadsheet by title, creating one, and everything
inside it. That is values, formulas, sheets, rows and columns,
formatting, named and protected ranges, validation, tables, banding,
conditional formats, charts, pivot tables, Connected Sheets, cell
comments and notes. Out of scope: folders, moving, sharing, trashing,
copying files and revisions, which belong to a server built on the Drive
API.

A Sheets server that also managed files would be a second, worse Drive
server, and a person running both would get two tools for one act. The
Drive calls this server makes are read-only lookups (D-49).

Cell comments are in because Sheets publishes them anchored to a cell,
and a thread follows its cell as the sheet changes
([CommentThread](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets#CommentThread)).
Drive's comment API sees the same threads without their cells, so only
this server can say where a thread is. A note is a field of the cell
itself. `testdata/api-coverage.tsv` records the verdict on every method
and union request this line leaves out (D-76).

Rejected: files, folders, sharing, trashing, copying and revisions;
leaving comments to Drive's comment API, which sees the threads without
their cells.
Cost: a spreadsheet `create_spreadsheet` makes stays in My Drive's root,
and the tool description says so. Moving it is a Drive server's job.

### D-03 One call works in one spreadsheet

Every tool takes one spreadsheet and works inside it. No tool takes a
list of spreadsheets, and none sweeps across files. `manage_sheet`
`copy_to` is the one call that writes into a second spreadsheet, and it
names that spreadsheet as its destination.

The reason is the cost of a mistake. A wrong id costs one refusal rather
than a sweep across files the person did not mean. Keeping a call to one
file also keeps every guard, checkpoint and count about a spreadsheet
the caller has read. A refusal can name what it found, and a dry run can
show one region, because there is one spreadsheet to describe.

A task over several spreadsheets is several calls, each one named,
resolved and refused on its own. That is the shape a client's approval
prompt shows best, since each call names the file it touches.

Rejected: a bulk tool that spans spreadsheets.
Cost: a change made to many spreadsheets costs a call each, with a
request each against the per-user quota (D-46), and the model carries
the list between calls.

### D-04 Tool names are snake_case verb_noun, distinct from the Drive and Docs servers' names

Tool names are snake_case verb_noun with no dots, such as `read_range`,
`search_spreadsheets` and `manage_pivot_table`. Claude Code prefixes
each with `mcp__<server>__`.

A person runs this server beside servers for Drive and Docs, and those
name their tools `search_files`, `search_documents` and `read_file`. A
model choosing between two tools whose names say the same thing picks
one by guesswork. So every name here says what it acts on in Sheets' own
words: a spreadsheet, a range, a sheet, a cell comment. A verb first
reads as an act, which is what a client's approval prompt shows.

A name is chosen once. A released tool name is part of the surface
contract and changes only in a major version (D-57), so a name has to be
right before its first release rather than after.

Rejected: generic names such as `search_files` or `read_file`, which a
Drive or Docs server already uses.
Cost: names run longer than generic ones, and a name that turns out to
be wrong stays until the next major version.

### D-05 The server talks only to Google's hosts over HTTPS, checked with the port before a credential is attached

The server sends requests to `sheets.googleapis.com`,
`www.googleapis.com`, `oauth2.googleapis.com` and `accounts.google.com`
over HTTPS, and to nothing else. `gapi.GoogleOnly` checks every URL
before a credential is attached: the scheme is `https`, the host is one
of the four, and the port is the default. A redirect or a wrong base URL
therefore cannot carry an access token anywhere else.

The reason is what a token reaches. An access token for these scopes
reads and writes every spreadsheet the account can open, so a request
that goes astray with it attached is a breach, not a bug. Checking the
port as well as the host closes the case of a well-known host name on a
port somebody else listens on.

There is no telemetry and no update check. The server has no reason to
reach anything Google does not run.

Rejected: telemetry and update checks.
Cost: the host allowlist is not a setting, so no configuration points
the server at a proxy host or a test endpoint. Tests reach the in-memory
Sheets through an allowlist given in code.

### D-06 Stdout carries only MCP JSON-RPC frames

MCP's stdio transport says the server "MUST NOT write anything to its
`stdout` that is not a valid MCP message", and "MAY write UTF-8 strings
to its standard error (`stderr`) for logging purposes"
([transports](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)).
A stray print corrupts the JSON-RPC stream, and the client stops working
without saying why.

So logs go to stderr through `slog`. `main` names the process's streams
once and passes them down as `io.Writer`. forbidigo refuses `fmt.Print*`
and `os.Stdout` everywhere else, with `scripts/` excluded. Its rules
carry their own messages, so a settings block that fails to load shows
up as forbidigo's default message.

The smoke gate drives the shipped binary over stdio and fails on any
line that is not a frame. `--version` and `--dump-schemas` write to
stdout because they do not serve. The Linux launcher in the Claude
Desktop bundle `exec`s the binary rather than calling it, and writes any
failure to stderr.

Rejected: holding it by discipline rather than a test; a launcher that
calls the binary as a child, or writes a failure to stdout.
Cost: only `main` names the process's streams, and everything else takes
an `io.Writer`.

### D-07 Logs record the call, never the payload

A call's log line carries the method, the tool, the outcome, the
duration and a truncated spreadsheet id. It never carries a cell value,
a formula, a sheet or spreadsheet title, a range, an address or a search
term.

A spreadsheet is somebody's payroll or customer list. A log somebody
pastes into a bug report has to be safe by construction rather than by
their care.

Two routes make that harder than it sounds. A search term travels in a
request URL, and a transport failure renders the whole URL, so `gapi`
strips the URL from transport errors before anything logs them. And this
server's own errors quote the spreadsheet back: a `[not_found]` listing
the sheet titles that do exist is the right answer for a model and the
wrong thing for a log. A tool error is returned, never logged as text,
and the log line carries its class.

`TestLogsCarryNothingFromTheSpreadsheet` drives every registered tool at
debug level against fixtures whose every value is unmistakable, and
fails if one reaches the log. It asserts the HTTP methods that reached
the fake, and `TestEveryToolIsDriven` keeps its argument table complete.

Rejected: logging a request URL, which carries a search term; logging a
tool error's text, which quotes the spreadsheet.
Cost: a transport error's text is replaced by the kind of failure it
was, and a truncated spreadsheet id is the only key that ties a log line
to a call.

### D-08 What is meant to be pasted is masked; a tool result is not, and fetches only what something needs

Three things a person is asked to paste mask what they print: a debug
log, `doctor` and `status`. So do the live driver's and the evals'
transcripts. `internal/redact` masks OAuth client ids, account addresses
and Google's opaque ids in them. The account is stored whole in the
profile and printed masked.

A tool result is not masked. `read_range` returns cells, and
`search_spreadsheets` returns titles, ids and the owner's address,
because the caller asked, the server holds their own token, and all of
it is one click away in Sheets or Drive. Masking would cost an answer
without removing anyone's access, and the owner is what makes an
`[ambiguous]` refusal usable. A leak needs a publisher, and the
publishing path is a transcript or a diagnostic pasted into an issue.

What the server does instead is not fetch what nothing needs. A search
hit's folder id is out of the Drive field masks, and the link is built
from the spreadsheet id rather than taken from `spreadsheetUrl`, which
carries the account's obfuscated id.

Rejected: masking tool results, which the guidance asking for it
addresses to multi-tenant servers and gateways; returning a search hit's
folder id, or the link that carries the account's id.
Cost: a tool result pasted into a public issue carries what it read, so
the issue form asks for a description instead.

### D-09 Nothing from a real spreadsheet, account or deployment enters the repository, and two allow-list scanners hold it

The repository is public, and the server is built by running it against
real spreadsheets. So nothing from a real spreadsheet, account or
deployment may appear in code, docs, fixtures, goldens, transcripts,
issues, pull requests, or commit and tag messages. That covers
spreadsheet ids and their URLs, titles, named range and table names, and
developer metadata. It covers cell values, notes and formulas, and a
formula is worse than a value, since `IMPORTRANGE` embeds another
spreadsheet's id. It covers account addresses, Cloud project ids, OAuth
client ids and secrets, refresh tokens, and the numeric account id.

Two scanners hold it, because they catch different things. gitleaks
(`make secrets`) covers credentials, with rules for Google client ids,
client secrets and refresh tokens. `scripts/gates leaks` covers
identifiers, which pass gitleaks untouched.

Every rule in `leaks` is an allow-list. A synthetic id says so in its
own text, with a marker such as `Fixture`. Each exception is listed by
value with its reason. A tracked binary is a finding, findings print
abbreviated, and a scan that read nothing fails. `leaks history` walks
every blob and every commit and tag message.

Rejected: a deny-list naming the domain or the organization, which would
itself be the disclosure; an inline `gitleaks:allow` comment, which
never clears the commit that introduced the line; patterns alone, which
cannot tell a real title from an invented one.
Cost: the gitleaks allowlist quotes the fabricated credential it
permits, so each exception carries a per-line marker and names its
scanner. `make leaks-history` runs by hand.

### D-10 Fixtures and goldens are generated, never recorded

`testdata/` and the goldens are built from `sheetstest`, whose content
is invented: column headings from a lorem vocabulary, and numbers from a
seeded generator. No fixture is a spreadsheet exported, copied or
trimmed down.

The reason is that the payload has no shape. A pattern catches an id or
an address, but a cell value, a sheet title and a note are ordinary
words, and a scanner cannot tell an invented column heading from
somebody's customer list. So the rule is structural rather than
diligent: there is no path by which a real value reaches `testdata/`.
`TestFixtureIsSyntheticThroughout` holds the fixture's content to the
invented vocabulary, and `TestNumbersAreDeterministic` holds the
generator to its seed, so a golden compares byte for byte.

Rejected: a fixture exported, copied or trimmed from a real spreadsheet.
Cost: a behavior only a real spreadsheet shows reaches the suite as a
case written by hand into the fake from a probe's answer (D-59), never
as data copied from the probe.

### D-11 The live driver and the evals work only in spreadsheets they made, and print only through one redacting helper

The live driver creates a scratch spreadsheet, fills it with its own
invented data, and drives every tool inside it. The evals seed the
spreadsheets they score in the same way. Neither ever reads a
spreadsheet it did not make, so the values in a transcript are the
driver's own and safe to paste.

Every line a driver prints goes through one helper. It substitutes the
ids the driver created, then runs `redact.Line` over the rest, since
Drive returns ids and addresses nobody knew in advance.
`scripts/gates transcript` reads the syntax tree and fails on any other
print. Redaction lives in that helper only: a redaction on the read path
turns a value into a placeholder, and the next call that sends it back
is refused.

Rejected: pointing the driver at an existing spreadsheet; redaction on
the read path; an allowlist of functions permitted to print; one scratch
spreadsheet reused across runs; a driver credential with a wider Drive
scope.
Cost: each run leaves its spreadsheets, titled `livesheet scratch`, for
a person to trash, since trashing needs a Drive write scope; the run
ends by printing the search that finds them. The live driver and the
evals each carry a copy of the redaction helper (#78).
### D-12 A1 is the contract: the model never sees a GridRange, and the conversion lives in `internal/a1`

Every range the model sees or sends is A1, because that is what the
person sees on screen and what the formula bar speaks. The API's
GridRange is zero-based and half-open, and a missing index means
unbounded on that side
([GridRange](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/other#GridRange)).
A1 is one-based and inclusive, and `A5:A` spells the same unbounded
range another way.

The arithmetic between the two lives in `internal/a1`, so the model
never sees an index and no other package converts by hand. A pivot
table's columns are named by letter or heading, and the server converts
them to the offsets the API takes against the source. A sort key is a
column of the sheet, and the API's absolute index is computed here. A
person can check every address the model names by looking at the sheet.

Rejected: GridRange objects composed by the model, as Google's own
Sheets MCP server has them.
Cost: every request that takes a GridRange pays a conversion, and every
index Google reports is turned back into A1 before a caller sees it.
Nothing but review keeps the arithmetic out of other packages.

### D-13 Column parsing is total and refuses nonsense, up to column ZZZ

`internal/a1` parses column letters to an index and refuses anything
else: digits, an empty string, punctuation, or a run of letters past
ZZZ. A bad range is `[invalid]`, and the message says what a range is.

A parser that rejects only the empty string returns a plausible index
for nonsense. One shipped server read `A1` as a column and returned the
index of column K, and `B2` that of column AL, so a write went to the
wrong column and looked right
([#959](https://github.com/taylorwilsdon/google_workspace_mcp/issues/959)).
A total parser turns that into a refusal.

ZZZ, column 18278, is the most columns one sheet takes. It is a limit of
the sheet apart from the cell ceiling, and Google refuses column 18279
with that limit named, while the API reference states none
([limits](https://developers.google.com/workspace/sheets/api/limits)).
Rows are bounded by the cell ceiling read as a row number.

Rejected: a parser that rejects only the empty string.
Cost: the limit here is a copy of Google's, changed by hand if Google
raises it. A reference past ZZZ is refused by the parser with A1's
reason rather than Google's.

### D-14 A range is built from a parsed title, always quoted, never concatenated or sent without its sheet

`internal/a1` builds every range from a parsed sheet title, always
quotes the title, and never sends a range without its sheet.

A1 has three traps, all in Google's own guide
([concepts](https://developers.google.com/workspace/sheets/api/guides/concepts)).
A title with a space or punctuation needs single quotes, and an
apostrophe inside it is doubled. `A1` with no sheet means a cell of the
first visible sheet, while `'A1'` is the whole sheet named A1. And a
bare name resolves as a named range before a sheet, so `'Data'` is the
sheet and `Data` may be a named range. A reference with a `!` reads its
left side as a sheet title either way.

Quoting is load-bearing for the whole-sheet form this server sends, and
harmless everywhere else. Quoting everywhere means no call site has to
know which case it is in. Joining a title to a range with a string
operation gets one of the three wrong for some title, and the request
then reads a different rectangle without an error.

Rejected: a sheet name joined to an A1 range by concatenation, as other
servers do.
Cost: every range on the wire carries its quoted title, even where
`'Data'!A1:B2` would read the same without the quotes.

### D-15 Sheet names are read, never assumed

No tool defaults a sheet, and no tool description offers `Sheet1` as an
example. `get_spreadsheet` is the first call: it lists every sheet's
title and id. A URL's `#gid=` names the sheet when the call names none.
A missing sheet is `[not_found]`, listing the titles that do exist.

Google names the first sheet in the account's language, so `Sheet1` is
`Página1` on a Portuguese account. A server whose descriptions taught
the model `Sheet1` failed there with
`Unable to parse range: Sheet1!A1:D3`, and the failure looked like a bug
in the request
([#94](https://github.com/xing5/mcp-google-sheets/issues/94)). A title
read from the card is right in every language. Sheet titles are unique
inside a spreadsheet, so a title read once is unambiguous.

The staleness gate fails on a tool description that names `Sheet1`.

Rejected: a default sheet, or `Sheet1` in a description.
Cost: `get_spreadsheet` is the first call on every spreadsheet, so a
task starts with a read before it can name a sheet.

### D-16 A range is A1: no named range, table reference or R1C1

A range is A1: a rectangle such as `B2:D40`, a column or row band such
as `B:B` or `2:5`, or a whole sheet. No tool takes a named range, a
table reference or R1C1.

`values.get` documents its `range` as A1 or R1C1
([values.get](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets.values/get)).
It also accepts a named range, as a bare name with no `!`, and that
spelling is the spelling a whole-sheet reference uses. A caller who
meant a sheet, and typed a title that is also a named range's name,
would get the other rectangle and no error. That edge is too sharp to
hand a model.

A table reference is accepted in no form. R1C1 is accepted, and it is a
second spelling of one address, which is one more thing for a model to
get wrong. A1 is also what the person sees.

Rejected: a named range as a range, whose one working spelling is the
whole-sheet form; R1C1, a second spelling of one address.
Cost: a named range is reached by the A1 range the card lists for it,
and a table by its range on the card.

### D-17 A spreadsheet is an id, a URL or a title, and a title matching several is refused with the candidates

A spreadsheet is named by an id, by a URL, or by a title. A URL's
`#gid=` or `?gid=` names the sheet. A title is resolved with Drive's
`files.list`, filtered to the spreadsheet MIME type, with the caller's
text escaped. More than one match is `[ambiguous]`, listing each
candidate's id, owner and modification time and saying to pass an id.

A person says a title, not an id. A server that takes only ids makes the
model ask the person to paste one
([#76](https://github.com/xing5/mcp-google-sheets/issues/76)). Taking
the first match acts on a file nobody chose. Text put into a Drive query
unescaped lets an apostrophe close the string, which detaches the MIME
type filter, so the search returns any file the account can see
([#93](https://github.com/xing5/mcp-google-sheets/issues/93)).
`TestAnUnescapedApostropheDetachesTheFilter` holds the escaping.

Rejected: taking the first match; caller text put into a Drive query
unescaped.
Cost: a title costs a Drive search, which needs the Drive API enabled
and `drive.readonly`. Drive's index can lag a spreadsheet made a moment
ago, so its title may not resolve at once.

### D-18 An object attached to a range is named by the range it covers, matched exactly

`manage_range` names an existing named range, protected range,
validation rule, table or banding by the range it covers, in A1. The
range must match exactly: a protection over a column and one over a cell
inside it are different objects. A range matching several is
`[ambiguous]` with the list, and one matching nothing is refused with
what is nearby.

A caller who can say "the protection on B2:B10" has not had to fetch an
id first, and A1 is this server's contract everywhere else (D-12). An
exact match keeps the call from acting on a neighbor: a looser match
would pick one of two overlapping objects for the caller.

A conditional format rule is the one exception. The API identifies those
by their position in the sheet's list
([DeleteConditionalFormatRuleRequest](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/request#DeleteConditionalFormatRuleRequest)),
so they take an `index`, which `read_formatting` reports beside each
rule.

Rejected: naming an object by its id, which a caller must fetch first.
Cost: a conditional format rule takes its position, so a caller reads it
from `read_formatting` before changing a rule.

### D-19 An anchor is an optional durable label on developer metadata, never the only way to reach anything

Developer metadata is the one label Google keeps attached to a location
while the sheet is edited around it: it "will remain associated at those
locations as they move around and the spreadsheet is edited"
([DeveloperMetadata](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets.developerMetadata#DeveloperMetadata)).
It attaches to a spreadsheet, a sheet or a band of rows or columns,
never to an arbitrary rectangle.

`manage_anchor` adds, lists, moves and removes a label on a row, a
column or a sheet, and `anchor:<name>` works wherever a range does,
except where a delete needs its target in A1 (D-41). It is offered so
that "come back to this row later" survives other people's edits. It is
never the only way to reach anything: every tool that takes an anchor
takes A1. A name is unique here although Google allows two alike, and
every anchor is written with `DOCUMENT` visibility.

Rejected: `PROJECT` visibility, which ties an anchor to the OAuth client
that made it, so it vanishes on a re-install under another client; an
anchor argument of its own beside the range.
Cost: an anchor names a row and does not notice its contents replaced.
Reading one needs a write-capable scope, so read-only mode has no
anchors.

### D-20 Every read renders an addressed grid, and addresses are the only handles

Every read renders a grid with column letters across the top and row
numbers down the side. The model writes back to what it read by naming
an address it can see, so there is nothing to remember between calls and
no handle to go stale. It is also what the person sees on screen, so the
two talk about the same cells.

The grid keeps its addresses true. A ragged response is padded back to
the rectangle asked for, since Google omits trailing empty rows and
columns. A run of empty rows folds into one line that names the rows it
covers, so an address inside it is still one the caller can write to. A
hidden band inside the read is named, since it shows nothing in the
values. The footer names the range shown, the range that exists,
`continue_from`, and the first cells it shortened.

Rejected: a handle the server remembers between calls.
Cost: every read spends characters on column letters, row numbers and
padding that a bare array would not carry.

### D-21 Every tool returns both halves, and the structured half carries the rendering

Every tool declares an output schema and returns both halves of a
result. The structured half carries the rendering, in `grid` for a read
and `summary` for a write, beside fields such as `range`, `checkpoint`,
`truncated` and `continue_from`. The text half carries the same
rendering with those fields in its footer. Neither is a serialized copy
of the other.

Clients disagree about which half they show the model. Claude Code
forwards `structuredContent` when both are present, while claude.ai and
ChatGPT show the text. The substance of a read is the addressed grid, so
a design that puts the addresses in one half loses them on some client,
and nothing says so until a task goes wrong. With the rendering in both
halves, no client is shown a half without the addresses.

`Def`'s output type is constrained to `service.Rendered`, so a tool with
no rendering does not compile. The raw arrays come only with `format`
`json`, `csv` or `tsv`, in `rows`.

Rejected: text only, which goes blank on Claude Code once a tool
declares an output schema; both halves sent in the hope that a client
shows the right one; the raw arrays beside the grid by default.
Cost: the machine form comes only when a caller asks for it with
`format`, and the rendering is then the smaller field.

### D-22 Reads are budgeted in cells and characters, the window resolved before the call

A read names its range and takes a cell budget and a character budget,
`max_cells` and `max_chars`, whose defaults and maxima are in
[configuration.md](configuration.md). The window is resolved against the
sheet's extent before the call, so a finite range is sent and an
open-ended `A:Z` never becomes a whole column in memory. A budget past
the maximum is refused rather than reduced. The footer says where to
continue. `get_spreadsheet` never asks for grid data, and a
`spreadsheets.get` without a field mask is refused before it is sent.

A display budget is not a memory budget. One server cut what it
displayed to fifty rows while still fetching `A:Z` whole, and its
maintainer found that "memory was already unbounded"
([#986](https://github.com/taylorwilsdon/google_workspace_mcp/issues/986)).
A silent cut is worse still: a short answer that says nothing is a wrong
answer
([#955](https://github.com/taylorwilsdon/google_workspace_mcp/issues/955)).
A caller who asks for more than the maximum and quietly gets less reads
a footer describing a window they never asked for.

Rejected: clamping the rendering while fetching an open-ended range
whole; an over-range budget silently reduced.
Cost: the cell window and the character budget can disagree, so a raised
`max_cells` fetches rows no rendering reaches (#69). A window is spent
on the sheet's allocated width rather than its data, and decoding the
response is most of a large read's time (#70).
### D-23 Content is data, and a read shows formulas rather than resolving them

What a read returns is data. The server never acts on what a cell, a
note or a comment says.

A read shows formulas rather than resolving them away. `show` takes
`values`, `formulas` or `both`, and `both` renders the formula under the
value where they differ. That is the only way to see that a number is
computed. It is also how a formula somebody planted becomes visible: an
`IMPORTXML` that sends the sheet's contents to a URL reads as a value
until its formula is shown. `find_in_spreadsheet` reports each match's
kind, a value, a formula or a note, for the same reason.

Values come back unformatted by default, because a currency symbol in a
number the model may compute with is a trap. A caller who wants the
display text asks for it with `formatted`.

Rejected: a read that shows only computed values, which hides what a
cell computes and what it fetches.
Cost: a read with `show=both` spends characters on the formula under
every computed value, and a caller has to ask for it.

### D-24 A values write reads its target first and refuses what it would destroy unseen

Before writing values, the server reads the target rectangle with one
masked request, and the guard refuses what the write would destroy
unseen:

- a formula, unless `overwrite_formulas` is set as well as `overwrite`;
- anything non-empty, unless `overwrite` is set;
- a protected range this account may not edit, which no acknowledgment
  allows;
- a merge the write covers only in part, which no acknowledgment allows;
- a smart chip, named and allowed by `overwrite`, because a value write
  erases it
  ([CellData](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/cells#CellData)).

A note and a validation rule are reported as kept, since a value write
leaves both in place. A refusal names the cells in A1 and the
acknowledgment that would allow the write.

The formula case needs its own acknowledgment because a formula and its
result render identically, and losing one loses the computation without
a visible change. Sheets has no undo, and in an unguarded server a write
over a formula succeeds silently.

Rejected: a minimal diff, which a rectangle of independent cells has no
structure for; one acknowledgment for a value and a formula alike;
tracked-change modes, which Sheets lacks.
Cost: a guarded write is two requests. A write into a pivot table's
output names the pivot only when its anchor lies within the read budget
(#81). The guard's refusals name the tool's arguments, so renaming an
argument means changing `internal/plan` too (#71).
### D-25 An overwrite carries the checkpoint of what it replaces

`overwrite` and `overwrite_formulas` each need `expect_checkpoint`, the
checkpoint of the cells the write replaces. Without it the write is
refused `[invalid]`, with the checkpoint of the cells as this call read
them. The guard's refusal and a dry run give the same checkpoint, so no
second read is needed. A change after that read is `[conflict]`.

A flag set after a refusal covers whatever the cells hold when the write
lands, a collaborator's later edit included, and a model can resend the
flag on its own. The checkpoint ties the overwrite to what was read, so
`overwrite` carries a decision about known contents. The server
instructions say that `overwrite` carries a decision the person made,
not a way past a refusal.

`transform_range` holds its overwrites the same way, over the cells its
guard reads: where a paste or a fill lands, where a split spills, or the
formulas a replacement rewrites. An action that reads nothing first
refuses `expect_checkpoint`, since there is nothing for it to match.

Rejected: `overwrite` alone, which covers whatever the cells hold when
the write lands.
Cost: an overwrite needs a refusal or a dry run first, for its
checkpoint.

### D-26 A checkpoint is a best-effort hash over what a read stored, and says so

Sheets has no write guard: no `writeControl`, no `requiredRevisionId`
and no ETag. Google also says of a batch that "it is not guaranteed that
the spreadsheet will reflect exactly your changes after this completes",
because collaborators edit at the same time
([batchUpdate](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/batchUpdate)).

So a read returns a checkpoint, `ck_` and a hash over the spreadsheet
id, the resolved range and what the cells store. It follows the formula
rather than its result, and ignores formatting. A write given
`expect_checkpoint` reads the range again and compares, and a mismatch
is `[conflict]` with the cells that changed.

A checkpoint narrows the window between a read and a write, and cannot
close it. The tool descriptions say it is best effort rather than
implying a guarantee. Where a person needs a real guarantee,
`manage_range` can protect the range.

Rejected: a revision guard, which Sheets does not have; Drive's
`version` field, which stayed the same across a value write and a
structural `batchUpdate`; locks.
Cost: a checkpoint narrows the window and cannot close it, and a
protected range is the only real guarantee.

### D-27 The caller chooses typed or literal input, and every write reports what Google changed

`input` is `typed` or `literal`, and the server never substitutes one
for the other. `typed` sends `USER_ENTERED`, which "is parsed exactly as
if it were entered into the Sheets UI": `1-2` becomes a date, `007`
becomes 7, and `=1+2` becomes a formula. `literal` sends `RAW`, which is
stored as a string
([ValueInputOption](https://developers.google.com/workspace/sheets/api/reference/rest/v4/ValueInputOption)).
An unknown value is refused before anything is sent.

Every write asks Google for the stored values back, in the same
response, and compares them with what was sent. The result names every
cell Google changed. It also says when Google attached a format:
`$100.15` is stored as the number 100.15 with a currency format, so it
still displays as typed, while `1-2` reads as a date. A report over
stored values alone could not tell those two apart.

The reason is that `typed` is how a person types, and a person sees what
Sheets made of it. A model does not, unless the result says.

Rejected: substituting one input option for the other; a diff over
stored values alone, which misses the format Google attaches.
Cost: `typed` is the default, so `007` becomes 7 unless the caller says
`literal`.

### D-28 A formula that reaches outside the spreadsheet is written only when the operator allows it

`IMPORTXML`, `IMPORTDATA`, `IMPORTHTML`, `IMPORTFEED`, `IMAGE` and
`HYPERLINK` take any URL, and Google fetches it from its own servers
([IMPORTXML](https://support.google.com/docs/answer/3093342)). A URL can
carry the sheet's contents in its query string, so writing one sends the
spreadsheet somewhere the person cannot see. `IMPORTRANGE` takes a
spreadsheet's URL instead, and pulls in any spreadsheet the account can
read ([IMPORTRANGE](https://support.google.com/docs/answer/3093340)).

A write creates any of them only when the server was started with
`GSHEETS_ALLOW_EXTERNAL_FORMULAS=true`. That covers every way a formula
is made: `write_values`, `create_spreadsheet`'s seed values,
`find_replace`, and the copies and fills that make formulas from the
ones they copy. A replacement inside formulas runs here first, as Google
would run it, since replacing `X(` with `(` turns `=IMAGEX(...)` into
`=IMAGE(...)`. Every formula a write creates is named in the result.

It is a setting rather than an argument, because a model persuaded to
write such a formula would pass the argument too.

Rejected: `allow_external_formulas` on the call; an external-fetch
formula treated as ordinary content.
Cost: a `find_replace` whose replacement names such a function is
refused whatever it searches (#67). A pattern Go cannot compile, such as
a lookbehind, cannot be run here first, so over formulas it is refused
unless the setting allows it.
### D-29 `append_rows` only inserts, takes its range, and reports where Google put the rows

`append_rows` always inserts rows, needs a range, and reports where the
rows went.

`values.append` writes after a table it detects in the given range, not
at the end of the sheet
([values.append](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets.values/append)).
So the result reports the range Google wrote and the table it found, and
says that the rows below moved. The range is required, since an append
with no range and an append at `A1` can land rows in different places on
the same sheet.

`append_rows` sends `INSERT_ROWS` and never `OVERWRITE`. An append picks
its destination during the call, so no read beforehand can see the rows
`OVERWRITE` would replace, and no checkpoint can witness them. A caller
who means to write over known cells uses `write_values`, which is
guarded (D-24).

Rejected: `OVERWRITE`, whose rows no read can witness; a default range,
which can land rows elsewhere than `A1` does.
Cost: rows below an insert move, so an address or a checkpoint a caller
holds for them goes stale. An append that may have landed is never
repeated (D-45).

### D-30 Every write takes `dry_run`, which sends nothing and previews even a refused write

Every write tool offers `dry_run`. With it, the call reports what it
would change and what would stop it, and returns before anything is
sent.

`add` finds the flag by reflection rather than trusting each tool to
declare it. A write without `dry_run` panics at registration, and a read
may not offer one. Each write's service function returns before it
builds or sends its request when the flag is set, and a test per tool
checks that nothing reached the fake.

The preview comes before the guard's refusal and lists what would stop
the write. A refusal's own advice is to dry-run the call, and a caller
who followed it would otherwise get the same refusal again. A dry run
sends nothing, so there is nothing to guard. Sheets has no tracked
changes, so the dry run stands in for a suggestion mode.

Rejected: the guard refusing before the preview; tracked-change modes,
which Sheets lacks.
Cost: nothing but each write's early return keeps it from sending under
`dry_run`, so a write added without one would send (#64).
`clear_values`, `manage_sheet` and `manage_anchor` check some refusals
before the preview, so a dry run there can answer with the refusal
(#74).
### D-31 A write says what changed, read back from Google rather than restated from the request

A write's result says what changed, from what Google reported rather
than from what was asked. A values write reports the range written,
Google's own count of cells, every coerced value, the formulas created,
the checkpoint after the write and the region as it reads after it. A
write shorter than what was there leaves the old tail alone, since
`values.update` skips cells with no data, and the result says what it
left
([values.update](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets.values/update)).

A structural write reports the counts the API returned. Where Google's
reply is `{}`, as for a deleted chart, the result is composed from the
card read before the call, so it can say what went. A pivot table's size
is in no request, so its result reads the output back. A data source
added reports that the refresh started, not that the data arrived. The
preview and the result of one call describe the same change.

A result restated from the request says what was asked, which is the one
thing the caller already knows.

Rejected: a result restated from the request rather than read back.
Cost: a structural write reads the card again after it, since the result
lists the sheets as they stand. A reply that is `{}` is reported from
the card read before the call.

### D-32 One tool call is one API request, and ops compile to one atomic batch

One tool call is one API request wherever the API allows it, and a
tool's ops compile into one `batchUpdate`.

Sheets counts requests rather than work: "Each batch request, including
any subrequest, is counted as one API request toward your usage limit"
([limits](https://developers.google.com/workspace/sheets/api/limits)). A
batch is also atomic: "If any request is not valid then the entire
request will fail and nothing will be applied"
([batchUpdate](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/batchUpdate)).
So a header row that is bold, centered and shaded is one call and one
request, and it lands whole or not at all. `find_in_spreadsheet` reads
every sheet in one request, and listing anchors is one request.

Where a guard needs to see the target first, that is one read with a
field mask over the target range only.

Rejected: splitting a batch so a failure is small, which costs quota and
gives up atomicity.
Cost: a guard's read is a second request, so a guarded write costs two.

### D-33 Formatting and transforms guard what they destroy, and read only when an op can destroy

`format_cells` has three ops that destroy something, and each is refused
until acknowledged. A merge keeps the top-left value of every merged
block and discards the rest, and the reply says nothing
([MergeCellsRequest](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/request#MergeCellsRequest)).
`clear_format` removes formatting Sheets cannot bring back. A note
replaces one that no values read showed. The cells are read only when
one of these is asked for, so an ordinary "make it bold" is one request.

`transform_range` runs the guard over the cells it lands on. A paste and
a fill land on cells the caller never named, so the landing rectangle is
read and guarded. A text split spills into the columns to its right. A
replacement inside formulas changes what a cell computes, so it needs
`overwrite_formulas`. `manage_range` refuses a table delete that would
take the conditional format rules over the table's range.

The read costs a request, so it is spent only where an op can destroy.

Rejected: reading the target of every formatting call.
Cost: a split whose delimiter Google detects has no known width, so the
guard reads a window to its right and treats all of it as at risk.

### D-34 Every tool registers through one function, and its Kind decides annotations, registration and rendering

Every tool is added through one function, `add` in `internal/tools`,
with a `Def` that has one `Kind`: Read, Write, IdempotentWrite,
Destructive, Connected or Notifying. The kind decides the annotations,
whether read-only mode and the settings register the tool, and that the
reply is rendered. Two fields beside it are refused on a kind they do
not describe: `Adds`, which marks a write as only adding, and
`PromptEveryCall`, the mark (D-38, D-39). The checks run before the
registration gate, so every `Def` is checked whatever the configuration.

Rules kept by hand at every call site are ways to be quietly wrong, and
one is wrong the first time a tool is added in a hurry. `Kind` is an
enum over which world a tool touches. It grew Connected for BigQuery,
and Notifying for a comment that emails a person.

Registration gates the tool, and the service gates the act. `confirm`
lives in the service, where the caller can be told why, which keeps
`Kind` from becoming a matrix.

Rejected: the rules kept by hand at each call site.
Cost: `Kind` grows an axis the first time a tool touches a second world.
`confirm` is held by each delete's tests, not by its kind.

### D-35 A tool that removes what cannot be read back is unregistered unless enabled, and needs `confirm`

A tool that removes something a person cannot get back by reading is
registered only under `GSHEETS_ENABLE_DESTRUCTIVE`, and refuses a call
without `confirm: true`. That is `clear_values`, `delete_sheet`,
`delete_dimensions`, `delete_data_source` and `delete_cell_comment`.

Sheets has no trash for a deleted sheet, cleared cells or a dropped
column, and the API cannot restore version history. Annotations are
hints a client may not trust, and a host in an auto-approve mode runs an
annotated tool without asking anyone
([tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)).
So both gates are the server's: one decides whether the tool exists, the
other whether this call meant it. `confirm` is a witness that the caller
meant this delete, never approval, since the model sets it. Each delete
counts what it takes before it asks (D-42).

The staleness gate holds the README to a permission rule for every tool
that takes `confirm`.

Rejected: client-side approval as the layer, since annotations are hints
a client may not trust.
Cost: what can be read first is guarded per call rather than gated, and
prompts by its hint (D-38). An operator turns deletes on for the whole
server, never for one call.

### D-36 A risky variant is its own tool, never an argument

A variant of a tool that is riskier than the tool is a tool of its own,
never an argument. A client remembers an approval per tool, never per
argument value, so a variant inside an approved tool goes unseen.

So `delete_dimensions` is its own Destructive tool rather than an action
of `edit_dimensions`. As an action it could not be left out of the
schema, and its tool said `destructiveHint: false`. `delete_data_source`
is split from `manage_data_source` the same way.

`connect_data_source` is its own tool, since it starts a query billed to
the project the call names, and a `refresh` names its source.
`append_rows` has no overwrite mode (D-29), and external formulas are a
setting (D-28). A `manage_sheet` `resize` that would shrink a sheet is
refused, naming `delete_dimensions`, which says what is on the rows
first.

Rejected: an action gated inside a tool by its description; one `Kind`
for a tool that reaches a second world.
Cost: `cancel_refresh` with no `id` cancels every refresh, kept because
it stops billing. `manage_cell_comment` assigns a comment to any
address, and Google emails the assignee (#66).
### D-37 Per-call approval is the client's, and the server asks the person nothing

The server never puts a question to the person. It sends no elicitation,
and no write waits on an answer. The MCP specification puts consent to
each tool call on the host, which "SHOULD" prompt for confirmation on
sensitive operations
([tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)).
The client draws that prompt from the annotations and the operator's
rules.

What the server holds on every client is registration, `confirm`, the
guards and checkpoints, and `dry_run`. A control that holds on some
clients and not others is not a control.

`GSHEETS_REQUIRE_PROMPT` refused the asking writes on a client that
could not ask, and went with the question. A configuration that still
turns it on is refused at start, because ignoring it would allow what
its owner set it to stop. The test client declares form elicitation and
fails any call the server asks about, so every tool the logging test
drives is held to asking nobody. The live driver does the same.

Rejected: the server's own question (D-77); `GSHEETS_REQUIRE_PROMPT`
ignored rather than refused at start.
Cost: a client running with nobody watching makes every registered
write, so `GSHEETS_ENABLE_DESTRUCTIVE` and `GSHEETS_ENABLE_DATA_SOURCES`
stay off for one.

### D-38 Annotations are exact: `readOnlyHint` on the reads, `destructiveHint` on every write that is not only additive

`readOnlyHint` is on the reads and nothing else. `destructiveHint` is
true on every write except those that only add: `create_spreadsheet`,
`append_rows` and `connect_data_source`. `openWorldHint` is true on the
Connected and Notifying tools.

The MCP schema says of `destructiveHint`: "If false, the tool performs
only additive updates", and its default is true
([schema](https://modelcontextprotocol.io/specification/2025-11-25/schema)).
A rename, a move, a reassigned comment and a refreshed data source all
change what is there, so none of them is additive. Clients prompt by
these hints: VS Code on every tool without `readOnlyHint`, and Codex and
claude.ai by `destructiveHint`. A hint that is wrong in the safe
direction costs a prompt, and one wrong the other way costs a write
nobody approved.

`Def.Adds` marks a write as only adding, and every other write says
true. A write added without thought errs toward a prompt. `add` refuses
the mark on a read or a Destructive tool.

Rejected: `destructiveHint: false` on a write that changes but cannot
take away, such as a rename or a refresh; the hint on the deletes and a
few writes only.
Cost: VS Code prompts on every tool without `readOnlyHint`, and Codex on
every write that does not only add, including writes the guard already
protects.

### D-39 The `requiresUserInteraction` mark is on `delete_data_source` alone

`delete_data_source` alone carries Claude Code's
`anthropic/requiresUserInteraction` mark. Claude Code then prompts on
every call to it, whatever the operator's rules say, and refuses the
call where nobody can be asked.

The mark is for a tool whose prompt is itself the point.
`delete_data_source` takes the data source's sheet, and Google unlinks
every table, pivot table, formula and chart built on the source
([DeleteDataSourceRequest](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/request#DeleteDataSourceRequest)).
Other objects depend on it, and Sheets cannot undo it.

`delete_sheet` is not marked: the file stays, the editor's version
history can restore a sheet, and removing scratch tabs is a delete done
many times in a row. Nor are `clear_values`, `delete_dimensions` or
`delete_cell_comment`, which are repeated deletes of one thing, where a
mark brings back a prompt per call. `Def.PromptEveryCall` sets the mark,
and `add` refuses it on anything but a Destructive tool.

Rejected: the mark on every Destructive tool, which is a prompt per call
on repeated deletes; the mark beside the server's own question.
Cost: the other deletes prompt only by the operator's rules. The README
gives an `ask` rule for each, and the staleness gate holds every name
there to a registered tool.

### D-40 The model never widens its own reach

No tool writes this server's settings, turns a tool on, or lifts a
refusal. The switches are the operator's, set in the environment the
client passes at start (D-56). A refusal names what is in the way and
what the person can decide, and never offers an argument that gets past
it.

The model chooses the calls, and a cell somebody else wrote can persuade
it. A server the model can reconfigure has no operator: every setting in
it would be one persuasive cell away from on.

`TestNoToolCanWriteTheServersSettings` walks every package
`internal/server` reaches, tests aside. It fails if one imports the
profile, the credentials, the sign-in, `os`, `os/exec` or `syscall`, and
it asserts a floor on how many packages it read, so a walk that found
nothing does not pass.

Rejected: a tool or an argument that changes a setting for the session,
such as allowing external formulas on one call (D-28).
Cost: turning on a tool or a formula means restarting the server with
another setting, which a person does and a model cannot.

### D-41 A gated delete takes its target in A1, and names it

A client's approval prompt shows the call's arguments, so a gated
delete's arguments say what goes, in terms a person can check.

`delete_dimensions` takes its band in A1. An `anchor:` band, which
resolves only when the call runs, is refused `[invalid]`, naming the
band it stands for. `clear_values` always takes its range in A1. An
omitted range, which would mean the whole sheet, and an `anchor:` are
refused, each naming the range it stands for. A whole sheet named in A1
is still a clear.

`delete_data_source` takes `sheet`, the title of the sheet the source
made, beside its `id`. A data source id means nothing to a person, and
the tab is what goes. The title is compared with the card, read again
past the cache before a mismatch is refused `[conflict]`. A dry run
without the title names the sheet. A call without an `id`, or with one
the spreadsheet does not have, lists each source and its sheet, since
with only the destructive switch on, no other tool lists their ids.

Rejected: an omitted range standing for the whole sheet; an `anchor:`
that resolves when the call runs.
Cost: `delete_data_source` without its sheet's title, or with one the
card disagrees with, is refused, so a caller reads the card first.

### D-42 A delete counts and names what it takes, including what Google removes silently

A delete counts what it takes and names what goes with it before it asks
for `confirm`, on the refusal, the dry run and the result alike.
Google's replies say nothing about most of this, so the server has to
say it beforehand.

`delete_sheet` counts the non-empty cells, formulas and charts, and
names the anchors on the sheet. `delete_dimensions` counts the cells and
formulas on the band. It names the anchors on the band, which
`deleteDimension` removes without a word, and the charts whose series
read it: a chart survives the delete, keeps its place and its title, and
draws nothing
([DeleteDimensionRequest](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/request#DeleteDimensionRequest)).

`clear_values` counts what it can remove rather than what is there. A
clear over a pivot table's output does nothing, while one over its
anchor takes the whole table, so an anchor in the range is named. A
clear larger than one read is refused with its size, since a guard that
read part of a range does not guard the rest. `delete_data_source` names
the sheet it takes.

Rejected: a clear sent for a range larger than its one read.
Cost: naming a band's anchors costs a developer metadata search on every
band delete. `delete_dimensions` does not name the pivot tables whose
source a band cuts, though their output can fall to a header and a grand
total (#63).
### D-43 Connected Sheets is opt-in: one setting registers its tools and asks for `bigquery.readonly`

Connected Sheets needs `bigquery.readonly`: `addDataSource` is refused
under `spreadsheets` and `drive.readonly`, with Google naming that
scope, and refreshing a real BigQuery source needs it too
([AddDataSourceRequest](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/request#AddDataSourceRequest)).

`GSHEETS_ENABLE_DATA_SOURCES` turns it on. It registers
`connect_data_source` and `manage_data_source`, and adds
`bigquery.readonly` to what `login` asks for. Off, which is the default,
the login screen asks for nothing about BigQuery.

Most people using a Sheets server do not use BigQuery, and asking them
to grant access to it for a tool that needs a billing-attached Cloud
project is a cost. The setting makes that cost theirs to choose.
`doctor` says which scopes the token carries.

Listing data sources needs no extra scope, so `get_spreadsheet` reports
a connected source in every configuration. `delete_data_source` is
Destructive rather than Connected, since the delete is reachable under
the default scopes wherever a source exists.

Rejected: no Connected Sheets, which was weighed and overruled;
`bigquery.readonly` at every login.
Cost: turning it on means signing in again, and a BigQuery source needs
a Cloud project with billing. Whether a collaborator without BigQuery
access sees the results is not known, so the description says only that
they go into a sheet (#65).
### D-44 Every error is a tool result, `[class] message`, from a closed vocabulary that says what to do

Every failure a caller sees is a tool result, not a protocol error, in
the form `[class] message`. The model sees a tool result and can act on
it. The message says what to do next.

The vocabulary is closed and declared once, in `service.Classes`, with a
comment on each class saying what it asks of the caller. A class exists
only where it asks for something the others do not.

`ambiguous` says a reference matched several things and the caller must
choose. `ambiguous_outcome` says a write may have landed and the caller
must go and look. `blocked` is this server refusing what the API would
allow, and `forbidden` is the account lacking access. A quota refusal
says it is quota, since one server reported a quota error as something
the person had to fix
([#75](https://github.com/xing5/mcp-google-sheets/issues/75)).

`make classes` reads the declared classes and every class the code
emits, and fails on one emitted and not declared, on one declared and
never emitted, and on a scan that read too little to mean anything.

Rejected: a smaller vocabulary of six classes, which collapses
`ambiguous` and `ambiguous_outcome` into `invalid`.
Cost: the SDK's own schema validation answers outside the vocabulary, so
an argument with a wrong name or type is refused in the SDK's words
(#79).
### D-45 Repeatability follows the HTTP method

Whether a request may be sent twice follows from its HTTP method. A GET
or a PUT may be repeated, and `values.update` is a PUT. A POST may not:
`values.append` would insert the rows twice, and `batchUpdate` would
make a second sheet. So a repeatable request retries a transient failure
with backoff, honoring `Retry-After`, and a POST retries only on 429, a
refusal to begin.

Any 5xx, 503 and 500 included, or a cut connection, on a write that is
not repeatable is `[ambiguous_outcome]`. It says to read the spreadsheet
before repeating the write. Google's `code.proto` says of `UNAVAILABLE`
that it is "not always safe to retry non-idempotent operations"
([code.proto](https://github.com/googleapis/googleapis/blob/master/google/rpc/code.proto)).
A write canceled while a retry waits, after a first attempt was sent,
is ambiguous too.

Three POSTs only read: `spreadsheets.getByDataFilter`,
`values.batchGetByDataFilter` and `developerMetadata.search`. A
`readOnly` flag marks them, and a test over the syntax tree of
`internal/gapi` allows the flag on those methods by name. A POST added
without thought counts as a write and fails closed.

Rejected: retrying a write the API calls idempotent; a method-name
pattern for the reading POSTs; retrying a write that is not repeatable
after a 503.
Cost: a write that may have landed is `[ambiguous_outcome]`, and the
caller reads before repeating it. A write canceled while its request is
in flight comes back `[unavailable]` instead (#84).
### D-46 Requests are paced at the documented per-user quota, one in flight

Requests are paced at the quota Google documents: 60 read requests and
60 write requests a minute per user, refilled every minute
([limits](https://developers.google.com/workspace/sheets/api/limits)).
`internal/gapi` holds a read limiter and a write limiter at one request
a second each, with a small burst, and lets one request be in flight at
a time. A limiter is taken inside the retry loop, once per attempt.

One request at a time is also Google's own advice for avoiding 503s on a
spreadsheet
([troubleshoot](https://developers.google.com/workspace/sheets/api/troubleshoot-api-errors)).
A 429 costs a retry and a wait for the minute to refill, so pacing below
the quota is faster than meeting it head on.

Raising the limiters would not raise the quota. Google counts a request
per user however fast it arrives, and a faster client would spend its
minute sooner and then wait.

Rejected: limits guessed rather than read from Google's page; limits
raised past the quota, which would not raise it.
Cost: a request takes about a second with one in flight, so a turn of
several calls takes seconds. No test pins the rates or the one request
in flight; review does.

### D-47 Every request runs under a deadline, the token refresh included

Every request runs under a deadline. A read attempt gets
`GSHEETS_HTTP_TIMEOUT`, and a write gets `GSHEETS_WRITE_TIMEOUT`, whose
default matches the 180 seconds Sheets lets a request run before it
answers with a timeout
([limits](https://developers.google.com/workspace/sheets/api/limits)).
`login` gives up waiting for the browser after a bound.

The token refresh is the request that is easy to leave unbounded. An
oauth2 refresh runs on the HTTP client found in its context, and with
none there it runs on `http.DefaultClient`, which has no timeout. So the
token source and the login code exchange each carry a bounded client of
their own, and a test hangs a listener to prove it.

The reason is that a stalled request hangs the call, and the client
shows the person nothing. With one request in flight (D-46), it also
holds up every call behind it.

Rejected: the client's `Timeout` alone, since an oauth2 refresh
otherwise runs on `http.DefaultClient`.
Cost: a write may run as long as Sheets allows, and a stuck write holds
the one request in flight for that long.

### D-48 Spreadsheet metadata is cached briefly and never trusted for correctness

The spreadsheet card is reused by id for `service.CacheWindow`, and the
sheet title to id map with it. A write through this server drops the
entry.

Every tool call that names a sheet reads the card first, so a turn that
calls `get_spreadsheet` and then two `read_range`s pays for the card
once instead of three times. With one request in flight (D-46), each
saved call is about a second. The gap between two tool calls is model
latency, not machine latency, so a window of a few seconds never fires.
The window is long enough to span a turn, and not much more.

A structural write through this server drops the cached card, so a
rename through this server is visible at once. A values write does not:
after `append_rows` adds rows, a read can resolve its window against the
row count from before the append until the window ends. A sheet renamed
by someone else inside the window fails the next call loudly rather than
reading the wrong rectangle. `delete_data_source` reads the card again
before it refuses a source or a sheet it does not find (D-41).

Rejected: a five-second window, which never spans a turn.
Cost: a sheet renamed by someone else inside the window fails the next
call that names it, and a read after `append_rows` can miss the appended
rows, until the window ends (#85).
### D-49 Scopes are `spreadsheets` plus `drive.readonly`, or their read-only pair

The server asks for `spreadsheets` and `drive.readonly`. Read-only mode
asks for `spreadsheets.readonly` and `drive.readonly` (D-50).
`drive.readonly` covers the Drive calls the server makes: `files.list`,
`files.get` and `about.get`.

`drive.file` reaches only the files the app created or the person opened
through the Google Picker
([scopes](https://developers.google.com/workspace/sheets/api/scopes)). A
stdio server cannot show a Picker, so a search under `drive.file` would
find almost nothing. Full `drive` would let the server manage files,
which it does not do (D-02). A scope asked for and not used is access
the person granted for nothing.

A missing scope is `[forbidden]`, naming the scope and saying to run
`login` again.

Rejected: `drive.file`, which reaches only files the app made or the
Picker showed; full `drive`; `openid` and `userinfo.email` to record an
address (D-54).
Cost: trashing a file needs a Drive write scope, so the live driver
leaves its scratch spreadsheets for a person to trash (D-11).

### D-50 Read-only mode asks for read-only scopes and registers only the reads

`GSHEETS_READ_ONLY=true` registers only the reads and asks for
`spreadsheets.readonly` and `drive.readonly` at `login`.

Read-only mode is a restriction Google enforces, not a label. Under a
read-only scope, only `spreadsheets.get`, `values.get` and
`values.batchGet` work. `getByDataFilter`, `values.batchGetByDataFilter`
and both developer metadata reads need a write-capable scope, as each
method's scopes in the discovery document say
([discovery](https://sheets.googleapis.com/$discovery/rest?version=v4)).
So A1 is the only addressing there is in read-only mode. No read may
depend on a data filter or developer metadata, and anchors are a label a
read can use but never needs (D-19).

A server that only hid its writes would still hold a token that can
write. With the read-only pair, Google refuses the write too.

Rejected: read-only as a scope swap alone, without changing what is
registered.
Cost: data filters and anchors are unavailable, so A1 is the only
addressing. Switching modes means signing in again.

### D-51 Sign-in is Google's desktop flow: a loopback redirect on 127.0.0.1, a random port and PKCE

`google-sheets-mcp login` reads a Desktop app client JSON, opens
Google's consent page in a browser, and listens on a random port on
`127.0.0.1`. It checks `state`, and exchanges the code with PKCE. This
is Google's documented flow for installed applications
([native app](https://developers.google.com/identity/protocols/oauth2/native-app)),
and the loopback redirect RFC 8252 describes.

A desktop client keeps everything on the person's machine: the client
file, the redirect and the token. Nothing has to be hosted to receive
the code. PKCE means another local process that catches the code cannot
exchange it. A random port leaves nothing listening at a known address.
A mismatched `state` is refused, and a denial on the consent page is
reported as one.

Rejected: a Web application client, which needs a redirect somebody
hosts.
Cost: `login` needs a browser on the machine, or a port forwarded over
SSH from one.

### D-52 The refresh token lives in the keyring, or an owner-only file warned about on every use, and the environment overrides both

The refresh token is found in this order: `GSHEETS_REFRESH_TOKEN` in the
environment, for CI and automation; the OS keyring, keyed by the
profile; then a file readable only by its owner, under the profile's
directory. The file is written only when the keyring was unavailable at
`login`, and every use warns about it.

A refresh token opens every spreadsheet the account can open, so it
belongs in the place the operating system keeps secrets. A broken
keyring, such as a machine with no secret service, falls through to the
file so a headless machine still works. Its error is reported when
nothing else is found, so a silent keyring is not mistaken for a missing
login.

The tests redirect the keyring as well as the config directory and the
environment, so `go test` never reads or deletes the real token.

Rejected: a token file as the default store, which leaves the credential
in plain text on every machine.
Cost: a token file is a credential in a file. `logout` leaves the
environment override alone, since it did not put it there.

### D-53 Profiles separate storage, not grants, and `logout` names the profiles it takes

`GSHEETS_PROFILE` keeps a separate client path, token and account under
`GSHEETS_CONFIG_DIR`. A profile is separate storage, not a separate
grant.

Google revokes the grant, not one copy of the token. Two profiles that
sign the same account in through the same OAuth client share that grant,
so `logout` under one signs out the other at the same moment. So
`logout` says that revocation covers every profile sharing the client,
and names those profiles before and after it revokes. `logout -local`
deletes this profile's stored copy without revoking anything.

The reason is that a logout which reports one profile while signing out
several misleads the person about what still works.

Rejected: profiles as separate grants.
Cost: `logout` revokes every profile sharing the OAuth client and
account, and `logout -local` keeps the others signed in.

### D-54 The account address comes from Drive's `about.get`, stored whole and printed masked

The profile records the signed-in account's address, read from Drive's
`about.get`, which `drive.readonly` already allows
([about.get](https://developers.google.com/workspace/drive/api/reference/rest/v3/about/get)).
It is stored whole and printed masked by `status` and `doctor` (D-08).

`tokeninfo` returns an address only for a token carrying an email scope,
and this server asks for none. Adding a scope to record a string would
widen the grant for a label.

Drive's `User.emailAddress` "may not be present in certain contexts if
the user has not made their email address visible to the requester"
([User](https://developers.google.com/workspace/drive/api/reference/rest/v3/User)).
So an absent address never erases a recorded one. `status` prints
`not configured` when nobody is signed in and `(not recorded)` when
somebody is but Drive gave no address.

Rejected: `tokeninfo`, which carries no address under these scopes; a
third scope to record a string.
Cost: an account that hides its address shows as `(not recorded)`.

### D-55 The server starts and serves without credentials, answering `[auth]` per call

The server starts and serves without a usable refresh token. It warms
the token off the startup path, and on a failure it logs and keeps
serving. Every tool then answers `[auth]`, naming `login`.

A server that exits at start shows the person "failed to connect" in the
client, and the model never learns why. A server that answers `[auth]`
tells both of them what to do. `--dump-schemas` works without
credentials too, so the schema gates run in CI where there are none.

The smoke gate drives the shipped binary with no credentials and checks
that a call answers `[auth]`. `doctor` checks the credentials, the token
exchange, the granted scopes and both APIs, for the person setting up.

Rejected: exiting, which shows the person "failed to connect" and the
model nothing.
Cost: a server with no credentials registers every tool, so a client
lists tools that each answer `[auth]` until `login` runs.

### D-56 Configuration is `GSHEETS_*` with a bound flag each, read through one registry and validated once

Every setting is a `GSHEETS_*` variable with a flag bound to the same
name. Every MCP client passes a stdio server a command, arguments and an
environment, and the environment is the one place they all have. A flag
given explicitly overrides the environment, and the environment
overrides the default.

Every reader goes through `config.Lookup`, which drops the spaces around
a value, so a form field cleared to a space means the default.
`config.Vars` is the registry of every variable the server reads, the
removed one included. Validation runs once at start, so a bad
configuration fails before the server announces itself. A removed
setting still turned on is refused rather than ignored. A test scans the
code and refuses a variable's name typed whole, and a server variable
read through `os.Getenv` or `os.LookupEnv`.

The staleness gate holds [configuration.md](configuration.md) to the
registry, and the `mcpb` gate derives the bundle's settings from it
(D-72).

Rejected: a server variable read with `os.Getenv`, or a name typed
whole.
Cost: a path that truly begins or ends with a space is not supported.

### D-57 The released tool surface is a contract

A released tool, resource template, input field or output field keeps
working until a major version.

`scripts/gates schema-diff` dumps the binary's schemas and compares them
with `testdata/schema-baseline.json`, the surface of the CHANGELOG's
newest release, at every depth: `threads[].cell` as much as `threads`. A
break is a tool, resource or field removed; an input that takes fewer
types or values; an output that may return a type it did not, or be
missing where it was required; or an input newly required. Types are
compared one way, as JSON Schema 2020-12 reads them. A break fails
unless the module path's major version is above the baseline's.
`schema-baseline` records the surface in a release's commit,
`schema-ack` needs a commit footer naming each changed item, and
`release-tag` holds a tag's major version to the module's.

One invalid schema is not one broken tool: a client validating it
rejects the whole request, and every tool in the session goes with it
([#138](https://github.com/freema/mcp-gsheets/issues/138)).

Rejected: top-level output fields compared with the last tag, which
passed a lost nested field; schemas trusted to be well formed.
Cost: a break ships only with a major version bump and a module path to
match, in `go.mod` and every import.

### D-58 Resources carry the card and a sheet's used range, never a listing

`gsheets://{spreadsheet}` is the card, and
`gsheets://{spreadsheet}/{sheet}` is that sheet's used range as CSV
under one character budget. There is no static list and no subscription.

A static list of spreadsheets would be a Drive listing, which is a Drive
server's (D-02). A subscription needs a feed of changes, and the API has
none (D-78). A resource carries what a tool already reads, for a client
that attaches content rather than calling a tool.

The used range rather than the sheet: a sheet has the grid it was given
whatever is on it, and a resource of empty cells is commas that describe
nothing. A read cut short by the budget says so in a second content
block rather than inside the CSV, since CSV has no comment syntax and a
note there parses as a row of data. A sheet title is percent-encoded in
the URI, since a title can hold a slash, a question mark or a hash.

Rejected: a static list, which is a Drive listing; subscriptions, which
the API cannot feed; a truncation note inside the CSV; reusing `Read`,
which renders a grid nobody asked for.
Cost: a resource builds a grid the size of its window rather than its
data, and a change to the fetch window has two places to land (#77).
### D-59 Tests run against an in-memory Sheets that replays what Google was seen to do

The unit suite runs against `internal/gapi/sheetstest`, an in-memory
Sheets with Drive's endpoints, behind `httptest`. It models sheets with
formulas and error values, merges, protected ranges, validation and
notes, and the `batchUpdate` union. It models append's table detection,
a write that skips rather than clears, and ragged responses. It injects
failures such as a 429, a 5xx, a cut connection and a timeout. A batch
applies nothing when one request in it fails, as Google's does.

The fake does not invent Google's parser. What `USER_ENTERED` makes of
an input is recorded from a live probe and replayed, and input nobody
recorded is a fake error, never a guess. A fake built from the
documentation inherits the documentation's errors, and a suite run
against it agrees with every one of them.

A small table lives in one place. An error type's display text lives in
`internal/gsheets` beside the type, so the fake and the code cannot
render one error two ways.

Rejected: a fake that simulates `USER_ENTERED` from the documentation; a
table copied between the fake and the code.
Cost: the fake ignores what a field mask asks for, so a mask that drops
a needed field passes the suite (#80). Its table delete keeps the
conditional format rules Google drops (#83).
### D-60 Behavior the reference does not pin down is probed live before code depends on it

The discovery document and Google's guides are the first source. Where
they are silent or vague, a probe asks a real account before code
depends on the answer. The probes are in `scripts/spikes`, each named
for its question, such as "what values.update makes of its input" or
"whether developer metadata is a durable anchor".

The answer lives in the comment at the line that depends on it, with the
reference link, where the reference is wrong or silent, and the probe or
live step that checks it.

The reason is that a reasonable reading of the reference has been wrong
here more than once. Deleting a row takes the anchors on it with nothing
in the reply. Sheets refuses a merge over a pivot table's output. A
typed boolean column turns a word into FALSE. A fake written from the
reference would agree with each wrong reading (D-59).

A probe runs at the start of the work it decides. One whose subject does
not exist yet gets skipped and then forgotten.

Rejected: a reference page's prose as evidence for a behavior code
depends on.
Cost: a probe needs a real account and a person to read what it printed,
and it runs before the work it decides, not after.

### D-61 Green gates are not done: the live driver covers every tool option, and a person reads its transcript

Every fake in the unit suite is written from what we believe Google
does, and only a live run tests the belief. `make live` drives the built
binary against a real account through every tool and every option, in
spreadsheets it made (D-11). How to run one is in
[CONTRIBUTING.md](../CONTRIBUTING.md#the-live-run).

Every step carries `expect_error` and a `why`. An expected refusal
proves as much as a success, and a success where a refusal was expected
is a failure. A result that states something about the spreadsheet is
read back and compared. Anything eventually consistent, such as Drive's
index, is polled, and the run says which answer it saw.

Coverage is checked twice. `scripts/gates live-cover` reads the driver's
source in CI and fails on an option no step sends. The driver records
what it put on the wire and checks it against the tool list the server
publishes, since a step can exist in the source and never run. Both read
the exemption list in `internal/livecover`, and each exemption needs a
real reason.

A person then reads the transcript. A driver that checks only that calls
succeeded reports success while results are wrong.

Rejected: a driver that checks calls succeeded rather than told the
truth; a static coverage check alone.
Cost: a run needs a real account, leaves its spreadsheets behind and is
read line by line. Some behaviors no run reaches, such as the data
source tools without a BigQuery project (#68).
### D-62 Evals score the end state and the trace, through this server's tools alone

`make evals` gives a model tasks with only this server's tools. Some ask
it to find a spreadsheet by name, total a column, append without
disturbing what is below, or fix a formula. Some need a refusal: a write
over a formula, or an external formula nobody asked for. Others use a
sheet name that is not English, the tail of a long sheet, or a number
written into a pivot table's output. The harness seeds every spreadsheet
it scores (D-11).

Each task is scored twice. The end state is read back through the
server. The trace is checked for invented ranges, guessed sheet names
and acknowledgments passed when nobody asked. A model that guessed a
range and was lucky completes the task, and only the trace shows it.

Every task answers what it checks when the world says no. Where an end
state cannot exist, the task scores the trace and prints which half went
unchecked. A prompt holding an unsubstituted placeholder is refused, and
a unit test walks every task's prompt, since a harness that sends a
literal placeholder can pass a task for the wrong reason.

Rejected: scoring the end state alone.
Cost: a run costs money, and its results come from the one client the
harness drives.

### D-63 A closed session ends the process cleanly

When the client closes stdin, the server exits with status 0.

The go-sdk reports a closed session as a JSON-RPC error with code
-32004, with the EOF only in its message text
([conn.go](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/internal/jsonrpc2/conn.go)).
So `errors.Is(err, io.EOF)` does not match it. A server that relies on
that match exits non-zero on an ordinary disconnect, and the host logs a
crash. The server matches the code through `jsonrpc.Error`.

The smoke gate catches a regression. It runs the binary twice. One run
holds stdin open, reads the replies, and checks that stdout carried only
frames. The other closes stdin at once and checks only the exit code.
The go-sdk drops a reply in flight when stdin reaches EOF, so one run
cannot do both.

Rejected: `errors.Is(err, io.EOF)`.
Cost: the smoke test is two runs of the binary, since one run cannot
read the replies and catch the exit. No run kills the server during
initialization (#76).
### D-64 Coverage is floored per package over `internal/` and `cmd/`, from a derived list

`make cover` holds every package to a coverage floor. The package list
is derived from `go list` over both `internal/` and `cmd/`, and an
exemption is named with its reason.

A hand-written list stops covering a package the day somebody adds one,
and nothing reports it. A list derived from the tree covers a package
from its first commit. Measuring `internal/` alone would leave `login`,
`logout`, `status` and `doctor` outside the profile, with nothing to
report that they had no tests. `TestBothTreesAreUnderAFloor` asserts
that a package from each tree is under a floor.

`cmd/` has a lower floor of its own. What remains uncovered there opens
a browser, holds a token or serves stdio, and the live driver and the
smoke gate cover those against the real thing. The lower number is a
floor rather than an exemption, so coverage there can only rise.

Rejected: a hand-written package list.
Cost: `cmd/` sits under a lower floor of its own until login and serve
can be driven in-process.

### D-65 A rule is a test, its list derived from the code, and its checker asserts a floor on what it read

A rule adopted here is a test or a gate. The list it checks is derived
from the code rather than typed out. The checker asserts a floor on how
much it read.

A rule written only in a document stops being true and nobody notices. A
pin that is a range is that failure. So is a security page promising
logs carry nothing while the code logs part of an id, or an error
vocabulary documented as closed while the code emits classes it never
names. Not one is caught by reading, and each is caught by a check that
can fail.

The floor is for the case reading cannot tell apart. A guard that has
caught nothing is working. A guard that looked at nothing reports the
same and is not. So `make classes` (D-44), the coverage floor (D-64),
the leak scan (D-09), `TestNoToolCanWriteTheServersSettings` (D-40) and
`live-cover` (D-61) each fail when they read too little.

Rejected: a rule kept only in a document.
Cost: each rule costs a test and a floor to maintain, and a rule nobody
can phrase as a check stays with review.

### D-66 Everything the repository runs on itself is Go

Everything the repository runs on itself is Go: the gates, the live
driver, the probes, the evals and the bundle packer, all under
`scripts/`.

A contributor needs one toolchain. The code that holds the gates shut is
built, vetted, linted and tested like the rest, so a gate's own bug
fails a test rather than a release.

The Claude Desktop bundle is packed here, by `scripts/gates mcpb-pack`,
with Go's own `archive/zip` rather than the official Node CLI. An
interpreter that `make check` needs is a prerequisite nobody declared,
and the CLI's schema validation is not the check worth having (D-71).

Rejected: the official Node CLI to pack the bundle.
Cost: checks another ecosystem ships ready-made, such as validating a
bundle manifest, are written and kept current here in Go.

### D-67 `make check` and CI run the same set

`make check` and CI run the same checks. `scripts/gates parity` compares
the Makefile's `check` target with `.github/workflows/ci.yml` and fails
when either has a check the other lacks. The gate list is read from the
`scripts/gates` dispatcher, so a gate is covered from its first commit.
A gate that runs elsewhere on purpose, such as at release time or only
on a pull request, is listed with the reason.

A contributor runs `make check` believing it is what CI runs. If the two
differ, a change that passes at home fails CI, or worse, a check CI
skips passes nowhere. A comment in the Makefile saying they match is not
a check.

The two are the Makefile's `check` target and CI's job list, and no doc
repeats either.

Rejected: a Makefile comment claiming the two match.
Cost: a check goes into both, or is listed in `scripts/gates/parity.go`
with the reason it runs elsewhere.

### D-68 CI runs every gate on Linux, macOS and Windows under a pinned bash

Every workflow sets `shell: bash` in its workflow-level `defaults`, and
every gate runs on Linux, macOS and Windows.

The runner hands every `run` block to a shell, and on Windows the
default is PowerShell, which reads `-coverprofile=cov.out` as a file
named `cov` and lets the suite carry on. A job-level default fixes the
jobs that exist and leaves the next one bare. An explicit `bash` also
runs with `-eo pipefail`, so a failing command in a pipe fails the step
([workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#defaultsrunshell)).
`scripts/gates pins` requires the workflow-level default in every file.

No gate is conditioned on `runner.os`. A platform in the matrix that no
gate reads is a platform nobody tests, while the matrix says it is
tested. `cancel-in-progress` spares `main`, so a merged commit keeps a
whole run. `.gitattributes` pins LF line endings, so goldens compare
byte for byte.

Rejected: a gate conditioned on `runner.os`; a job-level shell default.
Cost: every gate runs on three platforms for every push, and a gate that
leans on a Unix tool has to work on Windows too.

### D-69 Every action is pinned by commit SHA and every tool by exact version

Every action is pinned to a full commit SHA, with its version in a
trailing comment. A tag can be moved or deleted by whoever gains access
to the action's repository, and GitHub's own reference says to "pin
references to actions to a commit SHA"
([secure use](https://docs.github.com/en/actions/reference/security/secure-use)).

Every tool an action installs is pinned beside it by exact version,
`cosign-release`, `syft-version` and the GoReleaser version among them,
with a comment saying which half of the pin is load-bearing. The tools
the Makefile runs go through `go run <module>@<version>`. Never a `~>`
range: `goreleaser-action`'s own default is `~> v2`, and narrowing it to
`~> v2.18.0` is still a range, so the tool that decides what the
artifacts are would float. Every workflow starts at
`permissions: contents: read` and raises it per job, and checkouts set
`persist-credentials: false`.

`scripts/gates pins` asserts all of it, because a comment cannot hold a
version shut.

Rejected: a `~>` range, which floats across a release line.
Cost: every bump changes a SHA and its version together. A deployer's
policy requiring SHAs is stronger, and the gate stays because the
repository cannot assume one.

### D-70 A release is built from a tag, reproducible, signed and attested

A release is built by GoReleaser from a `v*` tag, for Linux, macOS and
Windows on amd64 and arm64. `checksums.txt` is signed keyless through
Sigstore, every archive gets an SBOM, and the build gets a provenance
attestation. `-trimpath` and `mod_timestamp` make a rebuilt tag byte for
byte the same. A release is never a draft. The steps are in
[release.md](release.md).

The reason is that a binary holding someone's Google token should be
checkable: its source tag, its hash and who built it. Reproducible
builds let anyone check the first two, and the signature and the
attestation the rest.

The Claude Desktop bundle is named in `checksum.extra_files` and
`release.extra_files`. A file a hook drops into `dist/` is not one of
GoReleaser's own artifacts, so without that it would ship outside
`checksums.txt`, and the signature is over that file. `release-tag`
holds the tag's major version to the module path's, `release-notes`
takes the release note from the CHANGELOG (D-74), and `registry-publish`
prints the MCP registry entry from the published checksums. `go install`
is the other path, with the version read from the build info.

Rejected: a bundle outside `checksums.txt`, which ships unsigned.
Cost: tags are pushed one at a time, since GitHub creates no event for
tags when more than three are pushed at once.

### D-71 The bundle manifest is held to the staged tree and a pinned schema

`scripts/gates mcpb` holds `packaging/mcpb/manifest.json` to the tree
the packer stages rather than to a schema alone. Every command and the
entry point name a program the packer stages, never `LICENSE` or
`README.md`. Every command starts with `${__dirname}/`. The entry point
is the file the default command runs. Every claimed platform runs the
file staged for it, and every `${user_config.x}` a value spends is
declared.

A manifest naming a binary nobody staged is well formed by any schema,
and makes a bundle that installs and then does nothing. Holding it to
the staged tree catches that.

The gate, the packer and the registry entry read the manifest through
one function, which refuses a key repeated in one object, or two keys
equal but for case. Go's decoder merges those, while the validator and
the host keep each. The committed manifest carries `0.0.0-dev`, and the
real version goes in through a JSON decode and encode. `$schema` is
pinned at a release tag and agrees with `manifest_version`, which has a
floor. The vendored schema is held to its recorded digest. A failed pack
leaves nothing under the release's name.

Rejected: the packer's schema validation alone; `$schema` at a branch or
a partial tag; a version substituted over text.
Cost: the manifest's rules are this repository's own code, kept current
by hand when the bundle format changes.

### D-72 The bundle asks for the settings a person sets elsewhere, each defaulting as the server does

The bundle asks for what a person sets in another client's
configuration: the OAuth client file, the profile, read-only mode, the
destructive switch, Connected Sheets and external formulas. Each key is
its variable's name without `GSHEETS_`, lowercased. The keys come from
`config.Vars` (D-56), and every variable left out, such as the log level
or the budgets, carries its reason in the `mcpb` gate.

Every setting reaches the server through `mcp_config.env`. `args` is
empty, and a platform override carries no `env` or `args`: an override's
replaces the base's, so the settings would not reach that platform
([config.ts](https://github.com/anthropics/mcpb/blob/v2.1.2/src/shared/config.ts)).
Each switch defaults to the server's own default, off, and the profile
and client file to `""`, which the server reads as unset. An optional
key with no default would reach the server as the literal
`${user_config.x}`.

Claude Desktop lets a person set a tool to always allow, so a switch
left off is the guard a bundle install keeps.

Rejected: a `tools` list, which is display only and wrong for most
installs, since settings change what registers; settings in `args` or an
override's `env`; an optional key with no default.
Cost: nothing holds the fields' sentences about read-only mode to what
the server registers (#82).
### D-73 `main` is released code, and work reaches it through a pull request the maintainer merges

`main` is released code. Nothing is pushed to it directly, release
commits included. Work goes on a short topic branch, and the maintainer
pushes it, opens the pull request and merges it once CI is green on
every platform.

The reason is that every commit on `main` has passed every gate on
Linux, macOS and Windows, so a release can be cut from any of them. A
direct push skips the one run that checks all three. The release steps
in [release.md](release.md) go through a pull request too, so a release
commit is checked like any other.

A contributor's change follows the same path. `CONTRIBUTING.md` says how
to open one, and what `make check` must say first.

Rejected: pushing to `main`, release commits included.
Cost: every change, a one-line fix or a release commit alike, waits for
a pull request and a green run on every platform.

### D-74 The CHANGELOG section is the release note

`CHANGELOG.md` follows Keep a Changelog. The release workflow takes the
version's section verbatim as the release note, through
`scripts/gates release-notes`, which takes only its own version's
section and refuses an empty one.

One text serves the person reading the release page and the person
reading the repository, and it is written while the change is fresh. A
note written at release time drifts from the log, and one generated from
commit subjects is written for contributors rather than for the people
who install the server.

The staleness gate asks for an entry under `[Unreleased]` whenever Go
code changed. `CONTRIBUTING.md` holds the rules for an entry, which are
not optional since the section ships verbatim.

Rejected: a changelog generated from commit subjects; release notes
written by hand at release time.
Cost: the staleness gate asks for an entry whenever Go changed, a change
no user sees included.

### D-75 Own wire types over raw REST, and no generated client

`internal/gsheets` holds hand-written wire types for the fields this
server reads and sends, and `internal/plan` builds the request union
members as typed values. `google.golang.org/api` is not a dependency.

The generated client brings gRPC, OpenTelemetry and the cloud auth stack
into a binary that needs JSON over HTTP. A struct field here means some
code reads it, so a field nobody needs is not fetched, decoded or held.
Requests are typed, so a misspelled field is a compile error rather than
a request Google rejects.

depguard refuses the import. Its rule carries its own message, so a
settings block that fails to load shows up as depguard's default
message. `scripts/gates api-fields` holds the wire types to the fields
Google publishes, with a verdict for each one left out (D-76).

Rejected: `google.golang.org/api`, which brings gRPC, OpenTelemetry and
the cloud auth stack; requests as free-form maps.
Cost: the structs cover only the fields read, written by hand, and a
field Google adds is absent until somebody adds it here.

### D-76 Every published method, union request and field has a written verdict

`testdata/api-surface.json` is what Google publishes, written by
`scripts/gates api-diff` from the discovery documents. Nobody edits it.
`testdata/api-coverage.tsv` holds one verdict per published method and
per `batchUpdate` request kind, written by hand.

`scripts/gates api-coverage` runs offline in `make check` and holds the
two files and the code to each other. A published item with no verdict
fails. A verdict for something Google does not publish fails. A request
some builder constructs while the record calls it out fails, and so does
a `used` verdict no code backs. `scripts/gates api-fields` does the same
for fields, through `testdata/api-fields.tsv`.

So a capability Google adds stops the build, rather than leaving a
paragraph to go stale. Each verdict says why. A saved filter view
changes how the spreadsheet looks to everyone who opens it. A second way
to do something already offered is worse than the first. A spreadsheet's
locale is not the model's to change (D-79).

Rejected: a paragraph listing what is out.
Cost: `api-diff` needs the network, so it runs by hand, and the snapshot
is as current as its last run.

### D-77 The server confirms a write by asking the person through elicitation

The server could confirm writes itself. Before each delete, and before a
data source is connected or refreshed, it would put a question to the
person through MCP form elicitation and write only on an accept. It is
declined.

The specification puts consent to each tool call on the host, and lets a
server ask through elicitation without making the answer consent: a
client gathers it "from the user or other sources"
([elicitation](https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation)).
Most widely used servers confirm nothing themselves. Clients answered
such a question with nobody there, did not draw it, or left it hanging,
and no client's allow and ask rules match it. A question on every call
is answered without reading. A control that holds on some clients and
not others is not a control (D-37).

Rejected: confirming writes through elicitation; a setting that refuses
the asking writes on a client that cannot ask.
Cost: a client running with nobody watching makes every registered
write, so `GSHEETS_ENABLE_DESTRUCTIVE` and `GSHEETS_ENABLE_DATA_SOURCES`
stay off for one. A configuration that still turns on the removed
`GSHEETS_REQUIRE_PROMPT` is refused at start.

### D-78 Changes arrive by a watch or push channel

The server could tell the model when a spreadsheet changes, through a
Drive watch channel or a subscription to a resource.

It is declined. A push channel delivers its notifications to a public
HTTPS endpoint
([push notifications](https://developers.google.com/workspace/drive/api/guides/push)),
and a stdio server on a person's machine has none. There is nothing to
poll instead, since the Sheets API has no feed of changes. So a read is
the way to see the spreadsheet as it stands, and a checkpoint is the way
to notice a change before a write (D-26). The resources offer no
subscriptions for the same reason (D-58).

Rejected: a push channel, which needs a public HTTPS endpoint; polling,
since the API has no changes feed.
Cost: a change somebody else makes is seen only on the next read, and a
write learns of it through its checkpoint.

### D-79 A tool changes the spreadsheet's title, locale or recalculation setting

`updateSpreadsheetProperties` renames a spreadsheet and changes its
locale and its recalculation setting
([UpdateSpreadsheetPropertiesRequest](https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets/request#UpdateSpreadsheetPropertiesRequest)).
A tool could send it. None does.

It is declined. Renaming a file is Drive's (D-02). The locale decides
how every number in the spreadsheet is parsed and displayed, so changing
it under somebody rewrites what the whole file means, and no result
could summarize that. The recalculation setting decides when every
volatile formula runs, for everyone who opens the file.

`create_spreadsheet` takes `locale` and `time_zone`, when nothing yet
depends on them. The card reports the locale, the time zone and the
recalculation setting, so a caller can read what applies.

Rejected: renaming is Drive's, and a locale change rewrites what every
number means with no result to summarize it.
Cost: a person changes a spreadsheet's title, locale or recalculation
setting in Sheets itself, and `testdata/api-coverage.tsv` records the
verdict.

Next ID: D-80
