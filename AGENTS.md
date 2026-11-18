# AGENTS.md

Instructions for any agent (human or model) working in this repository. Read this file, then
`README.md`, then `docs/metrics.md` before writing code.

## What this project is

A cross-platform host telemetry collector: one static binary per machine, installed as a boot
service by one command, emitting one schema (OpenTelemetry semantic conventions) over OTLP, cheap
enough to run on every laptop and server you own, and queryable by an AI without a UI.

The contract in `README.md` is the deliverable. Code exists to satisfy it.

## Non-negotiables

1. **Never hand-write a metric name.** Names, units and attribute keys come from
   `internal/schema` (generated from the pinned semconv model). If a needed name is missing, add an
   ADR in `docs/decisions/` first, then regenerate.
2. **Units are UCUM** (`s`, `By`, `1`, `Hz`, `Cel`, `rpm`, `{cpu}`, …). Utilization is a 0–1
   fraction, never a percent.
3. **Normalize at the edge.** Nothing outside the contract may reach storage. No "raw name plus a
   view".
4. **Budgets are tests, not intentions:** ≤1% of one core, ≤80 MB RSS, ≤256 MB WAL, ≤30 ms total
   collect. A change that breaks a budget is a failing build.
5. **Platform code is sealed behind `Source`.** No OS conditionals in the pipeline, no per-OS config
   files.
6. **Reuse boring infrastructure.** `prometheus/tsdb/wlog` for the buffer, stdlib for everything
   else. Do not write a ring buffer, a retry framework or an HTTP client wrapper.
7. **No UI, no alerting, no TSDB, no LLM tracing.** Those are explicitly out of scope; adding them
   is a rejected PR.

## Workflow

```sh
make generate   # regenerate schema constants from the vendored semconv model
make test       # unit + golden tests over testdata/{linux,darwin,windows}
make budget     # overhead benchmark (CPU/RSS/collect duration)
make lint
make run        # foreground collector, /metrics on :9101
```

- Golden tests are the safety net: raw platform fixtures in `testdata/<os>/` always produce the same
  series set. Adding a source means adding a fixture, not only code.
- Every new source needs: `Name()`, `Interval()`, at least one fixture, one golden row per metric,
  and a partial-failure case.
- Commit style: conventional commits (`feat:`, `fix:`, `docs:`, `chore:`). One logical change per
  commit. Do not bundle a schema change with a refactor.

## Definition of done (per milestone)

- `make test` and `make budget` pass locally and in CI.
- The behaviour is exercised end-to-end on the real platform that the milestone targets: agent
  started by the installed service (not by `go run`), data visible in a sink, `system.uptime`
  proves the boot path.
- Failure paths demonstrated once: sink down (WAL absorbs and replays), clock jump (no negative
  rates), `kill -9` (no history rewrite).
- `README.md` and `docs/metrics.md` updated in the same commit when behaviour changes.

## Where things go

| Change | Location |
|---|---|
| new platform | `internal/source/<os>/` + `testdata/<os>/` + `deploy/<os>` |
| new metric | ADR (if not in semconv) → `internal/schema` → `docs/metrics.md` → source |
| new workload attribution source | `internal/workload/<tool>/`, optional, off unless configured |
| transport / batching | `internal/otlp/` |
| buffer | `internal/wal/` (thin wrapper only) |
