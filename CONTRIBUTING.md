# Contributing

## Before anything else

**Nothing from a real spreadsheet may enter this repository.** Not cell
values, formulas or notes; not sheet or spreadsheet titles, named ranges
or metadata keys; not spreadsheet ids or the URLs carrying them; not
account addresses, Cloud project ids, OAuth client ids or secrets. This
holds for code, docs, fixtures, goldens, issues, pull requests, commit
and tag messages and logs.

A formula is the worst of them: `IMPORTRANGE` carries another
spreadsheet's id inside a string, and it is exactly the kind of cell
somebody copies into a test, because it is the interesting one.

Two gates enforce it — gitleaks for credentials, `make leaks` for
identifiers and data — and two structural rules make the rest
impossible rather than merely discouraged: fixtures are generated, and
the live driver reads only a spreadsheet it created and filled itself.

## Getting set up

```
make hooks
make check
```

`make check` is what CI runs. See
[docs/development.md](docs/development.md) for what each gate holds and
why.

## Making a change

- Work on a short topic branch. `main` is released code and is never
  pushed to directly.
- Add tests for new behaviour. A rule with no test is a rule nobody is
  keeping: five rules in a sibling repository were true on paper and
  false in the code at the same time, and not one was caught by reading.
- Put an entry under `[Unreleased]` in `CHANGELOG.md`. The staleness gate
  asks for one when Go changed.
- Verify against the discovery document or a live probe before adopting a
  convention, and record the verdict in the evidence log in
  `docs/architecture.md` §18. A reference page's prose is not evidence:
  this project has already shipped a design built on a field the API does
  not have.
- Keep the writing plain and short — code comments, commit messages, the
  CHANGELOG, tool descriptions. Lead with the outcome. One idea per
  sentence.

## Things that are decided

These are in `docs/architecture.md` §17 and are not reopened without new
evidence:

- A1 notation is the contract. The model never sees a `GridRange`.
- The write guard is on by default, and needs two separate
  acknowledgements to overwrite a formula.
- `input` is explicit and never substituted; the coercion report is what
  makes it safe.
- Checkpoints are best effort and say so; protected ranges are the only
  real guarantee the platform offers.
- Comment threads are out — they are a Drive resource. Cell notes are in.
- Stdio only, one Google account per profile, deployer-owned Cloud
  project.

## Reporting a bug

Use the issue form. It asks for `doctor` output, a debug log, the
version, how you installed it and which client — and it tells you what
not to paste. The debug log is safe to paste by construction: a test
fails the build if anything from a spreadsheet reaches a log line.
