synthetic, normalised to darwin sysctl + vm_stat + ioreg + netstat + mount + df + ps + macmon

# darwin fixture

`synthetic`。文件内容按真实 macOS 命令的文本形态合成，非真实机器采集。

`real-synthetic-*` 为多快照 fixture，`darwin.New` 按目录名排序；CPU/内存/uptime 取首末快照做差分，其余源取末个快照（`real-synthetic-01`）。`golden.txt` 由它们生成。
`sample-00` 为 M0 兼容快照（`legacy` 模式），只有 sysctl / vm_stat / boottime / now；缺文件的源整源跳过，`golden-sample.txt` 由它生成。

## 覆盖字段

- `sysctl.txt`：`sysctl -a` 形态。`hw.logicalcpu`、`hw.physicalcpu`、`hw.memsize`、`kern.clockrate`（`hz = N`）、`vm.swapusage`（used/free）、`kern.maxproc`（`system.process.limit`，缺失则不发点）。
- `vm_stat.txt`：页大小、`Pages free`、`Pages active`、`Pages inactive`、`Pages speculative`、`Pages wired down`、`Pages occupied by compressor`。
  `Pages free` 参与 available 计算，但不单独发 `state=free`：semconv v1.45.0 的 `system.memory.state` 只有 `used|available`，`free` 已 obsolete。
- `host_statistics.txt`：合成格式，首行 `# synthetic host_statistics(HOST_CPU_LOAD_INFO)`，随后 `user_ticks` / `system_ticks` / `idle_ticks`（单位 hz）。
- `boottime.txt` 与 `now.txt`：epoch 秒，差值即 `system.uptime`。
- `ioreg.txt`：`ioreg -r -c IOBlockStorageDriver -l` 形态。每个 `+-o IOBlockStorageDriver` 节点的 `"Statistics"` 字典提供 `Bytes (Read/Write)` → `system.disk.io`、`Operations (Read/Write)` → `system.disk.operations`、`Total Time (Read/Write)`（ns）→ `system.disk.operation_time`（s）。属性 `system.device.name` 取同一子树下 `Whole = Yes` 的 `IOMedia` 的 `"BSD Name"`。不发 `system.disk.io_time`。
- `netstat.txt`：`netstat -ib` 形态，只消费含 `<Link#N>` 的接口行。`Ibytes/Obytes` → `system.network.io`，`Ipkts/Opkts` → `system.network.packet.count`（属性用 `system.device.name`，与 semconv 一致），`Ierrs/Oerrs` → `system.network.errors`，`Drop` → 仅 transmit 的 `system.network.packet.dropped`。`system.network.io` 带 `network.interface.name`（`docs/metrics.md` 契约要求；否则多网卡会产生重复序列）。
- `mount.txt`：`mount` 形态，`device on mountpoint (fstype, option, ...)`；选项含 `read-only` → `system.filesystem.mode=ro`，否则 `rw`。
- `df.txt`：`df -k` 形态（9 列：Filesystem / 1024-blocks / Used / Available / Capacity / iused / ifree / %iused / Mounted on），按 mountpoint 与 `mount.txt` 关联。`system.filesystem.usage` / `utilization` 的 state 为 `used`（Used×1024）、`free`（Available×1024）、`reserved`（total−used−free，0 也发）。属性：`system.device.name`、`system.filesystem.mode`、`system.filesystem.mountpoint`、`system.filesystem.state`。
- `system.filesystem.type` 只在映射命中时写入：`hfs`→`hfsplus`、`exfat`→`exfat`、`msdos`→`fat32`、`ntfs`→`ntfs`；`apfs` 等未知类型照常发点但省略该属性。
- `ps.txt`：每行一条 `ps -axo stat=` 的状态串。首字符 `R`→running、`S/D/I`→sleeping、`T`→stopped、`Z`→defunct；四个 state 都发（0 也发），未知状态跳过。
- `macmon.json`：`macmon pipe` 的单条 JSON。`temp.cpu_temp_avg` / `temp.gpu_temp_avg` → `hw.temperature`（`hw.id`/`hw.name`/`hw.sensor_location` 均为 cpu 或 gpu）；`fans[].name` / `rpm` / `max_rpm` → `hw.fan.speed`、`hw.fan.speed.limit`（`hw.limit_type=max`）、`hw.fan.speed_ratio`（rpm/max_rpm），`hw.sensor_location=chassis`；`cpu_power` / `gpu_power` / `sys_power` → `hw.power`（`hw.type` 分别为 cpu / gpu / power_supply，system 的 `hw.id`/`hw.name` 为 system）。JSON null 字段跳过；文件不存在时整源不发点、不报错。

`real-synthetic-00` 与 `real-synthetic-01` 的数值相互独立；golden 取 `-01`。`-00` 的 `gpu_power` 为 null，用于覆盖 null 跳过路径。

## 未覆盖字段

- 每 CPU（`cpu.logical_number`）的 CPU 时间与利用率。
- `system.network.connection.count`。
- `system.process.created`。
- `system.pressure.*`（PSI，darwin 不在本契约范围）。
- 真机 live 采集（本 fixture 只做解析与 golden；live 接线由后续任务完成）。
- APFS 等未知文件系统不带 `system.filesystem.type` 属性。
