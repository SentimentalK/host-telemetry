synthetic, normalised to windows PDH typeperf+wmic CSV

# windows fixture

`synthetic`. 文件内容按 `typeperf` 与 `wmic ... /format:csv` 的原样输出形态合成，主机名统一为 `HOST`。

## 覆盖字段

- `typeperf.csv`：`\Processor(_Total)\% User Time`、`% Privileged Time`、`% Idle Time`、`\Memory\Available Bytes`、`\Memory\Committed Bytes`；取最后一行（两行样本，间隔 15 s）。
- `wmic-cpu.csv`：`NumberOfCores`、`NumberOfLogicalProcessors`。

## 未覆盖字段

- `system.uptime`（PDH 无开机时刻计数器）。
- `system.disk.*` 与 `system.network.*`：PDH per-sec 计数器不是累计计数器，M3 再接入。
- `system.cpu.time`、`system.paging.*`、文件系统、进程与连接数。

## M2-M3 待补

- `\PhysicalDisk(*)\Disk Read Bytes/sec` 等累计/速率计数器的换算与 `system.disk.io`。
- `\Network Interface(*)\Bytes Total/sec` 与 `system.network.io`。
- `LastBootUpTime`（WMI）以支持 `system.uptime`。
- `system.paging.usage` / `system.paging.faults`（`Win32_PageFileUsage`、`Memory\Pages/sec`）。
