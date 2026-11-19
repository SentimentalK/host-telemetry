package windows

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"strings"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

const (
	cpuInterval        = 15 * time.Second
	memoryInterval     = 15 * time.Second
	uptimeInterval     = 4 * time.Second
	diskInterval       = 15 * time.Second
	networkInterval    = 15 * time.Second
	filesystemInterval = 15 * time.Second
	processInterval    = 15 * time.Second
	inventoryInterval  = 300 * time.Second

	hundredNanosecondsPerSecond = 1e7
)

type cpuTimes struct {
	idle   float64
	kernel float64
	user   float64
}

type memoryCounters struct {
	totalPhys     float64
	availPhys     float64
	totalPagefile float64
	availPagefile float64
}

type volumeInfo struct {
	drive    string
	fsType   string
	readonly bool
	total    float64
	free     float64
}

type cimInfo struct {
	cores              float64
	logical            float64
	totalVisibleMemory float64
	maxProcesses       float64
}

type pdhRaw struct {
	path        string
	counterType uint32
	first       int64
}

func New(samples fs.FS) []source.Source {
	set := newFixtureSet(samples)
	return []source.Source{
		&cpuSource{set: set},
		&memorySource{set: set},
		&uptimeSource{set: set},
		&diskSource{set: set},
		&networkSource{set: set},
		&filesystemSource{set: set},
		&processSource{set: set},
		&inventorySource{set: set},
	}
}

func cpuMode(value string) schema.Attr {
	return schema.Attr{Key: schema.AttrCPUMode, Value: value}
}

func memoryState(value string) schema.Attr {
	return schema.Attr{Key: schema.AttrSystemMemoryState, Value: value}
}

func pagingState(value string) schema.Attr {
	return schema.Attr{Key: schema.AttrSystemPagingState, Value: value}
}

func cpuPoints(previous *cpuTimes, current cpuTimes) []schema.Point {
	points := []schema.Point{
		schema.NewPoint(schema.SystemCPUTime, current.idle/hundredNanosecondsPerSecond, cpuMode(schema.CPUModeIdle)),
		schema.NewPoint(schema.SystemCPUTime, (current.kernel-current.idle)/hundredNanosecondsPerSecond, cpuMode(schema.CPUModeSystem)),
		schema.NewPoint(schema.SystemCPUTime, current.user/hundredNanosecondsPerSecond, cpuMode(schema.CPUModeUser)),
	}
	if previous == nil {
		return points
	}
	idle := current.idle - previous.idle
	kernel := current.kernel - previous.kernel
	user := current.user - previous.user
	total := user + kernel
	if total <= 0 {
		return points
	}
	return append(points,
		schema.NewPoint(schema.SystemCPUUtilization, idle/total, cpuMode(schema.CPUModeIdle)),
		schema.NewPoint(schema.SystemCPUUtilization, (kernel-idle)/total, cpuMode(schema.CPUModeSystem)),
		schema.NewPoint(schema.SystemCPUUtilization, user/total, cpuMode(schema.CPUModeUser)),
	)
}

func memoryPoints(counters memoryCounters) ([]schema.Point, error) {
	if counters.totalPhys <= 0 || math.IsNaN(counters.totalPhys) || math.IsInf(counters.totalPhys, 0) {
		return nil, fmt.Errorf("windows: total physical memory %v is not positive and finite", counters.totalPhys)
	}
	physicalUsed := counters.totalPhys - counters.availPhys
	points := []schema.Point{
		schema.NewPoint(schema.SystemMemoryUsage, counters.availPhys, memoryState(schema.SystemMemoryStateAvailable)),
		schema.NewPoint(schema.SystemMemoryUsage, physicalUsed, memoryState(schema.SystemMemoryStateUsed)),
		schema.NewPoint(schema.SystemMemoryUtilization, counters.availPhys/counters.totalPhys, memoryState(schema.SystemMemoryStateAvailable)),
		schema.NewPoint(schema.SystemMemoryUtilization, physicalUsed/counters.totalPhys, memoryState(schema.SystemMemoryStateUsed)),
	}
	pagingTotal := counters.totalPagefile - counters.totalPhys
	if pagingTotal < 0 {
		pagingTotal = 0
	}
	if pagingTotal == 0 {
		return points, nil
	}
	committed := counters.totalPagefile - counters.availPagefile
	pagingUsed := committed - physicalUsed
	if pagingUsed < 0 {
		pagingUsed = 0
	}
	if pagingUsed > pagingTotal {
		pagingUsed = pagingTotal
	}
	return append(points,
		schema.NewPoint(schema.SystemPagingUsage, pagingUsed, pagingState(schema.SystemPagingStateUsed)),
		schema.NewPoint(schema.SystemPagingUsage, pagingTotal-pagingUsed, pagingState(schema.SystemPagingStateFree)),
	), nil
}

func uptimePoints(milliseconds float64) []schema.Point {
	return []schema.Point{schema.NewPoint(schema.SystemUptime, milliseconds/1000)}
}

