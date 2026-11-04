# Security model

The threat model and the invariants the code holds.
[`SECURITY.md`](../SECURITY.md) says how to report a vulnerability.

It applies to one binary, running as a subprocess of your MCP client, on
your machine, with your own OAuth client and your own token, against
Google's APIs only (D-01, D-05). It has no listener except while `login`
waits, no telemetry and no update check.

## Trust boundaries

| Boundary | Crosses | Assumption |
|---|---|---|
| MCP client ↔ this server | An OS pipe | Both run as the same OS user, unauthenticated. |
| This server ↔ Google | TLS | Requests go only to `sheets.googleapis.com`, `www.googleapis.com`, `oauth2.googleapis.com` and `accounts.google.com`, checked with the port before a credential is attached (D-05). |
| This server ↔ disk | The file system | The config directory: the profile's state and, without a keyring, the token file (D-52). No tool reads or writes a file (D-40). |
| Browser ↔ the login listener | The loopback interface, on a random port | Alive only while `login` waits. PKCE and `state` are checked on the callback (D-51). |
| Spreadsheet authors ↔ the model | Every cell, formula, note and comment a tool returns | Written by collaborators, sometimes to steer an assistant (D-23). |

## What is worth stealing

- **The refresh token.** `spreadsheets` reads and writes every
  spreadsheet the account can open, and `drive.readonly` reads every
  file in its Drive (D-49). It lives in the OS keyring where there is
  one, and in an owner-only file where there is not (D-52).
- **Spreadsheet content.** It passes through tool results to your client
  and nowhere else.
- **The spreadsheets themselves.** Sheets has no undo through the API,
  and the API cannot restore version history.

## Adversaries

**A collaborator writing to steer your assistant.** The server cannot
stop a model being persuaded, so it limits what persuasion reaches. A
formula that makes Google fetch a URL can carry the spreadsheet's data
in its query string, so no write creates one unless the operator allows
it (D-28). Deletes are absent unless enabled (D-35), and nothing the
model sends widens the server's reach (D-40). A read shows formulas
rather than resolving them, so an injected one is visible (D-23).

**A malicious MCP client, or a persuaded model driving an honest one.**
It can call any registered tool under your credentials. A client may
ignore annotations, so registration is the control. `confirm: true` is
a witness the model writes, never your approval (D-35, D-37). Past that,
Google's permissions and the guards below are the bound.

**A co-resident process.** It cannot finish a login without the PKCE
verifier, and the token file is readable by your account alone.

## Invariants the code holds

A test or a gate holds each of these. A way to break one is a
vulnerability.

- **Stdout carries only JSON-RPC.** forbidigo, the smoke test and
  `TestVersionFlagPrintsToStdoutAndNothingElse` hold it (D-06).
- **Credentials go only to Google's hosts.** `TestGoogleOnlyAllowlist`
  and `TestCredentialsGoNowhereElse` hold it (D-05).
- **Logs never carry the payload**, and a transport error loses its URL.
  `TestLogsCarryNothingFromTheSpreadsheet` and
  `TestTransportErrorsCarryNoURL` hold it (D-07).
- **What is meant to be pasted is masked.** `doctor`, `status` and a
  debug log mask the account and the client id; a tool result is not
  masked. `TestAccountIsStoredWholeAndPrintedMasked` holds it (D-08).
- **A write never destroys what it did not see.** Non-empty cells need
  `overwrite`, formulas `overwrite_formulas` too, and an overwrite
  carries the checkpoint of what it replaces.
  `TestWriteRefusesWhatItWouldDestroy` and
  `TestOverwriteNeedsTheCheckpointOfWhatItReplaces` hold it (D-24, D-25).
- **No write creates a formula that reaches outside** unless
  `GSHEETS_ALLOW_EXTERNAL_FORMULAS` is set, a replacement or a copy
  included. `TestExternalFormulasAreOffUnlessTheOperatorSaysSo` and
  `TestAFormulaRewriteCannotReachOutside` hold it (D-28).
- **The deletes are absent without `GSHEETS_ENABLE_DESTRUCTIVE`, need
  `confirm`, name what they take, and are tools of their own.**
  `TestDeleteIsOffUnlessDestructiveIsOn`,
  `TestDeleteSheetCountsBeforeItAsks` and `TestDeleteIsNotAManageAction`
  hold it (D-35, D-36, D-42).
