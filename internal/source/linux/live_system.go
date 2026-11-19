package linux

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

type systemSource struct {
	root fs.FS
	now  func() time.Time

	prevCPU map[string]float64
	prevAt  time.Time
}

func (s *systemSource) Name() string { return "linux-system" }

func (s *systemSource) Interval() time.Duration { return interval }

func (s *systemSource) Collect(ctx context.Context) ([]schema.Point, error) {
	stat, err := fs.ReadFile(s.root, "proc/stat")
	if err != nil {
		return nil, fmt.Errorf("proc/stat: %w", err)
	}
	cpu, logical, err := parseStat(stat, "proc/stat")
	if err != nil {
		return nil, err
	}
	created, err := parseStatProcesses(stat, "proc/stat")
	if err != nil {
		return nil, err
	}
	uptime, err := readUptime(s.root)
	if err != nil {
		return nil, err
	}
	mem, err := readMeminfo(s.root)
	if err != nil {
		return nil, err
	}

	now := s.now()
	points := []schema.Point{schema.NewPoint(schema.SystemUptime, uptime)}
	for _, mode := range modes {
		points = append(points, schema.NewPoint(schema.SystemCPUTime, cpu[mode], schema.Attr{Key: schema.AttrCPUMode, Value: mode}))
	}
	if s.prevCPU != nil {
		if elapsed := now.Sub(s.prevAt).Seconds(); elapsed > 0 {
			for _, mode := range modes {
				delta := cpu[mode] - s.prevCPU[mode]
				if delta < 0 {
					delta = 0
				}
				points = append(points, schema.NewPoint(schema.SystemCPUUtilization, delta/(elapsed*logical), schema.Attr{Key: schema.AttrCPUMode, Value: mode}))
			}
		}
	}
	s.prevCPU = cpu
	s.prevAt = now

	total := mem["MemTotal"]
	available := mem["MemAvailable"]
	used := total - available
	points = append(points,
		schema.NewPoint(schema.SystemMemoryUsage, used, schema.Attr{Key: schema.AttrSystemMemoryState, Value: schema.SystemMemoryStateUsed}),
		schema.NewPoint(schema.SystemMemoryUsage, available, schema.Attr{Key: schema.AttrSystemMemoryState, Value: schema.SystemMemoryStateAvailable}),
	)
	if total > 0 {
		points = append(points,
			schema.NewPoint(schema.SystemMemoryUtilization, used/total, schema.Attr{Key: schema.AttrSystemMemoryState, Value: schema.SystemMemoryStateUsed}),
			schema.NewPoint(schema.SystemMemoryUtilization, available/total, schema.Attr{Key: schema.AttrSystemMemoryState, Value: schema.SystemMemoryStateAvailable}),
		)
	}
	swapTotal := mem["SwapTotal"]
	swapFree := mem["SwapFree"]
	points = append(points,
		schema.NewPoint(schema.SystemPagingUsage, swapTotal-swapFree, schema.Attr{Key: schema.AttrSystemPagingState, Value: schema.SystemPagingStateUsed}),
		schema.NewPoint(schema.SystemPagingUsage, swapFree, schema.Attr{Key: schema.AttrSystemPagingState, Value: schema.SystemPagingStateFree}),
	)
	points = append(points, faultPoints(s.root)...)
	points = append(points, diskPoints(s.root)...)
	points = append(points, netPoints(s.root)...)
	points = append(points, pressurePoints(s.root)...)
	points = append(points, schema.NewPoint(schema.SystemProcessCreated, created))
	return points, nil
}

func readUptime(root fs.FS) (float64, error) {
	data, err := fs.ReadFile(root, "proc/uptime")
	if err != nil {
		return 0, fmt.Errorf("proc/uptime: %w", err)
	}
	return parseUptime(data, "proc/uptime")
}

func readMeminfo(root fs.FS) (map[string]float64, error) {
	data, err := fs.ReadFile(root, "proc/meminfo")
	if err != nil {
		return nil, fmt.Errorf("proc/meminfo: %w", err)
	}
	mem, err := parseMeminfo(data, "proc/meminfo")
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"MemTotal", "MemAvailable", "SwapTotal", "SwapFree"} {
		if _, ok := mem[key]; !ok {
			return nil, fmt.Errorf("proc/meminfo: missing %s", key)
		}
	}
	return mem, nil
}

func parseStatProcesses(data []byte, file string) (float64, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "processes" {
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return 0, fmt.Errorf("%s: processes: %w", file, err)
		}
		return value, nil
	}
	return 0, fmt.Errorf("%s: missing processes line", file)
}

