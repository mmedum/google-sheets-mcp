# Configuration

Every setting is a `GSHEETS_*` environment variable with a command-line
flag of the same name. A flag given explicitly beats the environment, and
the environment beats the built-in default.

Environment variables come first because that is what MCP clients pass:
Claude Code, Claude Desktop and Cursor all hand a stdio server a
`command`, its `args` and an `env` map, and nothing else. Everything is
validated once at start, so a misconfigured server fails before it
announces itself rather than on the first call.

## Settings

| Variable | Flag | Default | What it does |
|---|---|---|---|
| `GSHEETS_PROFILE` | `-profile` | `default` | Names a set of stored credentials, account and settings. Two profiles never share a token. Lower case, digits, `-` and `_`. |
| `GSHEETS_CLIENT_SECRET` | `-client-secret` | (stored) | Path to the Desktop-app OAuth client JSON. Normally set once by `login` and remembered. |
| `GSHEETS_REFRESH_TOKEN` | — | (unset) | A refresh token from the environment, for CI and automation. It wins over the keyring and the file, and `logout` does not touch it. |
| `GSHEETS_CONFIG_DIR` | — | `os.UserConfigDir()/google-sheets-mcp` | Where the profile's non-secret state and the fallback token file live. |
| `GSHEETS_LOG_LEVEL` | `-log-level` | `info` | `debug`, `info`, `warn` or `error`. Logs go to stderr; stdout carries JSON-RPC frames and nothing else. |
| `GSHEETS_LOG_FORMAT` | `-log-format` | `text` | `text` or `json`. |
| `GSHEETS_READ_ONLY` | `-read-only` | `false` | Requests read-only scopes and registers only the read tools. This is a real restriction rather than a label: with `spreadsheets.readonly` the API itself refuses the data-filter methods and both developer-metadata reads. |
| `GSHEETS_ENABLE_DESTRUCTIVE` | `-enable-destructive` | `false` | Registers the destructive tools at all. Each of them still needs `confirm: true` on the call. Sheets has no undo and the API cannot restore version history, so leaving this off is the safe default. |
| `GSHEETS_MAX_CELLS` | `-max-cells` | `5000` | The default cell budget for a read; a call may ask for less, or for up to 50000. Applied before the request, so an open-ended range never becomes an unbounded fetch. |
| `GSHEETS_MAX_CHARS` | `-max-chars` | `20000` | The default character budget for a rendering; up to 400000. |
| `GSHEETS_HTTP_TIMEOUT` | `-http-timeout` | `60s` | Per attempt on a read. |
| `GSHEETS_WRITE_TIMEOUT` | `-write-timeout` | `180s` | For a write. It matches the 180 seconds Sheets itself allows a request to run before it times out, so a slow batch is the platform working rather than this server hanging. |

Operational flags: `--version` prints the version and exits;
`--dump-schemas` prints the tool schemas as JSON and exits, which is what
the schema diff in CI compares.

## Profiles

```
GSHEETS_PROFILE=work google-sheets-mcp login -secret ~/work-client.json
GSHEETS_PROFILE=work google-sheets-mcp doctor
```

The default profile lives directly in the config directory; any other
lives under `profiles/<name>/`. Each has its own client secret path,
account, token and granted scopes.

**Profiles separate storage, not grants.** Google revokes the grant
rather than the individual token, so `logout` under one profile signs the
account out of **every** profile using the same OAuth client — verified
live, where signing out of a scratch profile stopped the default one
working. `logout` names the profiles it will affect before doing it, and
`logout -local` deletes this profile's stored token without revoking, so
the others keep working. To keep two profiles genuinely independent, give
them different OAuth clients or different Google accounts.

## Client configuration

```json
{
  "mcpServers": {
    "google-sheets": {
      "command": "google-sheets-mcp",
      "env": {
        "GSHEETS_PROFILE": "work",
        "GSHEETS_MAX_CELLS": "8000"
      }
    }
  }
}
```

## Budgets

A read is bounded twice, and the first bound is the one that matters.
`max_cells` resolves the window against the sheet's real extent **before
the request is built**, so `A:Z` on a large sheet becomes a finite range
rather than a whole column in memory. `max_chars` then bounds the
rendering, cutting at a row boundary so no address in the grid points at
a value that is not shown. Either bound reports where to continue, and
the read says which one stopped it.

## What is not configurable

The API host allowlist, the retry policy and the rate limiters. The
limiters are set to Google's documented per-user quota — 60 reads and 60
writes a minute — with one request in flight at a time, which is Google's
own advice for avoiding 503s. Raising them would not raise the quota.
