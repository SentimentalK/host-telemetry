package darwin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

const (
	cpuInterval        = 15 * time.Second
	memoryInterval     = 15 * time.Second
	uptimeInterval     = 4 * time.Second
	inventoryInterval  = 300 * time.Second
	diskInterval       = 15 * time.Second
	networkInterval    = 15 * time.Second
	filesystemInterval = 15 * time.Second
	processInterval    = 15 * time.Second
	sensorsInterval    = 30 * time.Second
)

const hostStatisticsHeader = "# synthetic host_statistics(HOST_CPU_LOAD_INFO)"

var (
	pageSizePattern = regexp.MustCompile(`page size of ([0-9]+) bytes`)
	clockHzPattern  = regexp.MustCompile(`hz = ([0-9]+)`)
)

type snapshot struct {
	logicalCPU  float64
	physicalCPU float64
	memoryLimit float64
	clockHz     float64
	swapUsed    float64
	swapFree    float64
	pageSize    float64
	free        float64
	active      float64
	inactive    float64
	speculative float64
	wired       float64
	compressor  float64
	userTicks   float64
	systemTicks float64
	idleTicks   float64
	boot        int64
	now         int64

	disk        []diskStat
	network     []networkStat
	filesystems []filesystemStat
	process     processStat
	sensors     sensorsStat
}

type snapshotErrors struct {
	sysctl     error
	vmstat     error
	cpu        error
	clock      error
	disk       error
	network    error
	filesystem error
	process    error
	sensors    error
}

type snapshotSet struct {
	samples fs.FS
	once    sync.Once
	snaps   []snapshot
	legacy  bool
	err     error
	errs    snapshotErrors
}

func newSnapshotSet(samples fs.FS) *snapshotSet {
	return &snapshotSet{samples: samples}
}

func (s *snapshotSet) load() ([]snapshot, bool, *snapshotErrors, error) {
	s.once.Do(s.parse)
	return s.snaps, s.legacy, &s.errs, s.err
}

func (s *snapshotSet) parse() {
	entries, err := fs.ReadDir(s.samples, ".")
	if err != nil {
		s.err = fmt.Errorf("read fixture root: %w", err)
		return
	}
	var synthetic, legacy []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		switch {
		case strings.HasPrefix(entry.Name(), "real-synthetic-"):
			synthetic = append(synthetic, entry.Name())
		case strings.HasPrefix(entry.Name(), "sample-"):
			legacy = append(legacy, entry.Name())
		}
	}
	dirs := synthetic
	if len(dirs) == 0 {
		dirs = legacy
		s.legacy = true
	}
	if len(dirs) == 0 {
		s.err = fmt.Errorf("read fixture root: no real-synthetic-* or sample-* directories")
		return
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		snap, errs := parseSnapshot(s.samples, dir)
		s.snaps = append(s.snaps, snap)
		firstError(&s.errs.sysctl, errs.sysctl)
		firstError(&s.errs.vmstat, errs.vmstat)
		firstError(&s.errs.cpu, errs.cpu)
		firstError(&s.errs.clock, errs.clock)
		firstError(&s.errs.disk, errs.disk)
		firstError(&s.errs.network, errs.network)
		firstError(&s.errs.filesystem, errs.filesystem)
		firstError(&s.errs.process, errs.process)
		firstError(&s.errs.sensors, errs.sensors)
	}
}

func firstError(dst *error, err error) {
	if *dst == nil && err != nil {
		*dst = err
	}
}

