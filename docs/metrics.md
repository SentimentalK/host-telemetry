# Metric catalog

Every name below is either taken verbatim from OpenTelemetry semantic conventions or marked
`experimental` with an ADR. Units are UCUM. **Do not add a row here without adding the same name to
the generated schema package.**

Legend — Platforms: `L` Linux, `M` macOS, `W` Windows. Requirement: `R` recommended baseline,
`O` opt-in (off unless configured).

## Contention (answers "was it bottlenecked?")

| Metric | Type | Unit | Attributes | Platform | Interval | Why it exists |
|---|---|---|---|---|---|---|
| `system.pressure.stall.time` | Counter | `s` | `system.pressure.resource=cpu\|memory\|io`, `system.pressure.type=some\|full` | L | 15s | Total stalled time. `full` > 0 means throughput was actually wasted. **experimental — see ADR 0002** |
| `system.pressure.utilization` | Gauge | `1` | same as above | L | 15s | Kernel-maintained avg10/60/300 stall fraction; the fastest way to answer "is it stalled now". **experimental** |
| `system.cpu.utilization` | Gauge | `1` | `cpu.mode=user\|system\|nice\|idle\|iowait\|interrupt\|steal`, `cpu.logical_number` (opt-in) | L M W | 15s | Utilization alone cannot prove contention; keep it as the denominator next to PSI |
| `system.cpu.time` | Counter | `s` | `cpu.mode`, `cpu.logical_number` | L M W | 15s | Rate source when the backend prefers counters |
| `system.cpu.frequency` | Gauge | `Hz` | `cpu.logical_number` | L M W | 30s | Frequency collapse at constant utilization = throttling |
| `system.cpu.logical.count` / `system.cpu.physical.count` | UpDownCounter | `{cpu}` | — | L M W | 300s | Normalizes every per-core metric across machines |
| `system.memory.usage` | UpDownCounter | `By` | `system.memory.state=used\|available` | L M W | 15s | `available` (not `free`) is the only honest "how much is left" |
| `system.memory.limit` | UpDownCounter | `By` | — | L M W | 300s | Denominator for utilization; changes on hot-plug |
| `system.memory.utilization` | Gauge | `1` | `system.memory.state=used\|available` | L M W | 15s | Cheap alerting signal |
| `system.paging.usage` | UpDownCounter | `By` | `system.paging.state=used\|free` | L M W | 15s | Swap in use; correlated with the first slow minute |
| `system.paging.operations` | Counter | `{operation}` | `system.paging.fault.type=major\|minor`, `system.paging.direction=in\|out` | L M W | 15s | Major faults / swap-in rate is the actual thrash signal |
| `system.paging.faults` | Counter | `{fault}` | `system.paging.fault.type=major\|minor` | L M W | 15s | Distinguishes cold cache from real pressure |
| `system.disk.io` | Counter | `By` | `disk.io.direction=read\|write`, `system.device.name` | L M W | 15s | Throughput, not latency |
| `system.disk.operations` | Counter | `{operation}` | `disk.io.direction`, `system.device.name` | L M W | 15s | IOPS; small random reads are the usual build bottleneck |
| `system.disk.io_time` | Counter | `s` | `system.device.name` | L M W | 15s | Busy-time; the closest portable `%util` |
| `system.disk.operation_time` | Counter | `s` | `disk.io.direction`, `system.device.name` | L M W | 15s | Divide by operations for latency — the metric people actually need |
| `system.filesystem.usage` | UpDownCounter | `By` | `system.filesystem.state=used\|free\|reserved`, `system.filesystem.mountpoint`, `system.filesystem.type` | L M W | 60s | Disk-full is a classic silent failure |
| `system.filesystem.utilization` | Gauge | `1` | same | L M W | 60s | Derived, but cheap to keep for alerts |
| `system.network.io` | Counter | `By` | `network.io.direction=receive\|transmit`, `network.interface.name` | L M W | 15s | "Big traffic came in" needs an interface-level denominator |
| `system.network.errors` | Counter | `{error}` | `network.io.direction`, `network.interface.name` | L M W | 15s | Hardware/driver trouble disguised as application slowness |
| `system.network.packet.dropped` | Counter | `{packet}` | `network.io.direction`, `network.interface.name` | L M W | 15s | Drops explain retransmit-shaped latency |
| `system.network.connection.count` | UpDownCounter | `{connection}` | `network.transport=tcp`, `network.connection.state` | L M W | 15s | Connection storms from many agents |
| `system.process.count` | UpDownCounter | `{process}` | `process.state=running\|sleeping\|stopped\|defunct` | L M W | 15s | Zombie/leak detection |
| `system.process.created` | Counter | `{process}` | — | L M W | 15s | Fork storms (test runners, builds) |
| `system.uptime` | Gauge | `s` | — | L M W | 60s | Proves the boot-install worked and detects unnoticed reboots |

## Hardware (answers "why did it get slow while idle?")

