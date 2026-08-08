#!/usr/bin/env bash
#
# Fails a pull request that changes code a release would ship without
# adding a changelog fragment, and rejects hand-written CHANGELOG.md
# version sections — the mandatory-changelog rule (CLAUDE.md), enforced
# rather than remembered.
#
# Reads $BASE and $HEAD (the pull request's base and head SHAs) and
# runs against a full-history checkout with tags. Lives here rather
# than inline in ci.yml so its behaviour can be tested — see
# internal/release/changelog_test.go.
set -euo pipefail

: "${BASE:?the pull request base SHA}"
: "${HEAD:?the pull request head SHA}"

CHANGED=$(git diff --name-only "${BASE}...${HEAD}")

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
    echo "manual release-prep PR assembles its own '## [${STAGED}]' section; fragment not required"
    exit 0
  fi
  echo "CHANGELOG.md sections match released tags; continuing to the fragment check"
fi

# Changes a release would skip need no entry. The filter mirrors the
# shipping decision in release-plan.sh — keep the two in lockstep.
NEEDS=$(printf '%s\n' "${CHANGED}" \
  | grep -vE '(^|/)[^/]*\.md$' \
  | grep -vE '^docs/' \
  | grep -vE '^\.github/' || true)
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
