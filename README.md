# host-telemetry

One binary per machine. One schema for every OS. Installed at boot. Queryable by an AI.

```
curl -fsSL https://get.host-telemetry.dev | sh -s -- --endpoint https://<tsdb>:8428 --token $HT_TOKEN
```

**Status: Linux, macOS and Windows sources implemented, pre-alpha.** Linux sources, WAL/OTLP
transport and the systemd installer exist today; macOS and Windows source, installer and service
code are implemented but have not been verified on real hardware. The contract in this document is
what every implementation must satisfy; read `AGENTS.md` before writing code.

---

## 1. Goals

| # | Goal | Verifiable when |
|---|---|---|
| **G1** | One static binary per host, installed by one command, started at boot on Linux / macOS / Windows | reboot → first sample visible within 30s |
| **G2** | One data contract for every platform: metric names, dimensions, units are identical on every OS | golden tests: same fixture → same series set, schema validator rejects anything else |
| **G3** | Answer "was the machine bottlenecked at that moment, on what, caused by which workload" | 15 agents running: PSI + per-workload CPU/memory correlate with the observed slowdown |
| **G4** | Hard overhead budget, self-reported: ≤1% of one core, ≤80 MB RSS, ≤256 MB disk | CI benchmark + `collector.self.*` metrics |
| **G5** | Dataset is directly usable by an AI: metrics via PromQL/REST, events as a separate stream, no UI required | a query like "top workloads by memory pressure yesterday 21:00–22:00" answers without a dashboard |
| **G6** | No cluster dependency: works with a single endpoint, no Kubernetes, no Prometheus server required | a bare laptop pushing to one VictoriaMetrics instance is a complete deployment |

## 2. Non-goals

- **No UI, no reports, no alerting engine.** Grafana, Headlamp or an AI may read the data; this repo does not render it.
- **No time-series database.** Ship metrics to whatever the user already runs.
- **No LLM/agent tracing.** Prompt/token spans belong to OpenTelemetry GenAI instrumentation on the agent side.
- **No 100% hardware coverage.** Cover the metrics that explain contention; leave an extension interface for the rest.
- **No auto-discovery of every service.** Workload attribution is explicit and pluggable.

## 3. Why this project exists

The collection layer is a solved problem; the *contract*, the *bootstrap* and the *attribution* are not.

| | node_exporter | Telegraf | Grafana Alloy | Netdata | Beszel | **host-telemetry** |
|---|---|---|---|---|---|---|
| cross-platform host metrics | partial (macOS lacks `processes`/`pressure`) | yes | yes | yes | yes | yes |
| per-process attribution | no | yes | partial | yes | no (containers only) | **yes, first-class** |
| workload/agent attribution | no | manual patterns | manual | per-app | no | **`workload.*` in schema** |
| installs itself as a boot service | no (scrape target) | no | no | yes | yes | **yes, one command** |
| enforces one schema/unit set | no | no | no | no | no | **yes (codegen + validator)** |
| pushes without a Prometheus server | no | yes | yes | yes | proprietary | **OTLP out** |
| AI-readable event stream | no | no | no | no | no | **yes** |

Reusing existing collectors as *engines* is allowed and expected; see
`docs/decisions/0001-otel-semconv-as-data-contract.md` for the boundaries.

## 4. Data contract

**The contract is OpenTelemetry semantic conventions, pinned, generated, validated.**
Nothing in this repo hand-writes a metric name.

### 4.1 Rules

1. **Utilization is a 0–1 fraction with unit `1`.** Never percent. This single rule removes most cross-platform drift.
2. **Bytes are `By`, time is `s`.** Millisecond / kilobyte conversions happen inside the collector.
3. **Cardinality lives in attributes, never in names.** `system.cpu.utilization{cpu.logical_number="0"}`, not `cpu0_usage`. `hw.fan.speed{hw.id="fan0"}`, not `fan0_rpm`.
4. **Units come from the semconv model, not from `README` tables.** The schema package is generated from the vendored YAML model; a unit mismatch is a build error.
5. **Anything not covered by semconv needs an ADR before it lands.** See `docs/decisions/`.

### 4.2 Pinned source

The semantic conventions are still `Development` status and explicitly allowed to break names
and units before stabilization (the spec even tells existing collectors to pin an older
version). Therefore:

- the semconv version is pinned in `internal/schema/semconv.lock`;
- `schema.version` is attached to every reported series;
- renaming is a breaking change and gets a migration note in `CHANGELOG.md`.

### 4.3 Resource attributes (what makes "one row per machine")

`host.id` (stable machine identity — `machine-id` / `IOPlatformUUID` / Windows `MachineGuid`;
**never** hostname, **never** a MAC address), `host.name`, `host.arch`, `os.type`,
`os.version`, `os.description`, `service.name=host-telemetry`, `service.version`, `schema.version`.

### 4.4 Known gap: pressure stall information

OTel's `system.*` namespace has **no** PSI metric, and PSI is the single most useful signal for
"is this box actually stalled". This project defines it in semconv style and proposes it upstream:

