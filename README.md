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
built on the Drive API.

**Status: design.** There is no code yet. The design, the platform
constraints it is built on, the decided trade-offs, the phase plan and
the evidence log are in [docs/architecture.md](docs/architecture.md).
Phase 0 starts on an explicit go.

## Licence

Apache-2.0.
