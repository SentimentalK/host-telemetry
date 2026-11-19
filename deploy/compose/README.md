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

Queries: VictoriaLogs applies `search.latencyOffset=30s`, so an instant query must pass
`time=now-60`.