- **No tool widens the server's reach**: its settings, the credentials,
  the profile, a file or the environment.
  `TestNoToolCanWriteTheServersSettings` holds it (D-40).
- **Read-only mode asks for read-only scopes and registers only the
  reads**, and `bigquery.readonly` is asked for only with
  `GSHEETS_ENABLE_DATA_SOURCES`. `TestReadOnlyModeKeepsTheReads` and
  `TestScopesAreOptIn` hold it (D-50, D-43).
- **A dry run sends nothing.** Each write returns before sending, held
  by a "sends nothing" test per tool, and
  `TestDryRunIsFoundByReflectionNotByMemory` holds that every write
  takes the flag (D-30).
- **A write that may have landed is never repeated.**
  `TestUnrepeatableWritesAreNotRepeated` holds it (D-45).
- **A login checks `state` and uses PKCE.**
  `TestLoginRefusesAMismatchedState` holds it (D-51).
- **The token file is owner-only and warns on every use**, and `logout`
  revokes at Google and never touches the environment's token.
  `TestKeyringThenFile` and `TestEnvironmentWinsAndIsNotDeletable` hold
  it (D-52).
- **The annotations are exact, and the server asks the person
  nothing.** `TestTheHintsAreExact` and `TestOnlyDeleteDataSourcePrompts`
  hold it (D-37, D-38, D-39).
- **Nothing in the repository identifies a real spreadsheet, account or
  person.** `make leaks` holds it (D-09). Fixtures are generated (D-10),
  and the live driver reads only what it wrote (D-11).

## Not a vulnerability

Do not report these as vulnerabilities. Open an ordinary issue if one
surprises you.

- **The client did not prompt.** Annotations and the
  `requiresUserInteraction` mark are hints. A delete registered while
  `GSHEETS_ENABLE_DESTRUCTIVE` is off is a vulnerability.
- **What runs as you can act as you.** Any process running as you can
  read the token and use it, with or without this server.
- **The scopes reach every spreadsheet and file the account can.**
  Google offers nothing narrower that a stdio server can use:
  `drive.file` reaches only files the app created or the person picked
  in the Drive Picker.
- **A tool result is not masked.** It is your data, returned to you.
- **A formula already in a spreadsheet fetches a URL.** The setting
  governs what this server writes; Google evaluates what is there.
- **A model following instructions in content.** That is the client's
  concern. Content that makes the server itself break an invariant is a
  vulnerability.
- **Someone else's code.** A flaw in Google's APIs goes to Google, and
  one in a dependency to that project. We will help route it.

## Dependency vulnerabilities

`govulncheck` runs in `make check` and in CI on every pull request and
push to `main`. It fails when this server's code can reach a vulnerable
symbol.

A CVE in a dependency is a vulnerability here only when `govulncheck`
reports it reachable. It is then fixed in a release whose CHANGELOG
entry names the CVE or GHSA. Otherwise the dependency is updated in the
normal course, and a report about it is not a vulnerability.

## What you are responsible for

- **Your own OAuth client.** Nothing is shipped in the binary, so
  revoking yours affects no one else.
  [`troubleshooting.md`](troubleshooting.md#tasks) says how to rotate
  the token, revoke access and handle a suspected exposure.
- **The switches you turn on.** `GSHEETS_ENABLE_DESTRUCTIVE` registers
  tools that cannot be undone, `GSHEETS_ENABLE_DATA_SOURCES` starts
  queries billed to a Cloud project, and
  `GSHEETS_ALLOW_EXTERNAL_FORMULAS` lets a write make Google fetch a
  URL. Leave them off for a client that runs with nobody watching, and
  use read-only mode, logged in again, where reading is enough.
- **Your client's approvals.** `manage_cell_comment` can make Google
  email an assignee. Give it and the deletes an `ask` rule, as the
  README shows.
- **Where the token lives.** Keep a keyring available, and keep
  `GSHEETS_REFRESH_TOKEN` out of shared shells and logs.
- **What you paste.** `doctor`, `status` and a debug log are built to be
  safe. A token, `token.json`, a client JSON and a tool result are not.
