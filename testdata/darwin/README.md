# darwin fixture (`synthetic`)

**synthetic**。本目录的文本 fixture 按真实 macOS 命令的输出形态人工合成，不是真机采集；它们只证明
解析器行为与 golden 序列稳定，不代表真机上的字段完整性。

## 文件与来源

| 文件 | 对应真实命令 | 覆盖字段 | 备注 |
|---|---|---|---|
| `sysctl.txt` | `sysctl -a`（子集） | `hw.logicalcpu`、`hw.physicalcpu`、`hw.ncpu`、`hw.memsize`、`kern.clockrate`（`hz`）、`vm.swapusage`（used/free）、`kern.maxproc` | `kern.maxproc` → `system.process.limit`；缺失则不发点 |
| `host_statistics.txt` | 无对应文本命令；`host_statistics(HOST_CPU_LOAD_INFO)` 结构 | `user_ticks`、`system_ticks`、`idle_ticks`（单位 hz） | 首行为 `# synthetic host_statistics(HOST_CPU_LOAD_INFO)`，解析器按 `key: value` 行读取 |
| `vm_stat.txt` | `vm_stat` | page size、`Pages free`、`Pages active`、`Pages inactive`、`Pages speculative`、`Pages wired down`、`Pages occupied by compressor` | `Pages free` 只参与 available 计算，不单独发 `state=free`（semconv v1.45.0 的 `system.memory.state` 已无 `free`） |
| `boottime.txt` / `now.txt` | `sysctl -n kern.boottime` / `date +%s` | epoch 秒 | 差值即 `system.uptime`；`now < boot` 时报错 |
| `ioreg.txt` | `ioreg -c IOBlockStorageDriver -r -l -w 0` | `Statistics` 的 `Bytes (Read/Write)`、`Operations (Read/Write)`、`Total Time (Read/Write)`（ns）；`IOMedia` 的 `BSD Name` | `system.device.name` 取 `Whole = Yes` 的 `IOMedia`；不发 `system.disk.io_time` |
| `netstat.txt` | `netstat -ibdnW` | `Ibytes/Obytes`、`Ipkts/Opkts`、`Ierrs/Oerrs`、`Drop` | 只消费含 `<Link#N>` 的接口行；`Drop` 只发 transmit 方向 |
| `mount.txt` / `df.txt` | `mount` + `df -kP` | 设备、mountpoint、fstype、`read-only` 选项；1024-blocks / Used / Available | 按 mountpoint 关联；`reserved = total - used - free`；解析器接受 5 列起的 POSIX 形态，fixture 额外保留 macOS `df -k` 的 `iused/ifree/%iused` 列 |
| `ps.txt` | `ps -axo stat=` | 状态串首字符 `R`、`S/D/I/U`、`T`、`Z` | 四种 state（running/sleeping/stopped/defunct）都发，0 也发；未知状态跳过 |
| `macmon.json` | `macmon pipe -s 1 -i 1000` | `temp.cpu_temp_avg`/`gpu_temp_avg`、`fans[].name/rpm/max_rpm`、`cpu_power`/`gpu_power`/`sys_power` | 可选子 source；字段名归一到 `hw.*`；JSON null 跳过；文件不存在时整源不发点、不报错 |

`ioreg-platform.txt`（`hostinfo` 测试用）与 `install-dry-run.golden`（launchd 安装计划测试用）是测试
harness fixture，不参与遥测采集。

## 快照与 golden

- `real-synthetic-00`、`real-synthetic-01`：两快照 fixture，目录名排序后取首末快照。`system.cpu.time`
  取末快照计数，`system.cpu.utilization` 用首末 tick 差分；内存、uptime 与其余源取末快照（`-01`）。
  `golden.txt` 由这两个目录经真实解析路径生成；JSON null 跳过由 `TestSensorsNullSkipped` 覆盖。
- `sample-00`：M0 兼容快照（`legacy` 模式），只有 `sysctl.txt` / `vm_stat.txt` / `boottime.txt` / `now.txt`；
  缺失文件的源整源跳过。`golden-sample.txt` 由它生成，覆盖 inventory / memory / paging / uptime 子集。
- 区别：`golden.txt` 是 real-synthetic 全源 golden；`golden-sample.txt` 是 sample-00 的 M0 回退 golden，
  两者都不能单独证明真机行为。

## 未覆盖

- APFS 等 pinned enum 外的文件系统不带 `system.filesystem.type` 属性（fixture 的 `/` 即 APFS）。
- 每 CPU（`cpu.logical_number`）的 CPU 时间与利用率。
- `system.network.connection.count`。
- `system.process.created`。
- `system.pressure.*`（PSI，darwin 不在本契约范围）。
- 真机 live 采集（本目录只覆盖解析与 golden；live 接线未经真机验证）。
- `macmon` 默认关闭：`--sensors macmon` 才注册 `hw.*` 子 source。
- 真机 CPU/RSS 采集预算：`make budget` 只跑 linux fixture。
