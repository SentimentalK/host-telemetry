#!/bin/sh
set -eu

STATE=${HT_STATE:-$HOME/.local/state/host-telemetry}
SRC=$STATE/events.ndjson
CURSOR=$STATE/m5/events.cursor
VLOGS=${VLOGS:-http://127.0.0.1:19428}

if [ ! -f "$SRC" ]; then
	echo "no new events"
	exit 0
fi

mkdir -p "$STATE/m5"

offset=0
if [ -f "$CURSOR" ]; then
	offset=$(cat "$CURSOR")
fi

size=$(wc -c <"$SRC" | tr -d ' ')
if [ "$size" -lt "$offset" ]; then
	offset=0
fi
if [ "$size" -eq "$offset" ]; then
	echo "no new events"
	exit 0
fi

payload=$STATE/m5/events.jsonl.$$
cursor_tmp=$CURSOR.tmp
trap 'rm -f "$payload"' EXIT

count=$(python3 - "$SRC" "$offset" "$payload" "$cursor_tmp" <<'PY'
import json
import sys

src, offset, payload_path, cursor_path = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4]
with open(src, "rb") as source:
    source.seek(offset)
    data = source.read()

lines = data.split(b"\n")
complete = lines[:-1]
consumed = sum(len(line) + 1 for line in complete)

written = 0
with open(payload_path, "w") as payload:
    for raw in complete:
        if not raw.strip():
            continue
        event = json.loads(raw)
        record = {
            "_time": event["time"],
            "_msg": event["event.name"],
            "event.name": event["event.name"],
        }
        for key, value in event.get("attributes", {}).items():
            record[key] = value if isinstance(value, str) else json.dumps(value)
        payload.write(json.dumps(record, separators=(",", ":")) + "\n")
        written += 1

with open(cursor_path, "w") as cursor:
    cursor.write(str(offset + consumed) + "\n")

print(written)
PY
)

if [ "$count" -eq 0 ]; then
	rm -f "$cursor_tmp"
	echo "no new events"
	exit 0
fi

curl -fsS -X POST -H 'Content-Type: application/stream+json' --data-binary "@$payload" "$VLOGS/insert/jsonline?_stream_fields=event.name"
mv "$cursor_tmp" "$CURSOR"
