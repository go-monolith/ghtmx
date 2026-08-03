# Releasing

Every component — compiler, runtime, LSP, dev tooling, adapters —
ships under one version. The tagged tree must be self-consistent, so
`go install`/`go get` consumers see the same version as the release
binaries: the workflow **verifies** the version, it never stamps it.

## Automated patch releases

Every merge to `main` cuts the next patch release. `auto-release.yml`
performs steps 1 and 2 of the manual procedure below — everything except
the `CHANGELOG.md` edit — then calls the Release workflow, so the same
gates run either way. Automated releases take their notes from GitHub's
generated changelog; `CHANGELOG.md` stays a hand-curated record, and
`[Unreleased]` accumulates until someone moves it.

**The tag is created after the gates pass, never before.** The prep
commit is parked on a scratch branch, `release.yml` gates that commit,
and only then does the tag exist. A failed gate leaves no published
version behind — which matters because a tag is immutable once the
module proxy has seen it.

- **Version.** The next patch above the highest `vX.Y.Z` tag. A `!:` in
  the merge subject, or a `BREAKING CHANGE` trailer anywhere in the
  message, bumps the **minor** instead — pre-1.0 this project allows
  breaking changes between minor versions, so they must not ship as a
  patch.
- **Opting out.** Put `[skip release]` in the merge commit message. A
  release is also skipped when nothing but `*.md`, `docs/`, or
  `.github/` has changed **since the last tag** — measured from the tag,
  not from the single push, so a merge whose event GitHub drops or
  coalesces is picked up by the next run instead of being lost.
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

## Mixing the two paths

Landing a manual release-prep commit on `main` and then tagging it by
hand still works, but do it in that order and within one merge. If the
prep commit merges first, the automation sees `main` already carrying
the target version, finds nothing to rewrite, and gates `main`'s tip
as-is — fine. If you tag *before* that merge lands, the automation reads
your new tag as the baseline and cuts the next patch from the same tree.

## Why the Release workflow is callable

`release.yml` takes two inputs. `ref` is the commit to gate and tag;
`tag` is the version that commit claims. A hand-pushed tag supplies
both implicitly — the tag exists already and is its own ref.

A tag pushed with the default `GITHUB_TOKEN` starts no workflow run, so
the automation could not rely on the tag-push trigger even if it wanted
to. It calls `release.yml` through `workflow_call` instead, which keeps
everything on the built-in token — no PAT or GitHub App to mint, store,
or rotate. `release.yml` still runs from a plain `git push origin
v1.2.3` exactly as before.
