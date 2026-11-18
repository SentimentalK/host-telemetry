#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

hits=$(
	grep -rnE --include='*.go' '"(system|hw|process|collector|workload)\.' . |
		grep -vE '^\./vendor/|^\./internal/schema/schema_gen\.go:|_test\.go:' |
		sed -E 's|^\./||; s|^([^:]+):([0-9]+):|\1:\2: |' || true
)

if [ -n "$hits" ]; then
	printf '%s\n' "$hits"
	exit 1
fi
