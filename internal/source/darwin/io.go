package darwin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

type diskStat struct {
	name      string
	readBytes float64
	writeByte float64
	readOps   float64
	writeOps  float64
	readTime  float64
	writeTime float64
}

type networkStat struct {
	name      string
	rxBytes   float64
	txBytes   float64
	rxPackets float64
	txPackets float64
	rxErrors  float64
	txErrors  float64
	txDropped float64
}

type filesystemStat struct {
	device     string
	mountpoint string
	mode       string
	fstype     string
	hasType    bool
	total      float64
	used       float64
	free       float64
	reserved   float64
}

type processStat struct {
	running  int
	sleeping int
	stopped  int
	defunct  int
	limit    float64
	hasLimit bool
}

type fanStat struct {
	name   string
	rpm    *float64
	maxRPM *float64
}

type sensorsStat struct {
	cpuTemp  *float64
	gpuTemp  *float64
	fans     []fanStat
	cpuPower *float64
	gpuPower *float64
	sysPower *float64
}

var ioregPairPattern = regexp.MustCompile(`"([^"]+)"\s*=\s*("[^"]*"|[^,}\s]+)`)

var mountPattern = regexp.MustCompile(`^(\S+) on (.+) \(([^)]*)\)$`)

type ioregNode struct {
	kind  string
	depth int
	body  []string
}

func parseIOreg(data []byte, file string) ([]diskStat, error) {
	var nodes []ioregNode
	for _, line := range strings.Split(string(data), "\n") {
		if index := strings.Index(line, "+-o "); index >= 0 {
			fields := strings.Fields(line[index+len("+-o "):])
			if len(fields) == 0 {
				continue
			}
			nodes = append(nodes, ioregNode{kind: fields[0], depth: index})
			continue
		}
		if len(nodes) > 0 {
			nodes[len(nodes)-1].body = append(nodes[len(nodes)-1].body, line)
		}
	}
	var out []diskStat
	for i, node := range nodes {
		if node.kind != "IOBlockStorageDriver" {
			continue
		}
		stat, err := parseIOregStatistics(strings.Join(node.body, "\n"), file)
		if err != nil {
			return nil, err
		}
		media := -1
		for j := i + 1; j < len(nodes) && nodes[j].depth > node.depth; j++ {
			if nodes[j].kind != "IOMedia" {
				continue
			}
			body := strings.Join(nodes[j].body, "\n")
			if ioregValue(body, "Whole") == "Yes" && ioregValue(body, "BSD Name") != "" {
				media = j
				break
			}
		}
		if media < 0 {
			continue
		}
		stat.name = ioregValue(strings.Join(nodes[media].body, "\n"), "BSD Name")
		out = append(out, stat)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no IOBlockStorageDriver backed by a whole IOMedia", file)
	}
	return out, nil
}

func parseIOregStatistics(text, file string) (diskStat, error) {
	start := strings.Index(text, `"Statistics"`)
	if start < 0 {
		return diskStat{}, fmt.Errorf("%s: IOBlockStorageDriver without Statistics", file)
	}
	open := strings.Index(text[start:], "{")
	if open < 0 {
		return diskStat{}, fmt.Errorf("%s: Statistics without a dictionary", file)
	}
	open += start
	depth := 0
	end := -1
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return diskStat{}, fmt.Errorf("%s: Statistics dictionary is not closed", file)
	}
	values := make(map[string]string)
	for _, match := range ioregPairPattern.FindAllStringSubmatch(text[open+1:end], -1) {
		values[match[1]] = strings.Trim(match[2], `"`)
	}
	number := func(key string) (float64, error) {
		raw, ok := values[key]
		if !ok {
			return 0, fmt.Errorf("%s: Statistics missing %q", file, key)
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, fmt.Errorf("%s: Statistics %q=%q: %w", file, key, raw, err)
		}
		return value, nil
	}
	var stat diskStat
	fields := []struct {
		key string
		dst *float64
	}{
		{"Bytes (Read)", &stat.readBytes},
		{"Bytes (Write)", &stat.writeByte},
		{"Operations (Read)", &stat.readOps},
		{"Operations (Write)", &stat.writeOps},
		{"Total Time (Read)", &stat.readTime},
		{"Total Time (Write)", &stat.writeTime},
	}
	for _, field := range fields {
		value, err := number(field.key)
		if err != nil {
			return diskStat{}, err
		}
		*field.dst = value
	}
	return stat, nil
}