func parseSnapshot(samples fs.FS, dir string) (snapshot, snapshotErrors) {
	var snap snapshot
	var errs snapshotErrors
	sysctlFile := dir + "/sysctl.txt"

	var sys map[string]string
	if data, err := readFile(samples, dir, "sysctl.txt"); err != nil {
		errs.sysctl = err
	} else if parsed, err := parseSysctl(data, sysctlFile); err != nil {
		errs.sysctl = err
	} else {
		sys = parsed
		errs.sysctl = parseSysctlSnapshot(&snap, sys, sysctlFile)
	}

	if data, err := readFile(samples, dir, "host_statistics.txt"); err != nil {
		errs.cpu = err
	} else if err := parseHostStatistics(data, dir+"/host_statistics.txt", &snap); err != nil {
		errs.cpu = err
	} else if sys == nil {
		if errs.sysctl != nil {
			errs.cpu = errs.sysctl
		} else {
			errs.cpu = fmt.Errorf("%s: missing kern.clockrate", sysctlFile)
		}
	} else if err := parseClockrate(&snap, sys, sysctlFile); err != nil {
		errs.cpu = err
	}

	if data, err := readFile(samples, dir, "vm_stat.txt"); err != nil {
		errs.vmstat = err
	} else {
		errs.vmstat = parseVMStatSnapshot(&snap, data, dir+"/vm_stat.txt")
	}

	if data, err := readFile(samples, dir, "ioreg.txt"); err != nil {
		errs.disk = err
	} else {
		snap.disk, errs.disk = parseIOreg(data, dir+"/ioreg.txt")
	}

	if data, err := readFile(samples, dir, "netstat.txt"); err != nil {
		errs.network = err
	} else {
		snap.network, errs.network = parseNetstat(data, dir+"/netstat.txt")
	}

	mountData, mountErr := readFile(samples, dir, "mount.txt")
	dfData, dfErr := readFile(samples, dir, "df.txt")
	switch {
	case mountErr != nil:
		errs.filesystem = mountErr
	case dfErr != nil:
		errs.filesystem = dfErr
	default:
		snap.filesystems, errs.filesystem = parseFilesystems(mountData, dfData, dir)
	}

	if data, err := readFile(samples, dir, "ps.txt"); err != nil {
		errs.process = err
	} else {
		snap.process, errs.process = parseProcesses(data, dir+"/ps.txt")
	}
	if errs.process == nil && sys != nil {
		if value, ok := sys["kern.maxproc"]; ok {
			limit, err := strconv.ParseFloat(value, 64)
			if err != nil || limit <= 0 {
				errs.process = fmt.Errorf("%s: kern.maxproc %q: invalid limit", sysctlFile, value)
			} else {
				snap.process.limit = limit
				snap.process.hasLimit = true
			}
		}
	}

	if data, err := readFile(samples, dir, "macmon.json"); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			errs.sensors = err
		}
	} else {
		snap.sensors, errs.sensors = parseSensors(data, dir+"/macmon.json")
	}

	if boot, err := readEpoch(samples, dir, "boottime.txt"); err != nil {
		errs.clock = err
	} else if now, err := readEpoch(samples, dir, "now.txt"); err != nil {
		errs.clock = err
	} else if now < boot {
		errs.clock = fmt.Errorf("%s/now.txt: now %v is before boot %v", dir, now, boot)
	} else {
		snap.boot = boot
		snap.now = now
	}

	return snap, errs
}

func parseSysctlSnapshot(snap *snapshot, sys map[string]string, file string) error {
	var err error
	if snap.logicalCPU, err = parseSysctlInt(sys, "hw.logicalcpu", file); err != nil {
		return err
	}
	if snap.physicalCPU, err = parseSysctlInt(sys, "hw.physicalcpu", file); err != nil {
		return err
	}
	if snap.memoryLimit, err = parseSysctlInt(sys, "hw.memsize", file); err != nil {
		return err
	}
	if snap.memoryLimit <= 0 {
		return fmt.Errorf("%s: hw.memsize must be positive", file)
	}
	swap, ok := sys["vm.swapusage"]
	if !ok {
		return fmt.Errorf("%s: missing vm.swapusage", file)
	}
	snap.swapUsed, snap.swapFree, err = parseSwapusage(swap, file)
	return err
}

func parseClockrate(snap *snapshot, sys map[string]string, file string) error {
	value, ok := sys["kern.clockrate"]
	if !ok {
		return fmt.Errorf("%s: missing kern.clockrate", file)
	}
	match := clockHzPattern.FindStringSubmatch(value)
	if match == nil {
		return fmt.Errorf("%s: kern.clockrate %q: missing hz", file, value)
	}
	hz, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return fmt.Errorf("%s: kern.clockrate %q: %w", file, value, err)
	}
	if hz <= 0 {
		return fmt.Errorf("%s: kern.clockrate hz must be positive", file)
	}
	snap.clockHz = hz
	return nil
}

func parseHostStatistics(data []byte, file string, snap *snapshot) error {
	values := make(map[string]float64)
	header := false
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !header {
			if trimmed != hostStatisticsHeader {
				return fmt.Errorf("%s: line %d: missing header %q", file, i+1, hostStatisticsHeader)
			}
			header = true
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return fmt.Errorf("%s: line %d: missing ':': %q", file, i+1, line)
		}
		number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return fmt.Errorf("%s: line %d: value %q: %w", file, i+1, value, err)
		}
		values[strings.TrimSpace(key)] = number
	}
	if !header {
		return fmt.Errorf("%s: missing header %q", file, hostStatisticsHeader)
	}
	fields := []struct {
		key string
		dst *float64
	}{
		{"user_ticks", &snap.userTicks},
		{"system_ticks", &snap.systemTicks},
		{"idle_ticks", &snap.idleTicks},
	}
	for _, field := range fields {
		value, ok := values[field.key]
		if !ok {
			return fmt.Errorf("%s: missing %q", file, field.key)
		}
		*field.dst = value
	}
	return nil
}

