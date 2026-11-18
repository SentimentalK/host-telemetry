# 0001 — OpenTelemetry semantic conventions are the data contract

- **Status:** accepted
- **Date:** 2026-11-18
- **Supersedes:** —

## Context

The requirement is that Linux, macOS, Windows and future machines produce a dataset with identical
dimensions and units, usable directly by an AI. Every existing collector invents its own names
(`node_cpu_seconds_total`, `cpu_usage_user`, `win_cpu_time`), so cross-platform datasets need a
translation layer that nobody maintains.

## Decision

1. **Names, units and attribute keys come from OpenTelemetry semantic conventions**, vendored as the
   upstream YAML model and pinned by version in `internal/schema/semconv.lock`.
2. **Code generation owns the names.** `go generate ./internal/schema` emits constants, the unit
   table and the validator. Hand-written metric strings are a review reject.
3. **Normalization happens at the edge**, inside the collector, so the store only ever sees contract
   names. There is no second "raw" name and no view layer.
4. **`schema.version` is attached to every reported series.** Renames are breaking changes.
5. **Reusing existing collectors as engines is allowed.** Wrapping Telegraf, `node_exporter`,
   `windows_exporter` or `macmon` as a *source* is fine; letting their names escape into storage is
   not.

## Consequences

- Positive: units cannot drift (`By`, `s`, `1`, `Cel`, `rpm`), attributes describe cardinality, and
  the dataset is portable to any OTLP/Prometheus-compatible backend without translation.
- Negative: semconv `system.*`/`hw.*` are still `Development` status and the spec explicitly permits
  breaking changes before stabilization. Mitigation: pinned version, `schema.version` on every
  series, and a migration note in `CHANGELOG.md` per bump.
- Negative: some platform-specific signals have no standard name yet (PSI, thermal throttle events).
  Those require an ADR — see `0002`.

## Alternatives considered

| Alternative | Rejected because |
|---|---|
| Invent a compact `ht_*` schema | Loses every existing backend's semantics and tooling; we would own translation forever |
| Prometheus naming as the contract | Units are conventions, not machine-checked; Unicode/case rules differ per ecosystem |
| Accept each engine's names and translate at ingest | Two sources of truth; the translation becomes the de-facto contract and drifts |
