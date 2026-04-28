#!/usr/bin/env bash
outdir=$(mktemp -d)

go test \
	-covermode=atomic \
	-coverprofile="$outdir"/coverage.out \
	. \
	>/dev/null
cat - - <(tail -n +2 -q "$outdir"/*.out) <<<'mode: atomic' >coverage.out
rm -rf "$outdir"
