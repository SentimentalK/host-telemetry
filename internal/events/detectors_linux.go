//go:build linux

package events

import (
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
)

var oomPattern = regexp.MustCompile(`Out of memory: Killed process (\d+) \(([^)]+)\).*anon-rss:(\d+)kB`)

var virtualFilesystems = map[string]bool{
	"proc":        true,
	"sysfs":       true,
	"devtmpfs":    true,
	"devpts":      true,
	"cgroup":      true,
	"cgroup2":     true,
	"securityfs":  true,
	"pstore":      true,
	"autofs":      true,
	"debugfs":     true,
	"tracefs":     true,
	"configfs":    true,
	"fusectl":     true,
	"mqueue":      true,
	"hugetlbfs":   true,
	"bpf":         true,
	"binfmt_misc": true,
	"nsfs":        true,
	"rpc_pipefs":  true,
}

var readOnlyFilesystems = map[string]bool{
	"squashfs": true,
	"iso9660":  true,
	"erofs":    true,
}

func LinuxDetectors(opts DetectorOptions) []Detector {
	if opts.FS == nil {
		opts.FS = os.DirFS("/")
	}
	if opts.DiskFullRatio <= 0 {
		opts.DiskFullRatio = 0.95
	}
	if opts.JournalLookback <= 0 {
		opts.JournalLookback = 5 * time.Minute
	}
	if opts.Journal == nil {
		opts.Journal = journalKernel
	}
	if opts.Statfs == nil {
		opts.Statfs = statfsPath
	}
	return []Detector{
		&oomDetector{fsys: opts.FS, journal: opts.Journal, lookback: opts.JournalLookback},
		&diskDetector{fsys: opts.FS, statfs: opts.Statfs, ratio: opts.DiskFullRatio},
		&thermalDetector{fsys: opts.FS},
	}
}

func journalKernel(since time.Time) ([]byte, error) {
	return exec.Command("journalctl", "-k", "-o", "cat", "--since", "@"+strconv.FormatInt(since.Unix(), 10)).Output()
}

func statfsPath(path string) (Statfs, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return Statfs{}, err
	}
	return Statfs{Blocks: uint64(stats.Blocks), Bfree: uint64(stats.Bfree)}, nil
}

func hasMountOption(options, want string) bool {
	for _, option := range strings.Split(options, ",") {
		if option == want {
			return true
		}
	}
	return false
}

type oomDetector struct {
	fsys     fs.FS
	journal  func(time.Time) ([]byte, error)
	lookback time.Duration
	last     time.Time
}

func (d *oomDetector) Name() string {
	return "oom"
}

func (d *oomDetector) Poll(now time.Time) []Event {
	since := now.Add(-d.lookback)
	if !d.last.IsZero() {
		since = d.last
	}
	data, err := d.journal(since)
	if err != nil {
		return nil
	}
	d.last = now

	matches := oomPattern.FindAllSubmatch(data, -1)
	result := make([]Event, 0, len(matches))
	for _, match := range matches {
		pid := string(match[1])
		rssKB, err := strconv.ParseUint(string(match[3]), 10, 64)
		if err != nil {
			continue
		}
		result = append(result, Event{
			Time: now,
			Name: OOMKill,
			Attrs: []Attr{
				{Key: "pid", Value: pid},
				{Key: schema.AttrWorkloadID, Value: d.workloadID(pid)},
				{Key: "rss", Value: strconv.FormatUint(rssKB*1024, 10)},
			},
			Subject: pid,
		})
	}
	return result
}

func (d *oomDetector) workloadID(pid string) string {
	data, err := fs.ReadFile(d.fsys, "proc/"+pid+"/cgroup")
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(data))
	if index := strings.LastIndexByte(value, '\n'); index >= 0 {
		value = value[index+1:]
	}
	if index := strings.LastIndexByte(value, '/'); index >= 0 {
		value = value[index+1:]
	}
	return strings.TrimSuffix(value, ".scope")
}

type diskDetector struct {
	fsys   fs.FS
	statfs func(string) (Statfs, error)
	ratio  float64
}

func (d *diskDetector) Name() string {
	return "disk"
}

func (d *diskDetector) Poll(now time.Time) []Event {
	data, err := fs.ReadFile(d.fsys, "proc/mounts")
	if err != nil {
		return nil
	}
	result := make([]Event, 0)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		mountpoint, fstype := fields[1], fields[2]
		if virtualFilesystems[fstype] || readOnlyFilesystems[fstype] || hasMountOption(fields[3], "ro") {
			continue
		}
		stats, err := d.statfs(mountpoint)
		if err != nil || stats.Blocks == 0 || stats.Bfree > stats.Blocks {
			continue
		}
		ratio := float64(stats.Blocks-stats.Bfree) / float64(stats.Blocks)
		if ratio < d.ratio {
			continue
		}
		result = append(result, Event{
			Time: now,
			Name: DiskFull,
			Attrs: []Attr{
				{Key: "mountpoint", Value: mountpoint},
				{Key: "used_ratio", Value: strconv.FormatFloat(ratio, 'f', 6, 64)},
			},
			Subject: mountpoint,
		})
	}
	return result
}

type thermalDetector struct {
	fsys fs.FS
}

func (d *thermalDetector) Name() string {
	return "thermal"
}

func (d *thermalDetector) Poll(now time.Time) []Event {
	entries, err := fs.ReadDir(d.fsys, "sys/class/thermal")
	if err != nil {
		return nil
	}
	maxFreq, ok := readIntFile(d.fsys, "sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq")
	if !ok {
		return nil
	}
	curFreq, ok := readIntFile(d.fsys, "sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq")
	if !ok {
		return nil
	}
	result := make([]Event, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "thermal_zone") {
			continue
		}
		base := "sys/class/thermal/" + name
		zoneType, err := fs.ReadFile(d.fsys, base+"/type")
		if err != nil {
			continue
		}
		temp, ok := readIntFile(d.fsys, base+"/temp")
		if !ok {
			continue
		}
		trip, ok := readIntFile(d.fsys, base+"/trip_point_0_temp")
		if !ok {
			continue
		}
		if trip <= 0 || trip > 200000 {
			continue
		}
		if temp < trip || curFreq >= maxFreq {
			continue
		}
		result = append(result, Event{
			Time: now,
			Name: ThermalThrottle,
			Attrs: []Attr{
				{Key: "cpu", Value: strings.TrimSpace(string(zoneType))},
				{Key: "from_hz", Value: strconv.FormatInt(maxFreq*1000, 10)},
				{Key: "to_hz", Value: strconv.FormatInt(curFreq*1000, 10)},
			},
			Subject: strings.TrimSpace(string(zoneType)),
		})
	}
	return result
}

func readIntFile(fsys fs.FS, path string) (int64, bool) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