func faultPoints(root fs.FS) []schema.Point {
	data, err := fs.ReadFile(root, "proc/vmstat")
	if err != nil {
		return nil
	}
	vm, err := parseVMStat(data, "proc/vmstat")
	if err != nil {
		return nil
	}
	var points []schema.Point
	if value, ok := vm["pgfault"]; ok {
		points = append(points, schema.NewPoint(schema.SystemPagingFaults, value, schema.Attr{Key: schema.AttrSystemPagingFaultType, Value: schema.SystemPagingFaultTypeMinor}))
	}
	if value, ok := vm["pgmajfault"]; ok {
		points = append(points, schema.NewPoint(schema.SystemPagingFaults, value, schema.Attr{Key: schema.AttrSystemPagingFaultType, Value: schema.SystemPagingFaultTypeMajor}))
	}
	for _, direction := range []struct {
		key   string
		value string
	}{
		{"pswpin", schema.SystemPagingDirectionIn},
		{"pswpout", schema.SystemPagingDirectionOut},
	} {
		value, ok := vm[direction.key]
		if !ok {
			continue
		}
		points = append(points, schema.NewPoint(schema.SystemPagingOperations, value,
			schema.Attr{Key: schema.AttrSystemPagingFaultType, Value: schema.SystemPagingFaultTypeMajor},
			schema.Attr{Key: schema.AttrSystemPagingDirection, Value: direction.value}))
	}
	return points
}

type diskStats struct {
	readOps    float64
	readBytes  float64
	writeOps   float64
	writeBytes float64
	readTime   float64
	writeTime  float64
	ioTime     float64
}

func parseDiskCounters(data []byte, file string) (map[string]diskStats, error) {
	out := make(map[string]diskStats)
	for i, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 13 {
			return nil, fmt.Errorf("%s: line %d: want at least 13 fields, got %d", file, i+1, len(fields))
		}
		values := make([]float64, 7)
		for j, index := range []int{3, 5, 6, 7, 9, 10, 12} {
			value, err := strconv.ParseFloat(fields[index], 64)
			if err != nil {
				return nil, fmt.Errorf("%s: line %d: field %d %q: %w", file, i+1, index+1, fields[index], err)
			}
			values[j] = value
		}
		out[fields[2]] = diskStats{
			readOps:    values[0],
			readBytes:  values[1] * 512,
			writeOps:   values[3],
			writeBytes: values[4] * 512,
			readTime:   values[2] / 1000,
			writeTime:  values[5] / 1000,
			ioTime:     values[6] / 1000,
		}
	}
	return out, nil
}

func diskPoints(root fs.FS) []schema.Point {
	data, err := fs.ReadFile(root, "proc/diskstats")
	if err != nil {
		return nil
	}
	disks, err := parseDiskCounters(data, "proc/diskstats")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(disks))
	for name := range disks {
		names = append(names, name)
	}
	sort.Strings(names)
	var points []schema.Point
	for _, name := range names {
		disk := disks[name]
		device := schema.Attr{Key: schema.AttrSystemDeviceName, Value: name}
		points = append(points,
			schema.NewPoint(schema.SystemDiskIO, disk.readBytes, device, schema.Attr{Key: schema.AttrDiskIODirection, Value: schema.DiskIODirectionRead}),
			schema.NewPoint(schema.SystemDiskIO, disk.writeBytes, device, schema.Attr{Key: schema.AttrDiskIODirection, Value: schema.DiskIODirectionWrite}),
			schema.NewPoint(schema.SystemDiskOperations, disk.readOps, device, schema.Attr{Key: schema.AttrDiskIODirection, Value: schema.DiskIODirectionRead}),
			schema.NewPoint(schema.SystemDiskOperations, disk.writeOps, device, schema.Attr{Key: schema.AttrDiskIODirection, Value: schema.DiskIODirectionWrite}),
			schema.NewPoint(schema.SystemDiskIOTime, disk.ioTime, device),
			schema.NewPoint(schema.SystemDiskOperationTime, disk.readTime, device, schema.Attr{Key: schema.AttrDiskIODirection, Value: schema.DiskIODirectionRead}),
			schema.NewPoint(schema.SystemDiskOperationTime, disk.writeTime, device, schema.Attr{Key: schema.AttrDiskIODirection, Value: schema.DiskIODirectionWrite}),
		)
	}
	return points
}

