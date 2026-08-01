#!/bin/bash
# On-demand fuzz campaign for the parser package. Set FUZZTIME for longer
# runs, e.g. FUZZTIME=10m ./fuzz.sh
set -e
FUZZTIME="${FUZZTIME:-120s}"
for target in FuzzParseString FuzzElement FuzzScriptParser FuzzExtractFuncDeclSignature; do
	echo "$target"
	go test -run '^$' -fuzz "^${target}$" -fuzztime "$FUZZTIME"
done
