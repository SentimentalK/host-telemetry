real dump, captured 2026-11-19 on kernel 7.0.0-43-generic，非合成

# linux real fixture

只读采集自本机 `/`，未改写任何值。

## 覆盖字段

- `proc/stat`、`proc/uptime`、`proc/meminfo`、`proc/vmstat`、`proc/diskstats`、`proc/cpuinfo`。
- `proc/self/mountinfo`、`proc/self/limits`。
- `proc/pressure/{cpu,memory,io}`。
- `proc/net/dev`。
- `proc/<pid>/stat`（采集时每个数字目录一份）。
- `sys/class/hwmon/hwmon*/` 的 `name`、`temp*_input`、`fan*_input`、`power*_input`。
- `sys/devices/system/cpu/cpu*/cpufreq/scaling_cur_freq`。
- `statfs.tsv`：`mountpoint<TAB>Blocks<TAB>Bfree<TAB>Bavail<TAB>Bsize`，只含 mountinfo 中可枚举类型的挂载点。

## 未覆盖字段

- `proc/<pid>/statm`：默认关闭的 top-N 进程内存路径不在 golden 中。
- `proc/<pid>/cmdline`、`proc/<pid>/status`、`/proc/net/snmp`、cgroup 数据。
- hwmon 的 limit/alarm 文件、`temperature.limit`。
- 连接数（`system.network.connection.count`）。