type netStats struct {
	rxBytes   float64
	rxPackets float64
	rxErrors  float64
	rxDropped float64
	txBytes   float64
	txPackets float64
	txErrors  float64
	txDropped float64
}

func parseNetCounters(data []byte, file string) (map[string]netStats, error) {
	out := make(map[string]netStats)
	for i, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, ":") {
			continue
		}
		name, rest, _ := strings.Cut(line, ":")
		fields := strings.Fields(rest)
		if len(fields) < 16 {
			return nil, fmt.Errorf("%s: line %d: want 16 counters, got %d", file, i+1, len(fields))
		}
		values := make([]float64, 8)
		for j, index := range []int{0, 1, 2, 3, 8, 9, 10, 11} {
			value, err := strconv.ParseFloat(fields[index], 64)
			if err != nil {
				return nil, fmt.Errorf("%s: line %d: counter %q: %w", file, i+1, fields[index], err)
			}
			values[j] = value
		}
		out[strings.TrimSpace(name)] = netStats{
			rxBytes:   values[0],
			rxPackets: values[1],
			rxErrors:  values[2],
			rxDropped: values[3],
			txBytes:   values[4],
			txPackets: values[5],
			txErrors:  values[6],
			txDropped: values[7],
		}
	}
	return out, nil
}

func netPoints(root fs.FS) []schema.Point {
	data, err := fs.ReadFile(root, "proc/net/dev")
	if err != nil {
		return nil
	}
	interfaces, err := parseNetCounters(data, "proc/net/dev")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(interfaces))
	for name := range interfaces {
		names = append(names, name)
	}
	sort.Strings(names)
	var points []schema.Point
	for _, name := range names {
		net := interfaces[name]
		iface := schema.Attr{Key: schema.AttrNetworkInterfaceName, Value: name}
		device := schema.Attr{Key: schema.AttrSystemDeviceName, Value: name}
		receive := schema.Attr{Key: schema.AttrNetworkIODirection, Value: schema.NetworkIODirectionReceive}
		transmit := schema.Attr{Key: schema.AttrNetworkIODirection, Value: schema.NetworkIODirectionTransmit}
		points = append(points,
			schema.NewPoint(schema.SystemNetworkIO, net.rxBytes, iface, receive),
			schema.NewPoint(schema.SystemNetworkIO, net.txBytes, iface, transmit),
			schema.NewPoint(schema.SystemNetworkErrors, net.rxErrors, iface, receive),
			schema.NewPoint(schema.SystemNetworkErrors, net.txErrors, iface, transmit),
			schema.NewPoint(schema.SystemNetworkPacketCount, net.rxPackets, device, receive),
			schema.NewPoint(schema.SystemNetworkPacketCount, net.txPackets, device, transmit),
			schema.NewPoint(schema.SystemNetworkPacketDropped, net.rxDropped, iface, receive),
			schema.NewPoint(schema.SystemNetworkPacketDropped, net.txDropped, iface, transmit),
		)
	}
	return points
}

func pressurePoints(root fs.FS) []schema.Point {
	resources := []struct {
		name    string
		path    string
		hasFull bool
	}{
		{schema.SystemPressureResourceCPU, "proc/pressure/cpu", false},
		{schema.SystemPressureResourceMemory, "proc/pressure/memory", true},
		{schema.SystemPressureResourceIO, "proc/pressure/io", true},
	}
	var points []schema.Point
	for _, resource := range resources {
		data, err := fs.ReadFile(root, resource.path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			kind := fields[0]
			if kind != schema.SystemPressureTypeSome && kind != schema.SystemPressureTypeFull {
				continue
			}
			if kind == schema.SystemPressureTypeFull && !resource.hasFull {
				continue
			}
			values := make(map[string]float64, len(fields)-1)
			for _, field := range fields[1:] {
				key, text, ok := strings.Cut(field, "=")
				if !ok {
					continue
				}
				value, err := strconv.ParseFloat(text, 64)
				if err != nil {
					continue
				}
				values[key] = value
			}
			attrs := []schema.Attr{
				{Key: schema.AttrSystemPressureResource, Value: resource.name},
				{Key: schema.AttrSystemPressureType, Value: kind},
			}
			if total, ok := values["total"]; ok {
				points = append(points, schema.NewPoint(schema.SystemPressureStallTime, total/1e6, attrs...))
			}
			if avg, ok := values["avg10"]; ok {
				points = append(points, schema.NewPoint(schema.SystemPressureUtilization, avg/100, attrs...))
			}
		}
	}
	return points
}

var _ source.Source = (*systemSource)(nil)
