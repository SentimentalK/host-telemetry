#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

names=$(
	awk '/^const \(/ { block++; next } block == 1 && /^\)/ { exit } block == 1' internal/schema/schema_gen.go |
		grep -E '= "[a-z0-9._]+"' |
		sed -E 's/.*= "//; s/"[[:space:]]*$//; s/\./\\./g' |
		paste -sd'|'
)

if [ -z "$names" ]; then
	echo "no metric names found in internal/schema/schema_gen.go" >&2
	exit 1
fi

hits=$(
	grep -rnE --include='*.go' "\"(${names})\"" . |
		grep -vE '^\./vendor/|^\./internal/schema/schema_gen\.go:|_test\.go:' |
		sed -E 's|^\./||; s|^([^:]+):([0-9]+):|\1:\2: |' || true
)

if [ -n "$hits" ]; then
	printf '%s\n' "$hits"
	exit 1
fi
