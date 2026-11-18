package linux

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

const (
	userHZ   = 100
	interval = 15 * time.Second
)

var modes = []string{
	schema.CPUModeUser,
	schema.CPUModeNice,
	schema.CPUModeSystem,
	schema.CPUModeIdle,
	schema.CPUModeIowait,
	schema.CPUModeInterrupt,
	schema.CPUModeSteal,
}

type linuxSource struct {
	samples fs.FS
	once    sync.Once
	points  []schema.Point
	err     error
}

func New(samples fs.FS) source.Source {
	return &linuxSource{samples: samples}
}

func (s *linuxSource) Name() string { return "linux-procfs" }

func (s *linuxSource) Interval() time.Duration { return interval }

func (s *linuxSource) Collect(ctx context.Context) ([]schema.Point, error) {
	s.once.Do(func() {
		s.points, s.err = s.load()
	})
	if s.err != nil {
		return nil, s.err
	}
	return s.points, nil
}

func (s *linuxSource) load() ([]schema.Point, error) {
	dirs, err := sampleDirs(s.samples)
	if err != nil {
		return nil, err
	}
	samples := make([]sample, 0, len(dirs))
	for _, dir := range dirs {
		s, err := s.readSample(dir)
		if err != nil {
			return nil, err
		}
		samples = append(samples, s)
	}
	return buildPoints(samples, interval.Seconds()), nil
}

func sampleDirs(samples fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(samples, ".")
	if err != nil {
		return nil, fmt.Errorf("read fixture root: %w", err)
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "sample-") {
			dirs = append(dirs, entry.Name())
		}
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("read fixture root: no sample-* directories")
	}
	sort.Strings(dirs)
	return dirs, nil
}

func (s *linuxSource) readSample(dir string) (sample, error) {
	var out sample
	stat, err := readFile(s.samples, dir, "proc/stat")
	if err != nil {
		return out, err
	}
	out.cpu, out.logical, err = parseStat(stat, dir+"/proc/stat")
	if err != nil {
		return out, err
	}
	uptime, err := readFile(s.samples, dir, "proc/uptime")
	if err != nil {
		return out, err
	}
	out.uptime, err = parseUptime(uptime, dir+"/proc/uptime")
	if err != nil {
		return out, err
	}
	meminfo, err := readFile(s.samples, dir, "proc/meminfo")
	if err != nil {
		return out, err
	}
	mem, err := parseMeminfo(meminfo, dir+"/proc/meminfo")
	if err != nil {
		return out, err
	}
	for _, key := range []string{"MemTotal", "MemAvailable", "SwapTotal", "SwapFree"} {
		if _, ok := mem[key]; !ok {
			return out, fmt.Errorf("%s/proc/meminfo: missing %s", dir, key)
		}
	}
	out.memTotal = mem["MemTotal"]
	out.memAvail = mem["MemAvailable"]
	out.swapTotal = mem["SwapTotal"]
	out.swapFree = mem["SwapFree"]
	vmstat, err := readFile(s.samples, dir, "proc/vmstat")
	if err != nil {
		return out, err
	}
	vm, err := parseVMStat(vmstat, dir+"/proc/vmstat")
	if err != nil {
		return out, err
	}
	for _, key := range []string{"pgfault", "pgmajfault"} {
		if _, ok := vm[key]; !ok {
			return out, fmt.Errorf("%s/proc/vmstat: missing %s", dir, key)
		}
	}
	out.minorFaults = vm["pgfault"]
	out.majorFaults = vm["pgmajfault"]
	diskstats, err := readFile(s.samples, dir, "proc/diskstats")
	if err != nil {
		return out, err
	}
	out.disk, err = parseDiskstats(diskstats, dir+"/proc/diskstats")
	if err != nil {
		return out, err
	}
	netdev, err := readFile(s.samples, dir, "proc/net/dev")
	if err != nil {
		return out, err
	}
	out.net, err = parseNetDev(netdev, dir+"/proc/net/dev")
	if err != nil {
		return out, err
	}
	return out, nil
}

func readFile(samples fs.FS, dir, name string) ([]byte, error) {
	data, err := fs.ReadFile(samples, dir+"/"+name)
	if err != nil {
		return nil, fmt.Errorf("%s/%s: %w", dir, name, err)
	}
	return data, nil
}

