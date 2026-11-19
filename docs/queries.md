# 查询配方

面向人和 AI：先执行前置，再照抄命令。`tools/ask.sh` 只读本地 sink，不改任何状态。

## 前置

```sh
make stack-up

go build -o $HT_STATE/m5/bin/host-telemetry ./cmd/host-telemetry
$HT_STATE/m5/bin/host-telemetry run \
  --listen 127.0.0.1:19101 \
  --endpoint http://127.0.0.1:18428/opentelemetry/api/v1/push \
  --wal-dir $HT_STATE/m5/wal \
  --workload

make ship-events
```

- `$HT_STATE` 默认是 `$HOME/.local/state/host-telemetry`。
- 演示用 `--events-disk-full-ratio 0.01` 可以立即产出 `host.disk.full` 事件；不加则只等真实事件。
- `make ship-events` 把 collector 的 `events.ndjson` 镜像进 VictoriaLogs；第二次运行会打印 `no new events`。
- 可调环境变量：`VM`（默认 `http://127.0.0.1:18428`）、`VLOGS`（默认 `http://127.0.0.1:19428`）、`LIMIT`（默认 5，超过 20 截到 20）、`AT`（instant query 的时间戳，默认 `now-60`）。

## 配方

### stall

问题：现在是否在 stall

```sh
make ask Q=stall
```

PromQL：

```promql
max by ("system.pressure.resource") ({__name__="system.pressure.utilization", "system.pressure.type"="full"})
```

返回列：`resource`（`cpu` / `memory` / `io`）、`full_utilization`（0–1 分数，保留 4 位）。

判读：PSI `full` 为 0 表示没有实际停顿；大于 0 说明该资源把任务真正卡住了。

### bottleneck

问题：瓶颈是 cpu、memory 还是 io

```sh
make ask Q=bottleneck
```

PromQL：

```promql
topk(1, max by ("system.pressure.resource") ({__name__="system.pressure.utilization", "system.pressure.type"="full"}))
```

返回列：`resource`、`full_utilization`（0–1 分数，保留 4 位）。

判读：只给当前 `full` 停顿分数最大的一个资源；全为 0 时结果没有诊断意义，改用 `stall` 或查 CPU/内存/磁盘明细。

### workload-memory

问题：哪个 workload 占内存最多

```sh
make ask Q=workload-memory
```

PromQL（`LIMIT` 会替换成实际数字）：

```promql
topk($LIMIT, {__name__="workload.memory.usage"})
```

返回列：`workload.id`、`bytes`（整数，UCUM `By`）、`mib`（`bytes / 1048576`，保留 1 位）。

判读：Linux 上这是 cgroup v2 `memory.current`，包含 page cache，不能与进程 RSS 逐项相加，也不要汇总成整机内存。

### disk

问题：磁盘是否快满

```sh
make ask Q=disk
```

PromQL：

```promql
topk($LIMIT, {__name__="system.filesystem.utilization", "system.filesystem.state"="used"})
```

返回列：`mountpoint`、`device`、`used_ratio`（0–1 分数，保留 3 位）。

判读：`used_ratio` 是已用比例，不是百分比；接近 1 表示快满，配合 `host.disk.full` 事件确认。

### oom

问题：最近有 OOM 吗

```sh
make ask Q=oom
```

LogsQL：

```logsql
event.name:"host.oom.kill" | sort by (_time) desc | limit $LIMIT
```

返回列：`time`、`pid`、`workload.id`、`rss`（`By`）；缺失字段输出 `-`。无数据时只打印 `no rows` 并 exit 0。

判读：`time` 是事件发生时间（RFC3339，UTC）；`workload.id` 为空说明内核日志里没有可归属的 cgroup scope。

### clock-skew

问题：最近一次时钟跳变

```sh
make ask Q=clock-skew
```

LogsQL：

```logsql
event.name:"host.clock.skew" | sort by (_time) desc | limit 1
```

返回列：`time`、`jump_ms`（毫秒，缺失为 `-`）。无数据时只打印 `no rows` 并 exit 0。

判读：`jump_ms` 是跳变幅度；负值表示时钟回拨。配合 `system.uptime` 和 rate 类指标排除假尖峰。

## 原始 PromQL

不是配方的参数一律当作 PromQL，输出固定两列 `labels`、`value`：

```sh
make ask Q='system.uptime'
```

- `labels`：`metric` 里排序后的 `key=value` 用逗号连接，含 `__name__`。
- `value`：`%.6g`。
- 无匹配序列时只打印 `no rows` 并 exit 0。

## 实测约束

- 点号指标名直接写（`system.uptime`）可以查；加引号当 selector（`"system.uptime"`）返回空。
- 标签名带点时必须放在 `{}` 内并加引号：`{"system.pressure.type"="full"}`。
- `max by` 的 selector 必须带括号：`max by ("system.pressure.resource") ({...})`。
- VictoriaMetrics 默认 `search.latencyOffset=30s`，instant query 要用 `time=now-60`；`tools/ask.sh` 已经用 `AT` 处理，手工 curl 时要自己减 60。
- 查询失败时 stderr 打印 `ask: <来源>: <原因>` 并 exit 1；VictoriaLogs 不可达时提示里会带对应来源（`VM` 或 `VLOGS`）、`make stack-up`、`make ship-events`。

指标目录见 [`docs/metrics.md`](metrics.md)；sink 与端口见 [`deploy/compose/README.md`](../deploy/compose/README.md)。
