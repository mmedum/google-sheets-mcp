# Development

Everything the repository runs on itself is Go, including the gates and
the live driver, so a contributor needs one toolchain and the code
holding the gates shut is built, vetted, linted and tested like the rest.

## Getting started

```
make hooks     # point git at .githooks
make build
make check     # everything CI runs
```

`make check` is the definition of done: gofmt, `go vet` including the
tagged tests, golangci-lint, race tests with an 80% coverage floor per
package, govulncheck, the licence allow-list, the error-class gate, the
leak scan, the workflow pin and shell check, the schema diff, the stdio
smoke test and the staleness gate.

## The gates

They live in `scripts/gates` and each has tests of its own, because a
gate nobody has watched fail is not yet a gate. Every one of them also
asserts a floor on how much it read: "found nothing" and "looked at
nothing" print the same sentence otherwise.

| Command | What it holds |
|---|---|
| `go run ./scripts/gates coverage cov.out 80` | Statement coverage per package. The list of packages is derived from `go list ./internal/...`, so a package added is under the floor from its first commit; exemptions are written in the gate with their reasons. |
| `go run ./scripts/gates classes` | The error vocabulary, from both sides: a class emitted and not declared fails, and so does one declared and never emitted. The emitted set is read out of the syntax tree. Classes a later phase will emit are listed in the gate with the phase that emits them. |
| `go run ./scripts/gates leaks` | Identifiers and data in the working tree. `leaks history` walks every blob and every commit and tag message; run it before making the repository public and after removing anything from it in a hurry. |
| `go run ./scripts/gates transcript` | Every print in the live driver and the spike probes goes through the one helper that redacts. §9.1 promises a transcript is safe to paste into a commit message, and that promise held by luck until this existed: nothing stopped a step printing directly, and a step that did would look exactly like the ones that do not. |
| `go run ./scripts/gates live-cover` | Every option of every registered tool is exercised by a step in the live driver, compared against the schema the binary publishes rather than a list anybody maintains. §13 promises the driver covers every tool and every op; when this first ran it covered 14 of 28 options. This half reads the driver's *source*, so it runs in CI without credentials — and it can be satisfied by a step that exists and never executes, which is why `make live` measures the same thing on the wire. Both read `internal/livecover` for the exemption list, so they cannot disagree. |
| `go run ./scripts/gates pins` | Every action pinned to a full commit SHA, every tool version exact, and every workflow pinning its shell at the workflow level. |
| `go run ./scripts/gates smoke` | Drives the built binary over stdio without credentials and asserts a clean exit when stdin closes. |
| `go run ./scripts/gates schema-diff` | Dumps the tool schemas and compares them with the last tag's, built in a throwaway worktree. A removed tool or field, or a new required field, is breaking. |
| `go run ./scripts/gates mcpb` | The Claude Desktop bundle's manifest against the files the packer will stage: `entry_point`, `mcp_config.command`, every `platform_overrides.*.command`, and every `${user_config.x}` an env value spends. It needs no build, because the staged *names* are static — so a manifest naming something that will never exist fails today rather than at the tag. The packing itself is `mcpb-pack`, which runs at release time and is excused from `parity` by name. |
| `go run ./scripts/gates parity` | `make check` and `ci.yml` run the same things. The gate list is derived from the dispatcher's own case labels, so it catches the third direction two lists compared with each other cannot: a gate the program implements that neither file runs. An excused gate needs a written reason, and an empty reason is itself a failure. |
| `go run ./scripts/gates staleness` | The README's tool table against the registered tools, `docs/configuration.md` against the settings `config.go` registers, the CHANGELOG against what changed since the last tag, every repository path the documents link to, the architecture's package tree against the packages that exist, and a status line claiming a version nothing can confirm. |

## Tests

Unit tests are table-driven and never touch the network.
`internal/gapi/sheetstest` is an in-memory Sheets and the one Drive
endpoint this server calls, with formulas, error cells, notes,
validation, merges, protected ranges, ragged responses and injected
failures.

Two rules about it are load-bearing.