func ioregValue(text, key string) string {
	for _, match := range ioregPairPattern.FindAllStringSubmatch(text, -1) {
		if match[1] == key {
			return strings.Trim(match[2], `"`)
		}
	}
	return ""
}

func parseNetstat(data []byte, file string) ([]networkStat, error) {
	var out []networkStat
	for i, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "Name" {
			continue
		}
		link := -1
		for j, field := range fields {
			if strings.HasPrefix(field, "<Link#") {
				link = j
				break
			}
		}
		if link < 0 {
			continue
		}
		if len(fields) < link+9 {
			return nil, fmt.Errorf("%s: line %d: interface %q has incomplete counters", file, i+1, fields[0])
		}
		numbers := fields[len(fields)-8:]
		values := make([]float64, len(numbers))
		for k, raw := range numbers {
			value, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, fmt.Errorf("%s: line %d: %q: %w", file, i+1, raw, err)
			}
			values[k] = value
		}
		out = append(out, networkStat{
			name:      fields[0],
			rxPackets: values[0],
			rxErrors:  values[1],
			rxBytes:   values[2],
			txPackets: values[3],
			txErrors:  values[4],
			txBytes:   values[5],
			txDropped: values[7],
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no <Link#> interfaces", file)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

type mountEntry struct {
	device     string
	mountpoint string
	fstype     string
	mode       string
}

type dfEntry struct {
	blocks    float64
	used      float64
	available float64
}

func parseFilesystems(mountData, dfData []byte, dir string) ([]filesystemStat, error) {
	return parseFilesystemsFrom(mountData, dfData, dir+"/mount.txt", dir+"/df.txt")
}

func parseFilesystemsFrom(mountData, dfData []byte, mountFile, dfFile string) ([]filesystemStat, error) {
	mounts, err := parseMount(mountData, mountFile)
	if err != nil {
		return nil, err
	}
	usage, err := parseDF(dfData, dfFile)
	if err != nil {
		return nil, err
	}
	var out []filesystemStat
	for _, mount := range mounts {
		row, ok := usage[mount.mountpoint]
		if !ok {
			continue
		}
		if row.blocks <= 0 {
			return nil, fmt.Errorf("%s: %s: 1024-blocks must be positive", dfFile, mount.mountpoint)
		}
		total := row.blocks * 1024
		used := row.used * 1024
		free := row.available * 1024
		reserved := total - used - free
		if reserved < 0 {
			reserved = 0
		}
		stat := filesystemStat{
			device:     mount.device,
			mountpoint: mount.mountpoint,
			mode:       mount.mode,
			total:      total,
			used:       used,
			free:       free,
			reserved:   reserved,
		}
		stat.fstype, stat.hasType = darwinFilesystemType(mount.fstype)
		out = append(out, stat)
	}
	return out, nil
}

func parseMount(data []byte, file string) ([]mountEntry, error) {
	var out []mountEntry
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		match := mountPattern.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("%s: line %d: %q", file, i+1, line)
		}
		options := strings.Split(match[3], ",")
		fstype := strings.TrimSpace(options[0])
		mode := "rw"
		for _, option := range options[1:] {
			if strings.TrimSpace(option) == "read-only" {
				mode = "ro"
			}
		}
		out = append(out, mountEntry{device: match[1], mountpoint: match[2], fstype: fstype, mode: mode})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no mounts", file)
	}
	return out, nil
}

