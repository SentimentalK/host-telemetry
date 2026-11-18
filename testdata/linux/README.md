synthetic, procfs dump

# linux fixture

`synthetic`. 目录结构复刻 Linux procfs 真实路径；数值为手工合成，非任何真实主机采集。

## 覆盖字段

- `proc/stat`：`cpu` 聚合行（user / nice / system / idle / iowait / irq / softirq / steal）与 `cpuN` 行计数。
- `proc/uptime`：第一列（秒）。
- `proc/meminfo`：`MemTotal`、`MemAvailable`、`SwapTotal`、`SwapFree`。
- `proc/vmstat`：`pgfault`、`pgmajfault`。
- `proc/diskstats`：`nvme0n1` 的读/写完成次数与扇区数（第 4/6/8/10 列）。
- `proc/net/dev`：`lo` 与 `eth0` 的 rx/tx 字节与错误数。

`sample-00` 与 `sample-01` 的计数器递增；间隔按 15 s 计算。

## 未覆盖字段

- per-cpu 单核指标（仅聚合 `cpu` 行进入指标）。
- 压力失速（PSI）、频率、温度、磁盘延迟/队列、文件系统用量、进程与连接数。
- `MemFree`、`Buffers`、`Cached`、`Dirty` 等未映射的 meminfo 字段仍存在于文件中，但解析器不消费。

## M2-M3 待补

- `system.pressure.*`（`/proc/pressure/*`）。
- `system.disk.io_time`、`system.disk.operation_time`。
- `system.filesystem.*`（`/proc/mounts` + `statfs`）。
- `system.process.*`、`system.network.connection.count`。
- per-cpu 归属（`cpu.logical_number`）。