func parseVMStatSnapshot(snap *snapshot, data []byte, file string) error {
	pageSize, pages, err := parseVMStat(data, file)
	if err != nil {
		return err
	}
	snap.pageSize = pageSize
	fields := []struct {
		key string
		dst *float64
	}{
		{"Pages free", &snap.free},
		{"Pages active", &snap.active},
		{"Pages inactive", &snap.inactive},
		{"Pages speculative", &snap.speculative},
		{"Pages wired down", &snap.wired},
		{"Pages occupied by compressor", &snap.compressor},
	}
	for _, field := range fields {
		value, ok := pages[field.key]
		if !ok {
			return fmt.Errorf("%s: missing %q", file, field.key)
		}
		*field.dst = value
	}
	return nil
}

func readFile(samples fs.FS, dir, name string) ([]byte, error) {
	data, err := fs.ReadFile(samples, dir+"/"+name)
	if err != nil {
		return nil, fmt.Errorf("%s/%s: %w", dir, name, err)
	}
	return data, nil
}

func readEpoch(samples fs.FS, dir, name string) (int64, error) {
	data, err := readFile(samples, dir, name)
	if err != nil {
		return 0, err
	}
	line := strings.TrimSpace(string(data))
	value, err := strconv.ParseInt(line, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s/%s: epoch %q: %w", dir, name, line, err)
	}
	return value, nil
}

func parseSysctl(data []byte, file string) (map[string]string, error) {
	out := make(map[string]string)
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("%s: line %d: missing ':': %q", file, i+1, line)
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out, nil
}

func parseSysctlInt(sys map[string]string, key, file string) (float64, error) {
	value, ok := sys[key]
	if !ok {
		return 0, fmt.Errorf("%s: missing %s", file, key)
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %s %q: %w", file, key, value, err)
	}
	return number, nil
}

func parseSwapusage(value, file string) (float64, float64, error) {
	fields := strings.Fields(value)
	var used, free float64
	var haveUsed, haveFree bool
	for i := 0; i+2 < len(fields); i++ {
		if fields[i+1] != "=" {
			continue
		}
		size, err := parseSize(fields[i+2])
		if err != nil {
			return 0, 0, fmt.Errorf("%s: vm.swapusage %q: %w", file, fields[i+2], err)
		}
		switch fields[i] {
		case "used":
			used, haveUsed = size, true
		case "free":
			free, haveFree = size, true
		}
	}
	if !haveUsed || !haveFree {
		return 0, 0, fmt.Errorf("%s: vm.swapusage %q: missing used or free", file, value)
	}
	return used, free, nil
}

func parseSize(value string) (float64, error) {
	multiplier := 1.0
	if value == "" {
		return 0, fmt.Errorf("empty size")
	}
	switch value[len(value)-1] {
	case 'K', 'k':
		multiplier = 1 << 10
		value = value[:len(value)-1]
	case 'M', 'm':
		multiplier = 1 << 20
		value = value[:len(value)-1]
	case 'G', 'g':
		multiplier = 1 << 30
		value = value[:len(value)-1]
	case 'B', 'b':
		value = value[:len(value)-1]
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, err
	}
	return number * multiplier, nil
}

func parseVMStat(data []byte, file string) (float64, map[string]float64, error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return 0, nil, fmt.Errorf("%s: empty file", file)
	}
	matches := pageSizePattern.FindStringSubmatch(lines[0])
	if matches == nil {
		return 0, nil, fmt.Errorf("%s: line 1: missing page size: %q", file, lines[0])
	}
	pageSize, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, nil, fmt.Errorf("%s: line 1: page size %q: %w", file, matches[1], err)
	}
	pages := make(map[string]float64)
	for i, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return 0, nil, fmt.Errorf("%s: line %d: missing ':': %q", file, i+2, line)
		}
		value = strings.TrimSuffix(strings.TrimSpace(value), ".")
		number, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, nil, fmt.Errorf("%s: line %d: value %q: %w", file, i+2, value, err)
		}
		pages[strings.TrimSpace(key)] = number
	}
	return pageSize, pages, nil
}

type cpuSource struct{ set *snapshotSet }

func (s *cpuSource) Name() string { return "darwin-cpu" }

func (s *cpuSource) Interval() time.Duration { return cpuInterval }

