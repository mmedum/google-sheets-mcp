# Releasing

A maintainer releases by pushing a tag. `release.yml` refuses a tag
whose major version is not go.mod's, runs the tests, and publishes a
release, never a draft, with the tag's CHANGELOG section as its notes
(D-74). It carries the archives, an SBOM each and the `.mcpb` bundle,
all in the cosign-signed `checksums.txt` and attested (D-70, D-71),
then publishes the MCP registry entry. A tag such as `vX.Y.Z-rc1` is a
prerelease and skips the registry, whose entries cannot be withdrawn.

## Before the release commit

- `make check` passes, and CI is green on Linux, macOS and Windows.
- `make live` has run and its transcript was read, and `make evals` if
  the release changes what a model sees.
- `/simplify`, `/code-review medium` and `/security-review` have run
  since the previous tag. A fixed finding is one line in its commit
  body; a declined one goes in the PR body.
- `make api-diff`, `make schema-refetch` and `make leaks-history`. A
  method Google added fails `make api-coverage` until it has a verdict.
- `make vuln` in a worktree of the previous tag. An advisory there and
  not at `HEAD` gets a Security line naming the CVE or GHSA.
- Rehearse the build into `dist/`, publishing nothing:

  ```bash
  go run github.com/goreleaser/goreleaser/v2@v2.18.1 release --snapshot --clean --skip=sign,sbom,publish
  ```

  The version must agree in the bundle's and the archives' filenames,
  the bundle's `manifest.json`, `--version` and `checksums.txt`. A
  bundle missing from `checksums.txt` ships unsigned and looks no
  different.

`--snapshot` skips goreleaser's dirty-tree check, and the `before` hook
runs `go mod tidy`. A rewrite there fails the tag while every rehearsal
passes, which is why `make check` runs `make tidy`.

## Steps

1. Merge a `Release X.Y.Z` commit through a pull request. It renames
   `[Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD`, adds its link reference,
   and leaves an empty `[Unreleased]` above it. `staleness` accepts it
   before the tag exists.
2. In the same commit, after the rename, run
   `make schema-baseline VERSION=vX.Y.Z`; `schema-diff` fails until it
   is done. A break ships only as a new major version, with go.mod's
   `/vN` raised and a `**Breaking:**` entry (D-57).
3. Wait for `ci.yml` to pass on that exact commit.
4. Tag it, annotated, and push the tag on its own:

   ```bash
   git checkout main && git pull --ff-only && make check
   git tag -a vX.Y.Z -m "vX.Y.Z: <what shipped>"
   git push origin vX.Y.Z
   ```

   GitHub creates no tag events past the third in one push. If the
   release never starts, run `release.yml` by hand on the tag.

## What no rehearsal reaches

Signing, attestation and the registry publish need the workflow's OIDC
token, so read the first run after any change to the pipeline.

| Fails | State afterward | Recovery |
|---|---|---|
| tests, cosign or SBOMs | no release | fix it, then release the next patch |
| attestation | release published, unattested | re-run the failed job |
| registry publish | release fine, no entry | `gh workflow run publish-mcp.yml --ref main -f tag=vX.Y.Z`; do not tag again |

## Verify from outside

```bash
cd "$(mktemp -d)" && gh release download vX.Y.Z --repo mmedum/google-sheets-mcp
sha256sum -c checksums.txt --ignore-missing
cosign verify-blob checksums.txt --bundle checksums.txt.bundle \
  --certificate-identity 'https://github.com/mmedum/google-sheets-mcp/.github/workflows/release.yml@refs/tags/vX.Y.Z' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify google-sheets-mcp_X.Y.Z_linux_amd64.tar.gz --repo mmedum/google-sheets-mcp
gh attestation verify google-sheets-mcp_X.Y.Z.mcpb --repo mmedum/google-sheets-mcp
```

A pass prints little, so read the exit code. Open the bundle in Claude
Desktop. Last, run
the README's install command outside the repository, as a user would,
and check that `google-sheets-mcp --version` names the release.

## A bad release

A bad release is fixed forward.

- Never move or delete a pushed tag. The Go module proxy caches it
  within minutes, and the checksum database records its hash.
- Ship a patch release with the fix.
- In that patch, add `retract vX.Y.Z // why` to `go.mod`, so the go
  command skips the bad version for `@latest`.
- Mark the bad version's CHANGELOG heading `[YANKED]`:
  `## [X.Y.Z] - YYYY-MM-DD [YANKED]`.
