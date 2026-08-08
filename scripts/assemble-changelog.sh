#!/usr/bin/env bash
# Fold the changelog fragments in changelog.d/ into CHANGELOG.md under a
# version heading, then delete them.
#
# Why fragments exist: every PR used to insert its entries at the TOP of
# one shared CHANGELOG.md (the [Unreleased] section), so two PRs always
# collided on the same lines even when the changes were unrelated — and
# nobody could write the real version number, because it is only known
# when the release is cut. One file per PR makes the conflict
# structurally impossible, and this script supplies the version at the
# only moment it is a fact rather than a guess.
#
# Usage: scripts/assemble-changelog.sh <version>        # e.g. 0.1.18
#
# Run from the repository root (the release automation and the tests run
# it against their own checkouts, so it operates on the working
# directory, never on the directory it lives in).
#
# Two modes, decided by whether the tag v<version> already exists:
#
# ASSEMBLE (no tag yet — the release-prep and manual paths):
#   1. Splits the fragments into NEW (absent from the last release's
#      base commit) and RELEASED (present there — meaning an earlier
#      release already folded their text into ITS tag, but the fold-back
#      PR that would have deleted them from main has not merged yet).
#      The base is computed exactly like release-plan.sh computes it:
#      the tag itself when it sits on main, its first parent when it is
#      an off-main prep commit.
#   2. Merges the NEW fragments' "### <section>" blocks in
#      keep-a-changelog order under "## [<version>] - <date>", inserted
#      before the first existing "## " heading. RELEASE_DATE=YYYY-MM-DD
#      overrides today's UTC date. An entirely blank fragment is
#      discarded with a warning instead of publishing an empty section.
#   3. Deletes every fragment — the NEW ones are now in the file, the
#      RELEASED ones already live in a section restored below.
#
# RESTORE (the tag exists — the post-release fold path): the release
#   already folded its fragments inside the prep commit the tag points
#   at, so nothing is folded here. Fragments the release covered are
#   deleted; fragments merged after it started are left for the next
#   release.
#
# Both modes also restore any released-but-unfolded sections: a tag
# newer than this CHANGELOG.md's newest "## [x.y.z]" heading carries
# its own section in its tree (the prep commit assembled it there), and
# that section is copied in verbatim — inserted, never overwriting, so
# preamble edits on main survive. Tags from before the fragment system
# have no section and are skipped. This is what keeps everything
# correct while a fold-back PR is still open: main cannot be written by
# CI, so main's CHANGELOG.md lags until a human merges that PR.
#
# Idempotent-ish: run it once per release. With nothing to fold,
# restore, or delete it leaves CHANGELOG.md untouched and exits 0, so a
# release with nothing to say never fails the pipeline; a CHANGELOG.md
# already carrying the version (the manual release-prep path) is
# tolerated as long as no unfolded fragment remains.
set -euo pipefail

VERSION="${1:-}"
if [ -z "${VERSION}" ]; then
  echo "usage: $0 <version>   (e.g. $0 0.1.18)" >&2
  exit 2
fi
if ! printf '%s' "${VERSION}" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "::error::version must be X.Y.Z, got '${VERSION}'" >&2
  exit 2
fi
DATE="${RELEASE_DATE:-$(date -u +%F)}"

FRAGMENT_DIR="changelog.d"
CHANGELOG="CHANGELOG.md"

# The section names a fragment may use, in the order the assembled
# release lists them. internal/installcheck enforces the same set on
# every fragment, so a typo fails CI long before it fails a release.
SECTION_ORDER="Added Changed Deprecated Removed Fixed Security"

# README.md documents the directory itself and is never a fragment.
# -maxdepth 1 is the fragment definition — changelog-gate.sh only
# accepts flat files, and installcheck rejects subdirectories.
#
# A read loop, not `mapfile`: that is a bash 4.0 builtin and macOS still
# ships bash 3.2, where `#!/usr/bin/env bash` resolves to it — and the
# manual release path (RELEASING.md) runs this by hand. The explicit
# `FRAGMENTS=()` also keeps `${#FRAGMENTS[@]}` legal under `set -u` on
# 3.2. LC_ALL=C pins the sort so fragment order cannot differ between a
# contributor's locale and the runner's.
FRAGMENTS=()
while IFS= read -r f; do
  FRAGMENTS+=("${f}")
done < <(
  find "${FRAGMENT_DIR}" -maxdepth 1 -name '*.md' ! -name 'README.md' -type f 2>/dev/null | LC_ALL=C sort
)

# ver_gt A B: true when X.Y.Z version A is greater than B. Pure bash so
# the manual path does not depend on GNU sort -V.
ver_gt() {
  local a1 a2 a3 b1 b2 b3
  IFS=. read -r a1 a2 a3 <<<"$1"
  IFS=. read -r b1 b2 b3 <<<"$2"
  if [ "${a1}" -ne "${b1}" ]; then [ "${a1}" -gt "${b1}" ]; return; fi
  if [ "${a2}" -ne "${b2}" ]; then [ "${a2}" -gt "${b2}" ]; return; fi
  [ "${a3}" -gt "${b3}" ]
}

