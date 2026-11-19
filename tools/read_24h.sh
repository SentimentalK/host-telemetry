#!/bin/sh
set -eu

VM=${VM:-http://127.0.0.1:8428}
SERVICE=${SERVICE:-host-telemetry}
DAYS=${DAYS:-1}
EPOCH=$(date +%s)
START=${SINCE:-$((EPOCH - DAYS * 86400))}
WINDOW=$((EPOCH - START))
NOW=$((EPOCH - 60))

curl_failed() {
	printf 'curl failed: %s\n' "$1" >&2
	exit 1
}

export_url="$VM/api/v1/export"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fetch() {
	metric=$1
	out=$2
	curl -fsS --get --data-urlencode "match[]=$metric" --data-urlencode "start=$START" "$export_url" >"$out" ||
		curl_failed "$export_url?match[]=$metric"
}

fetch 'system.uptime' "$tmp/uptime.jsonl"
fetch 'system.cpu.utilization' "$tmp/cpu.jsonl"

printf 'cadence\n'
python3 - "$WINDOW" "$tmp/uptime.jsonl" 'system.uptime' 4 "$tmp/cpu.jsonl" 'system.cpu.utilization' 15 <<'PY'
import json, statistics, sys

window = float(sys.argv[1])
pairs = [(sys.argv[i], sys.argv[i + 1], float(sys.argv[i + 2])) for i in range(2, len(sys.argv), 3)]
failed = 0
for path, name, interval in pairs:
    expected = window / interval
    series = {}
    with open(path) as handle:
        for line in handle:
            line = line.strip()
            if not line:
                continue
            entry = json.loads(line)
            key = json.dumps(entry.get("metric", {}), sort_keys=True)
            series.setdefault(key, []).extend(t / 1000.0 for t in entry.get("timestamps", []))
    if not series:
        print("%s interval=%gs expected=%.0f series=0" % (name, interval, expected))
        failed = 1
        continue
    ratios, gaps, max_gap, medians, samples = [], 0, 0.0, [], 0
    for timestamps in series.values():
        timestamps.sort()
        samples += len(timestamps)
        ratios.append(len(timestamps) / expected)
        diffs = [b - a for a, b in zip(timestamps, timestamps[1:])]
        if not diffs:
            continue
        median = statistics.median(diffs)
        medians.append(median)
        threshold = max(30.0, 3 * median)
        gaps += sum(1 for diff in diffs if diff > threshold)
        max_gap = max(max_gap, max(diffs))
    print(
        "%s interval=%gs expected=%.0f series=%d samples=%d min_ratio=%.4f gaps_gt_threshold=%d max_gap_s=%.1f median_gap_s=%.1f"
        % (name, interval, expected, len(series), samples, min(ratios), gaps, max_gap, statistics.median(medians) if medians else 0.0)
    )
sys.exit(1 if failed else 0)
PY

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
python3 - "$tmp/uptime.jsonl" <<'PY'
import json, sys

seen = {}
with open(sys.argv[1]) as handle:
    for line in handle:
        line = line.strip()
        if not line:
            continue
        entry = json.loads(line)
        label = json.dumps(entry.get("metric", {}), sort_keys=True)
        for timestamp in entry.get("timestamps", []):
            key = (label, timestamp)
            seen[key] = seen.get(key, 0) + 1
print("duplicates=%d" % sum(count - 1 for count in seen.values() if count > 1))
PY

printf '\nprocess\n'
systemctl --user show -p MemoryCurrent -p ActiveEnterTimestamp -p NRestarts "$SERVICE"
