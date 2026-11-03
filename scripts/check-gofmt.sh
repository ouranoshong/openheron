#!/bin/sh
# Fail when a Go file that is about to be committed is not gofmt-clean.
#
# ★ `gofmt -l` exits 0 even when it lists files, so the check has to be the
#   emptiness of its output. A hook that always passes is worse than no hook:
#   it looks like a check.
out=$(gofmt -l "$@")
if [ -n "$out" ]; then
	printf 'not gofmt-clean:\n%s\n' "$out"
	printf 'fix it with: gofmt -w <file>\n'
	exit 1
fi