**Fixtures are generated, never recorded.** Titles come from an invented
vocabulary and numbers from a seeded generator. No fixture is ever a real
spreadsheet trimmed down, and the goldens under `testdata/` are built
from the fixtures with `go test ./internal/render -update`.

**The fake does not invent Google's parser.** `USER_ENTERED` coercion
will be *recorded* from a live probe and replayed, never simulated. A
fake built from the documentation inherits the documentation's errors,
and a sibling project shipped a search bug that its entire test suite
agreed with.

## The live driver

```
make live
```

Green gates are not done. Anything touching the write path or an API
response shape gets a live run before it counts, **and the transcript is
read** — a sibling's driver twice reported that all calls behaved as
expected while three of its results were wrong, because it checked
whether calls succeeded rather than whether they told the truth.

Four rules keep that from happening here:

- Every step carries an expected outcome and a reason. An expected
  refusal proves as much as a success, and a success where a refusal was
  expected is a failure.
- A result that asserts state is read back and compared, because all
  three of those wrong results were describing the state from *before*
  the write.
- Anything eventually consistent is polled and says which it saw. Here
  that is Drive's index: a spreadsheet created a second ago may not be
  findable by `search_spreadsheets` yet, and one read cannot tell
  indexing lag from a broken search.
- The driver creates its own spreadsheets and fills them with its own
  synthetic data. It never reads a spreadsheet it did not write, so the
  values in a transcript are its own and are safe to paste into a commit
  message. Redaction of ids and links is a second line of defence and
  lives in the print helper only: scrubbing on the read path means a step
  parses a placeholder out of one result and feeds it back into the next
  call, which is a mistake a sibling project made and had to undo.
- A step writes what it will then count. Three steps in phase 1's first
  full run reported "0 non-empty cells" and passed, over rectangles an
  earlier step had emptied and a sheet duplicated before anything was
  written to it. A count that is always zero is a check that is always
  true.

**It leaves two spreadsheets behind, and cannot help it.** This server
asks for `drive.readonly` on purpose, and trashing a file needs a
write-capable Drive scope. Every file the driver makes is titled
`livesheet scratch …`, and the run ends by printing that as a Drive
search so the cleanup is one selection. Reusing a single scratch
spreadsheet across runs was considered and rejected: it would leave one
file forever and give every run a state the last run wrote, and a driver
whose results can be explained by the previous run is not a driver worth
reading.

Reading the transcript is not a formality. Phase 1 ran the driver five
times; every run was green or nearly so, and every transcript held
something the count did not — including a hole in the write guard.

**The leak scan reads the working tree, not the index.** That is
deliberate and it was not always true: it read tracked files only, so a
phase's new files — the ones nobody had scanned before — were invisible
until `git add`. Phase 3 ran it green while 28 of its own had never been
looked at, and the first staging found a spreadsheet id in a test that
did not declare itself synthetic. It now reads untracked files too, which
also means a build artifact is refused while it is still untracked,
before a wildcard `git add -A` can sweep it in. Anything `.gitignore`
covers is left alone.

## The spikes

```
go run -tags=live ./scripts/spikes -only K
```

The probes §15 lists, each answering a question the reference does not
pin down. `-only` runs one, because every run creates a scratch
spreadsheet it cannot trash and a narrower run is a smaller mess.

They are written to be *read*, not to pass. A spike prints what the API
did, and the verdict goes into the evidence log in `docs/architecture.md`
§18 rather than into somebody's memory. Two of them have now had a first
run that answered nothing while looking like an answer: spike G measured
the length of a JSON body and called it the length of a cell, and spike K
aimed four of its eleven questions at a row its own subject had already
moved. Both were visible only by reading the output against what the step
claimed to be asking.

The rule that came out of the second one: **a step that names a position
asks where the thing is at that moment.** A constant in a probe about
movement is a stale constant, and its answer reads as a finding.

## The evals

```
make evals
```

The live driver proves the tools work. The evals prove they can be
*used*, which is a different claim and the one that fails quietly: what
no driver catches is a result that is internally consistent and wrong.
Fifteen tasks go to a model through this server's tools alone, with the
shell and the file tools switched off, so what is being scored is the
tool surface rather than the model's resourcefulness.

