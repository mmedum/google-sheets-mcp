# google-sheets-mcp

A [Model Context Protocol](https://modelcontextprotocol.io) server for
Google Sheets, written in Go: read a range and see where every value
sits, write without destroying the formula underneath, reshape sheets and
dimensions, format, sort and validate, and get told exactly what changed
and what Google converted on the way in.

Single binary, stdio, one Google account per profile. You run it against
a Google Cloud project you own, so nothing about this repository is tied
to any particular organisation or account.

It works inside a spreadsheet. Finding, sharing, moving and trashing
files, and their comment threads and revisions, belong to a Drive server
built on the Drive API. A cell **note** is a Sheets field and is here.

**Status: phase 2, not yet released.** Reading, writing, formatting and
the objects attached to a range all work, and phase 2 has not yet been
run against a real account — see the status line in
[docs/architecture.md](docs/architecture.md). Resources, durable anchors and
the agent evals are phase 3; charts and pivot tables phase 4. The design,
the platform constraints it is built on, the decided trade-offs, the
phase plan and the evidence log are all in that document.

## Tools

| Tool | What it does |
|---|---|
| `get_spreadsheet` | The spreadsheet card: title, link, locale, and every sheet's exact title, id, size, frozen rows, hidden state and what it holds, plus named ranges, tables, protected ranges and filter views. No cell data, so it costs the same on any size of spreadsheet. Call it first. |
| `read_range` | An addressed grid of a range: column letters across the top, row numbers down the side. `show=both` prints each formula under the value it produced. Budgeted in cells and characters, with a continuation, and every read returns a checkpoint. |
| `search_spreadsheets` | Find a spreadsheet by part of its title, by text inside it, by owner or by when it changed. The only Drive call this server makes. |
| `find_in_spreadsheet` | Search one spreadsheet for text or an RE2 pattern and get back A1 addresses, saying whether each match was in a value, in the formula under it, or in a note beside it. |
| `create_spreadsheet` | A new spreadsheet, optionally with extra sheets and seed values. Returns its card, including the id every later call needs. |
| `write_values` | Write a rectangle, refusing first anything the write would destroy that you cannot see, then reporting every value Google stored differently from how it was sent. |
| `append_rows` | Add rows after a block of data and report where they actually landed — Google decides the destination, and the same sheet given different ranges appends in different places. |
| `manage_sheet` | Add, rename, duplicate, copy to another spreadsheet, hide, unhide, reorder, resize, freeze or colour a sheet. |
| `edit_dimensions` | Insert, move, resize, auto-size, group or ungroup rows and columns. |
| `read_formatting` | What a range looks like — number formats, fonts, colours, borders, alignment — summarised per block of identically formatted cells, with the merges, conditional rules, banding, validation and notes that decide how a cell looks without being on the cell. |
| `format_cells` | Number format, font, colours, borders, alignment, wrapping, merges and notes, applied in one atomic batch. A merge, a clear and a note replacement are the three that take something away, and each is refused until acknowledged. |
| `manage_range` | Add, update or delete what is attached to a range: a named range, a protected range, a validation rule, a table, banding, or a conditional format rule. Existing ones are named by the range they cover, not by an id. |
| `transform_range` | Sort, replace, trim, de-duplicate, split, shuffle, fill, copy or move a range — the operations that move data without you naming its new address, so each reads what it would land on first. |
| `delete_dimensions` | Destructive, off by default: remove rows or columns and the data on them, having counted what that is. |
| `clear_values` | Destructive, off by default: clear a range's values and keep its formatting, notes and validation rules. |
| `delete_sheet` | Destructive, off by default: delete a sheet and everything on it, having counted what that is. |

Charts, pivot tables and Connected Sheets data sources arrive in v0.4.0.

## What makes it different

- **Every read shows addresses.** A grid with column letters and row
  numbers, so the model can write back to what it just read without
  counting. There is nothing to remember between calls and nothing to go
  stale.
- **A1 is the contract.** The model never sees a `GridRange`. The
  zero-based half-open arithmetic lives in one package with table tests,
  including the trap where a named range silently shadows a sheet of the
  same name in an unquoted reference.
- **Sheet names are read, never assumed.** Google names the first sheet
  in the account's language, so `Sheet1` does not exist on a Portuguese
  account. No tool defaults a sheet and no description offers one as an
  example; a missing sheet is refused with the titles that do exist.
- **Reads are budgeted before the call.** The window is resolved against
  the sheet's real extent and a finite range is sent, so an open-ended
  `A:Z` never becomes a whole column in memory.
- **Errors keep Google's meaning.** `[class] message`, with a closed
  vocabulary a gate holds shut from both sides.

And the reason this server exists: **a write never destroys what it
cannot see.** Overwriting anything non-empty needs `overwrite`;
overwriting a formula needs `overwrite_formulas` as well; protected
ranges and partial merges are refused before the request is built, with
the cells named. Sheets has no undo, so a gate there is the only one
there is.

- **Coercion is reported, never hidden.** `input: typed` parses as a
  person typing, so `007` becomes `7` and `2026-09-05` becomes a date.
  Every write reads back what Google stored and names each value it
  changed — and for a date, what the cell still displays, so a serial
  number does not read as data loss. `input: literal` stores exactly
  what you send, and the server never substitutes one for the other.
- **`dry_run` on every write.** Sheets has no suggestion mode, so the
  preview reports what the guard found and what would change, having
  sent nothing.

## Install

Download an archive from the
[releases](https://github.com/mmedum/google-sheets-mcp/releases), or:

```
go install github.com/mmedum/google-sheets-mcp/cmd/google-sheets-mcp@latest
```

## Set up

You need your own Google Cloud project. In this order, which is also the
order `doctor` checks:

1. Create a Cloud project.
2. Enable the **Google Sheets API** and the **Google Drive API** (the
   second one only for `search_spreadsheets`).
3. Configure the consent screen: **Internal** for a Workspace account,
   **External + Testing** for a consumer one — which means re-running
   `login` about weekly.
4. Add the scopes: `.../auth/spreadsheets` and
   `.../auth/drive.readonly`.
5. Create a **Desktop app** OAuth client and download its JSON.
6. `google-sheets-mcp login -secret /path/to/client_secret.json`
7. `google-sheets-mcp doctor`

Then point your client at it:

```json
{
  "mcpServers": {
    "google-sheets": {
      "command": "google-sheets-mcp"
    }
  }
}
```

Settings are `GSHEETS_*` environment variables, each with a flag of the
same name; see [docs/configuration.md](docs/configuration.md).
`GSHEETS_READ_ONLY=true` requests read-only scopes and registers only the
read tools.

## Documentation

- [docs/architecture.md](docs/architecture.md) — the design, the platform
  constraints, the decisions and the evidence log.
- [docs/configuration.md](docs/configuration.md) — every setting.
- [docs/security.md](docs/security.md) — what is stored, what is logged,
  and what the server refuses to do.
- [docs/development.md](docs/development.md) — building, the gates, and
  releasing.

## Licence

Apache-2.0.
