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

1. **Never commit anything internal**: spreadsheet ids or URLs, anything
   from a real spreadsheet, account addresses, Cloud project ids, OAuth
   client ids, secrets, or a reference to another project, account or
   machine the maintainers use. Not in code, docs, fixtures,
   transcripts, commit messages, pull requests or logs. `make leaks` and
   gitleaks fail on the shapes they see (D-09).
2. **Never record a fixture, and never let the live driver or the evals
   read what they did not write.** Fixtures are generated (D-10). Both
   work in spreadsheets they made and print only through the redacting
   helper `make transcript` holds (D-11).
3. **Never write to stdout outside `main`.** Stdout carries only MCP
   JSON-RPC frames; logs go to stderr through `slog` (D-06). forbidigo
   and `make smoke` hold it, with `scripts/` excluded.
4. **Never log the payload**: cell values, formulas, titles, ranges or
   search terms; a transport error loses its URL (D-07).
   `TestLogsCarryNothingFromTheSpreadsheet` holds it.
5. **Never show the model a `GridRange` or make it count.** A1 is the
   contract and the conversion lives in `internal/a1` alone (D-12). A
   range is built from a parsed title, quoted on the wire (D-14). No
   tool defaults a sheet and no description offers `Sheet1` (D-15);
   `make staleness` refuses the second.
6. **Never write over what the caller did not see.** Non-empty cells
   need `overwrite` with `expect_checkpoint`, formulas
   `overwrite_formulas` too, and protected ranges and partial merges are
   refused (D-24, D-25). Formatting and transforms guard what they take
   (D-33).
7. **Never hide what Google changed.** The caller picks `typed` or
   `literal` and the server never substitutes; every write reports what
   Google stored, read back (D-27, D-31).
8. **Never write a formula that reaches outside the spreadsheet** unless
   `GSHEETS_ALLOW_EXTERNAL_FORMULAS` is set; no argument lifts it
   (D-28).
9. **Never repeat a write its HTTP method does not make safe.** An
   append or a `batchUpdate` that may have landed is `[ambiguous_outcome]`
   (D-45). `TestUnrepeatableWritesAreNotRepeated` holds it.
10. **Never register a tool outside `add`**, whose `Kind` decides
    annotations, registration and rendering (D-34). A delete is
    unregistered without `GSHEETS_ENABLE_DESTRUCTIVE` and needs
    `confirm: true` (D-35). A risky variant is its own tool, never an
    argument (D-36). The server asks the person nothing (D-37), and
    `requiresUserInteraction` stays on `delete_data_source` alone (D-39).
11. **Never let a tool widen the server's reach**: its settings, the
    credentials, the profile, a file or the environment (D-40).
    `TestNoToolCanWriteTheServersSettings` holds it.
12. **Never ask for another scope**, not even so the live driver can
    clean up. `spreadsheets` and `drive.readonly`, or their read-only
    pair, and `bigquery.readonly` only with `GSHEETS_ENABLE_DATA_SOURCES`
    (D-49, D-43). `TestScopes` holds it.
13. **Never import `google.golang.org/api`** or another project's code;
    extend `internal/gsheets` (D-75). depguard refuses it. Never call a
    method or model a field without a verdict in
    `testdata/api-coverage.tsv` or `testdata/api-fields.tsv` (D-76).
14. **Never rename or drop a released tool, argument or output field.**
    `make schema-diff` fails on a break unless go.mod's major version is
    above the release's (D-57).
15. **Never emit an error outside `[class] message`.** `make classes`
    holds the vocabulary closed (D-44).
16. **Never add a rule that cannot fail**: a test, a list derived from
    the code, and a floor on what the check read (D-65).
17. **Never commit to `main` or push**; the owner pushes, tags and
    merges (D-73).
18. **Never adopt a belief about Google from a reference page's prose.**
    Check the discovery document or a live probe (D-60). Record a
    verified Google behavior in the comment at the line that depends on
    it, with the source link and the live step that checks it.

## Definition of done

- `make check` passes. On a pull request CI also runs `schema-ack`,
  which needs a commit footer naming each changed item of the tool
  surface (`CONTRIBUTING.md`).
- New behavior has tests, and the schema diff has been read.
- `/simplify` and `/code-review medium` have run. A fixed finding is one
  line in the commit body. A declined one goes in the PR body, or
  becomes a present-tense constraint comment at the line it protects.
  Never write findings into `docs/`.
- Anything touching the write path or an API response shape has had a
  `make live` run whose transcript was read (D-61).

## Traps

- **Green gates are not done.** The fakes in `internal/gapi/sheetstest`
  are written from belief about Google; only `make live` tests it.
- **A dry run is each write's own early return.** Nothing in the API
  client refuses a write under `dry_run`, so a new write needs its own
  "sends nothing" test (D-30).
- **The card is cached for `service.CacheWindow`, and only structural
  writes drop it.** The format, transform and table-header guards read
  it through the cache, and so does a read after `append_rows` (D-48).
- **A test can redirect the config directory but not the OS keyring.**
  `isolate` in `cmd/google-sheets-mcp` gives each test its own profile;
  a test that skips it can delete your refresh token.
- **Never delete a decision or reuse an ID.** Supersede or deprecate it,
  and take the next ID from the file's `Next ID:` line.

## Writing

Plain and short: lead with the outcome, one idea per sentence, no
narration, no count a gate does not check. History goes in commit
messages. `release.yml` lifts the CHANGELOG section verbatim into the
release notes, so its rules in `CONTRIBUTING.md` are not optional.