Every task is scored twice, and the second half is the one that earns its
keep:

- **The end state**, read back through this server. A model's account of
  what it did is the least reliable thing in the run.
- **The trace** — the calls it made, in order, with their arguments —
  because a task can be completed by a model that guessed and was lucky.
  Guessing `Sheet1`, being refused, then reading the card and succeeding
  is a pass by outcome and a failure by every rule this server is built
  on. So is passing `overwrite: true` on a call that never needed it: the
  write succeeds either way, and only the trace can tell that the guard
  was switched off rather than satisfied.

Two rules, both inherited from a sibling's first full eval run rather
than from its plan:

- **Every task answers "what does this check when the world says no?"**
  Where the end state cannot exist, the task scores the trace and
  *prints* which half went unchecked. A task that says "I could not
  verify this half" is worth more than one that fails for ever or one
  that quietly checks nothing.
- **A prompt containing an unsubstituted placeholder is refused, and a
  unit test walks the whole table.** That sibling passed two tasks while
  sending the agent a literal `{folder}`. The guard is in `go test`,
  where it costs nothing, rather than in a run that costs ten minutes and
  an account — `go test ./scripts/evals` needs no credentials.

One entry is an A/B rather than a task: the same work with the addressed
grid in front of the model and with bare rows, counting the calls each
took. §4.9 is reasoning rather than measurement, and this is the
measurement. If the two arms write to *different* rows, that is a
stronger finding than any call count.

Like the driver, it leaves a scratch spreadsheet behind, titled `evals
scratch …`.

## `make check` and CI

`make check` is what CI runs, and `make parity` is what makes that
sentence true rather than aspirational. It compares the Makefile's
`check:` prerequisites against `.github/workflows/ci.yml` and fails when
either side has something the other lacks; the list of gates comes from
the dispatcher in `scripts/gates/main.go`, so a gate added there is under
the comparison from its first commit.

It exists because the claim was false three times over. The Makefile ran
`go vet -tags=live` and CI did not, so **nothing in CI compiled the live
driver or the spikes** — `go list ./scripts/livesheet` returns one stub
file, and a change that broke the driver stayed green until somebody
tried to run it, which is the step that closes a phase. CI scanned for
secrets and the Makefile had no such target, so the drift ran the other
way too. And `go mod tidy` was in neither: it ran only inside a release,
in the one form that writes, where a rewrite would fail the tag on a
dirty tree while every `--snapshot` rehearsal stayed green.

Adding a check means adding it to both and, if it is not a gate, a row in
`parityChecks`. Leaving it out of either fails.

## Branches and releases

`main` is released code and is never pushed to directly, release commits
included. Work on a short topic branch; the maintainer pushes, opens the
pull request and merges once CI is green on three platforms.

A release is a `Release N.N.N` commit on a topic branch, a pull request,
CI green, a merge, and then the tag pushed on its own. **Push tags one at
a time**: GitHub drops tag events past the third in a single push and the
release never runs.

The CHANGELOG entries move out of `[Unreleased]` and under the new
version heading in the release commit, which is what lets the staleness
gate pass on a release pull request — a gate without that exception fails
on the one pull request it was written to guard.

The tag builds the archives, the SBOMs, the signed `checksums.txt` and
the Claude Desktop bundle. To see what a release will produce without
tagging anything:

```bash
go run github.com/goreleaser/goreleaser/v2@v2.18.1 release   --snapshot --clean --skip=sign,publish
```

Then check the version in five places before believing it — the bundle
filename, the archive filenames, the manifest inside the bundle, the
binary's own `--version`, and `checksums.txt`. Four of the five agreeing
is what shipping unsigned looks like from the outside, and is how the
missing `checksum.extra_files` entry was found (§12).

## Phases

Each phase is one session and ends in a tagged release, then waits for an
explicit go. `docs/architecture.md` §16 is the plan and §18 is the
evidence log; anything verified live goes in the log rather than into
somebody's memory.
