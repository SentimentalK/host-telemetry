# windows fixture

`synthetic`。文件取自 Windows 原生 API 的输出形态：`GetSystemTimes`、`GlobalMemoryStatusEx`、`GetTickCount64`、PDH raw counter、`GetLogicalDriveStrings`/`GetVolumeInformation`/`GetDiskFreeSpaceEx` 的结构 dump，以及 `Get-CimInstance` 导出的 CSV。主机名统一为 `HOST`。

## 文件来源与覆盖字段

- `system-times.txt`：`kernel32!GetSystemTimes` 返回的 idle/kernel/user，单位 100 ns。覆盖 `system.cpu.time`（system 必须取 `kernel-idle`）与 `system.cpu.utilization`。
- `memory-status.txt`：`kernel32!GlobalMemoryStatusEx` 的 `ullTotalPhys`、`ullAvailPhys`、`ullTotalPageFile`、`ullAvailPageFile`，字节。覆盖 `system.memory.usage`、`system.memory.utilization`、`system.paging.usage`。
- `tick-count.txt`：`kernel32!GetTickCount64` 的开机毫秒。覆盖 `system.uptime`。
- `pdh-disk.txt`：`PdhGetRawCounterValue` 的 `\PhysicalDisk(*)\Disk Read Bytes/sec`、`Disk Write Bytes/sec`、`Disk Reads/sec`、`Disk Writes/sec`，格式为 `counter_path<TAB>counter_type<TAB>first_value<TAB>second_value`。覆盖 `system.disk.io`、`system.disk.operations`；`_Total` 实例被跳过。
- `pdh-network.txt`：同一结构，覆盖 `\Network Interface(*)\Bytes Received/sec`、`Bytes Sent/sec`、`Packets Received Errors`、`Packets Outbound Errors`、`Packets Received/sec`、`Packets Sent/sec`、`Packets Received Discarded`、`Packets Outbound Discarded`。覆盖 `system.network.io`、`system.network.errors`、`system.network.packet.count`、`system.network.packet.dropped`。
- `pdh-system.txt`：`\System\Processes` raw count，覆盖 `system.process.count`。
- `volumes.txt`：`GetLogicalDriveStrings` + `GetDriveType` + `GetVolumeInformation` + `GetDiskFreeSpaceEx` 的固定盘卷，覆盖 `system.filesystem.usage`、`system.filesystem.utilization`；`UDF` 卷不写 `system.filesystem.type`。
- `cim-processor.csv`：`Get-CimInstance Win32_Processor` 的 `NumberOfCores`、`NumberOfLogicalProcessors`，覆盖 `system.cpu.physical.count`、`system.cpu.logical.count`。
- `cim-operating-system.csv`：`Get-CimInstance Win32_OperatingSystem` 的 `TotalVisibleMemorySize`（KB，×1024 后写入 `system.memory.limit`）与 `MaxNumberOfProcesses`（`system.process.limit`）。

## 快照与时间

`real-synthetic-00`、`real-synthetic-01` 是同一台主机的两次采集。快照间没有墙钟字段：CPU 利用率仅由两次 `system-times.txt` 的累计计数差分得到，因此 fixture 隐含“两次采集隔一个采样周期”而不依赖绝对时间。

## 删除 typeperf/wmic fixture 的原因

旧 `sample-00/typeperf.csv` 与 `wmic-cpu.csv` 只有 PDH 百分比和 WMIC 的 CPU 计数，无法表达累计计数器、PDH counter type、分卷/网卡实例以及 `system.uptime`。原生 dump 直接对应 Windows API 的类型和字段，`GOOS=windows` 的 live 路径与 fixture 共用同一套解析与换算。

## 未覆盖字段

- `system.pressure.*`：Windows 没有对应的 PSI 语义。
- 硬件温度、风扇：无稳定的 Win32 接口。
- `system.disk.operation_time`：fixture 不合成 `% Disk Time` 的 raw 计数。
- PDH 32 位回绕：fixture 只验证单调增长的 64 位 raw 值。
