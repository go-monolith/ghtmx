#!/usr/bin/env bash
#
# Publishes the root tag and the lockstep adapter tags for $TAG, pointing
# at HEAD. Called only after every gate has passed — a tag is immutable
# once the module proxy has seen it, so it must not exist before the tree
# behind it is known good.
#
# Existence is checked against the REMOTE: the release checkout fetches
# no tags, so a local rev-parse would always miss and every re-run would
# mint a fresh tag object whose push is then rejected — leaving a partial
# release, root tagged and adapters not, that no re-run could finish.
#
# Idempotent by design: a tag already pointing at this commit is left
# alone, a missing one is filled in, and one pointing elsewhere is a
# published version trying to move under consumers, which fails loudly.
# See internal/release/plan_test.go.
set -euo pipefail

: "${TAG:?the version to publish, e.g. v1.2.3}"

git config user.name "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"

target=$(git rev-parse HEAD)
names=("$TAG")
for mod in adapters/*/go.mod; do
  test -e "$mod" || { echo "no adapter modules found — wrong tree?"; exit 1; }
  names+=("$(dirname "$mod")/$TAG")
done

for name in "${names[@]}"; do
  # An annotated tag resolves to its own object, so prefer the peeled ref
  # to compare commits rather than tag objects.
  peeled=$(git ls-remote origin "refs/tags/$name^{}" | cut -f1)
  raw=$(git ls-remote origin "refs/tags/$name" | cut -f1)
  existing=${peeled:-$raw}
  if [ -z "$existing" ]; then
    # -f is local only. Recreating a local tag is free; it is the push
    # below that must never force, and does not.
    git tag -f -a "$name" -m "Release $name"
    git push origin "refs/tags/$name"
    echo "published $name"
  elif [ "$existing" = "$target" ]; then
    echo "$name already points here — nothing to do"
  else
    echo "$name already exists at $existing, refusing to move it to $target"
    exit 1
  fi
done