func parseDF(data []byte, file string) (map[string]dfEntry, error) {
	out := make(map[string]dfEntry)
	for i, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "Filesystem" {
			continue
		}
		if len(fields) < 5 {
			return nil, fmt.Errorf("%s: line %d: %q", file, i+1, line)
		}
		blocks, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: 1024-blocks %q: %w", file, i+1, fields[1], err)
		}
		used, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: used %q: %w", file, i+1, fields[2], err)
		}
		available, err := strconv.ParseFloat(fields[3], 64)
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: available %q: %w", file, i+1, fields[3], err)
		}
		mountpoint := fields[len(fields)-1]
		if len(fields) >= 9 {
			mountpoint = strings.Join(fields[8:], " ")
		}
		out[mountpoint] = dfEntry{blocks: blocks, used: used, available: available}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no rows", file)
	}
	return out, nil
}

func darwinFilesystemType(fstype string) (string, bool) {
	switch fstype {
	case "hfs":
		return schema.SystemFilesystemTypeHfsplus, true
	case "exfat":
		return schema.SystemFilesystemTypeExfat, true
	case "msdos":
		return schema.SystemFilesystemTypeFat32, true
	case "ntfs":
		return schema.SystemFilesystemTypeNtfs, true
	default:
		return "", false
	}
}

func parseProcesses(data []byte, file string) (processStat, error) {
	var stat processStat
	lines := 0
	for _, line := range strings.Split(string(data), "\n") {
		state := strings.TrimSpace(line)
		if state == "" {
			continue
		}
		lines++
		switch state[0] {
		case 'R':
			stat.running++
		case 'S', 'D', 'I':
			stat.sleeping++
		case 'T':
			stat.stopped++
		case 'Z':
			stat.defunct++
		}
	}
	if lines == 0 {
		return stat, fmt.Errorf("%s: no processes", file)
	}
	return stat, nil
}

