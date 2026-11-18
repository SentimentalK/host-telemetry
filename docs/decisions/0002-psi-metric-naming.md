# 0002 — Report Pressure Stall Information as `system.pressure.*`

- **Status:** proposed (experimental until upstream decides)
- **Date:** 2026-11-18

## Context

PSI (`/proc/pressure/{cpu,memory,io}` and the per-cgroup equivalents) is the only portable Linux
signal for *stalled* time as opposed to *busy* time. It answers the question this project exists to
answer ("was the machine actually bottlenecked at that moment?") and cannot be derived from
utilization: a 12-core box at 100% utilization with zero stall is healthy, and a box at 55% with
12% memory stall is already hurting.

OTel semantic conventions have **no** PSI metric in the `system.*` namespace. `hw.*` does not fit:
pressure is a scheduler/MM observation, not a hardware sensor, and `hw.status` has no notion of a
fraction of time.

## Decision

Define the metrics in semconv style, mark them experimental, and propose them upstream in parallel:

```
system.pressure.stall.time      Counter   s    {system.pressure.resource=cpu|memory|io, system.pressure.type=some|full}
system.pressure.utilization     Gauge     1    {same}
```

Rules:

- `type="full"` means *all* non-idle tasks were stalled. `type="some"` means at least one was.
  `cpu` has no meaningful `full` (the kernel reports it as zero) — do not emit it.
- `stall.time` is cumulative and monotonic per resource; utilization is `avg10/avg60/avg300`
  expressed as 0–1 fractions, matching semconv's ratio convention.
- Emitted only where the kernel supports it (Linux 4.20+). On macOS and Windows the metric is
  absent, not zero. A zero would be a lie about "no stalls".
- Per-workload pressure uses the same resource/type attributes plus `workload.id`
  (`workload.pressure.utilization`).

## Consequences

- The dataset can distinguish "busy" from "stalled" on Linux, which no semconv-compliant dataset
  can today.
- If upstream renames or reshapes these before we stabilize our own schema, we bump
  `schema.version`, keep the old name readable via a recording rule for one minor release, and
  migrate.

## Upstream

Proposal to file: `open-telemetry/semantic-conventions` (system metrics) with the kernel doc
reference and the `full`/`some` semantics table.
