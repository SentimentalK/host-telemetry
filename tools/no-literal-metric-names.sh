#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

generated=internal/schema/schema_gen.go

metric_names=$(
	awk '/^const \(/ { block++; next } block == 1 && /^\)/ { exit } block == 1' "$generated" |
		grep -E '= "[a-z0-9._]+"' |
		sed -E 's/.*= "//; s/"[[:space:]]*$//' || true
)

attr_names=$(
	awk '/^const \(/ { block++; next } block == 2 && /^\)/ { exit } block == 2' "$generated" |
		grep -E '^[[:space:]]*Attr[A-Za-z0-9_]+[[:space:]]+= "' |
		sed -E 's/.*= "//; s/"[[:space:]]*$//' || true
)

if [ -z "$metric_names" ]; then
	echo "no metric names found in $generated" >&2
	exit 1
fi
if [ -z "$attr_names" ]; then
	echo "no attribute names found in $generated" >&2
	exit 1
fi

names=$(
	printf '%s\n%s\n' "$metric_names" "$attr_names" |
		grep -v '^$' |
		sed -E 's/\./\\./g' |
		paste -sd'|'
)

hits=$(
	grep -rnE --include='*.go' "\"(${names})\"" . |
		grep -vE "^\./vendor/|^\./${generated}:|_test\.go:" |
		sed -E 's|^\./||; s|^([^:]+):([0-9]+):|\1:\2: |' || true
)

if [ -n "$hits" ]; then
	printf '%s\n' "$hits"
	exit 1
fi
