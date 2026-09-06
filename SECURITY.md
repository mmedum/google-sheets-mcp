# Security policy

## Reporting a vulnerability

Please open a [private security
advisory](https://github.com/mmedum/google-sheets-mcp/security/advisories/new)
rather than a public issue.

Include what an attacker would be able to do and how to reproduce it.
**Do not include anything from a real spreadsheet** — no cell values or
formulas, no sheet or spreadsheet titles, no spreadsheet ids or links, no
account addresses. A synthetic reproduction is always enough; if it is
not, say so and we will work out how to get what is needed without it.

Expect an acknowledgement within a few days. Fixes go out as a normal
tagged release with the advisory published alongside.

## Supported versions

The latest release. This project is pre-1.0 and moves in phases; there
are no backports.

## What the server does with your data

[docs/security.md](docs/security.md) is the detail: what it talks to,
which scopes it asks for, where the refresh token is stored, what reaches
a log and what the server refuses to do. In short — it talks only to
Google over an allowlist checked with the port, the refresh token lives
in the OS keyring, logs carry no cell values, formulas, titles, ranges or
search terms, and a test fails the build if any of that changes.
