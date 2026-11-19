#!/bin/sh
set -eu

VM=${VM:-http://127.0.0.1:8428}
SERVICE=${SERVICE:-host-telemetry}

NOW=$(($(date +%s) - 60))
START=$(( $(date +%s) - 86400 ))

curl_failed() {
	printf 'curl failed: %s\n' "$1" >&2
	exit 1
}

export_url="$VM/api/v1/export"
series=$(curl -fsS --get --data-urlencode 'match[]=system.uptime' --data-urlencode "start=$START" "$export_url") || curl_failed "$export_url"

printf 'samples and gaps\n'
printf '%s' "$series" | python3 -c '
import json, sys

timestamps = []
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    series = json.loads(line)
    timestamps.extend(t / 1000.0 for t in series.get("timestamps", []))
timestamps.sort()

samples = len(timestamps)
expected = 5760
gaps = [b - a for a, b in zip(timestamps, timestamps[1:])]
print("samples=%d" % samples)
print("expected=%d" % expected)
print("ratio=%.4f" % (samples / expected))
print("gaps_gt_30s=%d" % sum(1 for gap in gaps if gap > 30))
print("max_gap_s=%.1f" % (max(gaps) if gaps else 0.0))
'

printf '\nbudgets\n'
query() {
	expr=$1
	url="$VM/api/v1/query"
	body=$(curl -fsS --get --data-urlencode "query=$expr" --data-urlencode "time=$NOW" "$url") || curl_failed "$url"
	printf '%s' "$body" | python3 -c '
import json, sys

expr = sys.argv[1]
payload = json.load(sys.stdin)
if payload.get("status") != "success":
    sys.exit("%s: %s" % (expr, payload.get("error", "query failed")))
values = [float(entry["value"][1]) for entry in payload.get("data", {}).get("result", [])]
print("%s = %s" % (expr, format(sum(values), ".6g")))
' "$expr"
}

query 'max_over_time(collector.wal.size[24h])'
query 'max_over_time(collector.self.memory.usage[24h])'
query 'rate(collector.self.cpu.time[24h])'
query 'sum_over_time(collector.wal.dropped[24h])'
query 'sum_over_time(collector.source.error[24h])'

printf '\nduplicates\n'
printf '%s' "$series" | python3 -c '
import json, sys

seen = {}
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    series = json.loads(line)
    label = json.dumps(series.get("metric", {}), sort_keys=True)
    for timestamp in series.get("timestamps", []):
        key = (label, timestamp)
        seen[key] = seen.get(key, 0) + 1
print("duplicates=%d" % sum(count - 1 for count in seen.values() if count > 1))
'

printf '\nprocess\n'
systemctl --user show -p MemoryCurrent -p ActiveEnterTimestamp -p NRestarts "$SERVICE"
