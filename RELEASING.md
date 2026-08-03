# Releasing

Every component — compiler, runtime, LSP, dev tooling, adapters —
ships under one version. The tagged tree must be self-consistent, so
`go install`/`go get` consumers see the same version as the release
binaries: the workflow **verifies** the version, it never stamps it.

## Automated patch releases

Every merge to `main` cuts the next patch release. `auto-release.yml`
does what the manual procedure below does, then calls the Release
workflow — so the two paths converge and the same gates run either way.

- **Version.** The next patch above the highest `vX.Y.Z` tag. A `!:` in
  the merge subject, or a `BREAKING CHANGE` trailer anywhere in the
  message, bumps the **minor** instead — pre-1.0 this project allows
  breaking changes between minor versions, so they must not ship as a
  patch.
- **Opting out.** Put `[skip release]` in the merge commit message.
  Merges touching only `*.md`, `docs/`, or `.github/` are skipped
  automatically.
- **Where the bump lives.** `main` is branch-protected with
  `enforce_admins`, so CI cannot commit to it. The prep commit is built
  **off** `main` — its parent is the merge commit — and the tag points
  at that commit. `main` is never written to, and the tagged tree still
  satisfies the verify-never-stamp invariant.
- **Consequence.** `.version` on `main` is only a dev placeholder; the
  value that ships is the one the workflow writes into the tag. Do not
  read `main`'s `.version` as the released version.
- **First release.** Automation bumps from an existing tag and fails
  loudly if none exists. Cut the first one by hand with the procedure
  below.

Because every release gets a fresh prep commit, `adapters/<name>/vX.Y.Z`
always requires the matching `ghtmx vX.Y.Z`. Adapter requires on `main`
still lag the latest tag; that only affects `go get .../adapters/...@main`,
never a released version.

## Manual procedure

Used for the first release, and any release the automation cannot make.

1. **Release-prep commit.** In one commit:
   - Set `.version` to the release version without the `v` prefix
     (e.g. `1.2.3`), no trailing newline.
   - In every `adapters/*/go.mod`, set the
     `require github.com/go-monolith/ghtmx` version to the tag (e.g.
     `v1.2.3`). The local `replace` stays — consumers ignore it; it
     only serves in-repo development.
   - `docs/official/go.mod` and `internal/wasmcheck/fixture/go.mod`
     depend on `adapters/chi`, so their requires move in lockstep too or
     they fail to build. Both stay internal and are never tagged.
   - Update `CHANGELOG.md` for the release.
2. **Tag and push.** Tag that commit `v1.2.3` and push the tag. The
   Release workflow then:
   - verifies `.version` and the adapter requires match the tag,
   - runs the full gate set (tests including the WASM matrix,
     generated-code currency, govulncheck, the performance gates, and
     the release artifact matrix),
   - builds checksummed archives for every supported platform,
     verifies the shipped binary reports the tag, publishes the GitHub
     release, and pushes the lockstep adapter tags
     (`adapters/<name>/v1.2.3`).
3. **Post-release commit.** Set `.version` back to the next
   `X.Y.Z-dev` and the adapter requires may stay at the released
   version until the next release-prep commit.

A mismatch anywhere fails the gates job before any artifact exists.

## Why the Release workflow is callable

A tag pushed with the default `GITHUB_TOKEN` does not start a new
workflow run, so `auto-release.yml` cannot rely on the tag-push trigger
to reach `release.yml`. It calls it directly through `workflow_call`
instead, which keeps the whole thing on the built-in token — no PAT or
GitHub App to mint, store, or rotate. `release.yml` still runs from a
plain `git push origin v1.2.3` exactly as before.
