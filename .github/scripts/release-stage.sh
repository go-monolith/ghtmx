#!/usr/bin/env bash
#
# Builds the release-prep commit RELEASING.md step 1 describes and parks
# it on a scratch branch for the gates to check out. It carries no tag:
# the tag is created only after release.yml has gated this commit.
#
# Reads $TAG, writes `ref=` (the scratch branch) to $GITHUB_OUTPUT.
# Extracted from auto-release.yml so the rewriting can be tested — see
# internal/release/plan_test.go.
set -euo pipefail

: "${TAG:?the version to stage, e.g. v1.2.3}"
: "${GITHUB_OUTPUT:?must point at a file to write step outputs to}"

git config user.name "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"

printf '%s' "${TAG#v}" > .version

# Only the `require` lines carry a version; the `replace` lines have none
# and are left alone, so in-repo development keeps building against the
# working tree.
sed -i -E "s|(github\.com/go-monolith/ghtmx) v[0-9]+\.[0-9]+\.[0-9]+\S*\$|\1 $TAG|" \
  adapters/*/go.mod docs/official/go.mod internal/wasmcheck/fixture/go.mod
sed -i -E "s|(github\.com/go-monolith/ghtmx/adapters/chi) v[0-9]+\.[0-9]+\.[0-9]+\S*\$|\1 $TAG|" \
  docs/official/go.mod internal/wasmcheck/fixture/go.mod

git add .version adapters/*/go.mod docs/official/go.mod internal/wasmcheck/fixture/go.mod

# A hand-made prep commit for this same version may already be on main
# (RELEASING.md's manual path), leaving nothing to do. Committing nothing
# is an error, so tag main's tip instead.
if git diff --cached --quiet; then
  echo "::notice::main already carries $TAG — gating its tip as-is."
else
  git commit -m "Release $TAG" \
    -m "Automated release-prep commit (auto-release.yml). Reachable only from the tag, so the tagged tree is self-consistent for go install and go get consumers."
fi

# Force-push is safe here and only here: a scratch branch is not a
# published version. It is deleted once the run succeeds.
branch="auto-release/$TAG"
git push --force origin "HEAD:refs/heads/$branch"
echo "ref=$branch" >> "$GITHUB_OUTPUT"
