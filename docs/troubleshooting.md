# Troubleshooting

What to do when something does not work, then the maintenance tasks.
Setting up in the first place is [`gcp-setup.md`](gcp-setup.md). Start
with these two commands:

```bash
google-sheets-mcp status
google-sheets-mcp doctor
```

`status` prints the profile, the config directory, the account, the
token store and the granted scopes. `doctor` checks, in order, the OAuth
client JSON, the refresh token, the granted scopes and the Drive API,
and `doctor -spreadsheet <id, URL or title>` adds the Sheets API. A
check that fails prints `FAIL`, the reason and what to do; a check that
depends on it is skipped rather than failed. Both mask the client id and
the account, so their output is safe to paste.

Run both with the profile and environment your MCP client gives the
server, since each checks what it is given.

## `auth: client secret JSON is not a Desktop app client (expected an "installed" section)`

`login` fails with this, and `doctor` fails `OAuth client JSON` the same
way. A wrong path fails as `auth: read client secret …`.

**Cause:** the file is a **Web application** client, or not a client
JSON at all. The redirect goes to a loopback port chosen at run time,
which only a Desktop app client accepts.

**Fix (resolves it):** create an **OAuth 2.0 Client ID** of type
**Desktop app** in the same project, download its JSON, and run
`google-sheets-mcp login -secret <path>`.

## `auth: timed out waiting for the browser`

**Cause:** the browser never reached the callback. Over SSH, the
callback goes to the remote machine's loopback interface while the
browser runs on yours. Otherwise Google showed an error page rather
than returning to the callback.

**Fix (resolves it):** over SSH, read the port from the printed URL,
where it appears as `127.0.0.1%3A<port>`, and forward it from a second
local terminal before opening the URL:

```bash
ssh -N -L <port>:127.0.0.1:<port> user@remote-host
```

For an error page, read it. An External app in **Testing** signs in only
the accounts listed under **Test users**.

## `auth: Google returned no refresh token; remove the app at https://myaccount.google.com/permissions and log in again`

**Cause:** Google answered the sign-in without a refresh token, and
the server cannot call Google again later without one.

