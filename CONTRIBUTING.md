# Contributing to google-sheets-mcp

Thanks for considering a contribution. This is a Go MCP server for
Google Sheets with a strict test and confidentiality posture. Read this
once before your first pull request; CI enforces the rest.

## Before anything else

**Nothing from a real spreadsheet may enter this repository** (D-09).
Not cell values, formulas or notes; not sheet or spreadsheet titles,
named ranges or metadata keys; not spreadsheet ids or the URLs carrying
them; not account addresses, Cloud project ids, OAuth client ids or
secrets. This holds for code, docs, fixtures, goldens, issues, pull
requests, commit and tag messages and logs.

A formula is the worst of them: `IMPORTRANGE` carries another
spreadsheet's id inside a string, and it is the cell somebody copies
into a test because it is the interesting one.

gitleaks scans for credentials and `make leaks` for identifiers and
data. Two structural rules cover what no pattern can see: fixtures are
generated, never recorded (D-10), and the live driver reads only a
spreadsheet it created and filled itself (D-11).

## Reporting bugs and requesting features

Open a [GitHub issue](https://github.com/mmedum/google-sheets-mcp/issues/new/choose)
with the bug report form. It asks for what
[`docs/troubleshooting.md`](docs/troubleshooting.md#reporting-a-problem)
lists: `doctor` output, the version and a debug log, all safe to paste.
Never paste a tool result: it is your spreadsheet's content.

For anything security-sensitive, do not open a public issue. Follow
[`SECURITY.md`](SECURITY.md).

## Development setup

```bash
git clone https://github.com/mmedum/google-sheets-mcp
cd google-sheets-mcp
make build
make hooks
```

Go is the only prerequisite, at the version in `go.mod`. Every other
tool runs at a pinned version through `go run`, never from your `PATH`
(D-66, D-69). `make hooks` points git at `.githooks/`, whose pre-commit
hook runs `go run ./scripts/gates precommit`: gofmt, vet, the leak scan,
the error classes and the transcript check.

## Gates that must pass

```bash
make check
```

`make check` runs the gates CI runs, and the `check` target in the
`Makefile` is the list. `make parity` fails when that list and `ci.yml`
run different commands, so a check you add goes in both, as the same
command (D-67). CI runs it on Linux, macOS and Windows (D-68).

Every check this repository runs on itself is Go, under `scripts/gates`.
`go run ./scripts/gates` lists them, and `go run ./scripts/gates <name>`
runs one. Each has tests of its own, and each asserts a floor on how
much it read, because "found nothing" and "read nothing" print the same
line (D-65). Before you trust a gate you changed, break what it holds
and watch it fail.

These are worth knowing before they fail on you:

- **The schema diff** compares the tool surface with the newest
  release's, recorded in `testdata/schema-baseline.json`, at any depth
  (D-57). It fails on a tool, resource or field removed; an argument
  that takes fewer types or values, or is newly required; and a returned
  field that may carry another type, or be missing where it was
  required. A break ships only as a new major version, with go.mod's
  `/vN` raised.
- **`make leaks`** fails on identifiers and data its allow-list rules do
  not excuse, in tracked and untracked files alike, and on a tracked
  binary. Anything `.gitignore` covers is left alone. Run
  `make leaks-history` before anything goes public.
- **`make api-coverage`** holds every method the Sheets and Drive
  discovery documents publish, and every `batchUpdate` request kind, to
  a verdict in `testdata/api-coverage.tsv`: used, naming the code, or
  written off with a reason (D-76). `make api-fields` does the same for
  fields in `testdata/api-fields.tsv`. `make api-diff` refetches the
  snapshot and is run by hand.
- **The docs gate** (`go run ./scripts/gates docs`) holds the docs to
  their references. A decision in `docs/architecture.md` is `D-NN`; an
  ID is never reused, and the file ends with its `Next ID:` line. Cite a
  decision by ID, or a doc by its path and heading anchor, never by
  section number. What you verified about Google goes in the comment at
  the line that depends on it, with the source link and the live step
  that checks it, written `Checked by the live step "<name>".`
- **`make staleness`** holds the README's tool table, scopes and
  permission rules to the binary, and `docs/configuration.md` to every
  setting the code reads. It prints what it expected.
- **Renderer goldens.** `testdata/golden` holds what each renderer
  prints, from generated data only.
  `go test ./internal/render -update` rewrites them; reading the diff is
  the part that matters.

## Tests

Unit tests are table-driven and never touch the network.
`internal/gapi/sheetstest` is an in-memory Sheets, with the Drive
endpoints this server calls, behind `httptest` (D-59). It models
formulas, error cells, notes, validation, merges, protected ranges,
ragged responses and injected failures.

The fake does not invent Google's parser. What Google does with typed
input is recorded from a live probe and replayed, because a fake built
from the documentation inherits the documentation's errors.

Tests in `cmd/google-sheets-mcp` call `isolate`, which points the config
directory at a temporary one and gives each test its own profile. The
OS keyring is addressed by profile name and no variable redirects it,
so a test that skips `isolate` can delete your own refresh token.

## Changing the tool surface

Add to a tool's arguments and fields; do not take away. On a pull
request, `make schema-ack` compares the surface at the merge-base and
the head, annotations included. If it changed, end one of the pull
request's commits with a footer naming each changed item:

```bash
git commit --allow-empty -m "Acknowledge the tool surface change" \
  -m "SCHEMA-CHANGE: write_values, append_rows: destructiveHint true"
```

The names are the list before the first colon followed by white space;
the text after it describes the change and names nothing. Name a tool
by its name, a resource by its URI or template, and the instructions as
`instructions`. Put a name with a comma in it in backticks, and keep the
list on one line: a wrapped line that starts `word: ` begins a new
footer, as Conventional Commits reads it. Names are separated by
commas, and a `Token #value` footer such as `Closes #12` acknowledges
nothing. If it breaks something a release shipped, name that item in a
`BREAKING CHANGE:` footer instead. A refusal prints the footer that
fixes it.

## Adding a tool

1. Declare it in `internal/tools` through `add`, with a `Kind` (D-34).
   The kind decides the annotations, whether it registers in read-only
   mode and under the switches, and its rendering. A write takes
   `dry_run`, found by reflection (D-30).
2. Put the logic in `internal/service`, not the handler.
3. Add it to the README's tool table, or `make staleness` fails.
4. Drive every option in `scripts/livesheet`, or record it in
   `livecover.Undrivable` with the reason, or `make live-cover` fails.
5. Read `make schema-diff`, and add the `SCHEMA-CHANGE:` footer above.

## The live run

Every fake in the unit suite is written from what we believe Google
does, and only a live run tests that belief (D-60, D-61). Anything
touching the write path or an API response shape needs one before it
counts, and the transcript has to be read: a pass count cannot show a
result that is confidently wrong.

```bash
make live
```

What it does, so nothing is a surprise:

- **Your account.** It builds the binary and signs in with your stored
  profile, `GSHEETS_PROFILE` or `default`.
- **Its own spreadsheets only.** It creates scratch spreadsheets titled
  `livesheet scratch …`, fills them with invented data, and drives every
  tool option against them. It never reads a spreadsheet it did not
  write, so the values in a transcript are its own (D-11).
- **Checks that can fail.** Every step carries an expected outcome and a
  reason, and an expected refusal proves as much as a success. A result
  that asserts state is read back. A step writes what it then counts.
- **Drive's index is eventually consistent.** A search for a spreadsheet
  made a second ago is polled, and a step that never saw it is
  undetermined, which is not a pass.
- **Cleanup is yours.** The server holds `drive.readonly`, which cannot
  trash a file, so the run ends by printing a Drive search that finds
  every scratch spreadsheet. Each run starts fresh rather than reusing
  one, so no result depends on the last run.
- **Reading it.** Every line goes through one redacting printer, which
  masks ids and links; `make transcript` holds that. Read the transcript
  before sharing it.
- **Coverage.** `make live-cover` reads the driver's source for every
  tool option, and the run measures the same on the wire.

**Spikes** ask Google the questions its reference leaves open, and a
comment that relies on an answer names its spike as a live step.

```bash
go run -tags=live ./scripts/spikes -list
go run -tags=live ./scripts/spikes -only "where an append lands,the shape of a Sheets error"
```

`-list` prints the names, and `-only` takes them, comma-separated,
ignoring case. A full run asks every spike. Each run signs in with the
`default` profile and leaves a `spike scratch …` spreadsheet behind, so
ask several questions in one run. A spike prints what the API did; it is
read, not passed.

**Evals** score whether a model can use the tools, which the driver
cannot show (D-62). `make evals` needs credentials and the `claude` CLI,
and spends money. A model works through this server's tools alone, with
the shell and file tools off, in an `evals scratch …` spreadsheet. Each
task is scored twice: the end state, read back through the server, and
the trace of calls, since guessing a sheet name or passing `overwrite`
unneeded passes by outcome alone. One entry is an A/B: the same work
with the addressed grid and with bare rows. `go test ./scripts/evals`
checks the task table and needs no credentials.

## Branch and commit conventions

- `main` is released code and takes pull requests only (D-73). Work on a
  short topic branch.
- Write the subject in the imperative, under 72 characters, saying what
  changes. Look at a recent `git log` for the tone.
- The body says the problem, why this change solves it, and the
  alternatives rejected. A fixed review finding is one line at the end
  of the body. A declined one goes in the pull request, or becomes a
  present-tense comment at the line it protects.
- The maintainer pushes, merges, tags and releases.

## Pull requests

1. Open against `main`, with a summary and a test plan.
2. CI must be fully green.
3. A maintainer reviews and merges.

## Changelog

Every user-visible change gets an entry under `## [Unreleased]` in
`CHANGELOG.md`, in the same pull request that makes the change. Leave
out changes only contributors see, unless they must act on them.
`make staleness` asks for an entry when the binary's source changed
since the last release: a package it imports, a file one embeds, or
`go.mod` outside its `require` and `tool` lines.

- **Sections** are the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
  set and nothing else: Added, Changed, Deprecated, Removed, Fixed and
  Security, in that order.
- **One line per change, 25 words or fewer, impact first.** Write for
  someone deciding whether to upgrade. The investigation belongs in the
  commit message.
- **Breaking changes** start with `**Breaking:**`, and may add one
  sentence saying what to do. Anything needing a new login, a new scope
  or a config change is breaking.
- **Security fixes** name the CVE or GHSA.
- **A dependency bump** needs no entry, except the MCP SDK's, whose
  version clients see.

`release.yml` lifts the matching section verbatim into the release
notes, so the entry is the release note (D-74). Versioning follows
[SemVer](https://semver.org/spec/v2.0.0.html), read through the tool
surface: tool names, input and output schemas, annotations, resources
and documented behavior.

## Releasing

Maintainers release as [`docs/release.md`](docs/release.md) says.

## Code style

- Match the surrounding code. Do not reformat what you did not touch.
- Comments explain why: a constraint, an invariant, something Google
  does that the code has to work around. Do not narrate what the code
  does.
- Write plainly: lead with the outcome, one idea per sentence. This
  holds for comments, commit messages, the CHANGELOG and tool
  descriptions.
- Logs record the call, never the payload (D-07).

`CLAUDE.md` has the short version of the rules, and
`docs/architecture.md` the reasoning, one decision per D-ID.

## License

By contributing, you agree that your contributions will be licensed
under the project's [Apache 2.0 License](./LICENSE).