func parseStat(data []byte, file string) (map[string]float64, float64, error) {
	cpu := make(map[string]float64, len(modes))
	logical := 0.0
	for i, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch {
		case fields[0] == "cpu":
			if len(fields) < 9 {
				return nil, 0, fmt.Errorf("%s: line %d: cpu line has %d counters, want 8", file, i+1, len(fields)-1)
			}
			ticks := make([]float64, 8)
			for j := range ticks {
				value, err := strconv.ParseFloat(fields[j+1], 64)
				if err != nil {
					return nil, 0, fmt.Errorf("%s: line %d: counter %q: %w", file, i+1, fields[j+1], err)
				}
				ticks[j] = value
			}
			cpu[schema.CPUModeUser] = ticks[0] / userHZ
			cpu[schema.CPUModeNice] = ticks[1] / userHZ
			cpu[schema.CPUModeSystem] = ticks[2] / userHZ
			cpu[schema.CPUModeIdle] = ticks[3] / userHZ
			cpu[schema.CPUModeIowait] = ticks[4] / userHZ
			cpu[schema.CPUModeInterrupt] = (ticks[5] + ticks[6]) / userHZ
			cpu[schema.CPUModeSteal] = ticks[7] / userHZ
		case strings.HasPrefix(fields[0], "cpu"):
			logical++
		}
	}
	if len(cpu) == 0 {
		return nil, 0, fmt.Errorf("%s: missing cpu line", file)
	}
	if logical == 0 {
		return nil, 0, fmt.Errorf("%s: missing per-cpu lines", file)
	}
	return cpu, logical, nil
}

func parseUptime(data []byte, file string) (float64, error) {
	lines := nonEmptyLines(data)
	if len(lines) == 0 {
		return 0, fmt.Errorf("%s: empty file", file)
	}
	fields := strings.Fields(lines[0].text)
	if len(fields) == 0 {
		return 0, fmt.Errorf("%s: line %d: empty line", file, lines[0].number)
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("%s: line %d: uptime %q: %w", file, lines[0].number, fields[0], err)
	}
	return value, nil
}

func parseMeminfo(data []byte, file string) (map[string]float64, error) {
	out := make(map[string]float64)
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("%s: line %d: missing ':': %q", file, i+1, line)
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return nil, fmt.Errorf("%s: line %d: missing value: %q", file, i+1, line)
		}
		value, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: value %q: %w", file, i+1, fields[0], err)
		}
		if len(fields) > 1 && fields[1] == "kB" {
			value *= 1024
		}
		out[key] = value
	}
	return out, nil
}

func parseVMStat(data []byte, file string) (map[string]float64, error) {
	out := make(map[string]float64)
	for i, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s: line %d: want 2 fields, got %d: %q", file, i+1, len(fields), line)
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: value %q: %w", file, i+1, fields[1], err)
		}
		out[fields[0]] = value
	}
	return out, nil
}

func parseDiskstats(data []byte, file string) (map[string]diskCounters, error) {
	out := make(map[string]diskCounters)
	for i, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 10 {
			return nil, fmt.Errorf("%s: line %d: want at least 10 fields, got %d", file, i+1, len(fields))
		}
		values := make([]float64, 4)
		for j, index := range []int{3, 5, 7, 9} {
			value, err := strconv.ParseFloat(fields[index], 64)
			if err != nil {
				return nil, fmt.Errorf("%s: line %d: field %d %q: %w", file, i+1, index+1, fields[index], err)
			}
			values[j] = value
		}
		out[fields[2]] = diskCounters{
			readOps:    values[0],
			readBytes:  values[1] * 512,
			writeOps:   values[2],
			writeBytes: values[3] * 512,
		}
	}
	return out, nil
}

func parseNetDev(data []byte, file string) (map[string]netCounters, error) {
	out := make(map[string]netCounters)
	for i, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, ":") {
			continue
		}
		name, rest, _ := strings.Cut(line, ":")
		fields := strings.Fields(rest)
		if len(fields) < 16 {
			return nil, fmt.Errorf("%s: line %d: want 16 counters, got %d", file, i+1, len(fields))
		}
		values := make([]float64, 4)
		for j, index := range []int{0, 2, 8, 10} {
			value, err := strconv.ParseFloat(fields[index], 64)
			if err != nil {
				return nil, fmt.Errorf("%s: line %d: counter %q: %w", file, i+1, fields[index], err)
			}
			values[j] = value
		}
		out[strings.TrimSpace(name)] = netCounters{
			rxBytes:  values[0],
			rxErrors: values[1],
			txBytes:  values[2],
			txErrors: values[3],
		}
	}
	return out, nil
}

type sample struct {
	cpu         map[string]float64
	logical     float64
	uptime      float64
	memTotal    float64
	memAvail    float64
	swapTotal   float64
	swapFree    float64
	minorFaults float64
	majorFaults float64
	disk        map[string]diskCounters
	net         map[string]netCounters
}

type diskCounters struct {
	readOps    float64
	readBytes  float64
	writeOps   float64
	writeBytes float64
}

type netCounters struct {
	rxBytes  float64
	txBytes  float64
	rxErrors float64
	txErrors float64
}

