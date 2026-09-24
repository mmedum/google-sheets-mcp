# Changelog

The format is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [semantic versioning](https://semver.org).

## [Unreleased]

### Added

- `make mcpb` and the registry gate validate their documents against the
  schemas those documents cite, rather than only checking that the
  `$schema` line is present, pinned and agreeing with the version beside
  it. Those are claims about the REFERENCE; a document can cite exactly
  the right file and not satisfy it. The registry entry is the expensive
  direction — a rejected publish costs a dispatch against a tag that
  already shipped, and an entry that is accepted and wrong cannot be
  withdrawn — so the refusal sits on the path that builds it, and the
  manifest check sits where the packer runs it too.

  The schemas are vendored under `scripts/gates/schemas`, embedded so the
  gate needs neither the network nor a particular working directory, and
  each is pinned by a recorded SHA-256: without that, "make the document
  pass" and "edit the schema" are the same amount of work.

  This schema constrains less than it appears to — `version` has no
  pattern and a package's `registryType` no enum — so it is a floor, and
  the registry gate's own rules hold what it leaves open.
- `make schema-refetch`, which is what a vendored copy cannot do for
  itself: a digest proves the bytes are the ones somebody reviewed, not
  that upstream still serves them. It fetches each source, reports a
  difference and refuses, and never rewrites anything, because a refresh
  is a decision somebody makes after reading what changed.

## [1.5.1] - 2026-09-18

### Fixed

- The provenance attestation covers the `.mcpb` bundle. It named the
  archives and `checksums.txt` and not the bundle, so
  `gh attestation verify` on the bundle answered 404 while every archive
  passed — the artifact most people install was the one without an
  attestation of its own. It was covered only through its row in
  `checksums.txt`, which is a claim about the checksum file.

## [1.5.0] - 2026-09-18

### Fixed

- The registry entry is not built from an unverified checksum file.
  `publish-mcp.yml` downloaded the published `checksums.txt` and fed it
  straight to the gate that writes the entry — and the entry's
  `fileSha256` comes out of that file, which is the number a
  registry-driven client checks its download against. The only
  `cosign verify-blob` in the job ran against the `mcp-publisher`
  tarball.

  Somebody able to replace a release asset could edit `checksums.txt`
  beside it. The signature and the attestation would both break, which is
  the detection this pipeline exists for, and neither was consulted on
  that path — while the dispatch route exists to re-publish for a tag
  that shipped weeks ago, and a registry entry cannot be withdrawn. The
  signature is verified before anything reads the file, with the
  certificate identity pinned to this repository's `release.yml` at the
  exact tag.

### Added

- This server publishes its entry to the MCP registry. `gates
  registry-publish` builds it from the release's **own `checksums.txt`**,
  so the hash describes the bytes that were published rather than a
  rebuild of them — and that hash is why this is not goreleaser's `mcp`
  block, whose package entry has nowhere to put one while clients verify
  the bundle before installing it.

  It is its own workflow, with `id-token: write` and `contents: read` and
  nothing else, because `mcp-publisher` is a third-party binary handed a
  token that can publish under this namespace. The binary is verified
  with cosign against the registry project's own release workflow before
  it is unpacked — a version pins which artifact to fetch, not that the
  bytes are the ones upstream built. A prerelease tag skips the step: an
  entry cannot be taken back.

### Changed

- The bundle manifest's `$schema` names a release tag rather than `main`,
  and `make mcpb` holds the whole URL rather than refusing three branch
  names. The version in the path pins the FORMAT; the ref pins the BYTES,
  so upstream amending that file in place changes what this document
  validates against with nothing looking different. The check is an
  allow-list — upstream's published path at a full release tag or a
  commit SHA — because refusing `main` passes a branch called anything
  else, a partial tag like `v2.1` that upstream re-points as it releases,
  and the right filename served by somebody who is not upstream.
- `make mcpb` also puts a floor under `manifest_version`. Every other
  claim it makes holds the manifest against ITSELF, and a stale manifest
  is perfectly self-consistent: 0.2 beside a 0.2 schema passed all of
  them, which is how that shape spread between repositories in the first
  place. Checked against the published schemas rather than against what
  other repositories do — 0.2, 0.3 and 0.4 are served and 0.5 is not, and
  0.4's only change is a `uv` value in the `server.type` enum, which a
  `binary` server gains nothing from.
- The bundle manifest's `description` is the short one it is meant to
  be. It was 148 characters, which is both wrong for MCPB — where the
  detail belongs in `long_description`, and already did — and over the
  registry schema's 100-character limit for a required field.

### Added
- The `pins` gate classifies every action, and an unknown one fails it.
  The version keys it already had each judge a version that is *written*,
  and the "every key must match something" check catches a key naming
  nothing — neither can see a tool named by no key at all. An action that
  installs a tool and sets no version input is an absence.

  The Pipedrive server's release published nothing on exactly that shape:
  `sigstore/cosign-installer` pinned by SHA with no `cosign-release`, so
  the job installed whatever cosign was newest, and that cosign had
  changed its default signing format. `anchore/sbom-action/download-syft`
  had the same hole one step below it. **A SHA pins the wrapper, not the
  tool.**

  This repository pins both and was never affected, but nothing held
  that. Every action is now in one of two tables — the installers with
  the input that pins each one's tool, and the actions that install
  nothing with the reason — and an action in neither fails the gate,
  because being unclassified is the state that let the other two through.

  Watched failing on all three shapes before being trusted.

## [1.4.0] - 2026-09-15

### Added
- `status --json` prints the same state as one JSON object on stdout, so
  a script can read whether this server is configured instead of parsing
  output written for a person. `credentials.configured` is the field to
  branch on; `schema_version` changes only when a field is removed or its
  meaning changes.

  The design is an outside contributor's, from the Drive server where it
  landed first, and the reason is drift this family caused: four servers
  printing four shapes for the same state, and a label that moves under a
  release taking a caller's check with it, silently.

  `not configured` is a state the object reports rather than a truncation
  — every key is present either way, because a caller cannot tell a short
  object from a failed parse. The account and the client-secret path stay
  masked, and the masking happens at the collector: the JSON encoder
  writes straight to the stream and passes through nothing that redacts.

  One collector, two renderers. The text output is byte-identical to what
  the released binary prints, asserted by diffing them.

## [1.3.4] - 2026-09-14

### Added
- `forbidigo` holds the rule that stdout carries only MCP JSON-RPC
  frames. That rule is in this repository's CLAUDE.md and in the MCP
  specification — the stdio transport says the server **MUST NOT** write
  anything to stdout that is not a valid MCP message, and **MAY** log to
  stderr — and until now nothing enforced it. Verified by injection
  rather than by reading: a `fmt.Println` added to `internal/service/`
  passed the entire `make check`, in all four servers. A stray print
  there corrupts the JSON-RPC stream, and the failure a person sees is a
  client that silently stops working.

  It is configuration rather than a new gate, because golangci-lint
  already runs here and `forbidigo` already does this job. Two patterns,
  `^fmt\.Print.*$` and `^os\.Stdout$`, with `analyze-types: true` so
  that an aliased import still matches and so that stdout is caught as a
  *destination*: after the writers were threaded through there are far
  fewer `fmt.Print*` calls and many `fmt.Fprint*`, and
  `fmt.Fprintf(os.Stdout, …)` corrupts the stream identically. Both
  spellings are held, and both were injected and watched to fail.

  The process's streams are now named in exactly one place — `main`,
  which carries the single `//nolint:forbidigo` and a sentence saying
  why. `scripts/` is excluded by path: it is maintainer tooling run at a
  terminal, not the server.

  A hand-written gate was drafted first and thrown away. It would have
  needed a path constant, which is how the existing print check came to
  read one file (`const file = "main.go"`) out of a whole server, and a
  floor on files read so it could not pass by reading nothing. Neither
  problem exists in a linter that is handed the package list.

### Changed
- Release notes are published one heading level up. In `CHANGELOG.md` a
  version is an `##` and its change kinds are `###` underneath it; on the
  release page the version heading is gone, because GitHub renders the
  tag name as the page's `h1`. Published unaltered, the notes therefore
  started at `h3` directly under an `h1` — a skipped rank, which the
  W3C's heading guidance says to avoid. Confirmed by reading the rendered
  page rather than guessing: `h1 v2.0.4`, then `h3 Added`.

  So the section's headings are lifted one level on the way out, and the
  page reads `h1` then `h2` with nothing missing between. No wrapper
  heading was added: "Changelog" restates what the page obviously is, and
  repeating the version duplicates what GitHub already prints above it.
  Fenced code is left alone, since a `#` comment in a shell block is not
  a heading, and only `h3` and deeper are lifted, so a second `h1` can
  never be emitted.

## [1.3.3] - 2026-09-13

### Added
- The release page carries the release notes. `gates release-notes`
  prints the `CHANGELOG.md` section for the tag and `release.yml` passes
  it to goreleaser with `--release-notes`, replacing a generated list of
  full commit SHAs that included the release commit itself. A tag whose
  section is missing or empty fails the release rather than publishing
  one that says nothing. The command comes from a sibling server rather
  than being written again.

  **Never write `changelog: disable: true` to suppress the generated
  list.** It is evaluated in the changelog pipe's `Skip`, which runs
  before `Run`, so `ctx.ReleaseNotes` is never assigned and the notes
  file is never opened: the body collapses to the footer alone. A sibling
  shipped exactly that. `release.footer` is untouched and still applies —
  `internal/pipe/release/body.go` renders `Header`, `ReleaseNotes`,
  `Footer` on every path. Read out of goreleaser v2.18.1.

### Changed
- The README follows the skeleton now shared by the four servers, checked
  against GitHub's own README guidance, the community profile checklist
  and the standard-readme spec: an opening line under 120 characters, a
  `Why google-sheets-mcp` section saying why the guarded writes and the
  A1 arithmetic are the point, `Getting help`, a `Documentation` section
  listing the five files under `docs/`, and a `Contributing` section,
  which the spec requires and which had been a sentence at the end of
  `Development`. `Connect your client` is `Connect a client`, `What keeps
  you safe` is `Safety`, and `Resources` becomes a subsection of `Tools`,
  where the sibling servers keep it.
- The README no longer opens with `> **Status: v1.3.2.**`. The version
  was a copy of a fact the release badge already carries and cannot get
  wrong; what the line said beyond the version — what works, that the
  twenty-one tools are stable, that one MCP client has driven it — moved
  into `Why`. The staleness gate needed no change: it skips a document
  with no status line, and its own comment already said the way out is to
  name the phase rather than a version. `docs/architecture.md` still
  carries one and is still checked.
- `golang.org/x/oauth2` is at v0.37.0 and `golang.org/x/time` at v0.16.0,
  what dependabot proposed. oauth2 is not an ordinary dependency here —
  it is the token refresh — so `doctor` was run against a real account
  and the refresh token exchange succeeded, which is the path no unit
  test reaches. It had landed with no changelog entry at all, which
  mattered less when the release page was a list of commits and matters
  now that the page is this file.
- `gates coverage` and `gates mcpb-pack` parse their arguments in
  functions of their own. The dispatch switch was exactly at the
  cyclomatic limit the linter enforces, so adding `release-notes` broke
  the build in `main` rather than in the case that added it — every case
  is one statement now, and the next command will not have to pay for it.

## [1.3.2] - 2026-09-13

### Fixed
- `--version` reports one spelling whichever way the binary was built.
  goreleaser stamps its own `{{.Version}}`, which has the leading `v`
  stripped, so a release archive said `1.1.0`; `go install` stamps
  nothing and the fallback reads `v1.1.0` out of the build info. The same
  release therefore reported two different strings depending on how
  somebody installed it, and anything parsing `--version` got a different
  answer per install method. Reported from outside by a reader comparing
  five servers side by side.
  The release stamp now carries the tag itself rather than goreleaser's
  v-stripped form, so the two sources agree at the source; the
  normalisation stays for a version passed by hand to `make`.
- `status` prints the same lines, in the same order, with the same
  labels as the three sibling servers, once a profile is configured (the
  not-yet-signed-in message still differs between them). They had drifted into four shapes
  — a version banner in three of them, `client secret` against
  `client json`, `read-only` against `read only`, four label widths — and
  the same reader found that too.
- `status` reports the account the same way in all four: the local part
  removed, the domain kept. The domain is the half a diagnosis uses —
  shared drives are a Workspace feature and a personal account cannot
  create one, so `@gmail.com` and a Workspace domain are two different
  sets of behaviour to explain — while the local part answers nothing.
  It is never an input to any command here, and this output is what the
  issue form asks people to paste. One server showed it in full, one
  masked the domain as well (which hid the useful half), and two sat in
  between.
- `--help` is an answer, not an error. It exited 1 and wrote the usage to
  stderr, where the three sibling servers exit 0 and write to stdout,
  which breaks `google-sheets-mcp --help | less` and any script that
  reads the exit code. An unknown flag is still an error, and still says
  so on stderr.
- A permission failure no longer repeats the account it refused. Google
  names the account in the message of a 403, and that message was
  repeated verbatim into the error string — as was the whole response
  body when it was not an error envelope at all. That string reaches
  stderr, and the MCP stdio transport says a server may write logging
  there and clients "MAY capture, forward, or ignore" it, while the
  protocol's logging section says log messages MUST NOT carry personal
  identifying information. The local part is now masked where Google's
  text is parsed — one place, rather than at each print, so a print added
  later is safe without its author knowing the rule, and a writer wrapper
  could split an address across two Write calls and miss it. The domain
  is kept, because it is what says which account was refused.

## [1.3.1] - 2026-09-13

### Added
- The API-fields gate judges every struct, not only the ones whose name a
  schema happens to share. It compared the name matches and skipped the
  rest in silence, with a floor of 20 under the number matched standing in
  for a check — which against a real 128 left a hundred renames of headroom. A repo-wide
  rename of a modelled struct took its properties out of the comparison
  and the gate still printed ok. It now runs a third direction over the
  wire package: every struct carrying a JSON tag must match a published
  schema, be named by an `alias` row, or carry a new `local` row saying it
  models none. The floor is gone rather than raised, because the rename
  now fails on the renamed type itself. Twenty-five structs were invisible; they are accounted for now, and 21 more schemas are compared (128 to 149).
  A rejected row no longer counts as a decision either: an invalid `out`
  row used to excuse the very field it named.

- **An API-fields gate.** `make api-fields` is the coverage gate one
  level down: `testdata/api-fields.json` is every schema and property the
  Sheets and Drive discovery documents publish, `testdata/api-fields.tsv`
  is one hand-written row per exception, and the modelled side is read
  out of `internal/gsheets` with `go/ast`. Both directions fail, and the
  number of schemas matched is part of the rule, because a gate that
  matches a struct to a schema by name goes blind the moment somebody
  renames a struct.

  The Sheets API is far larger than this server's surface and §1 says so;
  what the gate adds is that the difference is now a decision per field
  rather than a sentence. 152 properties are written off under eleven
  headings — Connected Sheets, filter views, developer metadata,
  dimension groups, the chart kinds `chartKind` already names in its
  refusals, chart painting, the flat `Color` Sheets deprecated in favour
  of `ColorStyle` — each with a reason, and every one checked against
  `internal/plan`, `internal/service` and `internal/grid` first, under
  the rule that a field a tool writes must be a field the types carry.
  None of the 152 is written. A field Google adds to a type this server
  models now fails the build until somebody says which heading it joins.

### Changed
- **Compact JSON on every request.** Google indents its JSON unless told
  otherwise, and `prettyPrint` is a system parameter of every Google API
  rather than a Sheets feature, so this client now asks for it once in
  the one place that builds an HTTP request instead of at each place that
  builds a query — a call added later and given no thought gets it too.
  A `read_range` over a large grid is the call that pays for the
  indentation, and it is the call this server exists to make: on a
  sibling server the same change took a large response from 7.44 MB to
  2.96 MB. Set after the host allowlist check, which is what makes
  rewriting the URL safe: every request reaching that point is one this
  client has already decided it may send a credential to. A query that
  names `prettyPrint` itself is left alone.

### Fixed
- **`Reply` was being read as Drive's, not Sheets'.** Sheets calls a
  batchUpdate reply a `Response`; this server's struct is called `Reply`,
  which is the name Drive gives a comment reply. Nothing was broken at
  runtime — the struct decodes the bytes Sheets sends either way — but no
  check could tell the two apart, and the first run of the fields gate
  reported all eighteen of `Reply`'s members as fields Drive does not
  publish. An `alias` row now says the struct models `sheets Response`
  and an `unrelated` row says this server has no comment tools, so the
  gate compares each against the right thing.

## [1.3.0] - 2026-09-10

### Fixed

- **`login` recorded no account, on every login.** The address is
  fetched from the Drive call this server already makes, and then
  `tokeninfo` was read back over it. `tokeninfo` returns an address only
  for a token carrying an email scope, which §17.6 asks for and this
  server does not, so what it returned was nothing — and nothing was
  what the profile kept. `status` printed `account: (none)` while
  `doctor`, asking Drive directly, resolved the address on the same
  credentials. The two disagreed about an authenticated profile, which
  is the one question that output exists to answer (§18).

- **An address Drive withholds erased the one already recorded.**
  `User.emailAddress` is absent when the account has not made it visible
  to the requester, and that arrives as an empty string with no error.
  It is no longer stored over a good address from an earlier login.

### Changed

- **Runs of empty rows are folded into one line.** A read of a sparse
  window drew every empty cell padded to its column's width: on one
  36-column read, a single empty row cost 562 characters, 523 of them
  spaces. Three or more consecutive empty rows now render as
  `… rows 21-24 empty`. The rows are named, so an address inside the
  fold is still one a caller can write to, and the footer still says
  where the data ends. `read_formatting` has always summarised a repeat
  this way; the grid did not.

- **The footer names the cells it shortened.** `13 value(s) shortened to
  50 characters` left the caller guessing which cell to read again. The
  first few addresses are named, with the count still the total.

- **`status` separates a missing account from a missing login.**
  `(none)` said both. A profile with a token and no address recorded
  says `(not recorded)`.

## [1.2.0] - 2026-09-09

### Fixed

- **`clear_values` took a whole pivot table and said it took one cell.**
  Clearing the cell a pivot is anchored at returns 200 from Google with
  `clearedRange` naming that one cell, and the pivot's definition and
  every cell of its output are gone with it. `values.clear` is not the
  documented way to delete a pivot table and nothing in the reply says
  it did. The confirm gate names the anchor now and says the whole table
  and everything it draws goes, none of which is in the count — a caller
  who read "removes 1 cell" and confirmed had agreed to something else
  entirely. The sixth silent destroy this project has found, and the
  first in a tool that had already shipped (§17a.31, §18).

- **A clear counted cells it cannot remove.** `values.clear` over a cell
  that something else draws — a pivot's output, an array formula's spill
  — returns 200, names the range and changes nothing. Those cells were
  counted into "removes 4 non-empty cell(s)", which named a loss that
  does not happen. The count is what a clear can actually take now, and
  the rest is described without promising it survives: a clear does not
  remove such a cell itself, and takes it anyway when whatever draws it
  was in the range too, which is what happens to an array formula's
  whole spill.

  Everything a clear costs is said on all three of the confirm gate, the
  dry run and the result. It used to be on the gate alone, whose own
  closing words send the caller to `dry_run`.

### Changed

- **`format_cells merge` over a pivot table is refused with the reason
  Sheets gives.** It used to be refused as "merging would keep the
  top-left value and discard F3, which is not empty" — describing a loss
  that cannot happen — and offered `overwrite` to get past itself, which
  reached a `400 You can't merge cells that are part of a pivot table`
  from Google instead. Sheets refuses such a merge outright, over the
  output as surely as over the anchor, so the refusal now says that and
  nothing acknowledges it. Every pivot the merge runs into is named, not
  the first: "merge cells outside it" pointing at cells inside a second
  table is advice that gets the caller refused again.

  Google goes by the pivot's whole footprint, so a merge over cells that
  are blank inside it is refused too. The guard cannot see that without
  measuring every pivot before every merge, so Google's own 400 is
  translated where the guard cannot answer — a caller never reads the
  raw wording, and no merge pays for a read it did not need.

## [1.1.0] - 2026-09-09

### Changed

- **A write into a pivot table's output names the pivot table.** It used
  to be refused as "I3 is not empty", which is true of every occupied
  cell on the sheet and says nothing about what the write would break.
  It now reads "I3 is inside the output of the pivot table anchored at
  H1, which covers H1:I6 as it stands", followed by what a write there
  does — the whole pivot stops drawing and collapses to `#REF!` at the
  anchor — and how to undo it, which is to clear the cell again.

  A pivot's output cells carry nothing that names the pivot: on the wire
  they are ordinary computed values, and the definition sits on the
  anchor alone. So naming it costs a read up and to the left of the
  write, and that read happens only where the guard is already refusing
  *and* something in the way is a cell nobody typed. A refusal over
  ordinary data costs exactly what it did before. The pivot's rectangle
  is measured rather than assumed, so a table that does not reach the
  write is not blamed for it (§17a.27, §7.6).

  Verified live: 211 steps, none failed. The acknowledged write
  collapsed the pivot, and clearing the cell brought it back to the same
  rectangle and the same checkpoint.

### Fixed

- **`manage_pivot_table list` measured two adjacent pivot tables as
  one.** The walk that measures what a pivot draws stops at the first
  empty row and column, and two tables side by side have neither between
  them — so the left one's rectangle swallowed the right one. It is
  pulled back off any other anchor inside it now. Wrong in the listing
  since 0.4.0, and found because the refusal above quotes the same
  rectangle: a listing that overstates a footprint is misleading, and a
  refusal that does it names a table the write would not have touched
  and promises to break it.

## [1.0.0] - 2026-09-07

**A stable tool surface.** Twenty-one tools, their arguments and the
twelve error classes are what this server promises from here: a breaking
change to any of them needs a major version, and the schema diff gate is
what makes that a promise rather than an intention — it has compared
every commit against the last tag since phase 0.

What 1.0.0 does not claim is worth saying in the same breath. This
server has been driven by one MCP client. §16 waited on a further eval
round with a second one, and that condition was set aside rather than
met; Claude Desktop through the `.mcpb` bundle is the obvious next one,
and every eval number in this repository comes from the CLI harness.

### Added

- **The evals cover charts and pivot tables: 18 tasks, 18 passing.**
  Three new ones — charting a column, summarising with a pivot table,
  and a write aimed into a pivot table's output. The last is the phase 4
  half of what the formula task does for phase 1: the cell is inside
  something a values read makes look ordinary, and what is scored is
  whether the guard refused before anything landed. The fixture seeds
  the pivot table that task collides with, so what is measured is the
  guard rather than whether a model can build one.

### Fixed

- `.gitignore` covered `livesheet` and `gates` and not `evals` or
  `spikes`, which are the two binaries a `go build ./scripts/...` drops
  in the repository root. The leak gate found one there, which is what
  it is for — but the ignore rules are what stop it being committed by a
  wildcard add, and two of the four were missing (§17a.17).

## [0.4.0] - 2026-09-07

### Added

- **`manage_chart`: add, update, move, delete and list charts and
  slicers.** A chart floats above the grid, so adding one overwrites
  nothing, and its data is named in A1 — one range per series, no index
  arithmetic. An update reads the whole chart and sends it back with the
  change applied, because Google's `updateChartSpec` carries no field
  mask and replaces the spec outright: a rename built from this server's
  own struct would have dropped every field the struct does not model. A
  chart kind this server cannot build is still readable, movable,
  renamable and deletable, and says so rather than being rebuilt as
  something else.
- **`manage_pivot_table`: add, update, delete and list pivot tables.**
  Columns are named in A1 or by their heading, never by counting into
  the source. Every result reports the rectangle the table covers *right
  now*, read back after the write: a pivot's size is computed from the
  data and appears in no request and no reply, so a caller who writes
  beside where they think it ends writes into it.
- **`manage_data_source`: Connected Sheets, behind
  `GSHEETS_ENABLE_DATA_SOURCES`.** Connect a BigQuery query or table,
  refresh it, cancel a refresh, delete it, or list what is connected.
  The setting is also what makes `login` ask for `bigquery.readonly`,
  which Google requires for `addDataSource` and which this server does
  not ask for by default — a spreadsheet server whose consent screen
  asks for BigQuery is asking most people to grant access to a product
  they do not have. `get_spreadsheet` reports whether a spreadsheet has
  a data source whatever the setting, at no scope and no cost.

### Fixed

- **Deleting a charted column left the chart drawing nothing, silently.**
  The chart keeps its place, its title and its id, loses the series, and
  Google's reply says nothing at all — the third silent destroy this
  project has found on an API whose reference mentions none of them.
  `delete_dimensions` now names the charts a band would break before the
  confirm gate, with the count: "would lose 1 of its 2 series" where
  that is what happens, and "would be left with nothing to draw" only
  where it is true. The first version said the second for both, and one
  live run with a two-series chart is what showed it.
- **A write into a pivot table's anchor is refused with what it costs.**
  The guard already refused it — a pivot's output cells are non-empty —
  but the refusal said only "not empty", which tells a caller to pass
  `overwrite` and nothing about what `overwrite` would do. It now says
  the cell anchors a pivot table and that a write there clears
  everything it draws. A write into the middle of the output is still
  refused as an ordinary non-empty cell; §17a.27 has why, and its cost.
- **A stacked line chart is refused here rather than by Google.**
  `stackedType` applies to area, bar, column, combo and stepped area,
  and reaches Google as a 400 on anything else. The refusal now names
  the types that take it.
- **The schema dump was missing a tool, and every test was green.**
  `--dump-schemas` built the full tool surface by listing the
  registration gates by hand, so phase 4's new gate — and the tool
  behind it — was absent from the dump and therefore from the schema
  diff, which is the gate that exists to say a tool has appeared. The
  surface now comes from `tools.FullSurface`, beside the gates it has to
  saturate, and a test enumerates the gates and requires no combination
  to register something the full surface does not.

- **Deleting a data source is `delete_data_source`, gated and
  confirmed.** It removes the `DATA_SOURCE` sheet Google made for the
  source and everything on it — strictly more than `delete_sheet` does —
  and what goes cannot be read back without re-running a billed query.
  It was an action inside `manage_data_source`, whose annotations told
  clients it was not destructive and whose gate is a scope rather than a
  danger; with `GSHEETS_ENABLE_DESTRUCTIVE=false` it was still reachable.
  Split the way `edit_dimensions`' delete became `delete_dimensions`. It
  needs no BigQuery scope — spike N's transcript already said so, in a
  400 about the id where `addDataSource` had given a 403 about the scope
  — so it reaches a data source somebody else connected.
- **A pie chart could not be updated.** `manage_chart update` refused
  every chart that was not a `basicChart` with "this server can retitle,
  move and delete but not rebuild" — true for a treemap, false for the
  pie the same tool had just created. A pie now takes `legend`, `domain`
  and `series`, and refuses `stacked`, `axis_title`, `headers` and
  `chart_type` by name rather than blaming the chart kind.
- **`headers` and `axis_title` were read on `add` and dropped on
  `update`.** A call passing only one of them was told there was nothing
  to change.
- **An update's unqualified range resolved against the chart's own
  sheet**, which for a chart on its own sheet is a `sheetType: OBJECT`
  sheet with no cells at all. It resolves against the `sheet` argument
  where one is given, and refuses with the reason where it cannot.
- **`manage_chart list` compared its `sheet` argument with the title**,
  so the numeric sheet id its own description offers matched nothing,
  and a misspelled title came back "no charts" instead of a refusal
  naming the sheets that exist. It goes through the same lookup every
  other listing uses.
- **`manage_pivot_table add` replaced an existing pivot table silently.**
  On an occupied anchor an add is an `updateCells` like any other: it
  discards the definition and its whole output. It refuses now, and
  points at `update`.
- **A pivot's reported footprint swallowed its neighbours.** The
  rectangle was "the furthest computed cell anywhere below and right of
  the anchor", so a second pivot table, an `ARRAYFORMULA` spill or an
  imported range joined it — and the result hands that rectangle to the
  caller as cells a write would break. It stops at the first empty row
  and the first empty column.
- **A pivot built over a whole-sheet range resolved its columns one too
  far.** An absent `startColumnIndex` reads as column zero, which made
  every offset one too high *and* disabled the bounds check that exists
  because the API accepts an out-of-range offset with a 200.

- **An API coverage gate, so §16's completeness claim is checked rather
  than written down.** Every method the Sheets and Drive discovery
  documents publish, and every one of the 69 `batchUpdate` request
  kinds, is now either used — naming the code that implements it — or
  written off with a reason, in `testdata/api-coverage.tsv`. `gates
  api-coverage` runs offline in `make check` and holds that file, the
  generated `testdata/api-surface.json` and the code to each other, so a
  capability Google adds fails the build instead of aging quietly into a
  paragraph that used to be true. `gates api-diff` refetches and reports
  NEW, GONE and CHANGED with verb and path, and stays manual: a gate
  that fails when Google is slow is one people learn to re-run until it
  passes. Prompted by google-chat-mcp, which built one first and passed
  on the two mistakes it had made.

### Changed

- **`get_spreadsheet` names the charts on each sheet**, and counts
  slicers and conditional format rules beside the tables and merges it
  already counted. A count says a sheet has three charts; the names say
  which one you mean. About 70 bytes a chart, against the 2 KB a whole
  chart spec would cost.
- **A conditional format rule update costs two requests instead of
  three.** The count now comes from the card, which carries the rules'
  ranges — §17a.15, closed by measuring rather than guessing.
- **A pivot table's update and delete cost one read where they cost
  three**, and a listing costs two whatever it finds, where ten pivot
  tables used to cost eleven round trips. The API allows sixty reads a
  minute and one round trip is about a second, so this is the most
  expensive kind of waste there is here. A test holds the count.
- Three deferred cleanups are closed, each by a live probe rather than a
  decision (§17a.7, §17a.15, §17a.20): a `batchUpdate` reply can be
  masked, so a structural write can carry its own card back; the card
  can carry the conditional format rules' ranges; and a grid read can
  carry the anchors on the rows it reads, at about 180 bytes a row.

## [0.3.1] - 2026-09-07

### Fixed

- **The profile now records `account_email`, as the sibling servers do.**
  It was the one field this server's profile lacked, found by comparing
  the four config files rather than by anything failing. It is not taken
  from the token: `tokeninfo` returns an address only for a token
  carrying an email scope, and this server asks for neither `openid` nor
  `userinfo.email` — adding a third scope to record a string would be the
  wrong way to match a convention. It comes from the Drive call this
  server already makes, and is stored whole and printed masked.
- **The README told people to add "the two scopes below" and never listed
  them.** No scope URL appeared anywhere in the file, so a reader
  following setup step 4 had nothing to add — a dangling reference that
  every gate passed, because no gate compared the setup instructions with
  the code. Both are listed now, in full, with what each is for and what
  read-only mode asks for instead; and the staleness gate fails when the
  README omits a scope `login` requests. Prompted by an outside setup
  report against a sibling server, which found the same class of gap
  there.
- `login` with no `-secret` lands on `~/.config/google-sheets-mcp/client_secret.json`,
  which is the siblings' convention and was already this server's default.
  A path passed to `-secret` is still recorded and still wins.

## [0.3.0] - 2026-09-06

### Fixed

- **Windows could build the server and never run it.** `go build -o
  <name>` writes exactly `<name>` on every platform, and a file with no
  extension in `PATHEXT` cannot be executed on Windows at all — so the
  build produced a binary every gate could stat and none could exec. The
  first CI run on `windows-latest` said so in as many words. The build
  produces a `.exe` there now. The comment that reasoned its way to the
  wrong answer is worth more than the fix: it was right that `-o` writes
  the name given, and that had nothing to do with whether the result
  runs. The test beside it asserted the binary could be *stat'd*, which
  is the exact state that failed.

### Added


- **`gsheets://` resources.** `gsheets://{spreadsheet}` is the card;
  `gsheets://{spreadsheet}/{sheet}` is that sheet's used range as CSV
  under one 400 000-character budget. The used range, not the sheet: a
  new sheet is 1 000 by 26 whatever is on it, and 26 000 commas describe
  nothing. A read cut short says so in a second content block rather
  than in the CSV, because a comment is not a thing CSV has and a note in
  the body parses as a row of data.
- **`manage_anchor`, and `anchor:<name>` wherever a range is taken.** An
  A1 address goes stale the moment somebody inserts a row above it, and
  nothing says so. An anchor does not: it follows its row through
  inserts, deletes, moves and sorts. It is a prefix on the existing range
  argument rather than a new argument on every tool, so all seventeen
  tools accept one and none of them grew an eighteenth argument. That
  covers the `band` argument too, which takes a different path from a
  range: without its own hook, `anchor:` failed with an A1 syntax error
  on `edit_dimensions` and `delete_dimensions` — the two tools that
  destroy anchors, and the ones "delete the row anchored as totals" is
  actually about. Found by a review pass reading the promise against the
  code rather than by anything failing.
- **Spike K, and what it found.** §6.4 has assumed since the design that
  developer metadata is durable. It is, and the reference says none of
  it. An anchored row survives an insert above, a delete above, a
  `moveDimension` and a `sortRange` — and after a sort it lands where its
  row's *values* landed, which is the one behaviour a reading of the
  reference would have got backwards. Deleting the row deletes the
  anchor, and the reply says nothing at all: the same shape as phase 2's
  `deleteTable` finding, and the second one this project has found. So
  `delete_dimensions` now names the anchors it would take, before it
  takes them.
- Three more from the same probe, each one a refusal written here rather
  than a message forwarded. A metadata key is **not unique** — Google
  will hold two entries under one name, so uniqueness is this server's to
  keep or `anchor:totals` is a question with two answers. A location must
  be a single bounded row or column, and the API's refusal names a type
  the caller never mentioned. And a spreadsheet-level lookup cannot be
  intersecting, which rules out the obvious way to list everything; the
  way that works is an **empty lookup**, one request, which the reference
  does not describe.
- **A move now stores the note it reports.** `manage_anchor action=move`
  built its answer from the request and sent a field mask that wrote only
  the location, so a new note was reported and never stored — the result
  and the spreadsheet disagreeing, which hard rule 7 exists to prevent. A
  move with no note of its own now reports the note that is still there
  rather than an empty one, which was the same lie in the other
  direction.
- **`delete_sheet` names the anchors it takes**, like `delete_dimensions`.
  Deleting a sheet takes every anchor on it and the reply mentions none.
- **The leak scan reads the working tree, not just the index.** It read
  tracked files, so a phase's new files were invisible to it until they
  were staged — and those are precisely the ones nobody has scanned
  before. This phase ran `make check` green a dozen times over 167 files
  while 28 of its own were untracked; the first `git add -A` took it to
  195 and the gate immediately found a fixture spreadsheet id that did
  not declare itself synthetic. The gate was doing what it said; "make
  check is green" was the false claim. Refusing an untracked file also
  means a build artifact is caught before a wildcard add can sweep it in,
  which is how two sibling repositories put megabytes of compiled binary
  into their history this week. A tracked binary was already refused —
  the branch has been there since phase 0 — but only `isBinary` had a
  test and the branch that uses it had none, so what the scan did with a
  binary was answerable only by reading it. Three people on three
  repositories read it and got it wrong on the same day, one of them
  having just described it to another. Both paths are tested now, and
  watched failing.
- **The transcript gate could be walked past with `fmt.Fprintln(os.Stdout, …)`.**
  It matched `fmt.Print`, `Printf` and `Println`, justified by a comment
  saying an `Fprintf` goes to a caller-supplied writer — true of a
  caller's writer and not of `os.Stdout`. One such line would have put a
  sheet title on the terminal unredacted with the gate reporting nothing.
  It now matches the *mention* of a printer or of `os.Stdout` rather than
  the call, which also closes what the first fix had recorded as needing
  a type checker: `p := fmt.Println; p(x)` fails at the assignment, an
  aliased writer fails at the alias, and handing `os.Stdout` to a child
  process fails too. Prompted by a sibling repository finding the same
  shape in a gate of its own, and the mention-rather-than-call rule is
  theirs. A comment that argues for a gap is worse than one that leaves
  it unexplained: it buys the reader's agreement in advance, so the gate
  ends up protected by its own prose from the person most likely to
  notice.
- The stdio smoke gate now drives `resources/templates/list` and
  `resources/read` as well as the tools. A tool and a resource are
  different JSON-RPC methods with different result shapes, and until this
  the only thing proving the templates were published was an in-memory
  client in a unit test. It also holds the uncredentialed resource read
  to saying `[auth]`: a resource cannot carry a class the way a tool
  result does, so the message is the only place a client learns the
  answer is "log in" rather than "no such spreadsheet".
- **`make bench`, and the numbers in §11.** Rendering was never the
  expensive half. §11 has said "a 5 000-cell read rendered under 20 ms"
  since the design; it renders in 0.73 ms, and decoding the JSON that
  carried those cells takes ten times longer. At the maximum budget the
  decode is 83 ms, which is the same order as the round trip that
  delivered it. The target stands and now names the right half.
- **`make evals`: fifteen agent tasks and an A/B**, driven through
  `claude -p` with only this server's tools. Every task is scored twice —
  the end state read back through the server, and the trace, because a
  task can be completed by a model that guessed a range and was lucky.
  Guessing `Sheet1`, being refused, then reading the card and succeeding
  is a pass by outcome and a failure by every rule this server is built
  on. Tasks that cannot check an end state say which half went unchecked
  rather than quietly checking one, and a unit test walks the whole table
  refusing any prompt that still carries a placeholder.
### Changed


- **`go test ./...` was deleting the developer's refresh token.** The
  command tests redirect a config directory and an environment variable,
  and the OS keyring is the one store no variable redirects — it is
  addressed by service name and profile, and the tests took both from the
  defaults. So a logout test called `Delete("google-sheets-mcp",
  "default")` on the real keyring, on every `make check`, and a token
  "vanished" five times in one session. The tests now run under a profile
  derived from the test name, and one asserts that they do.
- **"No refresh token found" can also mean the keyring will not answer.**
  A locked collection reads as an absence through `go-keyring`, and "run
  login" is the wrong advice for it: it writes a second token beside the
  first. When the profile records a token in the keyring and the keyring
  says nothing, the two together now say so.

- **The first eval run found both of its defects in the harness.** One
  task passed on two reads and no write: it asked whether a word was
  anywhere on the sheet, and the word was one the fixture seeds its own
  rows from, so the sheet had contained it since setup — §13's "task that
  quietly checks nothing", inside the harness written to catch that. The
  helper behind it is deleted rather than fixed, because the shape is the
  problem and not the word. The other was a scorer reading past the
  seeded block into a total another task had written seven tasks earlier,
  which failed a model that had done exactly what it was asked. A third
  surfaced once those were fixed: a prompt asking for "the name and the
  value" against a four-column table, which the model correctly refused
  to guess at. A failing task now prints the model's own answer, because
  the calls say what it did and only its answer says why.
- **The evals found one thing about the server, recorded rather than
  fixed.** Asked for the last non-empty row of a 900-row sheet, a model
  took eleven reads and then observed that the footer had said "data ends
  at row 900" all along. It had — once a read overshoots the data — and
  reaching that cost eleven reads because a read's window is sized by the
  sheet's *allocated* width. The sheet is 26 columns wide and its data
  occupies two, so 5 000 cells buys 192 rows instead of 2 500. §17a.25
  has the options; it touches the card, the budget and possibly a new
  capability, which is not phase 3's to decide.
- **Two things the live run found that nothing had failed on.** A
  refusal told a `read_range` caller that "any A1 band works here
  instead" — a band being something `read_range` does not take, and the
  wording arrived when a review pass merged the range and band lookups
  and kept one noun. The test asserted the message contained "A1", which
  both wordings do. And a delete quoted an anchor's name in its refusal
  and printed it bare in the result a moment later, the same act
  described two ways. 169 steps, none failed, and the count said nothing
  about either.

- **A resource stopped rendering the cells it was about to throw away.**
  `SheetCSV` rendered the whole fetched window and then sliced to the
  used range, and wrote its CSV through a fresh `csv.Writer` — with a
  4 KB buffer — for every row. Trimming first and using one writer took
  the path from 42 ms to 23 ms and cut a quarter of its memory; the
  writer change alone is 4× faster and 13× lighter on a thousand rows.

- **The write guard stopped naming cells it would never mention.** It
  formatted an A1 address for every cell in the target and kept ten: 28
  000 allocations on a 5 000-cell write, 298 000 on a 50 000-cell one.
  Now 25 and 50 — flat, whichever size the rectangle is — and 13.7 times
  faster. Found by `make bench` rather than by reading, and it took three
  passes. The first was wrong in a way only the benchmark could show: it
  asked whether *any* finding set was full, and on a rectangle of plain
  values three of the four sets stay empty, an empty set is not full, and
  every address got built anyway. The measurement did not move, which is
  what gave it away. The last pass moved the decision inside the type
  that owns the cap, so no caller has to know it.
- **The parity gate could be switched off with one `#`.** It matched by
  substring over the whole workflow text, so `gates staleness` was found
  inside a commented-out step and counted as running — making the
  cheapest way to unblock a red build also the way to disable the check
  that would have noticed. Reported by a sibling repository and present
  here when it was looked for. The stripping is inside `checkParity`
  rather than in the function that reads the files, because a fix in the
  reader is a fix any other caller bypasses — the new test found that by
  still failing after the first attempt looked green. The match is now
  restricted to the workflow's `run:` and `uses:` scalars, which closes
  the class rather than the one spelling: a gate named in a step's
  `name:` or in an `env:` value no longer stands in for a step that runs
  it.

## [0.2.0] - 2026-09-06

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
- **Three refusals the live run found**, none of them in any reference
  read for this project. A merge spanning the edge of a frozen band is
  refused before sending, naming where the freeze ends and how to undo
  it — Sheets answers "You can't merge frozen and non-frozen columns"
  and does not say where the edge is. A named range with a space, or a
  name that is also a cell address, is refused with the rule; Google
  answers "The name given to this range is invalid". And **deleting a
  table takes every conditional format rule over its range with it**,
  which nothing in the reply mentions, so it now needs `overwrite` and
  names the rules that would go.
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

### Added — the Claude Desktop bundle

- Every release now carries a **`.mcpb`**. Opening it installs the server
  and asks for the OAuth client JSON, so the alternative is no longer
  asking somebody to hand-edit a config file. macOS, Windows and Linux on
  both architectures each: a universal binary for macOS, amd64 for
  Windows, and both Linux binaries with a launcher that reads `uname -m`
  and `exec`s the right one — writing any failure to stderr, because
  stdout is the JSON-RPC channel.
- **Packed in Go**, in `scripts/gates`, rather than by the official Node
  CLI: a `.mcpb` is a deflate zip and the standard library writes one, so
  the alternative was an interpreter nothing declared.
- **The manifest is validated against the files being packed**, which is
  the check a schema cannot do. `entry_point`, `mcp_config.command`,
  every `platform_overrides.*.command` and every `${user_config.x}` an
  env value spends must name something real. Each is broken on purpose in
  the tests and watched being refused; a sibling's packer verified two of
  the three and a typo in the Windows path would have packed, installed
  and been caught by nothing. `make mcpb` runs it on every commit against
  the names the packer will stage, so it needs no build.
- The bundle is reproducible: entries carry a fixed timestamp rather than
  the source file's, and the names are sorted, so the same inputs give
  byte-identical output.

### Fixed — before it ever shipped

- **The bundle was packed and left out of `checksums.txt`.** Packing in
  the universal binary's post hook is what makes it *possible* for the
  checksum file to cover it; the checksum step covers goreleaser's own
  artifacts, and a file a hook drops into `dist/` is not one. It needed
  naming in `checksum.extra_files` as well, and in `release.extra_files`
  or it would have been hashed and not uploaded. Caught by checking the
  version in all five places the standard names on a snapshot build: it
  agreed in four and was absent from the fifth, which is exactly what
  shipping unsigned looks like from the outside.

### Added — documentation and the gates that hold it

- The README is a release-shaped document now: badges, verifying a
  download against the signed checksums and the build provenance, the
  scope each tool needs, what keeps you safe, and how the packages fit
  together.
- [`docs/gcp-setup.md`](docs/gcp-setup.md), which walks through the Cloud
  project with the reasons — including why it is `drive.readonly` and not
  `drive.file` or the full scope.
- [`docs/runbook.md`](docs/runbook.md): rotating a token, revoking one,
  suspected exposure, moving machines, two accounts on one machine, and
  what the weekly expiry on a consumer account looks like from the
  inside.
- `CODE_OF_CONDUCT.md` — Contributor Covenant 3.0.
- **The staleness gate reads prose for paths.** Every repository path a
  document links to has to exist. A link that stopped resolving is the
  cheapest staleness to introduce and nothing else in `make check` was
  looking. CHANGELOG history is excluded on purpose: an entry naming a
  file that has since gone is history, not drift.
- **The architecture's package tree is derived rather than trusted.** It
  named 13 of 15 packages when the check was written, and one of the two
  missing was `internal/redact` — the single redactor every live driver
  prints through, which is to say the one carrying a confidentiality
  guarantee.

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
- **The log leak scan could fail on a timestamp.** It scanned the whole
  log line, including the handler's own `time=`, so a four-character
  numeric cell value could collide with the clock: `07.502` contains
  `7.50`, which is a cell in the fixture, and about one run in
  twenty-five failed naming a leak that had not happened. The two fields
  this server computes rather than reads are exempt now, by name and with
  the list asserted, and a test reproduces the collision deterministically
  rather than waiting for it. A gate whose failures cannot be trusted is
  worse than no gate: the next real finding reads as the flake.
- **A package's coverage number was its subtree's.** The floor gate
  matched a package by prefix, so `internal/gapi` was scored on
  `internal/gapi/sheetstest` as well — and the number it printed
  described neither. It held for two phases because the only subpackage
  was small and well covered. `internal/gapi` is 90% on its own and was
  reported as 69%.

### Notes

- **Run against a real account**: 143 driver steps, none failed, one
  undetermined (Drive's content index, which no single read can tell from
  a broken search). Three of the fixes above came from it. Five driver steps failed on the first run of the new
  surface: three were the server and two were the driver's own
  expectations, left stale by a review pass that had changed the
  behaviour deliberately. §18 of `docs/architecture.md` carries the
  split. It took three runs in all — the second because an edit to the
  driver never reached disk, and the third because a band assumed to
  hold values held none, so the merge that was supposed to be refused
  succeeded instead.
- **`sortRange`'s `dimensionIndex` is the sheet's own column, not an
  offset into the sorted range.** The plan claimed it and the reference
  did not settle it, so it was written down as a belief with a test
  waiting for it. The test ran: a range starting at B, sorted by C, comes
  back in C's order.
- Spike G answered §15's last size question, and answered none of it on
  its first attempt while appearing to — it printed the length of a JSON
  body as though it were the length of a cell, and asked for one column
  past ZZZ on a sheet where a different ceiling answers first. Both were
  visible only by reading the transcript, which is the whole reason this
  project reads them.
- Spike J was added mid-phase, for a question the plan did not have: what
  a table does to the rules over its range.

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

[Unreleased]: https://github.com/mmedum/google-sheets-mcp/compare/v1.5.0...HEAD
[1.5.1]: https://github.com/mmedum/google-sheets-mcp/compare/v1.5.0...v1.5.1
[1.5.0]: https://github.com/mmedum/google-sheets-mcp/compare/v1.4.0...v1.5.0
[1.4.0]: https://github.com/mmedum/google-sheets-mcp/compare/v1.3.4...v1.4.0
[1.3.4]: https://github.com/mmedum/google-sheets-mcp/compare/v1.3.3...v1.3.4
[1.3.3]: https://github.com/mmedum/google-sheets-mcp/compare/v1.3.2...v1.3.3
[1.3.2]: https://github.com/mmedum/google-sheets-mcp/compare/v1.3.1...v1.3.2
[1.3.1]: https://github.com/mmedum/google-sheets-mcp/compare/v1.3.0...v1.3.1
[1.3.0]: https://github.com/mmedum/google-sheets-mcp/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/mmedum/google-sheets-mcp/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/mmedum/google-sheets-mcp/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/mmedum/google-sheets-mcp/compare/v0.4.0...v1.0.0
[0.4.0]: https://github.com/mmedum/google-sheets-mcp/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/mmedum/google-sheets-mcp/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/mmedum/google-sheets-mcp/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/mmedum/google-sheets-mcp/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/mmedum/google-sheets-mcp/compare/v0.0.1...v0.1.0
[0.0.1]: https://github.com/mmedum/google-sheets-mcp/releases/tag/v0.0.1
