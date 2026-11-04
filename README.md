# google-sheets-mcp

[![CI](https://github.com/mmedum/google-sheets-mcp/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/mmedum/google-sheets-mcp/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/mmedum/google-sheets-mcp?sort=semver)](https://github.com/mmedum/google-sheets-mcp/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/mmedum/google-sheets-mcp/v4.svg)](https://pkg.go.dev/github.com/mmedum/google-sheets-mcp/v4)
[![License: Apache 2.0](https://img.shields.io/github/license/mmedum/google-sheets-mcp)](./LICENSE)

Google Sheets as MCP tools. Read and write ranges without destroying the
formulas underneath. One Go binary over stdio, signed in as you, with
your own OAuth client and the refresh token in your OS keyring.

It is for a person with a Google account who wants an assistant to read,
write and reshape the spreadsheets they can already open, as them. It
works **inside** a spreadsheet: values, formulas, formatting, sheets,
charts, pivot tables, Connected Sheets, cell comments and notes.
Finding, sharing, moving and trashing files, and their revisions, belong
to a server built on the Drive API.

## Goals and non-goals

Goals:

- **A write never destroys what it did not see.** The target is read
  first. Overwriting anything non-empty, a formula above all, is refused
  until the call says so, naming the cells, and carries the checkpoint
  of what it replaces.
- **What Google changes is said.** Every write reports each value Google
  stored differently from how it was sent, and every delete names what
  it takes, including what Google removes without saying so.
- **The model never guesses an address.** Ranges are A1 throughout,
  sheet titles are read and quoted, and every read is a grid with its
  column letters and row numbers.
- **Nothing irreversible or outward-reaching runs unless you turn it
  on.** Deletes are not registered by default and need `confirm`, and a
  formula that makes Google fetch a URL needs a setting no argument can
  lift.
- **Reads are bounded.** Every read has a budget in cells and characters,
  resolved before the call, and says where to continue.
- **Act as you, and log nothing about your spreadsheets.** Every call
  runs as the signed-in account, and the logs never carry cell values,
  formulas, titles, ranges or search terms.

Non-goals:

- **Files, folders, sharing and revisions.** Drive is read only to find
  a spreadsheet by title, show its owner and modified time, and name the
  signed-in account.
- **Spreadsheet settings.** No tool changes a spreadsheet's title,
  locale or recalculation setting.
- **Changes pushed to it.** The Sheets API has no push channel or change
  feed.
- **Apps Script, add-ons or a calculation engine.** Formulas are written
  and read as text, and Google computes them.
- **A hosted service.** It speaks stdio only, so Claude on the web and on
  mobile cannot reach it.
- **Asking you before a call.** Your client approves each call; the
  server asks nothing.

## What makes this one different

- **Guarded writes.** A spreadsheet has no undo behind an API call, and
  the value you can see is rarely the whole cell: underneath it there
  may be a formula, a different stored type, or a note. `write_values`
  reads the target first and refuses to overwrite anything non-empty
  without `overwrite`, or a formula without `overwrite_formulas` as
  well.
- **It does the index arithmetic.** Google names the first sheet in the
  account's language, so it is often not an English word. Titles are
  read from the spreadsheet and quoted for you, and the model never sees
  a `GridRange`.
- **Coercion is reported.** `input: typed` parses as a person typing, so
  `007` becomes `7`; every value Google changed comes back named.
- **A curated surface, gated by registration.** A tool that is switched
  off is not registered, so it cannot be called at all.
- **Verifiable releases.** Signed checksums, build provenance, SBOMs and
  reproducible builds.

## Tools

The Scope column names what each tool needs. A spreadsheet named by its
title is found through Drive, under `drive.readonly`. In read-only mode,
`spreadsheets.readonly` takes the place of `spreadsheets`.

| Tool | What it does | Scope |
|---|---|---|
| `get_spreadsheet` | The spreadsheet card: title, link, locale, and every sheet's exact title, id, size, frozen rows, hidden state and what it holds, plus named ranges, tables, protected ranges and filter views. No cell data, so it costs the same on a spreadsheet of ten cells and one of ten million. Call it first | `spreadsheets` |
| `read_range` | An addressed grid of a range: column letters across the top, row numbers down the side. `show=both` prints each formula under the value it produced. Budgeted in cells and characters, with a continuation, and every read returns a checkpoint | `spreadsheets` |
| `read_formatting` | What a range looks like — number formats, fonts, colors, borders, alignment — per block of identically formatted cells rather than per cell, with the merges, conditional rules, banding, validation and notes that decide how a cell looks without being on the cell | `spreadsheets` |
| `search_spreadsheets` | Find a spreadsheet by part of its title, by text inside it, by owner or by when it changed | `drive.readonly` |
| `find_in_spreadsheet` | Search one spreadsheet for text or an RE2 pattern and get back A1 addresses, saying whether each match was in a value, in the formula under it, or in a note beside it | `spreadsheets` |
| `create_spreadsheet` | A new spreadsheet, optionally with extra sheets and seed values. Returns its card, including the id every later call needs | `spreadsheets` |
| `write_values` | Write a rectangle, refusing first anything the write would destroy that you cannot see, then reporting every value Google stored differently from how it was sent. An overwrite carries the checkpoint of what it replaces | `spreadsheets` |
| `append_rows` | Add rows after a block of data and report where they actually landed — Google decides the destination, and the same sheet given different ranges appends in different places. It inserts, so it never writes over a row | `spreadsheets` |
| `format_cells` | Number format, font, colors, borders, alignment, wrapping, merges and notes, applied in one atomic batch. A merge, a clear and a note are the three that take something away, and each is refused until acknowledged | `spreadsheets` |
| `manage_range` | Add, update or delete what is attached to a range: a named range, a protected range, a validation rule, a table and the types of its columns, banding, or a conditional format rule or color scale. Existing ones are named by the range they cover, not by an id | `spreadsheets` |
| `transform_range` | Sort, replace, trim, de-duplicate, split, shuffle, fill, copy or move a range — the operations that move data without you naming its new address, so each reads what it would land on first | `spreadsheets` |
| `manage_anchor` | Label a row, column or sheet so it can be found again after the spreadsheet has been edited around it. An anchor follows its row through inserts, deletes, moves and sorts, where an A1 address goes stale the moment somebody inserts a row. Pass `anchor:<name>` anywhere a range or band is taken, except in `delete_dimensions` and `clear_values`, which take the rows or range itself so the call shows what goes | `spreadsheets` |
| `manage_chart` | Add, update, move, delete or list charts and slicers. A chart floats above the grid, so adding one overwrites nothing, and its data is named in A1, one range per series. A listing reports which charts have lost their series — what deleting a charted column does, and what nothing in Sheets tells you | `spreadsheets` |
| `manage_pivot_table` | Add, update, delete or list pivot tables, with groups by date or number ranges, filters and calculated values. Columns are named in A1 or by their heading, never by counting, and every result reports the rectangle the table covers right now: the size is computed from the data rather than chosen. A listing reads every rule back in the spelling the tool takes | `spreadsheets` |
| `connect_data_source` | Off by default: connect a BigQuery query or table through Connected Sheets. Google makes a sheet for it and runs the query, charged to the Cloud project you name, now and on every refresh | `spreadsheets`, `bigquery.readonly` |
| `manage_data_source` | Off by default: refresh one Connected Sheets data source, cancel a refresh, or list what is connected. `get_spreadsheet` already says whether a spreadsheet has one, with no extra scope | `spreadsheets`, `bigquery.readonly` |
| `read_cell_comments` | The comment threads on a spreadsheet's cells: the cell each is on now, open or resolved, who it is assigned to, and every post. A thread follows its cell as rows move, and one whose row was deleted is still listed, without a cell | `spreadsheets` |
| `manage_cell_comment` | Comment on a cell, reply, edit a post, resolve or reopen. Google emails an assignee, and may notify an address the text names, which is how this server reaches someone outside the spreadsheet | `spreadsheets` |
| `manage_sheet` | Add, rename, duplicate, copy to another spreadsheet, hide, unhide, reorder, resize, freeze or color a sheet | `spreadsheets` |
| `edit_dimensions` | Insert, move, resize, auto-size, group, ungroup, hide or unhide rows and columns | `spreadsheets` |
| `delete_dimensions` | Destructive, off by default: remove rows or columns and the data on them, having counted what that is | `spreadsheets` |
| `clear_values` | Destructive, off by default: clear a range's values and keep its formatting, notes and validation rules | `spreadsheets` |
| `delete_sheet` | Destructive, off by default: delete a sheet and everything on it, having counted what that is | `spreadsheets` |
| `delete_cell_comment` | Destructive, off by default: delete a comment thread with its replies, or one reply. Resolving keeps it, and is usually what is wanted | `spreadsheets` |
| `delete_data_source` | Destructive, off by default: delete a Connected Sheets data source, the sheet Google made for it, and everything on that sheet; Google unlinks what was built on the source. The call names that sheet's title. Needs no BigQuery scope, so it reaches a source somebody else connected | `spreadsheets` |

The destructive tools are **not registered at all** unless
`GSHEETS_ENABLE_DESTRUCTIVE=true`, and each still needs `confirm: true`
on the call. `connect_data_source` and `manage_data_source` are not
registered unless `GSHEETS_ENABLE_DATA_SOURCES=true`, which is also what
makes `login` ask for the BigQuery scope. Connected Sheets needs a Cloud
project with BigQuery and billing before it does anything, so asking
everyone for that scope would be the wrong default.

### Resources

For a client that attaches a spreadsheet rather than calling a tool.
There is no static list, since listing a person's spreadsheets is a
Drive listing, and no subscriptions, since the Sheets API has no push.

| URI | What it is |
|---|---|
| `gsheets://{spreadsheet}` | The card: every sheet with its exact title, id and size, plus named ranges, tables and protected ranges. Reads no cells |
| `gsheets://{spreadsheet}/{sheet}` | That sheet's **used range** as CSV — the rows and columns holding something, not the sheet's allocated size. The sheet title is percent-encoded |

## Install

```bash
go install github.com/mmedum/google-sheets-mcp/v4/cmd/google-sheets-mcp@latest
```

Or take an archive for Linux, macOS or Windows, on amd64 or arm64, from
the [latest release](https://github.com/mmedum/google-sheets-mcp/releases/latest),
and [verify it](#verify-a-release) before you run it.

Claude Desktop users can open the `.mcpb` bundle from the same release.
It installs the server and asks for the settings a client's
configuration would carry: your OAuth client JSON, the profile,
read-only, the destructive switch, Connected Sheets and external
formulas. It covers macOS, Windows and Linux on both architectures.

The bundle does **not** log you in. Install the binary as well and run
`google-sheets-mcp login` as below; the bundle picks up the same
credentials. Leave its OAuth client field empty to use the client that
login recorded; a file you pick there must be that same client, since
the refresh token belongs to it.

## Sign in

You bring your own OAuth client.
[`docs/gcp-setup.md`](docs/gcp-setup.md) walks through it with the
reasons. In short, and in the order `doctor` checks things in:

1. Create a Google Cloud project.
2. Enable the **Google Sheets API** and the **Google Drive API**.
3. Configure the consent screen: **Internal** for a Workspace account,
   **External + Testing** for a consumer one, which means running
   `login` again weekly, because Google expires a testing app's refresh
   token.
4. On an External app, add the scopes `login` requests. Google asks for
   the list only from apps used by people outside your Workspace
   organization, so an Internal app can skip this step:

   | Scope | What it is for |
   |---|---|
   | `https://www.googleapis.com/auth/spreadsheets` | Everything this server does inside a spreadsheet |
   | `https://www.googleapis.com/auth/drive.readonly` | Finding spreadsheets, their owner and modified time, and the account's address; never a write |

   With `GSHEETS_ENABLE_DATA_SOURCES=true`, add a third:

   | Scope | What it is for |
   |---|---|
   | `https://www.googleapis.com/auth/bigquery.readonly` | `connect_data_source` and `manage_data_source` |

   With `GSHEETS_READ_ONLY=true`, `login` asks for
   `https://www.googleapis.com/auth/spreadsheets.readonly` in place of
   the first, and the write tools are not registered at all.
5. Create an **OAuth 2.0 Client ID** of type **Desktop app** and download
   the JSON.

Then log in, and check it:

```bash
google-sheets-mcp login -secret ./client_secret.json
google-sheets-mcp doctor
```

`login` prints the authorization URL and tries to open your browser.
The callback lands on `127.0.0.1` on a random port, with PKCE and
state. The refresh token goes into your OS keyring; if there is no
keyring, it goes into a `0600` file under your config directory and the
command says so. Add `-enable-data-sources=true` to log in for Connected
Sheets.

`doctor` checks the client JSON, the token, the granted scopes and one
call to Drive, and names what is missing. `status` says which account
is signed in and where the token lives. `status --json` prints the same
as one object for a script, with the account masked to its domain:
`credentials.configured` is the field to branch on, and
`schema_version` changes only when a field is removed or changes
meaning. `logout -yes` revokes the token at Google and deletes the
local copy.

Over SSH, the callback goes to the remote host's loopback address. Read
the port from the printed URL, where it appears as `127.0.0.1%3A<port>`,
forward it from a second local terminal, then open the URL locally:

```bash
ssh -N -L <port>:127.0.0.1:<port> user@remote-host
```

## Connect a client

Claude Code:

```bash
claude mcp add google-sheets -- google-sheets-mcp
```

Any client that takes a JSON server list:

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
binary's absolute path there if it is not on the app's `PATH`, or open
the `.mcpb` bundle instead.

## Configuration

Every setting is a `GSHEETS_*` environment variable with a flag of the
same name. [`docs/configuration.md`](docs/configuration.md) lists them
all, with profiles for keeping a work and a personal account apart.

## Safety

What registers depends on the settings the server starts with:

| Setting | Registers |
|---|---|
| `GSHEETS_READ_ONLY=true` | The read tools only, with read-only scopes |
| default | The reads and the writes that refuse to destroy what they cannot see |
| `GSHEETS_ENABLE_DESTRUCTIVE=true` | Adds the deletes, each needing `confirm: true` |
| `GSHEETS_ENABLE_DATA_SOURCES=true` | Adds the Connected Sheets tools, and the BigQuery scope at login |
| `GSHEETS_ALLOW_EXTERNAL_FORMULAS=true` | No tool; lets a write create a formula that fetches a URL or pulls in another spreadsheet |

Your MCP client approves each call; the server does not ask you itself.
claude.ai and Codex prompt for the tools marked destructive, which is
every write but those that only add: `create_spreadsheet`,
`append_rows` and `connect_data_source`. VS Code prompts for every tool
that writes. In Claude Code, `delete_data_source` asks on every call
whatever your rules say, since it takes a sheet and unlinks everything
built on the source. For the rest, list the calls you want to approve
one by one under `permissions.ask` in your settings, with the name you
gave the server in place of `google-sheets`. For example, the writes
that delete, start a billed query, or can email someone:

```json
{
  "permissions": {
    "ask": [
      "mcp__google-sheets__delete_sheet", "mcp__google-sheets__delete_dimensions",
      "mcp__google-sheets__clear_values", "mcp__google-sheets__delete_cell_comment",
      "mcp__google-sheets__delete_data_source", "mcp__google-sheets__connect_data_source",
      "mcp__google-sheets__manage_cell_comment"
    ]
  }
}
```

A client that runs with nobody watching can make every write its
settings register, so leave `GSHEETS_ENABLE_DESTRUCTIVE` and
`GSHEETS_ENABLE_DATA_SOURCES` off for one.

**A write never destroys what it cannot see.** Overwriting anything
non-empty needs `overwrite`, and a formula needs `overwrite_formulas` as
well, because a formula and its result look identical in a read. Each
refusal names the cells. An overwrite also needs `expect_checkpoint`,
which the refusal and a dry run give, so a collaborator's edit after
that read makes the write a `[conflict]` rather than something replaced
unseen. The guard also covers what Google removes without saying so:

- a **merge** keeps the top-left value of each merged block and
  discards the rest;
- **`clear_format`** removes formatting Sheets cannot bring back;
- a **note** set or cleared replaces something no values read shows;
- a **paste** or an **auto-fill** lands on cells you did not name, so
  the landing rectangle is read and guarded;
- a **`text_to_columns`** spills into the columns to its right;
- a **`find_replace` with `in_formulas`** rewrites what a cell computes;
- **deleting a table** takes every conditional format rule over its
  range;
- **deleting a row or column** takes any anchor on it, which
  `delete_dimensions` names first.

Beyond the guard:

- **Formulas that reach outside are the operator's call.** `IMPORTXML`,
  `IMPORTDATA`, `IMPORTHTML`, `IMPORTFEED`, `IMAGE` and `HYPERLINK` make
  Google fetch a URL, and `IMPORTRANGE` pulls in another spreadsheet.
  Every write refuses them unless the server was started with
  `GSHEETS_ALLOW_EXTERNAL_FORMULAS=true`.
- **The gated deletes name what they take**, in A1, so the call your
  client shows says what goes.
- **`dry_run` on every write.** Sheets has no suggestion mode, so the
  preview reports what the guard found and what would change, and sends
  nothing.
- **Sheet names are read, never assumed.** `Sheet1` does not exist on a
  Portuguese account, so no tool defaults a sheet, and a missing one is
  refused with the titles that do exist.

[`docs/security.md`](docs/security.md) has the threat model.

## Verify a release

Each release signs `checksums.txt` with a keyless Sigstore certificate
tied to the release workflow, attests every archive and the bundle, and
ships an SBOM per archive. Builds are reproducible: rebuilding a tag
gives the same bytes.

```bash
sha256sum -c checksums.txt --ignore-missing
cosign verify-blob checksums.txt --bundle checksums.txt.bundle \
  --certificate-identity "https://github.com/mmedum/google-sheets-mcp/.github/workflows/release.yml@refs/tags/<tag>" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify <archive> --repo mmedum/google-sheets-mcp
```

## Getting help

Run `google-sheets-mcp doctor` first: most first-run trouble is an API
that was never enabled or a consent screen without you on it.
[`docs/troubleshooting.md`](docs/troubleshooting.md) covers each
failure. Then open an issue with `doctor`'s output and the version.
**Never paste a spreadsheet id or URL, cell contents, a client JSON, a
token or a tool result**; describe the shape instead.

## Documentation

- [`docs/gcp-setup.md`](docs/gcp-setup.md): the Cloud project, the OAuth
  client and logging in.
- [`docs/configuration.md`](docs/configuration.md): every setting,
  profiles, and where state is stored.
- [`docs/troubleshooting.md`](docs/troubleshooting.md): symptoms and
  their fixes, then rotating the token, revoking access and handling a
  suspected exposure.
- [`docs/security.md`](docs/security.md): the threat model.
- [`docs/architecture.md`](docs/architecture.md): how it works, and the
  design decisions a contributor should not undo.
- [`docs/release.md`](docs/release.md): how a release is cut and
  checked.
- [`CHANGELOG.md`](CHANGELOG.md): what each release changed. The tool
  names, their arguments and their output fields are the contract, and
  anything you have to act on is marked **Breaking**.

## Contributing

[`CONTRIBUTING.md`](CONTRIBUTING.md) covers the gates, the live run and
the review flow. Security reports go through [`SECURITY.md`](SECURITY.md),
not an issue. Participation follows the
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

## License

Apache 2.0. See [`LICENSE`](LICENSE).
