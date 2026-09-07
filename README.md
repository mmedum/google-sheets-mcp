# google-sheets-mcp

[![CI](https://github.com/mmedum/google-sheets-mcp/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/mmedum/google-sheets-mcp/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/mmedum/google-sheets-mcp?sort=semver)](https://github.com/mmedum/google-sheets-mcp/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/mmedum/google-sheets-mcp.svg)](https://pkg.go.dev/github.com/mmedum/google-sheets-mcp)
[![License: Apache 2.0](https://img.shields.io/github/license/mmedum/google-sheets-mcp)](./LICENSE)

Google Sheets as MCP tools. Read a range and see where every value sits,
write without destroying the formula underneath, reshape sheets and
dimensions, format, sort and validate — from Claude Code, Claude Desktop,
or any other MCP client.

A single Go binary that speaks MCP over stdio. It runs as a subprocess of
your client, on your own machine, against your own Google account. There
is no server to host, no shared deployment and no service account: you
create a Google OAuth client, log in once, and the refresh token stays in
your OS keyring.

It works **inside** a spreadsheet. Finding, sharing, moving and trashing
files, and their comment threads and revisions, belong to a server built
on the Drive API. A cell **note** is a Sheets field and is here.

> **Status: v0.3.1, phase 3 of five.** Reading, writing,
> formatting, the objects attached to a range, `gsheets://` resources and
> durable anchors all work. Charts, pivot tables and Connected Sheets data
> sources are phase 4. The phase plan is §16 of
> [`docs/architecture.md`](docs/architecture.md).

## Resources

Two, for a client that attaches a spreadsheet rather than calling a tool.
There is no static list — enumerating a person's spreadsheets is a Drive
listing, which belongs to a server built on the Drive API — and no
subscriptions, because the Sheets API has no push and no changes feed.

| URI | What it is |
|---|---|
| `gsheets://{spreadsheet}` | The card: every sheet with its exact title, id and size, plus named ranges, tables and protected ranges. Reads no cells |
| `gsheets://{spreadsheet}/{sheet}` | That sheet's **used range** as CSV — the rows and columns holding something, not the sheet's allocated size. The sheet title is percent-encoded |

## Install

```bash
go install github.com/mmedum/google-sheets-mcp/cmd/google-sheets-mcp@latest
```

Or take an archive from the
[latest release](https://github.com/mmedum/google-sheets-mcp/releases/latest)
— Linux, macOS and Windows, on amd64 and arm64 — and verify it before you
run it:

```bash
sha256sum -c checksums.txt --ignore-missing

# The checksum file is signed with a keyless Sigstore certificate tied to
# the release workflow's identity. The bundle carries both.
cosign verify-blob checksums.txt \
  --bundle checksums.txt.bundle \
  --certificate-identity-regexp 'https://github\.com/mmedum/google-sheets-mcp/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# And the archive itself carries build provenance.
gh attestation verify google-sheets-mcp_*.tar.gz --repo mmedum/google-sheets-mcp
```

Every archive also ships an SBOM, so you can see what is inside a binary
you did not build. Builds are reproducible: `-trimpath`, and the commit's
timestamp rather than the build's, so rebuilding a tag gives the same
bytes.

### Claude Desktop

Every release also carries a `.mcpb` bundle. Open it and Claude Desktop
installs the server and asks for your OAuth client JSON — no config file
to edit. It covers macOS, Windows and Linux on both architectures each:
macOS through a universal binary, Windows through amd64, and Linux
through a small launcher that picks the right binary at start, because a
bundle manifest has no key for the architecture. Its SHA-256 is in the
same signed `checksums.txt`.

The bundle does **not** log you in. Install the binary as well, run
`google-sheets-mcp login -secret <your client JSON>` once, and the bundle
picks up the same credentials. Claude Code does not install `.mcpb`
files, so it uses the command below.

## Set up Google

You need your own OAuth client. It takes about fifteen minutes once, and
[`docs/gcp-setup.md`](docs/gcp-setup.md) walks through it with the
reasons. In short, and in the order `doctor` checks things in:

1. Create a Google Cloud project.
2. Enable the **Google Sheets API**, and the **Google Drive API** — the
   second for `search_spreadsheets`, which is the only Drive call this
   server makes.
3. Configure the consent screen: **Internal** for a Workspace account,
   **External + Testing** for a consumer one — which means re-running
   `login` weekly, because Google expires a testing app's refresh token.
4. Add these two scopes, which are exactly what `login` requests:

   | Scope | What it is for |
   |---|---|
   | `https://www.googleapis.com/auth/spreadsheets` | Everything this server does inside a spreadsheet |
   | `https://www.googleapis.com/auth/drive.readonly` | `search_spreadsheets`, and nothing else |

   `drive.readonly` rather than `drive` or `drive.file` on purpose: this
   server finds spreadsheets and never creates, moves or trashes a file,
   and the narrower scope is what makes that a guarantee rather than a
   promise. It is also why the live driver cannot clean up after itself.

   With `GSHEETS_READ_ONLY=true`, `login` asks for
   `https://www.googleapis.com/auth/spreadsheets.readonly` instead of the
   first, and the write tools are not registered at all.
5. Create an **OAuth 2.0 Client ID** of type **Desktop app** and download
   the JSON.

Then log in:

```bash
google-sheets-mcp login -secret ./client_secret.json
```

Login prints the authorization URL and then tries to open your browser;
if it cannot, the URL is already on screen. The callback lands on
`127.0.0.1` on a random port, with PKCE and state throughout. The refresh
token goes into your OS keyring; if there is no keyring it goes into a
`0600` file under your config directory and the command tells you so.

`google-sheets-mcp status` says which account is signed in and where the
token lives. `google-sheets-mcp logout` revokes the token at Google and
deletes the local copy. `google-sheets-mcp doctor` checks the
credentials, the granted scopes and what Google actually answers, and
names what is missing — it masks the client id and the account, so its
output is safe to paste into an issue.

### Logging in over SSH

The callback goes to the *remote* host's loopback address and your
browser is local, so forward the port. It is chosen at random and printed
only once login is already waiting, so read it out of the printed URL —
it appears percent-encoded, as `127.0.0.1%3A<port>` — and in a second
local terminal:

```bash
ssh -N -L <port>:127.0.0.1:<port> user@remote-host
```

Then open the URL locally.

## Connect your client

Claude Code:

```bash
claude mcp add google-sheets -- google-sheets-mcp
```

Or, in a client config file:

```json
{
  "mcpServers": {
    "google-sheets": {
      "command": "google-sheets-mcp"
    }
  }
}
```

Claude Desktop reads the same shape from its own config file. Use the
binary's absolute path there if it is not on the app's `PATH`.

Every setting is a `GSHEETS_*` environment variable with a flag of the
same name, listed in [`docs/configuration.md`](docs/configuration.md).
The three worth knowing now: `GSHEETS_READ_ONLY=true` requests read-only
scopes and registers only the read tools,
`GSHEETS_ENABLE_DESTRUCTIVE=true` registers the three tools that remove
things, and `GSHEETS_PROFILE` lets one machine hold a work account and a
personal one.

## Tools

Seventeen tools. Everything but `search_spreadsheets` needs only the
Sheets scope; in read-only mode the read tools ask for
`spreadsheets.readonly` instead.

| Tool | What it does | Scope |
|---|---|---|
| `get_spreadsheet` | The spreadsheet card: title, link, locale, and every sheet's exact title, id, size, frozen rows, hidden state and what it holds, plus named ranges, tables, protected ranges and filter views. No cell data, so it costs the same on a spreadsheet of ten cells and one of ten million. Call it first | `spreadsheets` |
| `read_range` | An addressed grid of a range: column letters across the top, row numbers down the side. `show=both` prints each formula under the value it produced. Budgeted in cells and characters, with a continuation, and every read returns a checkpoint | `spreadsheets` |
| `read_formatting` | What a range looks like — number formats, fonts, colours, borders, alignment — per block of identically formatted cells rather than per cell, with the merges, conditional rules, banding, validation and notes that decide how a cell looks without being on the cell | `spreadsheets` |
| `search_spreadsheets` | Find a spreadsheet by part of its title, by text inside it, by owner or by when it changed. The only Drive call this server makes | `drive.readonly` |
| `find_in_spreadsheet` | Search one spreadsheet for text or an RE2 pattern and get back A1 addresses, saying whether each match was in a value, in the formula under it, or in a note beside it | `spreadsheets` |
| `create_spreadsheet` | A new spreadsheet, optionally with extra sheets and seed values. Returns its card, including the id every later call needs | `spreadsheets` |
| `write_values` | Write a rectangle, refusing first anything the write would destroy that you cannot see, then reporting every value Google stored differently from how it was sent | `spreadsheets` |
| `append_rows` | Add rows after a block of data and report where they actually landed — Google decides the destination, and the same sheet given different ranges appends in different places | `spreadsheets` |
| `format_cells` | Number format, font, colours, borders, alignment, wrapping, merges and notes, applied in one atomic batch. A merge, a clear and a note are the three that take something away, and each is refused until acknowledged | `spreadsheets` |
| `manage_range` | Add, update or delete what is attached to a range: a named range, a protected range, a validation rule, a table, banding, or a conditional format rule. Existing ones are named by the range they cover, not by an id | `spreadsheets` |
| `transform_range` | Sort, replace, trim, de-duplicate, split, shuffle, fill, copy or move a range — the operations that move data without you naming its new address, so each reads what it would land on first | `spreadsheets` |
| `manage_anchor` | Label a row, column or sheet so it can be found again after the spreadsheet has been edited around it. An anchor follows its row through inserts, deletes, moves and sorts, where an A1 address goes stale the moment somebody inserts a row. Pass `anchor:<name>` anywhere a range or band is taken, including the two tools that delete rows | `spreadsheets` |
| `manage_sheet` | Add, rename, duplicate, copy to another spreadsheet, hide, unhide, reorder, resize, freeze or colour a sheet | `spreadsheets` |
| `edit_dimensions` | Insert, move, resize, auto-size, group or ungroup rows and columns | `spreadsheets` |
| `delete_dimensions` | Destructive, off by default: remove rows or columns and the data on them, having counted what that is | `spreadsheets` |
| `clear_values` | Destructive, off by default: clear a range's values and keep its formatting, notes and validation rules | `spreadsheets` |
| `delete_sheet` | Destructive, off by default: delete a sheet and everything on it, having counted what that is | `spreadsheets` |

The three destructive tools are **not registered at all** unless
`GSHEETS_ENABLE_DESTRUCTIVE=true`, and each still needs `confirm: true`
on the call.

## What keeps you safe

**A write never destroys what it cannot see.** Sheets has no undo through
the API, so a refusal before the request is built is the only guard there
is. Overwriting anything non-empty needs `overwrite`; overwriting a
formula needs `overwrite_formulas` as well, because a formula and its
result look identical in a read. Each refusal names the cells and the
argument that would allow it.

The same guard covers everything phase 2 added, and each of these was
found to be a silent loss rather than assumed to be one:

- a **merge** keeps the top-left value of every merged block and discards
  the rest, with nothing in the response saying so;
- **`clear_format`** removes formatting Sheets cannot bring back;
- a **note** set or cleared replaces something no values read would have
  shown you;
- a **paste** or an **auto-fill** lands on cells you did not name, so the
  landing rectangle is read and guarded like any other target;
- a **`text_to_columns`** spills into the columns to its right;
- a **`find_replace` with `in_formulas`** rewrites what a cell computes
  rather than what it shows;
- **deleting a table** takes every conditional format rule over its
  range with it — verified against the live API, not read anywhere;
- and **deleting a row or column** takes any anchor on it, which nothing
  in Google's reply mentions either. `delete_dimensions` names them
  before it takes them.

Beyond the guard:

- **Coercion is reported, never hidden.** `input: typed` parses as a
  person typing, so `007` becomes `7` and `2026-09-05` becomes a date.
  Every write reads back what Google stored and names each value it
  changed — and for a date, what the cell still displays, so a serial
  number does not read as data loss. `input: literal` stores exactly what
  you send, and the server never substitutes one for the other.
- **`dry_run` on every write.** Sheets has no suggestion mode, so the
  preview reports what the guard found and what would change, having sent
  nothing. It is found by reflection rather than declared per tool, so a
  write that offers the flag cannot fail to honour it.
- **Read-only mode leaves the write tools unregistered.** A tool that is
  not registered cannot be called, whatever permission mode the client is
  in. It is set where you start the server, so it binds the session
  rather than one call.
- **Every read shows addresses**, so the model can write back to what it
  just read without counting. **A1 is the contract**: the model never
  sees a `GridRange`, and the zero-based half-open arithmetic lives in
  one package with table tests.
- **Sheet names are read, never assumed.** Google names the first sheet
  in the account's language, so `Sheet1` does not exist on a Portuguese
  account. No tool defaults a sheet and no description offers one as an
  example; a missing sheet is refused with the titles that do exist.
- **Reads are budgeted before the call.** The window is resolved against
  the sheet's real extent and a finite range is sent, so an open-ended
  `A:Z` never becomes a whole column in memory.
- **Logs never carry the payload.** They record the method, the tool, the
  outcome, the duration and a truncated spreadsheet id. Cell values,
  formulas, titles, ranges and search terms stay out — a search term
  reaches a log through a request URL, so transport errors are stripped
  of it — and a test fails the build if any of it appears.

## How it works

```
MCP client ──stdio──► google-sheets-mcp
                       ├── tools     one handler per tool; one Kind decides its annotations
                       ├── service   the rules: resolve, budget, guard, report
                       ├── plan      typed batchUpdate requests, and the write guards
                       ├── grid      the server's view of a rectangle, and its checkpoints
                       ├── render    every word a caller reads
                       ├── a1        A1 notation ↔ GridRange, the only place that conversion lives
                       ├── gapi      REST client for Sheets and the one Drive call
                       └── auth      refresh token → access token
```

[`docs/architecture.md`](docs/architecture.md) has the request flow, the
package layout, the phase plan, the decisions a contributor should not
undo, and an evidence log recording what was checked against the API and
what the API turned out to do instead. The threat model is in
[`docs/security.md`](docs/security.md), and the procedures for rotating
or recovering credentials are in [`docs/runbook.md`](docs/runbook.md).

## Development

```bash
make build     # the binary
make test      # race detector, coverage floor
make check     # everything CI runs
```

`make check` is the definition of done: formatting, `go vet` including
the build-tagged code, golangci-lint, race tests with a per-package
coverage floor, `govulncheck`, a licence allow-list, two secret and
identifier scans, a stdio smoke test, a schema diff against the released
tool surface, a check that the Claude Desktop bundle's manifest names
only files that will be packed, a check that the Makefile and CI run the
same things, and a staleness gate that fails when this README, the docs
or the changelog drift from the code.

Green gates are not the whole of it. Anything touching the write path or
an API response shape also gets a run against a real account, and the
transcript is read rather than counted — `docs/development.md` has the
commands. Contributing conventions are in
[`CONTRIBUTING.md`](CONTRIBUTING.md).

## Versioning

Tool names and their output fields are stable within a major version. A
change needing you to act — a new scope, another login, a different
command in your client config — is marked **Breaking:** in
[`CHANGELOG.md`](CHANGELOG.md), which is also what the release notes are
made from.

## Security

[`SECURITY.md`](SECURITY.md) says how to report a vulnerability.

## Code of conduct

[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) — Contributor Covenant 3.0.

## License

Apache 2.0 — see [`LICENSE`](LICENSE).