func parseSensors(data []byte, file string) (sensorsStat, error) {
	var doc struct {
		Temp struct {
			CPUTempAvg *float64 `json:"cpu_temp_avg"`
			GPUTempAvg *float64 `json:"gpu_temp_avg"`
		} `json:"temp"`
		Fans []struct {
			Name   string   `json:"name"`
			RPM    *float64 `json:"rpm"`
			MaxRPM *float64 `json:"max_rpm"`
		} `json:"fans"`
		CPUPower *float64 `json:"cpu_power"`
		GPUPower *float64 `json:"gpu_power"`
		SysPower *float64 `json:"sys_power"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return sensorsStat{}, fmt.Errorf("%s: %w", file, err)
	}
	stat := sensorsStat{
		cpuTemp:  doc.Temp.CPUTempAvg,
		gpuTemp:  doc.Temp.GPUTempAvg,
		cpuPower: doc.CPUPower,
		gpuPower: doc.GPUPower,
		sysPower: doc.SysPower,
	}
	for _, fan := range doc.Fans {
		if fan.Name == "" {
			continue
		}
		stat.fans = append(stat.fans, fanStat{name: fan.Name, rpm: fan.RPM, maxRPM: fan.MaxRPM})
	}
	return stat, nil
}

type diskSource struct{ set *snapshotSet }

func (s *diskSource) Name() string { return "darwin-disk" }

func (s *diskSource) Interval() time.Duration { return diskInterval }

func (s *diskSource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, legacy, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.disk != nil {
		if legacy && errors.Is(errs.disk, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, errs.disk
	}
	return diskPoints(snaps[len(snaps)-1].disk), nil
}

func diskPoints(disks []diskStat) []schema.Point {
	var points []schema.Point
	for _, disk := range disks {
		device := schema.Attr{Key: schema.AttrSystemDeviceName, Value: disk.name}
		points = append(points,
			schema.NewPoint(schema.SystemDiskIO, disk.readBytes, device, directionAttr(schema.DiskIODirectionRead)),
			schema.NewPoint(schema.SystemDiskIO, disk.writeByte, device, directionAttr(schema.DiskIODirectionWrite)),
			schema.NewPoint(schema.SystemDiskOperations, disk.readOps, device, directionAttr(schema.DiskIODirectionRead)),
			schema.NewPoint(schema.SystemDiskOperations, disk.writeOps, device, directionAttr(schema.DiskIODirectionWrite)),
			schema.NewPoint(schema.SystemDiskOperationTime, disk.readTime/1e9, device, directionAttr(schema.DiskIODirectionRead)),
			schema.NewPoint(schema.SystemDiskOperationTime, disk.writeTime/1e9, device, directionAttr(schema.DiskIODirectionWrite)),
		)
	}
	return points
}

type networkSource struct{ set *snapshotSet }

func (s *networkSource) Name() string { return "darwin-network" }

func (s *networkSource) Interval() time.Duration { return networkInterval }

func (s *networkSource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, legacy, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.network != nil {
		if legacy && errors.Is(errs.network, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, errs.network
	}
	return networkPoints(snaps[len(snaps)-1].network), nil
}

func networkPoints(stats []networkStat) []schema.Point {
	var points []schema.Point
	for _, net := range stats {
		iface := schema.Attr{Key: schema.AttrNetworkInterfaceName, Value: net.name}
		device := schema.Attr{Key: schema.AttrSystemDeviceName, Value: net.name}
		receive := schema.Attr{Key: schema.AttrNetworkIODirection, Value: schema.NetworkIODirectionReceive}
		transmit := schema.Attr{Key: schema.AttrNetworkIODirection, Value: schema.NetworkIODirectionTransmit}
		points = append(points,
			schema.NewPoint(schema.SystemNetworkIO, net.rxBytes, iface, receive),
			schema.NewPoint(schema.SystemNetworkIO, net.txBytes, iface, transmit),
			schema.NewPoint(schema.SystemNetworkPacketCount, net.rxPackets, device, receive),
			schema.NewPoint(schema.SystemNetworkPacketCount, net.txPackets, device, transmit),
			schema.NewPoint(schema.SystemNetworkErrors, net.rxErrors, iface, receive),
			schema.NewPoint(schema.SystemNetworkErrors, net.txErrors, iface, transmit),
			schema.NewPoint(schema.SystemNetworkPacketDropped, net.txDropped, iface, transmit),
		)
	}
	return points
}

type filesystemSource struct{ set *snapshotSet }

func (s *filesystemSource) Name() string { return "darwin-filesystem" }

func (s *filesystemSource) Interval() time.Duration { return filesystemInterval }

func (s *filesystemSource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, legacy, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.filesystem != nil {
		if legacy && errors.Is(errs.filesystem, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, errs.filesystem
	}
	return filesystemPoints(snaps[len(snaps)-1].filesystems), nil
}

func filesystemPoints(stats []filesystemStat) []schema.Point {
	var points []schema.Point
	for _, fsStat := range stats {
		base := []schema.Attr{
			{Key: schema.AttrSystemDeviceName, Value: fsStat.device},
			{Key: schema.AttrSystemFilesystemMode, Value: fsStat.mode},
			{Key: schema.AttrSystemFilesystemMountpoint, Value: fsStat.mountpoint},
		}
		if fsStat.hasType {
			base = append(base, schema.Attr{Key: schema.AttrSystemFilesystemType, Value: fsStat.fstype})
		}
		for _, state := range []struct {
			name  string
			value float64
		}{
			{schema.SystemFilesystemStateUsed, fsStat.used},
			{schema.SystemFilesystemStateFree, fsStat.free},
			{schema.SystemFilesystemStateReserved, fsStat.reserved},
		} {
			attrs := append(append([]schema.Attr(nil), base...), schema.Attr{Key: schema.AttrSystemFilesystemState, Value: state.name})
			points = append(points,
				schema.NewPoint(schema.SystemFilesystemUsage, state.value, attrs...),
				schema.NewPoint(schema.SystemFilesystemUtilization, state.value/fsStat.total, attrs...),
			)
		}
	}
	return points
}

type processSource struct{ set *snapshotSet }

func (s *processSource) Name() string { return "darwin-process" }

func (s *processSource) Interval() time.Duration { return processInterval }

func (s *processSource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, legacy, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.process != nil {
		if legacy && errors.Is(errs.process, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, errs.process
	}
	return processPoints(snaps[len(snaps)-1].process), nil
}

func processPoints(last processStat) []schema.Point {
	points := []schema.Point{
		schema.NewPoint(schema.SystemProcessCount, float64(last.running), stateAttr(schema.ProcessStateRunning)),
		schema.NewPoint(schema.SystemProcessCount, float64(last.sleeping), stateAttr(schema.ProcessStateSleeping)),
		schema.NewPoint(schema.SystemProcessCount, float64(last.stopped), stateAttr(schema.ProcessStateStopped)),
		schema.NewPoint(schema.SystemProcessCount, float64(last.defunct), stateAttr(schema.ProcessStateDefunct)),
	}
	if last.hasLimit {
		points = append(points, schema.NewPoint(schema.SystemProcessLimit, last.limit))
	}
	return points
}

type sensorsSource struct{ set *snapshotSet }

func (s *sensorsSource) Name() string { return "darwin-sensors" }

func (s *sensorsSource) Interval() time.Duration { return sensorsInterval }

func (s *sensorsSource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, _, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.sensors != nil {
		return nil, errs.sensors
	}
	return sensorsPoints(snaps[len(snaps)-1].sensors), nil
}

func sensorsPoints(sensors sensorsStat) []schema.Point {
	var points []schema.Point
	if sensors.cpuTemp != nil {
		points = append(points, schema.NewPoint(schema.HWTemperature, *sensors.cpuTemp, hwAttrs("cpu", "cpu", "cpu")...))
	}
	if sensors.gpuTemp != nil {
		points = append(points, schema.NewPoint(schema.HWTemperature, *sensors.gpuTemp, hwAttrs("gpu", "gpu", "gpu")...))
	}
	for _, fan := range sensors.fans {
		attrs := hwAttrs(fan.name, fan.name, "chassis")
		if fan.rpm != nil {
			points = append(points, schema.NewPoint(schema.HWFanSpeed, *fan.rpm, attrs...))
		}
		if fan.maxRPM != nil {
			limitAttrs := append(append([]schema.Attr(nil), attrs...), schema.Attr{Key: schema.AttrHWLimitType, Value: schema.HWLimitTypeMax})
			points = append(points, schema.NewPoint(schema.HWFanSpeedLimit, *fan.maxRPM, limitAttrs...))
		}
		if fan.rpm != nil && fan.maxRPM != nil && *fan.maxRPM > 0 {
			points = append(points, schema.NewPoint(schema.HWFanSpeedRatio, *fan.rpm / *fan.maxRPM, attrs...))
		}
	}
	if sensors.cpuPower != nil {
		points = append(points, schema.NewPoint(schema.HWPower, *sensors.cpuPower, powerAttrs("cpu", schema.HWTypeCPU)...))
	}
	if sensors.gpuPower != nil {
		points = append(points, schema.NewPoint(schema.HWPower, *sensors.gpuPower, powerAttrs("gpu", schema.HWTypeGpu)...))
	}
	if sensors.sysPower != nil {
		points = append(points, schema.NewPoint(schema.HWPower, *sensors.sysPower, powerAttrs("system", schema.HWTypePowerSupply)...))
	}
	return points
}

func directionAttr(value string) schema.Attr {
	return schema.Attr{Key: schema.AttrDiskIODirection, Value: value}
}

func stateAttr(value string) schema.Attr {
	return schema.Attr{Key: schema.AttrProcessState, Value: value}
}

func hwAttrs(id, name, location string) []schema.Attr {
	return []schema.Attr{
		{Key: schema.AttrHWID, Value: id},
		{Key: schema.AttrHWName, Value: name},
		{Key: schema.AttrHWSensorLocation, Value: location},
	}
}

func powerAttrs(id, kind string) []schema.Attr {
	return []schema.Attr{
		{Key: schema.AttrHWID, Value: id},
		{Key: schema.AttrHWName, Value: id},
		{Key: schema.AttrHWType, Value: kind},
	}
}

var _ source.Source = (*diskSource)(nil)
var _ source.Source = (*networkSource)(nil)
var _ source.Source = (*filesystemSource)(nil)
var _ source.Source = (*processSource)(nil)
var _ source.Source = (*sensorsSource)(nil)
