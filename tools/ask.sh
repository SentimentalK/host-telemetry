#!/bin/sh
set -eu

VM=${VM:-http://127.0.0.1:18428}
VLOGS=${VLOGS:-http://127.0.0.1:19428}
LIMIT=${LIMIT:-5}
AT=${AT:-$(( $(date +%s) - 60 ))}

if [ "$LIMIT" -gt 20 ]; then
	LIMIT=20
fi

BODY=
trap 'if [ -n "$BODY" ]; then rm -f "$BODY"; fi' EXIT

usage() {
	echo "usage: tools/ask.sh --list | <recipe> | <PromQL>" >&2
	exit 2
}

list() {
	printf '%s\t%s\n' \
		stall '现在是否在 stall' \
		bottleneck '瓶颈是 cpu、memory 还是 io' \
		workload-memory '哪个 workload 占内存最多' \
		disk '磁盘是否快满' \
		oom '最近有 OOM 吗' \
		clock-skew '最近一次时钟跳变'
}

render() {
	python3 - "$1" "$LIMIT" "$BODY" <<'PY'
import json
import sys

mode, limit, path = sys.argv[1], int(sys.argv[2]), sys.argv[3]


def fail(source, reason):
    print("ask: %s: %s" % (source, reason), file=sys.stderr)
    sys.exit(1)


def field(record, key):
    value = record.get(key)
    if value is None or value == "":
        return "-"
    return str(value)


def scalar(value):
    try:
        return float(value)
    except (TypeError, ValueError):
        fail("victoriametrics", "unexpected value %r" % (value,))


with open(path) as handle:
    body = handle.read()

if mode in ("oom", "skew"):
    rows = []
    for line in body.splitlines():
        if not line.strip():
            continue
        try:
            rows.append(json.loads(line))
        except json.JSONDecodeError:
            fail("victorialogs", "invalid JSON response")
    rows = rows[:limit]
    if not rows:
        print("no rows")
        sys.exit(0)
    if mode == "oom":
        print("\t".join(("time", "pid", "workload.id", "rss")))
        for row in rows:
            print("\t".join((field(row, "_time"), field(row, "pid"), field(row, "workload.id"), field(row, "rss"))))
    else:
        print("\t".join(("time", "jump_ms")))
        for row in rows:
            print("\t".join((field(row, "_time"), field(row, "jump_ms"))))
    sys.exit(0)

try:
    response = json.loads(body)
except json.JSONDecodeError:
    fail("victoriametrics", "invalid JSON response")
if response.get("status") != "success":
    fail("victoriametrics", response.get("error") or response.get("status") or "query failed")
rows = response.get("data", {}).get("result", [])
if not rows:
    print("no rows")
    sys.exit(0)
if mode == "raw":
    print("\t".join(("labels", "value")))
    for row in rows[:limit]:
        metric = row.get("metric", {})
        labels = ",".join("%s=%s" % (key, metric[key]) for key in sorted(metric))
        print("\t".join((labels, "%.6g" % scalar(row.get("value", [None, None])[1]))))
elif mode == "pressure":
    print("\t".join(("resource", "full_utilization")))
    for row in rows[:limit]:
        metric = row.get("metric", {})
        print("\t".join((field(metric, "system.pressure.resource"), "%.4f" % scalar(row.get("value", [None, None])[1]))))
elif mode == "workload":
    print("\t".join(("workload.id", "bytes", "mib")))
    for row in rows[:limit]:
        metric = row.get("metric", {})
        size = int(scalar(row.get("value", [None, None])[1]))
        print("\t".join((field(metric, "workload.id"), str(size), "%.1f" % (size / 1048576.0))))
elif mode == "disk":
    print("\t".join(("mountpoint", "device", "used_ratio")))
    for row in rows[:limit]:
        metric = row.get("metric", {})
        print("\t".join((
            field(metric, "system.filesystem.mountpoint"),
            field(metric, "system.device.name"),
            "%.3f" % scalar(row.get("value", [None, None])[1]),
        )))
PY
}

metric() {
	BODY=$(mktemp)
	if ! err=$(curl -fsS -G "$VM/api/v1/query" --data-urlencode "query=$1" --data-urlencode "time=$AT" -o "$BODY" 2>&1); then
		echo "ask: victoriametrics: $err" >&2
		exit 1
	fi
	render "$2"
}

logs() {
	BODY=$(mktemp)
	if ! err=$(curl -fsS -G "$VLOGS/select/logsql/query" --data-urlencode "query=$1" --data-urlencode "limit=$LIMIT" -o "$BODY" 2>&1); then
		echo "ask: victorialogs: $err; $VLOGS unreachable, run make stack-up and make ship-events" >&2
		exit 1
	fi
	render "$2"
}

case ${1-} in
'')
	usage
	;;
--list)
	list
	;;
stall)
	metric 'max by ("system.pressure.resource") ({__name__="system.pressure.utilization", "system.pressure.type"="full"})' pressure
	;;
bottleneck)
	metric 'topk(1, max by ("system.pressure.resource") ({__name__="system.pressure.utilization", "system.pressure.type"="full"}))' pressure
	;;
workload-memory)
	metric "topk($LIMIT, {__name__=\"workload.memory.usage\"})" workload
	;;
disk)
	metric "topk($LIMIT, {__name__=\"system.filesystem.utilization\", \"system.filesystem.state\"=\"used\"})" disk
	;;
oom)
	logs "event.name:\"host.oom.kill\" | sort by (_time) desc | limit $LIMIT" oom
	;;
clock-skew)
	logs "event.name:\"host.clock.skew\" | sort by (_time) desc | limit 1" skew
	;;
*)
	metric "$1" raw
	;;
esac
