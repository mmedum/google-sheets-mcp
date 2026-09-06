# Security

## What this server talks to

Only Google, over HTTPS: `sheets.googleapis.com`, `www.googleapis.com`,
`oauth2.googleapis.com` and `accounts.google.com`. The allowlist is
checked **with the port** before any credential is attached, so a
redirect or a misconfigured base URL cannot send an access token
somewhere else. There is no telemetry and no update check.

## Scopes

Full access asks for `.../auth/spreadsheets` and
`.../auth/drive.readonly`. Read-only mode asks for
`.../auth/spreadsheets.readonly` and `.../auth/drive.readonly`.

`drive.file` is deliberately not requested: it reaches only files the app
created or the person picked through the Drive Picker, which a stdio
server cannot show. Full `drive` is not requested either, because this
server does not manage files.

Drive is used for exactly one thing: turning a spreadsheet title into an
id. A missing scope becomes `[forbidden]` naming the scope and telling
you to run `login` again.

## Where the token lives

The refresh token goes to the OS keyring — Secret Service on Linux,
Keychain on macOS, Credential Manager on Windows. If there is no keyring,
it goes to a `0600` file under the profile directory and the server warns
about that on **every use**, not once at login. `GSHEETS_REFRESH_TOKEN`
overrides both, for CI.

`logout` revokes the token at Google and deletes the local copy. It does
not touch the environment override: deleting a token this process does
not own would be a surprise.

## What gets logged

Logs go to stderr through `slog`. Stdout carries JSON-RPC frames and
nothing else, held by a smoke test rather than by discipline.

A log line carries the method, the tool name, the outcome, the duration
and a **truncated** spreadsheet id. It carries no cell values, no
formulas, no sheet or spreadsheet titles, no ranges, no addresses and no
search terms.

Two routes make that harder than it sounds, and both are closed
deliberately:

- **A search term travels inside a request URL.** So requests are logged
  by operation name rather than by URL, and a transport error's own text
  — which always names the URL it was calling — is dropped and replaced
  with the kind of failure it was.
- **This server's own error messages quote the spreadsheet back.** A
  `[not_found]` that lists the sheet titles which do exist is the right
  answer to give a model and the wrong thing to write into a log. A tool
  error is returned, never logged as text; the log line carries whether
  the call refused, not what it said.

A test drives every registered tool at debug level against a fixture
whose every value is a word that exists nowhere else, and fails if any of
it reaches the log — refusals included, because a refusal is where a
message is most tempted to quote what it refused. The forbidden list is
read out of the fixture rather than typed, so a value added to the
fixture is covered from the moment it is added, and a second test fails
if a registered tool is not in the table the first one drives.

The truncated id is the one identifier that may appear. It is the
correlation key an operator needs to trace a retry back to the call that
caused it, and six characters of a 44-character base64url id cannot be
looked up or pasted into a URL.

This is what makes it safe to ask for a debug log in a bug report.

The same rule applies to `doctor` and `status`, and it has to be stated
separately, because a guarantee about logs is not a guarantee about the
product. They mask the account address and the OAuth client id; and no
check reports a sheet title, a range that carries one, or a cell's
contents — `doctor -spreadsheet` says how many sheets there are and that
A1 of the first one could be read, and discards the rest. A title has no
shape a redactor could match, which is why that path is removed rather
than filtered.

## What a tool result carries

A tool result is not masked, and that is deliberate. `read_range`
returns your cells; `search_spreadsheets` returns titles, ids and the
owner's address. You asked for them, the server holds your own OAuth
token, and you can see all of it in the Sheets or Drive interface — so
masking would degrade an answer without removing anybody's access. The
owner in particular is how an `[ambiguous]` refusal lets you tell two
spreadsheets of the same name apart.

What the server does instead is **not fetch what nothing needs**. The
Drive field masks ask for the title, id, MIME type, times, owner and
link, and no longer for `parents`: a raw folder id is unusable — no tool
here takes a folder, and turning one into a name needs a Drive call this
server does not make — so it was an identifier fetched and printed
because it happened to be in the response. A field mask is where
minimisation is cheap.

The risk that is real is the **paste path**: a result copied into a
public issue. That is why the issue form says to describe a tool result
rather than paste it, and why the three artefacts that *are* meant to be
pasted — a debug log, `doctor` and `status` — are the ones that mask.

## What the server refuses to do

- **Destructive tools are not registered** unless
  `GSHEETS_ENABLE_DESTRUCTIVE=true`, and each still needs `confirm: true`
  on the call. Tool annotations are hints the specification says a client
  may treat as untrusted, and a host in an auto-approve permission mode
  runs an annotated tool without prompting anybody — so every gate is
  server-side. Client-side approval is not one of the layers here.
- **A write never destroys what it cannot see.** Anything
  non-empty needs `overwrite`; a formula needs `overwrite_formulas` as
  well; a protected range or a partially covered merge is refused before
  the request is built, with the obstacle named in A1. Sheets has no
  undo and the API cannot restore version history, so this is the only
  guard there is. A formula that evaluated to an error is still a
  formula and still needs the second acknowledgement: a live run found
  the version of this check that tested the cell's *kind*, which
  `#REF!` overwrites.
- **A formula that reaches outside the spreadsheet needs saying so.**
  `IMPORTXML`, `IMPORTDATA`, `IMPORTHTML`, `IMPORTFEED`, `IMAGE` and
  `HYPERLINK` take an arbitrary URL, which Google fetches from its own
  servers with whatever the sheet puts in the query string: writing one
  is an outbound request with the spreadsheet's contents attached, made
  by a machine the person cannot see. `IMPORTRANGE` is the other
  direction — it takes a spreadsheet URL and cannot exfiltrate, but it
  pulls any spreadsheet the signed-in account can read into this one.
  Both need `allow_external_formulas: true`, for those two different
  reasons, and every formula a write creates is named in the result.
- **Content is data, never instructions.** A read returns what the cells
  hold and the server never acts on it. Formulas are shown rather than
  resolved, so an injected one is visible instead of hidden behind its
  value.

## What is in this repository

Nothing deployer-specific: no spreadsheet ids or URLs, no account
addresses, no Cloud project ids, no OAuth client ids or secrets, no cell
values, formulas, notes, titles or named ranges from a real spreadsheet.

Two gates, because they catch different things. **gitleaks** covers
credentials, in the pre-commit hook and in CI. **`go run ./scripts/gates
leaks`** covers identifiers and data, which are not secrets and pass a
credential scanner untouched — a spreadsheet id in a fixture leaks what
somebody works on rather than a password. Every rule in it is an
allow-list: a deny-list naming the domain or the organisation to watch
for would itself be the disclosure.

What a pattern cannot do is the other half. A cell value, a sheet title,
a named range and a note are ordinary words, and no regex separates an
invented column heading from somebody's customer list. So those are made
structurally impossible instead: **fixtures are generated, never
recorded** — there is no path by which a real value reaches `testdata/` —
and **the live driver reads only a spreadsheet it created and filled
itself**, so a transcript carries nobody's data.

## Reporting a vulnerability

Open a [private security
advisory](https://github.com/mmedum/google-sheets-mcp/security/advisories/new)
rather than an issue. Please do not include anything from a real
spreadsheet.
