# Setting up a Google Cloud project

You need your own OAuth client. This server is a desktop application that
runs on your machine, under your Google account, so there is nothing
shared to connect to and no service account to be granted access. It
takes about fifteen minutes once.

This is the order [`doctor`](../README.md#set-up-google) checks things
in, so if you work through it top to bottom, `doctor` will agree with you
at the end.

## 1. Create a project

In the [Google Cloud console](https://console.cloud.google.com/), create
a project, or pick an existing one you own. Its name is never seen by
this server or by anybody else; it is a container for the OAuth client.

## 2. Enable the two APIs

Under **APIs & Services → Library**, enable:

- **Google Sheets API** — everything this server does.
- **Google Drive API** — one call, `search_spreadsheets`, which finds a
  spreadsheet by title, by text inside it, by owner or by when it
  changed. Without it every other tool still works, and you have to pass
  an id or a URL rather than a title.

## 3. Configure the consent screen

Under **APIs & Services → OAuth consent screen**:

- **Internal** if this is a Google Workspace account. Nothing expires and
  nobody has to approve anything.
- **External**, left in **Testing**, for a consumer account. Add your own
  address under **Test users**. Google expires a testing app's refresh
  token after a week, so you will re-run `login` about weekly. Moving to
  **In production** ends that, at the cost of a verification review you
  do not need for a client only you use.

## 4. Add the scopes

Two, and no more:

| Scope | Why |
|---|---|
| `https://www.googleapis.com/auth/spreadsheets` | Read and write the spreadsheets you already have access to |
| `https://www.googleapis.com/auth/drive.readonly` | `search_spreadsheets`, and nothing else |

Both are **restricted** scopes in Google's classification, which is why
the consent screen asks you to confirm them.

`GSHEETS_READ_ONLY=true` asks for `spreadsheets.readonly` instead of the
first, and registers only the tools that read. Grant whichever pair you
intend to use: a token issued for the read-only scope cannot be widened
without logging in again.

**Not** `drive.file` and not the full `drive` scope. `drive.file` only
sees files this application created, which is the opposite of useful for
a server whose whole job is working in spreadsheets you already have; the
full scope would let it manage every file in your Drive, which it has no
reason to do. Moving, sharing and trashing files belong to a server built
on the Drive API.

## 5. Create the OAuth client

Under **APIs & Services → Credentials**, create an **OAuth 2.0 Client ID**
of type **Desktop app**. Download the JSON.

Desktop app, not Web application: the redirect goes to a loopback address
on a port chosen at run time, and a Web client would require every port
to be registered in advance.

Keep the file. It is not a password — a desktop client's secret is not
treated as confidential by the OAuth specification for native apps — but
it identifies your project, and this server never copies it anywhere.

## 6. Log in

```bash
google-sheets-mcp login -secret ~/Downloads/client_secret_*.json
```

The path is remembered in your profile, so later logins need only
`google-sheets-mcp login`.

## 7. Check it

```bash
google-sheets-mcp doctor
```

It checks the client JSON, the refresh token, the granted scopes and what
Google actually answers, and names what is missing. It masks the client
id and the account address, so its output is safe to paste into an issue.

To check the Sheets API against a real spreadsheet as well:

```bash
google-sheets-mcp doctor -spreadsheet <id, URL or exact title>
```

## If something is wrong

| What `doctor` says | What to do |
|---|---|
| `OAuth client JSON` fails | The path is wrong, or the file is a Web client rather than a Desktop one. Re-run `login -secret <path>` |
| `refresh token` fails | Run `login`. On a consumer account in Testing, this is the weekly expiry |
| `granted scopes` is missing one | You added a scope after logging in. Run `login` again to re-consent |
| `Drive API` fails and Sheets works | The Drive API is not enabled on the project, or `drive.readonly` was not granted. Only `search_spreadsheets` needs it |

[`runbook.md`](runbook.md) covers rotating and recovering credentials
once this is working.
