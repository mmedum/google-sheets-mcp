# Runbook

What to do when credentials have to change. Setting them up in the first
place is [`gcp-setup.md`](gcp-setup.md).

Everything here is local to one machine and one profile. There is no
deployment, no shared state and nothing to coordinate with anybody else:
the whole of this server's persistent state is a refresh token and a
small non-secret profile file.

## Where the state lives

| What | Where | Secret |
|---|---|---|
| Refresh token | OS keyring, service `google-sheets-mcp`, account = the profile name | Yes |
| Refresh token, no keyring | `token.json`, mode `0600`, in the profile's config directory | Yes |
| Profile | `config.json` in the config directory: client secret path, token store, granted scopes | No |
| OAuth client JSON | Wherever you downloaded it; the path is recorded in the profile | No |

`google-sheets-mcp status` prints the config directory, which account is
signed in and which of the two token stores is in use. Run it first,
always: half the procedures below differ depending on the answer.

## Rotating the token

Routine, and the same as logging in again:

```bash
google-sheets-mcp login
```

The new token replaces the old one in the same store. The old one is not
revoked — it is the same grant, re-issued.

## Revoking access

When a machine is being decommissioned, or you want the grant gone:

```bash
google-sheets-mcp logout
```

This revokes the token at Google and deletes the local copy. Revoking at
Google ends **every** token issued from that grant, on every machine, for
that account and that OAuth client.

To forget the token here and leave other machines working:

```bash
google-sheets-mcp logout -local
```

## Suspected exposure

If a refresh token may have leaked — a copied home directory, a shared
machine, a `token.json` in a backup:

1. `google-sheets-mcp logout` on any machine that still has it. This
   revokes the grant everywhere, which is the point.
2. If you cannot run it, revoke by hand at
   [Google account permissions](https://myaccount.google.com/permissions):
   find the OAuth client by the name you gave it and remove its access.
3. `google-sheets-mcp login` on the machines that should keep working.

A leaked **client JSON** is a different and smaller problem: for a
desktop app it is not treated as confidential, and on its own it grants
nothing — an attacker would still need a user to complete a consent
flow. Rotate it anyway if it bothers you, by creating a new OAuth client
in the console, deleting the old one and running `login -secret <new
path>`. Deleting the old client invalidates tokens issued from it.

## Moving to another machine

Do not copy the token. Install the binary, copy the OAuth client JSON,
and run `login`. The keyring entry is not portable and a copied
`token.json` is a second live copy of a credential.

## Two accounts on one machine

Profiles keep them apart:

```bash
GSHEETS_PROFILE=work google-sheets-mcp login -secret ./work_client.json
GSHEETS_PROFILE=personal google-sheets-mcp login -secret ./personal_client.json
```

Each profile has its own config directory, its own keyring entry and its
own granted scopes. Point each MCP client entry at the profile it should
use with the same environment variable.

## The keyring is not available

On a headless machine, or one with no Secret Service, `login` says so and
writes `token.json` at mode `0600` instead. That is a deliberate
fallback, not a failure — but it is a credential in a file, so:

- keep it out of backups that leave the machine;
- do not copy the home directory to another host;
- prefer `logout` over deleting the file, so the grant is revoked rather
  than orphaned.

`status` tells you which store is in use.

## Weekly expiry on a consumer account

An **External + Testing** consent screen expires refresh tokens after a
week. The symptom is every tool failing with `[auth]` and `doctor`
reporting no usable token. The fix is `google-sheets-mcp login`. To stop
it, either use a Workspace account with an **Internal** consent screen or
move the app to **In production**.

## A tool suddenly returns `[forbidden]`

Usually a scope that was never granted rather than one that was taken
away. `doctor` prints the granted scopes; compare them against
[`gcp-setup.md`](gcp-setup.md#4-add-the-scopes). Adding a scope in the
console does not change an existing token — run `login` again to
re-consent.