```
system.pressure.stall.time{system.pressure.resource=cpu|memory|io, system.pressure.type=some|full}  Counter  s
system.pressure.utilization{同上}                                                                     Gauge    1
```

Marked `experimental` until accepted upstream. Linux-only (macOS/Windows have no equivalent
kernel facility; on those platforms the metric is simply absent, and thermal/paging signals carry
the load).

The full catalog lives in [`docs/metrics.md`](docs/metrics.md).

## 5. Architecture

```mermaid
flowchart TB
  subgraph BIN["host-telemetry (single static binary, ~20 MB)"]
    S1[linux source<br/>procfs / sysfs / hwmon / cgroup v2 / PSI]
    S2[darwin source<br/>sysctl / IOKit SMC / vm_stat]
    S3[windows source<br/>PDH / Perflib / process list]
    WK[workload source<br/>orca / docker / systemd — pluggable]
    N[normalizer<br/>→ semconv names + UCUM units]
    V[schema validator<br/>reject unknown names/units]
    W[(bounded WAL<br/>ring, 256 MB)]
    O[OTLP/HTTP + gzip<br/>batch 15s, backoff, Retry-After]
    S1 --> N
    S2 --> N
    S3 --> N
    WK --> N
    N --> V --> W --> O
  end
  O --> BE[(VictoriaMetrics / otel-collector / Grafana Cloud)]
  BE --> Q[AI: PromQL + REST over narrow tables]
  BIN --- SVC[systemd | launchd | Windows Service]
```

### 5.1 One interface per platform

```go
type Source interface {
    Name() string
    Interval() time.Duration
    Collect(ctx context.Context) ([]Point, error)
}
```

Platform differences are sealed behind this interface. Adding a platform means adding one
package; the core never learns which OS it runs on. No per-platform config files, no build tags
in the pipeline.

### 5.2 Normalize at the edge

Only schema-approved names reach storage. There is exactly one name per concept — no "raw name
plus a view", because two names is the same as no contract.

### 5.3 Transport

- **OTLP/HTTP protobuf is canonical.** gzip, 15s batches, exponential backoff with jitter,
  honor `Retry-After`. One protocol now and later for events (and traces, if agents add them).
- **`GET /metrics` (Prometheus text) is compatibility/debug output only**, not the contract.
- **The WAL is not hand-rolled.** Reuse `github.com/prometheus/prometheus/tsdb/wlog`: bounded,
  replayable, battle-tested. A custom ring buffer is the classic way these projects die.

### 5.4 Self-monitoring is mandatory

`collector.self.cpu.time`, `collector.self.memory.usage`, `collector.wal.size`,
`collector.send.queue.length`, `collector.scrape.duration`. The project's whole claim is
"it costs nothing"; it must prove that with its own metrics.

## 6. Sampling matrix

| Group | Interval | Rationale |
|---|---|---|
| cpu / memory / swap / paging / disk / filesystem / network | 15s | matches Prometheus convention; PSI avg10/60/300 windows are maintained by the kernel |
| temperature / fan / frequency / power | 30s | thermal inertia is 10–30s; faster sampling is noise and money |
| processes (whitelisted agent trees) | 15s | top-N by memory; unbounded process metrics are a cardinality bomb |
| workload state (count/status per session) | 30s | aligned with the source-of-truth poll cost (e.g. Orca process sweep) |
| events | on occurrence | never aggregated into metrics |

## 7. Boot and enrollment

| OS | Unit | Required properties |
|---|---|---|
| Linux | systemd | `Restart=always`, `RestartSec=5`, `MemoryMax=128M`, `CPUQuota=2%`, `ProtectSystem=strict`, `NoNewPrivileges=yes` |
| macOS | LaunchDaemon | `RunAtLoad`, `KeepAlive`, `ProcessType=Background`, `LowPriorityIO` |
| Windows | Service | automatic start, restart on failure (`x/sys/windows/svc`) |

Enrollment is one command and **idempotent** — running it again upgrades:

```sh
host-telemetry install --endpoint https://tsdb:8428 --token <token> [--dry-run]
```

It resolves `host.id`, writes `config.yaml` (mode `0600`) to the platform config dir, installs and
starts the service, prints `host.id`. `--dry-run` prints every planned action and changes nothing —
an open-source installer that cannot be inspected does not get installed.

Config precedence: `flags > HT_* env > config file > defaults`. The token lives in a `0600` file,
not in Keychain/DPAPI: a boot daemon reading an interactive keyring is a support nightmare, and
the tradeoff is documented rather than hidden.

## 8. Workload attribution (the differentiator)

Standard `process.*` metrics exist; "this is my 7th agent, on project X" does not. Keep it out of
the `system.*` namespace:

```
workload.count{workload.kind, workload.state}                        Gauge            {workload}
workload.cpu.time{workload.id, workload.project}                     Counter          s
workload.memory.usage{workload.id, workload.project}                 UpDownCounter    By
workload.pressure.utilization{workload.id}                           Gauge            1
```

Three-tier attribution, most accurate first:

