# Compose stack

Local sink stack for the collector: VictoriaMetrics for metrics, VictoriaLogs for events,
Grafana (profile) for queries. State lives outside the repo.

|Service|Port|Profile|
|---|---|---|
|VictoriaMetrics|18428|default|
|VictoriaLogs|19428|default|
|Grafana|3000|`grafana`|

Data directories: `$HT_STATE/m5/{vmdata,vlogsdata,grafana}`. Containers write as root, and
`make stack-down` keeps them.

Collector:

```sh
host-telemetry run --listen 127.0.0.1:19101 --endpoint http://127.0.0.1:18428/opentelemetry/api/v1/push --wal-dir $HT_STATE/m5/wal --workload
```

`make ship-events` mirrors the collector's NDJSON event file into VictoriaLogs. The collector's
OTLP client has a single endpoint, so it cannot deliver metrics and logs to different backends;
events are shipped from their NDJSON mirror instead.

Queries: `tools/ask.sh` wraps both sinks for the six recipes in
[`docs/queries.md`](../../docs/queries.md) and passes `time=now-60`. `make ask Q=stall` runs one
recipe; `./tools/ask.sh --list` lists them; an argument that is not a recipe is treated as a raw
PromQL query. VictoriaMetrics applies `search.latencyOffset=30s`, so a hand-written instant query
must pass `time=now-60`.

Events: `make ship-events` ships once and records a cursor in
`$HT_STATE/m5/events.cursor`; a second run prints `no new events`. Query them with
`curl -G http://127.0.0.1:19428/select/logsql/query --data-urlencode 'query=event.name:"host.disk.full"'`.
An instant metric or event query needs `make stack-up` first; unreachable sinks make
`tools/ask.sh` exit 1 with the failing URL.
