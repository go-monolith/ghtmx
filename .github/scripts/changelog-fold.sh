#!/usr/bin/env bash
#
# After a release, brings main's CHANGELOG.md and changelog.d/ in line
# with what the tag shipped: the release-prep commit folded the
# fragments into CHANGELOG.md under the released version (see
# scripts/assemble-changelog.sh), but that commit lives off main —
# main is branch-protected and CI cannot write to it. This script
# rebuilds the same fold on top of main's current tip and parks it on
# changelog/<tag> for a pull request a human merges.
#
# It reuses assemble-changelog.sh, which recognises the released
# version and runs in RESTORE mode: the tag's section is inserted into
# main's file (never overwriting it, so preamble edits merged in the
# meantime survive), the fragments that release folded are deleted, and
# a fragment merged after the release started is left for the next one.
# Because sections are inserted rather than the file replaced,
# re-running an old release's fold after a newer one has merged cannot
# delete the newer sections — it simply finds nothing to do.
#
# Runs against a checkout of main with tags fetched. Reads $TAG, writes
# `branch=` (empty when there was nothing to fold) to $GITHUB_OUTPUT.
# Extracted from auto-release.yml so the behaviour can be tested — see
# internal/release/changelog_test.go.
set -euo pipefail

: "${TAG:?the released tag to fold, e.g. v1.2.3}"
GITHUB_OUTPUT="${GITHUB_OUTPUT:-/dev/null}"

# No tag means the release did not publish (its gates failed before
# tagging). A clean no-op, not an error: the job runs whenever a
# release was planned, so the fold is attempted even when only part of
# the release pipeline failed — see auto-release.yml.
if ! git rev-parse -q --verify "refs/tags/${TAG}^{commit}" >/dev/null; then
  echo "::notice::tag ${TAG} does not exist — the release never published; nothing to fold"
  echo "branch=" >> "${GITHUB_OUTPUT}"
  exit 0
fi

git config user.name "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"

bash "$(dirname "${BASH_SOURCE[0]}")/../../scripts/assemble-changelog.sh" "${TAG#v}"

# Checked before the docs sync on purpose: the sync only re-copies
# documents, so with CHANGELOG.md and the fragments already in line
# there is nothing it could change either.
if ! git status --porcelain | grep -q .; then
  echo "::notice::main already carries the ${TAG} changelog; nothing to fold"
  echo "branch=" >> "${GITHUB_OUTPUT}"
  exit 0
fi

# Refresh the docs site's embedded copies so the fold PR passes the
# content-drift gate. Guarded by the directory so the synthetic test
# repositories skip it.
if [ -d docs/official/internal/sync ]; then
  (cd docs/official && go run ./internal/sync)
  git add docs/official/content
fi

git add CHANGELOG.md
git commit -q -m "Fold the ${TAG} changelog into main" \
  -m "The release-prep commit under ${TAG} assembled changelog.d/ into CHANGELOG.md off main (auto-release.yml); this brings main's copy — and the docs site that deploys from it — in line. Markdown- and docs-only, so merging it does not cut a release."

branch="changelog/${TAG}"
# Force-push is safe here and only here: the branch exists to carry
# exactly this fold, and a re-run must be able to replace a stale one.
git push --force origin "HEAD:refs/heads/${branch}"
echo "branch=${branch}" >> "${GITHUB_OUTPUT}"
echo "==> ${branch} carries the fold; open or refresh its pull request"
