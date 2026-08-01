# Releasing

Every component — compiler, runtime, LSP, dev tooling, adapters —
ships under one version. The tagged tree must be self-consistent, so
`go install`/`go get` consumers see the same version as the release
binaries: the workflow **verifies** the version, it never stamps it.

## Procedure

1. **Release-prep commit.** In one commit:
   - Set `.version` to the release version without the `v` prefix
     (e.g. `1.2.3`), no trailing newline.
   - In every `adapters/*/go.mod`, set the
     `require github.com/go-monolith/ghtmx` version to the tag (e.g.
     `v1.2.3`). The local `replace` stays — consumers ignore it; it
     only serves in-repo development.
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
