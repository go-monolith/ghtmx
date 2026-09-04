#!/usr/bin/env bash
#
# Fails a pull request that changes code a release would ship without
# adding a changelog fragment, rejects hand-written CHANGELOG.md
# version sections — the mandatory-changelog rule (CLAUDE.md), enforced
# rather than remembered — and fails once main has fallen two or more
# releases behind because its fold pull requests were never merged.
#
# Reads $BASE and $HEAD (the pull request's base and head SHAs) and
# runs against a full-history checkout with tags. Lives here rather
# than inline in ci.yml so its behaviour can be tested — see
# internal/release/changelog_test.go.
set -euo pipefail

: "${BASE:?the pull request base SHA}"
: "${HEAD:?the pull request head SHA}"

# The fold-lag rule (issue #46). Every release assembles its sections
# inside an off-main prep commit and pushes the same fold to a
# changelog/<tag> branch, because branch protection stops CI writing to
# main. Until someone merges that branch, main's CHANGELOG.md sits one
# release behind — expected lag, not a bug (CLAUDE.md). Two releases
# behind is not: it means the fold PRs are being dropped, and every
# Migration: note since then is invisible to anyone reading the
# changelog. That is exactly how main reached 0.1.18 while v0.2.0 was
# out.
#
# Measured at HEAD first, and the failure needs BASE to be behind too.
# A fold PR's own base is the lagging main, so reading BASE alone would
# fail the very pull request that fixes the lag and nothing could ever
# merge. But reading HEAD alone fails an ordinary pull request whose
# branch was simply cut before the outstanding fold merged: its stale
# CHANGELOG.md is not the repository's problem, and the fix is a rebase,
# not the fold PR the message names. Failing only when BOTH are two or
# more behind keeps the fold PR mergeable (its HEAD is current) and lets
# a stale branch through (its BASE is current).
#
# Tags older than the newest section are not counted: releases cut
# before the fragment system have no section and never will (the
# assembler skips them too).

# newest_section <ref>: the newest '## [x.y.z]' version in that ref's
# CHANGELOG.md, or nothing when the file or such a section is absent.
newest_section() {
  git show "$1:CHANGELOG.md" 2>/dev/null \
    | grep -m1 -E '^## \[[0-9]+\.[0-9]+\.[0-9]+\]' \
    | sed -E 's/^## \[([0-9]+\.[0-9]+\.[0-9]+)\].*/\1/' || true
}