1. **cgroup v2 scope** (Linux): launch each workload in its own scope
   (`systemd-run --user --scope -u agent-<id> ...`), read `memory.current`, `memory.peak`,
   `cpu.stat`, `*.pressure`. This is the only non-double-counted per-workload memory.
2. **Process-tree matching** (all platforms): executable whitelist + recursive children.
3. **Host-tool adapter** (optional plugin): e.g. `orca diagnostics memory --json` →
   `workload.*`. Such sources sum RSS and therefore over-count shared pages; the mapping must
   say so in its docs and must never be used for host totals.

## 9. Events

Metrics say "the curve dropped"; events tell an AI *why*. Same pipeline, OTLP Logs, `event.name`:

```
host.thermal.throttle    {cpu, from_hz, to_hz}
host.oom.kill            {pid, workload.id, rss}
host.disk.full           {mountpoint, used_ratio}
host.clock.skew          {jump_ms}
workload.agent.start     {workload.id, project, model}
workload.agent.stop      {workload.id, reason}
deploy.started/finished  {service, commit}
```

Event payloads are flat attributes. Never encode these as metrics — that is how cardinality
explodes.

## 10. Repository layout

```
cmd/host-telemetry/          main + subcommands: install, enroll, status, run, print
internal/schema/             generated constants, unit table, validator, semconv.lock   ← core asset
internal/source/{linux,darwin,windows}/
internal/workload/{orca,docker,systemd}/
internal/wal/                thin wrapper over prometheus tsdb/wlog
internal/otlp/
deploy/{systemd,launchd,windows}/
docs/metrics.md              human-readable catalog: why each metric exists, how to read it
docs/decisions/              ADRs (schema changes require one)
testdata/{linux,darwin,windows}/   real raw samples for golden tests
```

## 11. Milestones

| # | Scope | Acceptance |
|---|---|---|
| **M0** | vendored semconv model, codegen, validator, golden tests over three platforms' fixtures, `/metrics` output only | same fixture → identical series forever; validator rejects an unknown name or wrong unit |
| **M1** | Linux sources (PSI, mem, swap, cpu, disk, net, hwmon, cgroup, process matcher) + systemd install + enroll + WAL + OTLP | 24h with no gap; <1% of one core; <80 MB RSS |
| **M2** | macOS (SMC via sysctl/IOKit; may temporarily wrap `macmon` as a sub-source) + LaunchDaemon | reboot → first sample within 30s |
| **M3** | Windows (PDH counters + process table; optional LHM bridge for fans) + Service | service restart and power loss both lose no samples |
| **M4** | workload layer (cgroup + one host-tool adapter) + event stream | 15 concurrent agents: per-workload ranking matches the host tool's own report |
| **M5** | reference stack (compose: VictoriaMetrics + optional Grafana) + AI query recipes | one natural-language question → one PromQL query → narrow table |

## 12. Budgets and failure semantics

Non-negotiable, enforced in CI:

| Budget | Limit |
|---|---|
| CPU | ≤1% of one core at 15s default |
| RSS | ≤80 MB |
| Disk (WAL) | ≤256 MB, oldest-first eviction |
| Per-source collect | ≤5 ms; total collect ≤30 ms |

Failure semantics:

- **Clock:** store wall-clock ms; compute rates from a monotonic clock. A detected jump emits `host.clock.skew` and never produces a negative rate.
- **Counters:** report cumulative counters; the collector restarting must not rewrite history.
- **Backpressure:** when the sink is down, the WAL absorbs; when the WAL is full, drop the oldest and count it in `collector.wal.dropped`.
- **Partial failure:** one broken source must not stop the others; emit `collector.source.error{source}` instead.

## 13. Open decisions

Tracked as issues; the design above does not depend on them.

1. Go module path and license header convention.
2. Whether `workload.*` should eventually become an OTel entity/attribute set instead of a metric namespace.
3. Alternative output: OTLP is canonical — do we also need Prometheus `remote_write` for older sinks?
4. Windows fan/temperature path: LHM bridge (driver, admin) vs "absent by default".
5. Name of the project/binary if `host-telemetry` collides with an existing tool.

## 14. References

- OTel system metrics: <https://opentelemetry.io/docs/specs/semconv/system/system-metrics/>
- OTel process metrics: <https://opentelemetry.io/docs/specs/semconv/system/process-metrics/>
- OTel hardware metrics (`hw.fan.speed`, `hw.temperature`, `hw.status`): <https://opentelemetry.io/docs/specs/semconv/hardware/>
- PSI: <https://docs.kernel.org/accounting/psi.html>
- cgroup v2: <https://docs.kernel.org/admin-guide/cgroup-v2.html>
- node_exporter collector/platform matrix (why per-process needs its own layer): <https://github.com/prometheus/node_exporter>
- windows_exporter collectors: <https://github.com/prometheus-community/windows_exporter>
- Telegraf `procstat` (process-tree matching reference): <https://github.com/influxdata/telegraf/blob/master/plugins/inputs/procstat/README.md>
- Apple Silicon hardware metrics without sudo: <https://github.com/vladkens/macmon>