# Strip leading and trailing blank lines, so fragments can be written
# with or without surrounding whitespace without changing the output.
trim_blank() {
  sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}'
}

# The last release's base commit, mirroring release-plan.sh: a hand-cut
# tag sits on main and IS the base; an automated tag sits on an off-main
# prep commit, so its first parent is. Keep the two in lockstep.
latest=$(git tag --list 'v[0-9]*' --sort=-v:refname 2>/dev/null \
  | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' \
  | head -1 || true)
base=""
if [ -n "${latest}" ]; then
  if git merge-base --is-ancestor "${latest}" HEAD 2>/dev/null; then
    base=$(git rev-parse "${latest}^{commit}")
  else
    base=$(git rev-parse -q --verify "${latest}^" || true)
  fi
fi

# RESTORE mode when the version is already released: its section lives
# in the tag and is restored below; nothing is folded, and a fragment
# merged after the release started is left alone for the next one.
RESTORE_ONLY=0
if git rev-parse -q --verify "refs/tags/v${VERSION}" >/dev/null 2>&1; then
  RESTORE_ONLY=1
fi

# A fragment already covered by a release is present at the base AND
# absent from the latest tag's own tree — the prep commit deletes what
# it folds, so absence is the proof the fold happened. A fragment still
# present in the tag's tree belongs to a tag cut without running this
# script (a hand-tag that skipped the manual procedure); its entries
# made no section anywhere, so it is folded now rather than deleted —
# better attributed late than lost.
FOLD=()
DELETE=()
for f in ${FRAGMENTS[@]+"${FRAGMENTS[@]}"}; do
  if [ -n "${base}" ] && git rev-parse -q --verify "${base}:${f}" >/dev/null 2>&1 \
    && ! git rev-parse -q --verify "refs/tags/${latest}:${f}" >/dev/null 2>&1; then
    DELETE+=("${f}")
  elif [ "${RESTORE_ONLY}" -eq 1 ]; then
    : # not yet released; keep it for the next assembly
  elif [ -z "$(trim_blank < "${f}")" ]; then
    echo "::warning::${f} is empty; deleting it without folding anything" >&2
    DELETE+=("${f}")
  else
    FOLD+=("${f}")
  fi
done

if [ ! -f "${CHANGELOG}" ]; then
  if [ "${#FOLD[@]}" -gt 0 ]; then
    echo "::error::${CHANGELOG} not found but ${#FOLD[@]} fragment(s) need assembling" >&2
    exit 1
  fi
  echo "no ${CHANGELOG} and nothing to assemble"
  exit 0
fi

# Validate the fragments before touching anything. The merge is by
# exact heading, so any other heading-looking line — '### Fix',
# '#### Added', '## [1.2.3]' — would silently orphan its entries or
# smuggle structure into the assembled file.
for f in ${FOLD[@]+"${FOLD[@]}"}; do
  while IFS= read -r heading; do
    ok=0
    for name in ${SECTION_ORDER}; do
      if printf '%s' "${heading}" | grep -qE "^### ${name}[[:space:]]*$"; then
        ok=1
        break
      fi
    done
    if [ "${ok}" -eq 0 ]; then
      echo "::error::${f} contains heading '${heading}' — fragments may only use: ### Added, ### Changed, ### Deprecated, ### Removed, ### Fixed, ### Security" >&2
      exit 1
    fi
  done < <(grep -E '^#' "${f}" || true)
done

if grep -qE "^## \[${VERSION//./\\.}\]" "${CHANGELOG}" && [ "${RESTORE_ONLY}" -eq 0 ]; then
  if [ "${#FOLD[@]}" -gt 0 ]; then
    echo "::error::${CHANGELOG} already has a '## [${VERSION}]' section but ${#FOLD[@]} unfolded fragment(s) remain" >&2
    exit 1
  fi
  echo "${CHANGELOG} already carries ${VERSION}; nothing to assemble"
  exit 0
fi

# Released-but-unfolded sections, newest first. Only versions at or
# below the one being assembled and above this CHANGELOG's newest
# heading are candidates; a tag whose own CHANGELOG.md has no section
# for itself predates the fragment system and is skipped.
newest=$(sed -n 's/^## \[\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)\].*/\1/p' "${CHANGELOG}" | head -1)
MISSING=$(mktemp)
SECTION=$(mktemp)
trap 'rm -f "${MISSING}" "${SECTION}" "${SECTION}.new"' EXIT
while IFS= read -r tv; do
  if ver_gt "${tv}" "${VERSION}"; then
    continue
  fi
  if [ "${tv}" = "${VERSION}" ] && [ "${RESTORE_ONLY}" -eq 0 ]; then
    continue
  fi
  if [ -n "${newest}" ] && ! ver_gt "${tv}" "${newest}"; then
    continue
  fi
  # The dots in ${tv} are regex metacharacters, but a version digit
  # string cannot false-positive against another version heading.
  restored=$(git show "refs/tags/v${tv}:${CHANGELOG}" 2>/dev/null | awk -v ver="${tv}" '
    $0 ~ "^## \\[" ver "\\]" { found = 1 }
    found && /^## / && $0 !~ "^## \\[" ver "\\]" { exit }
    found { print }
  ' | trim_blank || true)
  if [ -n "${restored}" ]; then
    echo "    + restoring '## [${tv}]' from its tag" >&2
    printf '%s\n\n' "${restored}" >> "${MISSING}"
  fi
done < <(
  git tag --list 'v[0-9]*' 2>/dev/null \
    | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' \
    | sed 's/^v//' \
    | sort -t. -k1,1nr -k2,2nr -k3,3nr
)

if [ "${#FOLD[@]}" -eq 0 ] && [ ! -s "${MISSING}" ] && [ "${#DELETE[@]}" -eq 0 ]; then
  echo "nothing to fold or restore; leaving ${CHANGELOG} unchanged"
  exit 0
fi

# The new section: intro prose (fragment text above any heading) first,
# then each known section with every fragment's entries merged in, both
# in fragment filename order. If two entries need a specific order
# relative to each other, put them in one fragment.
if [ "${#FOLD[@]}" -gt 0 ]; then
  echo "==> assembling ${#FOLD[@]} fragment(s) into ${CHANGELOG} as ${VERSION}"
  {
    printf '## [%s] - %s\n' "${VERSION}" "${DATE}"
    for f in "${FOLD[@]}"; do
      echo "    + ${f}" >&2
      intro=$(awk '/^### /{exit} {print}' "${f}" | trim_blank)
      if [ -n "${intro}" ]; then
        printf '\n%s\n' "${intro}"
      fi
    done
    for name in ${SECTION_ORDER}; do
      body=""
      for f in "${FOLD[@]}"; do
        part=$(awk -v want="${name}" '
          /^### / { in_want = ($0 ~ "^### " want "[[:space:]]*$"); next }
          in_want { print }
        ' "${f}" | trim_blank)
        if [ -n "${part}" ]; then
          body="${body}${body:+
}${part}"
        fi
      done
      if [ -n "${body}" ]; then
        printf '\n### %s\n\n%s\n' "${name}" "${body}"
      fi
    done
    printf '\n'
  } > "${SECTION}"
  cat "${MISSING}" >> "${SECTION}"
else
  cat "${MISSING}" > "${SECTION}"
fi

if [ -s "${SECTION}" ]; then
  # Insert immediately before the FIRST existing version heading, which
  # keeps the file's explanatory preamble on top and the releases
  # reverse-chronological. A CHANGELOG with no '## ' heading yet gets
  # the block appended.
  awk -v section_file="${SECTION}" '
    BEGIN { inserted = 0 }
    /^## / && !inserted {
      while ((getline line < section_file) > 0) print line
      close(section_file)
      inserted = 1
    }
    { print }
    END {
      if (!inserted) {
        while ((getline line < section_file) > 0) print line
        close(section_file)
      }
    }
  ' "${CHANGELOG}" > "${SECTION}.new"
  mv "${SECTION}.new" "${CHANGELOG}"

  # Post-condition before anything is deleted. awk's `getline < file`
  # returns -1 (not 0) when the file cannot be read, so a failed read
  # would skip the insert loop, still set inserted=1, and exit 0 — the
  # CHANGELOG would come through unchanged and we would then delete
  # every fragment, losing the entries from both places. Prove the
  # section landed instead of trusting the exit status.
  if [ "${#FOLD[@]}" -gt 0 ]; then
    grep -qE "^## \[${VERSION//./\\.}\]" "${CHANGELOG}" || {
      echo "::error::assembly produced no '## [${VERSION}]' section; refusing to delete fragments" >&2
      exit 1
    }
  fi
fi

# In assemble mode the folded fragments are deleted too — their text is
# in the file now. git rm when a fragment is tracked (the release
# path), plain rm otherwise (a local dry run on files not yet
# committed).
#
# -f is required, not cosmetic: plain `git rm` REFUSES a file whose
# staged content differs from HEAD, which is every freshly-added
# fragment. Without it the script dies here having ALREADY rewritten
# CHANGELOG.md — a half-applied state with the entry duplicated in both
# places. We are deleting the file on purpose, so any staged state is
# irrelevant.
if [ "${RESTORE_ONLY}" -eq 0 ]; then
  DELETE+=(${FOLD[@]+"${FOLD[@]}"})
fi
for f in ${DELETE[@]+"${DELETE[@]}"}; do
  if git ls-files --error-unmatch "${f}" >/dev/null 2>&1; then
    git rm -q -f "${f}"
  else
    rm -f "${f}"
  fi
done

if [ "${#FOLD[@]}" -gt 0 ]; then
  echo "==> ${CHANGELOG} now carries ${VERSION}; fragments removed"
elif [ -s "${MISSING}" ]; then
  echo "==> restored released section(s) into ${CHANGELOG}; removed ${#DELETE[@]} already-released fragment(s)"
else
  echo "==> nothing new to fold; removed ${#DELETE[@]} fragment(s)"
fi