# unfolded_tags <ref>: the released tags newer than that ref's newest
# section, newest first and space-separated. Strict vX.Y.Z only — the
# same filter release-plan.sh applies, so a prerelease sorting above its
# release cannot inflate the count. Empty when the ref has no section at
# all, and empty when its newest section names a version that was never
# tagged: that is the manual release-prep shape (RELEASING.md), where
# the version has not shipped yet, so there is nothing to have folded.
unfolded_tags() {
  local newest tag out=""
  newest=$(newest_section "$1")
  [ -n "${newest}" ] || return 0
  for tag in $(git tag --list 'v[0-9]*' --sort=-v:refname \
    | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$'); do
    if [ "${tag}" = "v${newest}" ]; then
      printf '%s' "${out}"
      return 0
    fi
    out="${out}${tag} "
  done
  return 0
}

UNFOLDED=$(unfolded_tags "${HEAD}")
COUNT=$(printf '%s' "${UNFOLDED}" | wc -w | tr -d '[:space:]')
if [ "${COUNT}" -ge 2 ]; then
  BASE_COUNT=$(unfolded_tags "${BASE}" | wc -w | tr -d '[:space:]')
  if [ "${BASE_COUNT}" -ge 2 ]; then
    NEWEST_SECTION=$(newest_section "${HEAD}")
    NEWEST_TAG=$(printf '%s' "${UNFOLDED}" | awk '{print $1}')
    echo "::error::CHANGELOG.md stops at ${NEWEST_SECTION}, but ${COUNT} releases have shipped since: ${UNFOLDED}"
    echo "::error::Their fold pull requests were never merged, so every Migration: note in them is invisible."
    echo "::error::The newest fold branch carries all of them — open and merge it:"
    echo "::error::  gh pr create --base main --head changelog/${NEWEST_TAG}"
    echo "::error::(One unmerged fold is expected lag; two or more means the folds are being dropped. The automation cannot open the PR itself unless 'Allow GitHub Actions to create and approve pull requests' is enabled.)"
    exit 1
  fi
  echo "this branch's CHANGELOG.md is ${COUNT} releases behind (${UNFOLDED}) but its base is current — merge or rebase on the base branch to refresh it"
elif [ "${COUNT}" -eq 1 ]; then
  echo "changelog is one release behind (${UNFOLDED}) — expected lag while its fold PR is open"
fi

CHANGED=$(git diff --name-only "${BASE}...${HEAD}")

# Changes a release would skip need no entry. The filter mirrors the
# shipping decision in release-plan.sh — keep the two in lockstep.
NEEDS=$(printf '%s\n' "${CHANGED}" \
  | grep -vE '(^|/)[^/]*\.md$' \
  | grep -vE '^docs/' \
  | grep -vE '^\.github/' || true)

# A PR may legitimately add a '## [x.y.z]' section to CHANGELOG.md in
# exactly two shapes:
#   - a fold PR (changelog/v* branches, or the same fold made by hand),
#     whose sections restore releases that already shipped — every
#     version it adds has an existing tag;
#   - the manual release-prep path (RELEASING.md), which runs
#     assemble-changelog.sh itself — the version it adds is exactly
#     what it stages in .version.
# Anything else is someone editing CHANGELOG.md by hand, which the
# assembled model forbids: on the next release the fold would collide
# with the hand-written section.
#
# Only the manual-prep shape short-circuits the fragment requirement:
# it assembles its fragments away inside the branch, so the ADDED check
# below could never see them. The released-tag shape gets no such
# exemption — a genuine fold PR is markdown- and docs-only and passes
# the NEEDS filter on its own, while a PR that edits a released heading
# AND changes code must still bring a fragment for that code.
SECTIONS=$(git diff "${BASE}...${HEAD}" -- CHANGELOG.md \
  | grep -E '^\+## \[[0-9]+\.[0-9]+\.[0-9]+\]' \
  | sed -E 's/^\+## \[([0-9]+\.[0-9]+\.[0-9]+)\].*/\1/' || true)
if [ -n "${SECTIONS}" ]; then
  STAGED=$(git show "${HEAD}:.version" 2>/dev/null | tr -d '[:space:]' || true)
  MANUAL_PREP=0
  for v in ${SECTIONS}; do
    if [ -n "${STAGED}" ] && [ "${v}" = "${STAGED}" ]; then
      MANUAL_PREP=1
      continue
    fi
    if git rev-parse -q --verify "refs/tags/v${v}" >/dev/null; then
      continue
    fi
    echo "::error::CHANGELOG.md gained a '## [${v}]' section, but v${v} is not a released tag and .version stages '${STAGED:-nothing}'."
    echo "::error::Do not write changelog sections by hand — add changelog.d/<your-branch>.md instead (see changelog.d/README.md)."
    echo "::error::Only a fold PR (sections for released tags) or a manual release-prep PR (section matching .version) may carry one."
    exit 1
  done
  if [ "${MANUAL_PREP}" -eq 1 ]; then
    # The exemption exists because assembly deletes the PR's own
    # fragments (net-zero ADDED below), and it is earned only by the
    # release-prep shape itself: the releasable changes must be exactly
    # the files release-stage.sh stamps. Without this, a hand-written
    # .version plus a hand-written section would exempt arbitrary code
    # from the fragment rule — the exact abuse this gate exists to
    # prevent. (docs/official/go.mod is already excluded as docs/, but
    # is listed for robustness against filter changes.)
    EXTRA=$(printf '%s\n' "${NEEDS}" \
      | grep -vE '^(\.version|adapters/[^/]+/go\.mod|docs/official/go\.mod|internal/wasmcheck/fixture/go\.mod)$' \
      | grep -v '^$' || true)
    if [ -n "${EXTRA}" ]; then
      echo "::error::CHANGELOG.md stages a '## [${STAGED}]' release-prep section, but the PR also changes code beyond the version stamps:"
      echo "${EXTRA}"
      echo "::error::A release-prep PR may only carry .version, the go.mod version stamps, the assembled CHANGELOG.md, fragment deletions, and docs copies (RELEASING.md). Ship other changes in their own PR with a changelog fragment."
      exit 1
    fi
    echo "manual release-prep PR assembles its own '## [${STAGED}]' section; fragment not required"
    exit 0
  fi
  echo "CHANGELOG.md sections match released tags; continuing to the fragment check"
fi

if [ -z "${NEEDS}" ]; then
  echo "no releasing changes; fragment not required"
  exit 0
fi

# ADDED files only: a PR that deletes someone else's fragment, or edits
# one, must not satisfy its own requirement. [^/]+ keeps the definition
# in lockstep with the assembler's `find -maxdepth 1` — a fragment in a
# subdirectory would pass here and then never be folded or deleted.
# -x anchors the README exclusion so a file merely containing that name
# cannot pass.
ADDED=$(git diff --name-only --diff-filter=A "${BASE}...${HEAD}" \
  | grep -E '^changelog\.d/[^/]+\.md$' | grep -vx 'changelog\.d/README\.md' || true)
if [ -n "${ADDED}" ]; then
  echo "changelog fragment(s) present:"
  echo "${ADDED}"
  exit 0
fi

echo "::error::This PR changes code a release will ship, but adds no changelog fragment."
echo "::error::Add changelog.d/<your-branch>.md with your entries — see changelog.d/README.md."
echo "Changed files needing an entry:"
echo "${NEEDS}"
exit 1
