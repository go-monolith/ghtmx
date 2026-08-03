#!/usr/bin/env bash
#
# Decides whether the current main tip ships, and as what version.
#
# Runs against the repository in the working directory and writes
# `release=` and (when releasing) `tag=` to $GITHUB_OUTPUT. Lives here
# rather than inline in auto-release.yml so its behaviour can be tested
# against synthetic histories — see internal/release/plan_test.go.
#
# Exit 1 means the decision could not be made safely and no release
# should follow.
set -euo pipefail

: "${GITHUB_OUTPUT:?must point at a file to write step outputs to}"

skip() {
  echo "release=false" >> "$GITHUB_OUTPUT"
  echo "::notice::$1"
  exit 0
}

# Opting out is about this merge, so it reads the tip only: it means
# "not now", and the next merge releases the backlog.
case "$(git log -1 --pretty=%B)" in
  *"[skip release]"*|*"[release skip]"*)
    skip "Opted out via [skip release]." ;;
esac

# Strict vX.Y.Z only. A prerelease such as v0.2.0-rc1 sorts ABOVE v0.2.0
# under -v:refname without versionsort.suffix set, which would silently
# skip a version.
latest=$(git tag --list 'v[0-9]*' --sort=-v:refname \
  | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' \
  | head -1 || true)
if [ -z "$latest" ]; then
  echo "::error::No vX.Y.Z tag exists yet. Automation only bumps from an existing tag — cut the first release by hand (RELEASING.md), then merges will auto-bump from it."
  exit 1
fi

# The base is the MAIN commit the last release was built from, not this
# push's delta. A per-push delta permanently drops any merge whose event
# is lost, coalesced, or cancelled while pending; measuring from the last
# tag makes the decision idempotent and self-healing.
#
# Which commit that is depends on how the tag was cut. A hand-cut tag
# sits on main, so it IS the base. An automated tag sits on an off-main
# prep commit, so its first parent is. Using the prep commit either way
# would count its own .version and go.mod edits as shipped changes, and
# the docs-only skip below could never fire.
if git merge-base --is-ancestor "$latest" HEAD; then
  base=$(git rev-parse "${latest}^{commit}")
else
  base=$(git rev-parse -q --verify "${latest}^" || true)
fi
if [ -n "$base" ]; then
  changed=$(git diff --name-only "$base" HEAD)
  shipped=$(printf '%s\n' "$changed" \
    | grep -vE '(^|/)[^/]*\.md$' \
    | grep -vE '^docs/' \
    | grep -vE '^\.github/' || true)
  if [ -z "$shipped" ]; then
    skip "Nothing but docs, markdown, or workflows since $latest."
  fi
fi

number=${latest#v}
major=${number%%.*}
rest=${number#*.}
minor=${rest%%.*}
patch=${rest##*.}

# Pre-1.0 the project allows breaking changes between MINOR versions
# (CHANGELOG.md), so a breaking marker must not ship as a patch. This
# scans every commit the release covers, not just the tip: the release
# spans everything since the last tag, and a marker in a merge whose own
# run was coalesced away would otherwise ship as a patch. It also reaches
# the individual commits behind a true merge commit, whose own subject
# carries no marker.
#
# `!:` is read from subjects only, and the trailer must start its own
# line, so prose mentioning a breaking change does not force a bump.
range=${base:+$base..}HEAD
if git log "$range" --pretty=%s | grep -qE '^[^ ]*!:' \
   || git log "$range" --pretty=%B | grep -qE '^BREAKING[ -]CHANGE:'; then
  minor=$((minor + 1))
  patch=0
  echo "::notice::Breaking marker found — bumping the minor version."
else
  patch=$((patch + 1))
fi

next="v${major}.${minor}.${patch}"

if git rev-parse -q --verify "refs/tags/$next" >/dev/null; then
  echo "::error::$next already exists. Refusing to move a published tag — module versions are immutable once the proxy has seen them."
  exit 1
fi

echo "Releasing $next (previous $latest, base ${base:-none})"
echo "tag=$next" >> "$GITHUB_OUTPUT"
echo "release=true" >> "$GITHUB_OUTPUT"