**Fix (resolves it):** remove the app at
[Google account permissions](https://myaccount.google.com/permissions),
then run `google-sheets-mcp login` again.

## `` [auth] google auth: no_credentials: run `google-sheets-mcp login` ``

Every tool answers this, and the server keeps running (D-55).

**Cause:** this profile has no stored token. Either nobody logged in
under it, or the MCP client starts the server with another profile or
config directory than your shell.

**Fix (resolves it):** run `google-sheets-mcp login` with the same
`GSHEETS_PROFILE` and `GSHEETS_CONFIG_DIR` as the client entry.

## `[auth] google auth: invalid_grant: …`

Every tool fails with this, and `doctor` fails `refresh token`.

**Cause:** Google refuses the refresh token. On an **External**
app left in **Testing**, Google expires it after a week. It also dies
when the access is removed in the Google account, the OAuth client is
deleted, or `logout` ran under another profile using the same client
and account, since Google revokes the whole grant (D-53).

**Fix (resolves it):** run `google-sheets-mcp login`. To stop the weekly
expiry, use a Workspace account with an **Internal** consent screen, or
move the app to **In production**.

## `[forbidden] … the granted scopes do not cover this call`

`doctor` fails `granted scopes` with `not granted: …`, and `login` warns
`Warning: these scopes were not granted: …`.

**Cause:** a scope was never granted. A box was left unticked at login,
an External app's consent screen lacks the scope, or a setting asks for
a scope the stored token predates: `GSHEETS_READ_ONLY` and
`GSHEETS_ENABLE_DATA_SOURCES` both change the set. A token never gains a
scope.

**Fix (resolves it):** add the scope to the consent screen if the app is
External, as [`gcp-setup.md`](gcp-setup.md#4-add-the-scopes) lists. Then
run `login` again under the configuration the client uses, and tick
every box.

## `… Please include bigquery.readonly scope`

**Cause:** a data source call ran on a token without
`bigquery.readonly`, which `login` asks for only with
`GSHEETS_ENABLE_DATA_SOURCES=true` (D-43).

**Fix (resolves it):** run
`google-sheets-mcp login -enable-data-sources=true`, and set the same
setting in the client entry.

## `doctor` fails `Drive API`: `enable the Google Drive API in your Cloud project`

**Cause:** the Drive API is not enabled in the project that owns the
OAuth client, or `drive.readonly` was not granted. Drive finds a
spreadsheet by title, supplies the owner and modified time on the card,
and names the signed-in account (D-49, D-54).

**Fix (resolves it):** enable the **Google Drive API** under **APIs &
Services → Library**, then run `doctor` again. If it still fails, treat
it as a missing scope, above.

**Workaround:** pass a spreadsheet's id or URL rather than its title.

## `doctor` fails `Sheets API: get_spreadsheet`: `enable the Google Sheets API in your Cloud project`

**Cause:** the Sheets API is not enabled in the project, or the
spreadsheet passed with `-spreadsheet` is not one the account can open.

**Fix (resolves it):** enable the **Google Sheets API** under **APIs &
Services → Library**. If it is enabled, try a spreadsheet you can open
in the browser.

## `credentials: the profile records a refresh token in the OS keyring and the keyring returned nothing. …`

**Cause:** the keyring holds the token and will not hand it over. It is
more likely locked, or its session bus unreachable, than empty.

**Fix (resolves it):** unlock the keyring and try again.

**Workaround:** `login` writes a fresh token beside the first, which
leaves the cause in place.

## `keyring unavailable (…); refresh token saved in plaintext at … (mode 0600)`

Every later use warns `refresh token read from the plaintext file …`.

**Cause:** the OS keyring could not be reached at login, such as on a
headless Linux machine with no Secret Service running (D-52).

**Fix (resolves it):** start a keyring, or log in from a desktop
session, and run `login` again. The token moves to the keyring and the
file is deleted.

**Impact:** the file is a live credential. Keep it out of backups that
leave the machine, do not copy the home directory to another host, and
remove it with `logout -yes` rather than by deleting the file, so the
grant is revoked.

## `config: invalid: GSHEETS_REQUIRE_PROMPT was removed: …`

The server refuses to start.

**Cause:** the setting, or `-require-prompt`, is set to anything but
`false`. The server asks the person nothing, and the client approves
each call (D-37).

**Fix (resolves it):** unset it. For a client that runs with nobody
watching, leave `GSHEETS_ENABLE_DESTRUCTIVE` and
`GSHEETS_ENABLE_DATA_SOURCES` off.

## A tool you expect is missing

**Cause:** registration follows the settings the server started with,
not the call (D-35, D-43, D-50). The deletes register only with
`GSHEETS_ENABLE_DESTRUCTIVE=true`, the data source tools only with
`GSHEETS_ENABLE_DATA_SOURCES=true`, and `GSHEETS_READ_ONLY=true` leaves
only the reads. `status` prints whether read-only and the deletes are
on.

**Fix (resolves it):** set the variable in the client's entry for this
server, not in your shell, and restart the client.

## `[blocked] … this server does not write one unless the person running it sets GSHEETS_ALLOW_EXTERNAL_FORMULAS`

**Cause:** the write would create `IMPORTXML`, `IMPORTDATA`,
`IMPORTHTML`, `IMPORTFEED`, `IMAGE`, `HYPERLINK` or `IMPORTRANGE`, and
no argument allows that (D-28).

**Fix (resolves it):** if you mean to allow such formulas, set
`GSHEETS_ALLOW_EXTERNAL_FORMULAS=true` in the client entry and restart
it. [`security.md`](security.md) says what they can send.

## `[conflict] the checkpoint does not match …`

**Cause:** the cells changed after the read that gave the checkpoint,
or the checkpoint came from a read of another range (D-25).

**Fix (resolves it):** read the range again, check what changed, and
pass the checkpoint that read returns.

## `[rate_limited] … this is Google's per-minute quota, which refills, so retry shortly`

**Cause:** the account spent its Sheets quota for the minute. The server
already paces requests at the documented per-user quota (D-46), so
another program on the same account is the usual cause.

**Fix:** wait a minute and retry.

## `[ambiguous_outcome] … may have been applied; read the spreadsheet before repeating it`

**Cause:** Google failed or timed out on a write that cannot be repeated
safely, such as an append or a `batchUpdate`, so the server cannot know
whether it landed (D-45).

**Fix (resolves it):** read the target and see whether the write is
there before sending it again.

**Impact:** repeating an append that landed duplicates its rows.

## Tasks

### Rotating the token

Log in again:

```bash
google-sheets-mcp login
```

The new token replaces the old one in the same store. The old one is
not revoked, since it is the same grant, issued again.

### Revoking access

```bash
google-sheets-mcp logout -yes
```

This revokes the token at Google and deletes the local copy. Without
`-yes`, `logout` says what it would do, naming every profile that shares
the OAuth client and account, and stops. Revoking ends every token from
that grant, on every machine and every such profile (D-53).

To forget the token here and leave the rest working:

```bash
google-sheets-mcp logout -local -yes
```

`logout` never touches `GSHEETS_REFRESH_TOKEN`.

### Suspected exposure

If a refresh token may have leaked, such as in a copied home directory,
a shared machine or a `token.json` in a backup:

1. Run `google-sheets-mcp logout -yes` on any machine that still has it.
   This revokes the grant everywhere.
2. If you cannot, remove the OAuth client's access at
   [Google account permissions](https://myaccount.google.com/permissions).
3. Run `google-sheets-mcp login` on the machines that should keep
   working.

A leaked client JSON grants nothing on its own: a desktop client's
secret is not confidential, and a sign-in still needs the account's
consent. To rotate it anyway, create a new Desktop app client, delete
the old one, and run `login -secret <new path>`. Deleting the old client
invalidates its tokens.

### Moving to another machine

Do not copy the token. Install the binary, copy the OAuth client JSON,
and run `login`. The keyring entry does not move, and a copied
`token.json` is a second live copy of a credential.

### Two accounts on one machine

Profiles keep them apart:

```bash
GSHEETS_PROFILE=work google-sheets-mcp login -secret ./work_client.json
GSHEETS_PROFILE=personal google-sheets-mcp login -secret ./personal_client.json
```

Each profile has its own token, account and granted scopes. Point each
client entry at its profile with the same variable. Profiles separate
storage, not grants: give the two different OAuth clients or accounts
if a `logout` in one must not sign out the other.

### Reporting a problem

Open an [issue](https://github.com/mmedum/google-sheets-mcp/issues/new/choose)
with:

- the output of `google-sheets-mcp doctor` and `google-sheets-mcp --version`;
- how you installed it and which client runs it;
- a debug log, from `GSHEETS_LOG_LEVEL=debug`, which never carries cell
  values, formulas, titles, ranges or search terms (D-07).

Never paste a spreadsheet id or URL, cell contents, a client JSON, a
token or a tool result; describe the shape instead. A suspected
vulnerability goes through [`SECURITY.md`](../SECURITY.md), privately.