func buildPoints(samples []sample, intervalSeconds float64) []schema.Point {
	last := samples[len(samples)-1]
	points := []schema.Point{
		schema.NewPoint(schema.SystemUptime, last.uptime),
		schema.NewPoint(schema.SystemCPULogicalCount, last.logical),
	}
	for _, mode := range modes {
		points = append(points, schema.NewPoint(schema.SystemCPUTime, last.cpu[mode], schema.Attr{Key: schema.AttrCPUMode, Value: mode}))
	}
	points = append(points,
		schema.NewPoint(schema.SystemMemoryUsage, last.memTotal-last.memAvail, schema.Attr{Key: schema.AttrSystemMemoryState, Value: schema.SystemMemoryStateUsed}),
		schema.NewPoint(schema.SystemMemoryUsage, last.memAvail, schema.Attr{Key: schema.AttrSystemMemoryState, Value: schema.SystemMemoryStateAvailable}),
		schema.NewPoint(schema.SystemPagingUsage, last.swapTotal-last.swapFree, schema.Attr{Key: schema.AttrSystemPagingState, Value: schema.SystemPagingStateUsed}),
		schema.NewPoint(schema.SystemPagingUsage, last.swapFree, schema.Attr{Key: schema.AttrSystemPagingState, Value: schema.SystemPagingStateFree}),
		schema.NewPoint(schema.SystemPagingFaults, last.minorFaults, schema.Attr{Key: schema.AttrSystemPagingFaultType, Value: schema.SystemPagingFaultTypeMinor}),
		schema.NewPoint(schema.SystemPagingFaults, last.majorFaults, schema.Attr{Key: schema.AttrSystemPagingFaultType, Value: schema.SystemPagingFaultTypeMajor}),
	)
	devices := make([]string, 0, len(last.disk))
	for name := range last.disk {
		devices = append(devices, name)
	}
	sort.Strings(devices)
	for _, name := range devices {
		disk := last.disk[name]
		points = append(points,
			schema.NewPoint(schema.SystemDiskIO, disk.readBytes, schema.Attr{Key: schema.AttrSystemDeviceName, Value: name}, schema.Attr{Key: schema.AttrDiskIODirection, Value: schema.DiskIODirectionRead}),
			schema.NewPoint(schema.SystemDiskIO, disk.writeBytes, schema.Attr{Key: schema.AttrSystemDeviceName, Value: name}, schema.Attr{Key: schema.AttrDiskIODirection, Value: schema.DiskIODirectionWrite}),
			schema.NewPoint(schema.SystemDiskOperations, disk.readOps, schema.Attr{Key: schema.AttrSystemDeviceName, Value: name}, schema.Attr{Key: schema.AttrDiskIODirection, Value: schema.DiskIODirectionRead}),
			schema.NewPoint(schema.SystemDiskOperations, disk.writeOps, schema.Attr{Key: schema.AttrSystemDeviceName, Value: name}, schema.Attr{Key: schema.AttrDiskIODirection, Value: schema.DiskIODirectionWrite}),
		)
	}
	interfaces := make([]string, 0, len(last.net))
	for name := range last.net {
		interfaces = append(interfaces, name)
	}
	sort.Strings(interfaces)
	for _, name := range interfaces {
		net := last.net[name]
		points = append(points,
			schema.NewPoint(schema.SystemNetworkIO, net.rxBytes, schema.Attr{Key: schema.AttrNetworkInterfaceName, Value: name}, schema.Attr{Key: schema.AttrNetworkIODirection, Value: schema.NetworkIODirectionReceive}),
			schema.NewPoint(schema.SystemNetworkIO, net.txBytes, schema.Attr{Key: schema.AttrNetworkInterfaceName, Value: name}, schema.Attr{Key: schema.AttrNetworkIODirection, Value: schema.NetworkIODirectionTransmit}),
			schema.NewPoint(schema.SystemNetworkErrors, net.rxErrors, schema.Attr{Key: schema.AttrNetworkInterfaceName, Value: name}, schema.Attr{Key: schema.AttrNetworkIODirection, Value: schema.NetworkIODirectionReceive}),
			schema.NewPoint(schema.SystemNetworkErrors, net.txErrors, schema.Attr{Key: schema.AttrNetworkInterfaceName, Value: name}, schema.Attr{Key: schema.AttrNetworkIODirection, Value: schema.NetworkIODirectionTransmit}),
		)
	}
	if len(samples) >= 2 {
		previous := samples[len(samples)-2]
		for _, mode := range modes {
			delta := last.cpu[mode] - previous.cpu[mode]
			points = append(points, schema.NewPoint(schema.SystemCPUUtilization, delta/intervalSeconds, schema.Attr{Key: schema.AttrCPUMode, Value: mode}))
		}
	}
	return points
}

type numberedLine struct {
	number int
	text   string
}

func nonEmptyLines(data []byte) []numberedLine {
	var out []numberedLine
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, numberedLine{number: i + 1, text: line})
		}
	}
	return out
}