func inventoryPoints(info cimInfo) []schema.Point {
	return []schema.Point{
		schema.NewPoint(schema.SystemCPULogicalCount, info.logical),
		schema.NewPoint(schema.SystemCPUPhysicalCount, info.cores),
		schema.NewPoint(schema.SystemMemoryLimit, info.totalVisibleMemory*1024),
	}
}

func filesystemPoints(volumes []volumeInfo) ([]schema.Point, error) {
	var points []schema.Point
	var errs []error
	for _, volume := range volumes {
		if volume.total <= 0 || math.IsNaN(volume.total) || math.IsInf(volume.total, 0) {
			errs = append(errs, fmt.Errorf("windows: %s: total size %v is not positive and finite", volume.drive, volume.total))
			continue
		}
		attrs := []schema.Attr{
			{Key: schema.AttrSystemDeviceName, Value: volume.drive},
			{Key: schema.AttrSystemFilesystemMountpoint, Value: volume.drive + `\`},
			{Key: schema.AttrSystemFilesystemMode, Value: filesystemMode(volume.readonly)},
		}
		if fsType := filesystemType(volume.fsType); fsType != "" {
			attrs = append(attrs, schema.Attr{Key: schema.AttrSystemFilesystemType, Value: fsType})
		}
		used := volume.total - volume.free
		points = append(points,
			schema.NewPoint(schema.SystemFilesystemUsage, used, filesystemState(attrs, schema.SystemFilesystemStateUsed)...),
			schema.NewPoint(schema.SystemFilesystemUsage, volume.free, filesystemState(attrs, schema.SystemFilesystemStateFree)...),
			schema.NewPoint(schema.SystemFilesystemUtilization, used/volume.total, filesystemState(attrs, schema.SystemFilesystemStateUsed)...),
			schema.NewPoint(schema.SystemFilesystemUtilization, volume.free/volume.total, filesystemState(attrs, schema.SystemFilesystemStateFree)...),
		)
	}
	return points, errors.Join(errs...)
}

func filesystemState(attrs []schema.Attr, state string) []schema.Attr {
	return append(append([]schema.Attr(nil), attrs...), schema.Attr{Key: schema.AttrSystemFilesystemState, Value: state})
}

func filesystemMode(readonly bool) string {
	if readonly {
		return "ro"
	}
	return "rw"
}

func filesystemType(value string) string {
	switch strings.ToUpper(value) {
	case "NTFS":
		return schema.SystemFilesystemTypeNtfs
	case "REFS":
		return schema.SystemFilesystemTypeRefs
	case "EXFAT":
		return schema.SystemFilesystemTypeExfat
	case "FAT32", "FAT":
		return schema.SystemFilesystemTypeFat32
	default:
		return ""
	}
}

type counterSpec struct {
	metric       string
	counterType  uint32
	directionKey string
	direction    string
	instanceAttr string
	deviceName   bool
}

var counterSpecs = map[string]counterSpec{
	`\PhysicalDisk\Disk Read Bytes/sec`:             {metric: schema.SystemDiskIO, counterType: 272696576, directionKey: schema.AttrDiskIODirection, direction: schema.DiskIODirectionRead, instanceAttr: schema.AttrSystemDeviceName, deviceName: true},
	`\PhysicalDisk\Disk Write Bytes/sec`:            {metric: schema.SystemDiskIO, counterType: 272696576, directionKey: schema.AttrDiskIODirection, direction: schema.DiskIODirectionWrite, instanceAttr: schema.AttrSystemDeviceName, deviceName: true},
	`\PhysicalDisk\Disk Reads/sec`:                  {metric: schema.SystemDiskOperations, counterType: 272696320, directionKey: schema.AttrDiskIODirection, direction: schema.DiskIODirectionRead, instanceAttr: schema.AttrSystemDeviceName, deviceName: true},
	`\PhysicalDisk\Disk Writes/sec`:                 {metric: schema.SystemDiskOperations, counterType: 272696320, directionKey: schema.AttrDiskIODirection, direction: schema.DiskIODirectionWrite, instanceAttr: schema.AttrSystemDeviceName, deviceName: true},
	`\Network Interface\Bytes Received/sec`:         {metric: schema.SystemNetworkIO, counterType: 272696576, directionKey: schema.AttrNetworkIODirection, direction: schema.NetworkIODirectionReceive, instanceAttr: schema.AttrNetworkInterfaceName},
	`\Network Interface\Bytes Sent/sec`:             {metric: schema.SystemNetworkIO, counterType: 272696576, directionKey: schema.AttrNetworkIODirection, direction: schema.NetworkIODirectionTransmit, instanceAttr: schema.AttrNetworkInterfaceName},
	`\Network Interface\Packets Received Errors`:    {metric: schema.SystemNetworkErrors, counterType: 272696320, directionKey: schema.AttrNetworkIODirection, direction: schema.NetworkIODirectionReceive, instanceAttr: schema.AttrNetworkInterfaceName},
	`\Network Interface\Packets Outbound Errors`:    {metric: schema.SystemNetworkErrors, counterType: 272696320, directionKey: schema.AttrNetworkIODirection, direction: schema.NetworkIODirectionTransmit, instanceAttr: schema.AttrNetworkInterfaceName},
	`\Network Interface\Packets Received/sec`:       {metric: schema.SystemNetworkPacketCount, counterType: 272696320, directionKey: schema.AttrNetworkIODirection, direction: schema.NetworkIODirectionReceive, instanceAttr: schema.AttrSystemDeviceName},
	`\Network Interface\Packets Sent/sec`:           {metric: schema.SystemNetworkPacketCount, counterType: 272696320, directionKey: schema.AttrNetworkIODirection, direction: schema.NetworkIODirectionTransmit, instanceAttr: schema.AttrSystemDeviceName},
	`\Network Interface\Packets Received Discarded`: {metric: schema.SystemNetworkPacketDropped, counterType: 272696320, directionKey: schema.AttrNetworkIODirection, direction: schema.NetworkIODirectionReceive, instanceAttr: schema.AttrNetworkInterfaceName},
	`\Network Interface\Packets Outbound Discarded`: {metric: schema.SystemNetworkPacketDropped, counterType: 272696320, directionKey: schema.AttrNetworkIODirection, direction: schema.NetworkIODirectionTransmit, instanceAttr: schema.AttrNetworkInterfaceName},
	`\System\Processes`:                             {metric: schema.SystemProcessCount, counterType: 65536},
}

func counterPathParts(path string) (class, instance, name string, err error) {
	if !strings.HasPrefix(path, `\`) {
		return "", "", "", fmt.Errorf("%s: counter path must start with a backslash", path)
	}
	rest := strings.TrimLeft(path, `\`)
	if strings.HasPrefix(path, `\\`) {
		separator := strings.IndexByte(rest, '\\')
		if separator < 0 {
			return "", "", "", fmt.Errorf("%s: counter path has no counter name", path)
		}
		rest = rest[separator+1:]
	}
	if open := strings.IndexByte(rest, '('); open >= 0 {
		closing := strings.IndexByte(rest[open:], ')')
		if closing < 0 {
			return "", "", "", fmt.Errorf("%s: counter path has no closing parenthesis", path)
		}
		closing += open
		class = rest[:open]
		if separator := strings.LastIndexByte(class, '\\'); separator >= 0 {
			class = class[separator+1:]
		}
		instance = rest[open+1 : closing]
		tail := rest[closing+1:]
		if !strings.HasPrefix(tail, `\`) {
			return "", "", "", fmt.Errorf("%s: counter path has no counter name", path)
		}
		name = tail[1:]
	} else {
		separator := strings.LastIndexByte(rest, '\\')
		if separator < 0 {
			return "", "", "", fmt.Errorf("%s: counter path has no counter name", path)
		}
		class = rest[:separator]
		if last := strings.LastIndexByte(class, '\\'); last >= 0 {
			class = class[last+1:]
		}
		name = rest[separator+1:]
	}
	if class == "" || name == "" {
		return "", "", "", fmt.Errorf("%s: malformed counter path", path)
	}
	return class, instance, name, nil
}

func pdhPoints(raws []pdhRaw, class string) ([]schema.Point, error) {
	var points []schema.Point
	var errs []error
	for _, raw := range raws {
		counterClass, instance, name, err := counterPathParts(raw.path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if counterClass != class {
			continue
		}
		if class == "PhysicalDisk" && instance == "_Total" {
			continue
		}
		spec, ok := counterSpecs[`\`+class+`\`+name]
		if !ok {
			continue
		}
		if raw.counterType != spec.counterType {
			errs = append(errs, fmt.Errorf("%s: counter type %d, want %d", raw.path, raw.counterType, spec.counterType))
			continue
		}
		point, err := counterPoint(spec, instance, float64(raw.first))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		points = append(points, point)
	}
	return points, errors.Join(errs...)
}

func counterPoint(spec counterSpec, instance string, value float64) (schema.Point, error) {
	var attrs []schema.Attr
	if spec.instanceAttr != "" {
		name := instance
		if spec.deviceName {
			device, err := diskDevice(instance)
			if err != nil {
				return schema.Point{}, err
			}
			name = device
		}
		attrs = append(attrs, schema.Attr{Key: spec.instanceAttr, Value: name})
	}
	if spec.directionKey != "" {
		attrs = append(attrs, schema.Attr{Key: spec.directionKey, Value: spec.direction})
	}
	return schema.NewPoint(spec.metric, value, attrs...), nil
}

func diskDevice(instance string) (string, error) {
	end := 0
	for end < len(instance) && instance[end] >= '0' && instance[end] <= '9' {
		end++
	}
	if end == 0 {
		return "", fmt.Errorf("%s: disk instance has no leading index", instance)
	}
	return "PhysicalDrive" + instance[:end], nil
}
