synthetic, normalised to darwin sysctl+vm_stat

# darwin fixture

`synthetic`. 文件内容按 `sysctl` / `vm_stat` / 启动时刻的文本形态合成，非真实机器采集。

`real-synthetic-*` 为多快照 fixture，`darwin.New` 按目录名排序后取首个与末个快照做差分，`golden.txt` 由它们生成。
`sample-00` 为 M0 兼容快照（`legacy` 模式），`golden-sample.txt` 由它生成。

## 覆盖字段

- `sysctl.txt`：`hw.logicalcpu`、`hw.physicalcpu`、`hw.memsize`、`kern.clockrate`（`hz = N`）、`vm.swapusage`（used/free）。
- `vm_stat.txt`：页大小、`Pages free`、`Pages active`、`Pages inactive`、`Pages speculative`、`Pages wired down`、`Pages occupied by compressor`。
  `Pages free` 参与 available 计算，但不单独发 `state=free`：semconv v1.45.0 的 `system.memory.state` 只有 `used|available`，`free` 已 obsolete。
- `host_statistics.txt`：合成格式，首行 `# synthetic host_statistics(HOST_CPU_LOAD_INFO)`，随后 `user_ticks` / `system_ticks` / `idle_ticks`（单位 hz）。缺失时 `legacy` 模式不发 CPU 点。
- `boottime.txt` 与 `now.txt`：epoch 秒，差值即 `system.uptime`。

## 未覆盖字段

- 磁盘、网络、文件系统、进程、连接数、温度与电源。

## M2-M3 待补

- `host_processor_info`（per-cpu tick）以支持 live 的 CPU 时间与利用率。
- `system.disk.*`、`system.network.*`（`getifaddrs` + IOKit）。
- `system.filesystem.*`、`system.paging.faults`（`vm_stat` fault 行）。