func (s *cpuSource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, legacy, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.cpu != nil {
		if legacy && errors.Is(errs.cpu, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, errs.cpu
	}
	first, last := snaps[0], snaps[len(snaps)-1]
	points := []schema.Point{
		schema.NewPoint(schema.SystemCPUTime, last.userTicks/last.clockHz, cpuMode(schema.CPUModeUser)),
		schema.NewPoint(schema.SystemCPUTime, last.systemTicks/last.clockHz, cpuMode(schema.CPUModeSystem)),
		schema.NewPoint(schema.SystemCPUTime, last.idleTicks/last.clockHz, cpuMode(schema.CPUModeIdle)),
	}
	if errs.clock == nil && len(snaps) > 1 {
		if elapsed := last.now - first.now; elapsed > 0 && last.logicalCPU > 0 {
			scale := 1 / (last.clockHz * float64(elapsed) * last.logicalCPU)
			points = append(points,
				schema.NewPoint(schema.SystemCPUUtilization, (last.userTicks-first.userTicks)*scale, cpuMode(schema.CPUModeUser)),
				schema.NewPoint(schema.SystemCPUUtilization, (last.systemTicks-first.systemTicks)*scale, cpuMode(schema.CPUModeSystem)),
				schema.NewPoint(schema.SystemCPUUtilization, (last.idleTicks-first.idleTicks)*scale, cpuMode(schema.CPUModeIdle)),
			)
		}
	}
	return points, nil
}

func cpuMode(value string) schema.Attr {
	return schema.Attr{Key: schema.AttrCPUMode, Value: value}
}

type memorySource struct{ set *snapshotSet }

func (s *memorySource) Name() string { return "darwin-memory" }

func (s *memorySource) Interval() time.Duration { return memoryInterval }

func (s *memorySource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, _, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.sysctl != nil {
		return nil, errs.sysctl
	}
	if errs.vmstat != nil {
		return nil, errs.vmstat
	}
	last := snaps[len(snaps)-1]
	used := (last.active + last.wired + last.compressor) * last.pageSize
	available := (last.free + last.inactive + last.speculative) * last.pageSize
	points := []schema.Point{
		schema.NewPoint(schema.SystemMemoryUsage, used, memoryState(schema.SystemMemoryStateUsed)),
		schema.NewPoint(schema.SystemMemoryUsage, available, memoryState(schema.SystemMemoryStateAvailable)),
		schema.NewPoint(schema.SystemMemoryUtilization, used/last.memoryLimit, memoryState(schema.SystemMemoryStateUsed)),
		schema.NewPoint(schema.SystemMemoryUtilization, available/last.memoryLimit, memoryState(schema.SystemMemoryStateAvailable)),
		schema.NewPoint(schema.SystemPagingUsage, last.swapUsed, pagingState(schema.SystemPagingStateUsed)),
		schema.NewPoint(schema.SystemPagingUsage, last.swapFree, pagingState(schema.SystemPagingStateFree)),
	}
	if total := last.swapUsed + last.swapFree; total > 0 {
		points = append(points,
			schema.NewPoint(schema.SystemPagingUtilization, last.swapUsed/total, pagingState(schema.SystemPagingStateUsed)),
			schema.NewPoint(schema.SystemPagingUtilization, last.swapFree/total, pagingState(schema.SystemPagingStateFree)),
		)
	}
	return points, nil
}

func memoryState(value string) schema.Attr {
	return schema.Attr{Key: schema.AttrSystemMemoryState, Value: value}
}

func pagingState(value string) schema.Attr {
	return schema.Attr{Key: schema.AttrSystemPagingState, Value: value}
}

type uptimeSource struct{ set *snapshotSet }

func (s *uptimeSource) Name() string { return "darwin-uptime" }

func (s *uptimeSource) Interval() time.Duration { return uptimeInterval }

func (s *uptimeSource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, _, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.clock != nil {
		return nil, errs.clock
	}
	last := snaps[len(snaps)-1]
	return []schema.Point{schema.NewPoint(schema.SystemUptime, float64(last.now-last.boot))}, nil
}

type inventorySource struct{ set *snapshotSet }

func (s *inventorySource) Name() string { return "darwin-inventory" }

func (s *inventorySource) Interval() time.Duration { return inventoryInterval }

func (s *inventorySource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, _, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.sysctl != nil {
		return nil, errs.sysctl
	}
	last := snaps[len(snaps)-1]
	return []schema.Point{
		schema.NewPoint(schema.SystemCPULogicalCount, last.logicalCPU),
		schema.NewPoint(schema.SystemCPUPhysicalCount, last.physicalCPU),
		schema.NewPoint(schema.SystemMemoryLimit, last.memoryLimit),
	}, nil
}

func New(samples fs.FS) []source.Source {
	set := newSnapshotSet(samples)
	return []source.Source{
		&cpuSource{set: set},
		&memorySource{set: set},
		&uptimeSource{set: set},
		&inventorySource{set: set},
		&diskSource{set: set},
		&networkSource{set: set},
		&filesystemSource{set: set},
		&processSource{set: set},
		&sensorsSource{set: set},
	}
}
