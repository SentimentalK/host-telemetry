#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
out="$root/testdata/linux/real-00"

rm -rf "$out/proc" "$out/sys" "$out/statfs.tsv"
mkdir -p "$out"

copy() {
	src=$1
	rel=$2
	[ -e "$src" ] || return 0
	mkdir -p "$out/$(dirname "$rel")"
	cp "$src" "$out/$rel" 2>/dev/null || rm -f "$out/$rel"
}

for file in \
	proc/stat \
	proc/uptime \
	proc/meminfo \
	proc/vmstat \
	proc/diskstats \
	proc/cpuinfo \
	proc/self/mountinfo \
	proc/self/limits \
	proc/pressure/cpu \
	proc/pressure/memory \
	proc/pressure/io \
	proc/net/dev
do
	copy "/$file" "$file"
done

for dir in /proc/[0-9]*; do
	[ -d "$dir" ] || continue
	pid=$(basename "$dir")
	copy "$dir/stat" "proc/$pid/stat"
done

for dir in /sys/class/hwmon/hwmon*; do
	[ -d "$dir" ] || continue
	name=$(basename "$dir")
	copy "$dir/name" "sys/class/hwmon/$name/name"
	for file in "$dir"/temp*_input "$dir"/fan*_input "$dir"/power*_input; do
		copy "$file" "sys/class/hwmon/$name/$(basename "$file")"
	done
done

for dir in /sys/devices/system/cpu/cpu*; do
	[ -d "$dir" ] || continue
	name=$(basename "$dir")
	copy "$dir/cpufreq/scaling_cur_freq" "sys/devices/system/cpu/$name/cpufreq/scaling_cur_freq"
done

tsv="$out/statfs.tsv"
: > "$tsv"
tab=$(printf '\t')
awk '{ mp=$5; sep=0; for (i=6; i<=NF; i++) if ($i=="-") { sep=i; break }; if (!sep) next; print mp "\t" $(sep+1) }' /proc/self/mountinfo |
while IFS="$tab" read -r mountpoint fstype; do
	case "$fstype" in
		ext4|vfat|exfat|ntfs|ntfs3|hfsplus|refs) ;;
		*) continue ;;
	esac
	mountpoint=$(printf '%b' "$mountpoint")
	set -- $(stat -f -c '%b %f %a %S' "$mountpoint")
	printf '%s\t%s\t%s\t%s\t%s\n' "$mountpoint" "$1" "$2" "$3" "$4" >> "$tsv"
done

cat > "$out/README.md" <<EOF
real dump, captured $(date -u +%Y-%m-%d) on kernel $(uname -r)，非合成

# linux real fixture

只读采集自本机 \`/\`，未改写任何值。

## 覆盖字段

- \`proc/stat\`、\`proc/uptime\`、\`proc/meminfo\`、\`proc/vmstat\`、\`proc/diskstats\`、\`proc/cpuinfo\`。
- \`proc/self/mountinfo\`、\`proc/self/limits\`。
- \`proc/pressure/{cpu,memory,io}\`。
- \`proc/net/dev\`。
- \`proc/<pid>/stat\`（采集时每个数字目录一份）。
- \`sys/class/hwmon/hwmon*/\` 的 \`name\`、\`temp*_input\`、\`fan*_input\`、\`power*_input\`。
- \`sys/devices/system/cpu/cpu*/cpufreq/scaling_cur_freq\`。
- \`statfs.tsv\`：\`mountpoint<TAB>Blocks<TAB>Bfree<TAB>Bavail<TAB>Bsize\`，只含 mountinfo 中可枚举类型的挂载点。

## 未覆盖字段

- \`proc/<pid>/statm\`：默认关闭的 top-N 进程内存路径不在 golden 中。
- \`proc/<pid>/cmdline\`、\`proc/<pid>/status\`、\`/proc/net/snmp\`、cgroup 数据。
- hwmon 的 limit/alarm 文件、\`temperature.limit\`。
- 连接数（\`system.network.connection.count\`）。
EOF