| Metric | Type | Unit | Attributes | Platform | Interval | Why it exists |
|---|---|---|---|---|---|---|
| `hw.temperature` | Gauge | `Cel` | `hw.id`, `hw.name`, `hw.sensor_location`, `hw.parent` | L M W | 30s | Context, not a diagnosis — always read next to frequency |
| `hw.temperature.limit` | Gauge | `Cel` | + `hw.limit_type=high.critical\|high.degraded\|throttled` | L M W | 300s | Gives a normalized distance-to-throttle |
| `hw.fan.speed` | Gauge | `rpm` | `hw.id`, `hw.name`, `hw.sensor_location` | L M W | 30s | Fans pinned at max + falling frequency = thermal wall |
| `hw.fan.speed_ratio` | Gauge | `1` | same | L M W | 30s | Portable across 2-fan laptops and 8-fan servers |
| `hw.fan.speed.limit` | Gauge | `rpm` | + `hw.limit_type` | L M W | 300s | Denominator for the ratio when the OS does not provide it |
| `hw.status` | UpDownCounter | `1` | `hw.type=fan\|temperature\|cpu`, `hw.state=ok\|degraded\|failed`, `hw.id` | L M W | 60s | The portable way to report "throttled" without inventing a metric (`hw.limit_type=throttled`) |
| `hw.power` | Gauge | `W` | `hw.id`, `hw.type` | L M W | 30s | Package power tells whether the SoC is power-limited rather than thermally limited |

## Workload attribution (answers "which agent did it?")

| Metric | Type | Unit | Attributes | Platform | Interval | Why it exists |
|---|---|---|---|---|---|---|
| `workload.count` | Gauge | `{workload}` | `workload.kind=agent\|container\|unit`, `workload.state=working\|blocked\|done` | all | 30s | "At 21:00 I had 14 agents" is a metric, not a log grep |
| `workload.cpu.time` | Counter | `s` | `workload.id`, `workload.project`, `cpu.mode` | all | 15s | Attribution; cgroup-backed on Linux |
| `workload.memory.usage` | UpDownCounter | `By` | `workload.id`, `workload.project` | all | 15s | **cgroup v2 `memory.current` on Linux — the only non-double-counted number** |
| `workload.pressure.utilization` | Gauge | `1` | `workload.id`, `system.pressure.resource` | L | 15s | `memory.pressure` per scope: which agent was being throttled, not just which was big |

## Process detail (opt-in, bounded)

Off by default. When enabled, only whitelisted executables and their recursive children, capped at
`process.top_n` (default 32) by memory. `process.pid` is high cardinality — it is for debugging, not
for dashboards, and `workload.id` is always preferred over it.

| Metric | Type | Unit | Attributes | Why |
|---|---|---|---|---|
| `process.cpu.time` / `process.cpu.utilization` | Counter / Gauge | `s` / `1` | `process.pid`, `process.command`, `process.parent_pid` | Per-agent CPU |
| `process.memory.usage` | UpDownCounter | `By` | same | RSS; over-counts shared pages — never sum it into host totals |
| `process.memory.virtual` | UpDownCounter | `By` | same | Catch "address space exploded" bugs |
| `process.thread.count` | UpDownCounter | `{thread}` | same | Thread leaks look exactly like slowness |
| `process.disk.io` | Counter | `By` | + `disk.io.direction` | Which agent is thrashing the disk |
| `process.context_switches` | Counter | `{context_switch}` | + `process.context_switch.type=voluntary\|involuntary` | Involuntary switches = CPU oversubscription |
| `process.unix.file_descriptor.count` / `process.windows.handle.count` | UpDownCounter | `{file_descriptor}\|{handle}` | same | FD exhaustion in long-running agents |
| `process.uptime` | Gauge | `s` | same | Restart detection |

## Collector self-observability (proves G4)

| Metric | Type | Unit | Attributes | Why |
|---|---|---|---|---|
| `collector.self.cpu.time` | Counter | `s` | — | The overhead claim, measured by the process itself |
| `collector.self.memory.usage` | UpDownCounter | `By` | — | Same |
| `collector.wal.size` | Gauge | `By` | — | Bounded buffer proof |
| `collector.wal.dropped` | Counter | `{sample}` | — | Silent loss must never be silent |
| `collector.send.queue.length` | Gauge | `{sample}` | — | Backpressure visibility |
| `collector.scrape.duration` | Gauge | `s` | — | Per-source cost; catches a source misbehaving |
| `collector.source.error` | Counter | `{error}` | `collector.source` | Partial-failure isolation |

## Reading order (how an AI should use this)

1. `system.pressure.utilization{type="full"}` — was anything actually stalled?
2. If `memory`: check `system.paging.operations{type="major"}` and `workload.memory.usage` top-k.
3. If `cpu`: check `system.cpu.utilization`, then `process.context_switches{type="involuntary"}`.
4. If `io`: check `system.disk.operation_time / system.disk.operations` for latency, then per-workload `process.disk.io`.
5. If none are elevated: the bottleneck is outside the host — rate limits, network, or waiting on a human. Check `workload.count{state="blocked"}` and the event stream.

`/metrics` uses normalized sample names (dots to underscores, `_total` on counters); the OTel name and UCUM unit stay in the `# HELP` text — debug output, not the contract.
