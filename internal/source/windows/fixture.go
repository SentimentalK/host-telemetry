package windows

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
)

type snapshot struct {
	times    *cpuTimes
	memory   *memoryCounters
	tickMS   float64
	disk     []pdhRaw
	network  []pdhRaw
	system   []pdhRaw
	volumes  []volumeInfo
	cores    float64
	logical  float64
	memoryKB float64
	maxProcs float64
}

type fixtureErrors struct {
	times     error
	memory    error
	tick      error
	disk      error
	network   error
	system    error
	volumes   error
	processor error
	os        error
}

type fixtureSet struct {
	samples fs.FS
	once    sync.Once
	snaps   []snapshot
	errs    fixtureErrors
	err     error
}

func newFixtureSet(samples fs.FS) *fixtureSet {
	return &fixtureSet{samples: samples}
}

func (s *fixtureSet) load() ([]snapshot, *fixtureErrors, error) {
	s.once.Do(s.parse)
	return s.snaps, &s.errs, s.err
}

func (s *fixtureSet) parse() {
	entries, err := fs.ReadDir(s.samples, ".")
	if err != nil {
		s.err = fmt.Errorf("read fixture root: %w", err)
		return
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "real-synthetic-") {
			dirs = append(dirs, entry.Name())
		}
	}
	if len(dirs) == 0 {
		s.err = fmt.Errorf("read fixture root: no real-synthetic-* directories")
		return
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		snap, errs := parseSnapshot(s.samples, dir)
		s.snaps = append(s.snaps, snap)
		firstError(&s.errs.times, errs.times)
		firstError(&s.errs.memory, errs.memory)
		firstError(&s.errs.tick, errs.tick)
		firstError(&s.errs.disk, errs.disk)
		firstError(&s.errs.network, errs.network)
		firstError(&s.errs.system, errs.system)
		firstError(&s.errs.volumes, errs.volumes)
		firstError(&s.errs.processor, errs.processor)
		firstError(&s.errs.os, errs.os)
	}
}

func firstError(dst *error, err error) {
	if *dst == nil && err != nil {
		*dst = err
	}
}

func parseSnapshot(samples fs.FS, dir string) (snapshot, fixtureErrors) {
	var snap snapshot
	var errs fixtureErrors

	if data, readErr := readFile(samples, dir, "system-times.txt"); readErr != nil {
		errs.times = readErr
	} else {
		snap.times, errs.times = parseSystemTimes(data, dir+"/system-times.txt")
	}

	if data, readErr := readFile(samples, dir, "memory-status.txt"); readErr != nil {
		errs.memory = readErr
	} else {
		snap.memory, errs.memory = parseMemoryStatus(data, dir+"/memory-status.txt")
	}

	if data, readErr := readFile(samples, dir, "tick-count.txt"); readErr != nil {
		errs.tick = readErr
	} else {
		snap.tickMS, errs.tick = parseTickCount(data, dir+"/tick-count.txt")
	}

	if data, readErr := readFile(samples, dir, "pdh-disk.txt"); readErr != nil {
		errs.disk = readErr
	} else {
		snap.disk, errs.disk = parsePDH(data, dir+"/pdh-disk.txt")
	}

	if data, readErr := readFile(samples, dir, "pdh-network.txt"); readErr != nil {
		errs.network = readErr
	} else {
		snap.network, errs.network = parsePDH(data, dir+"/pdh-network.txt")
	}

	if data, readErr := readFile(samples, dir, "pdh-system.txt"); readErr != nil {
		errs.system = readErr
	} else {
		snap.system, errs.system = parsePDH(data, dir+"/pdh-system.txt")
	}

	if data, readErr := readFile(samples, dir, "volumes.txt"); readErr != nil {
		errs.volumes = readErr
	} else {
		snap.volumes, errs.volumes = parseVolumes(data, dir+"/volumes.txt")
	}

	if data, readErr := readFile(samples, dir, "cim-processor.csv"); readErr != nil {
		errs.processor = readErr
	} else {
		snap.cores, snap.logical, errs.processor = parseProcessorCSV(data, dir+"/cim-processor.csv")
	}

	if data, readErr := readFile(samples, dir, "cim-operating-system.csv"); readErr != nil {
		errs.os = readErr
	} else {
		snap.memoryKB, snap.maxProcs, errs.os = parseOSCSV(data, dir+"/cim-operating-system.csv")
	}

	return snap, errs
}

func readFile(samples fs.FS, dir, name string) ([]byte, error) {
	data, err := fs.ReadFile(samples, dir+"/"+name)
	if err != nil {
		return nil, fmt.Errorf("%s/%s: %w", dir, name, err)
	}
	return data, nil
}

func parseKeyValues(data []byte, file string) (map[string]float64, error) {
	values := make(map[string]float64)
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s: line %d: missing '=': %q", file, i+1, line)
		}
		number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: %q: %w", file, i+1, value, err)
		}
		values[strings.TrimSpace(key)] = number
	}
	return values, nil
}

func requiredValue(values map[string]float64, file, key string) (float64, error) {
	value, ok := values[key]
	if !ok {
		return 0, fmt.Errorf("%s: missing %s", file, key)
	}
	return value, nil
}

func parseSystemTimes(data []byte, file string) (*cpuTimes, error) {
	values, err := parseKeyValues(data, file)
	if err != nil {
		return nil, err
	}
	var times cpuTimes
	for _, field := range []struct {
		key string
		dst *float64
	}{
		{key: "idle", dst: &times.idle},
		{key: "kernel", dst: &times.kernel},
		{key: "user", dst: &times.user},
	} {
		value, err := requiredValue(values, file, field.key)
		if err != nil {
			return nil, err
		}
		*field.dst = value
	}
	return &times, nil
}

func parseMemoryStatus(data []byte, file string) (*memoryCounters, error) {
	values, err := parseKeyValues(data, file)
	if err != nil {
		return nil, err
	}
	var counters memoryCounters
	for _, field := range []struct {
		key string
		dst *float64
	}{
		{key: "total_phys", dst: &counters.totalPhys},
		{key: "avail_phys", dst: &counters.availPhys},
		{key: "total_pagefile", dst: &counters.totalPagefile},
		{key: "avail_pagefile", dst: &counters.availPagefile},
	} {
		value, err := requiredValue(values, file, field.key)
		if err != nil {
			return nil, err
		}
		*field.dst = value
	}
	return &counters, nil
}

func parseTickCount(data []byte, file string) (float64, error) {
	values, err := parseKeyValues(data, file)
	if err != nil {
		return 0, err
	}
	return requiredValue(values, file, "ms")
}

func parsePDH(data []byte, file string) ([]pdhRaw, error) {
	var raws []pdhRaw
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			return nil, fmt.Errorf("%s: line %d: want 4 tab-separated fields, got %d", file, i+1, len(fields))
		}
		counterType, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: counter type %q: %w", file, i+1, fields[1], err)
		}
		first, err := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: first value %q: %w", file, i+1, fields[2], err)
		}
		if _, err := strconv.ParseInt(strings.TrimSpace(fields[3]), 10, 64); err != nil {
			return nil, fmt.Errorf("%s: line %d: second value %q: %w", file, i+1, fields[3], err)
		}
		raws = append(raws, pdhRaw{path: strings.TrimSpace(fields[0]), counterType: uint32(counterType), first: first})
	}
	return raws, nil
}

func parseVolumes(data []byte, file string) ([]volumeInfo, error) {
	var volumes []volumeInfo
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		values := make(map[string]string)
		for _, field := range strings.Split(line, "\t") {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				return nil, fmt.Errorf("%s: line %d: missing '=': %q", file, i+1, field)
			}
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
		for _, key := range []string{"drive", "type", "readonly", "total", "free"} {
			if _, ok := values[key]; !ok {
				return nil, fmt.Errorf("%s: line %d: missing %s", file, i+1, key)
			}
		}
		readonly, err := strconv.ParseBool(values["readonly"])
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: readonly %q: %w", file, i+1, values["readonly"], err)
		}
		total, err := strconv.ParseFloat(values["total"], 64)
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: total %q: %w", file, i+1, values["total"], err)
		}
		free, err := strconv.ParseFloat(values["free"], 64)
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: free %q: %w", file, i+1, values["free"], err)
		}
		volumes = append(volumes, volumeInfo{drive: values["drive"], fsType: values["type"], readonly: readonly, total: total, free: free})
	}
	return volumes, nil
}

func readCSV(data []byte, file string) ([][]string, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	var out [][]string
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		out = append(out, record)
	}
	return out, nil
}

func columnIndex(header []string, column, file string) (int, error) {
	for i, name := range header {
		if strings.TrimSpace(name) == column {
			return i, nil
		}
	}
	return 0, fmt.Errorf("%s: missing %s column", file, column)
}

func parseCell(row []string, index int, column, file string) (float64, error) {
	if index >= len(row) {
		return 0, fmt.Errorf("%s: row has %d fields, want %s", file, len(row), column)
	}
	value := strings.TrimSpace(row[index])
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %s %q: %w", file, column, value, err)
	}
	return number, nil
}

func parseProcessorCSV(data []byte, file string) (float64, float64, error) {
	records, err := readCSV(data, file)
	if err != nil {
		return 0, 0, err
	}
	if len(records) < 2 {
		return 0, 0, fmt.Errorf("%s: want a header and at least one row, got %d rows", file, len(records))
	}
	coresIndex, err := columnIndex(records[0], "NumberOfCores", file)
	if err != nil {
		return 0, 0, err
	}
	logicalIndex, err := columnIndex(records[0], "NumberOfLogicalProcessors", file)
	if err != nil {
		return 0, 0, err
	}
	row := records[len(records)-1]
	cores, err := parseCell(row, coresIndex, "NumberOfCores", file)
	if err != nil {
		return 0, 0, err
	}
	logical, err := parseCell(row, logicalIndex, "NumberOfLogicalProcessors", file)
	if err != nil {
		return 0, 0, err
	}
	return cores, logical, nil
}

func parseOSCSV(data []byte, file string) (float64, float64, error) {
	records, err := readCSV(data, file)
	if err != nil {
		return 0, 0, err
	}
	if len(records) < 2 {
		return 0, 0, fmt.Errorf("%s: want a header and at least one row, got %d rows", file, len(records))
	}
	memoryIndex, err := columnIndex(records[0], "TotalVisibleMemorySize", file)
	if err != nil {
		return 0, 0, err
	}
	processIndex, err := columnIndex(records[0], "MaxNumberOfProcesses", file)
	if err != nil {
		return 0, 0, err
	}
	row := records[len(records)-1]
	memory, err := parseCell(row, memoryIndex, "TotalVisibleMemorySize", file)
	if err != nil {
		return 0, 0, err
	}
	processes, err := parseCell(row, processIndex, "MaxNumberOfProcesses", file)
	if err != nil {
		return 0, 0, err
	}
	return memory, processes, nil
}

type cpuSource struct{ set *fixtureSet }

func (s *cpuSource) Name() string { return "windows-cpu" }

func (s *cpuSource) Interval() time.Duration { return cpuInterval }

func (s *cpuSource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.times != nil {
		return nil, errs.times
	}
	last := snaps[len(snaps)-1]
	var previous *cpuTimes
	if len(snaps) > 1 {
		previous = snaps[0].times
	}
	return cpuPoints(previous, *last.times), nil
}

type memorySource struct{ set *fixtureSet }

func (s *memorySource) Name() string { return "windows-memory" }

func (s *memorySource) Interval() time.Duration { return memoryInterval }

func (s *memorySource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.memory != nil {
		return nil, errs.memory
	}
	return memoryPoints(*snaps[len(snaps)-1].memory), nil
}

type uptimeSource struct{ set *fixtureSet }

func (s *uptimeSource) Name() string { return "windows-uptime" }

func (s *uptimeSource) Interval() time.Duration { return uptimeInterval }

func (s *uptimeSource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.tick != nil {
		return nil, errs.tick
	}
	return uptimePoints(snaps[len(snaps)-1].tickMS), nil
}

type diskSource struct{ set *fixtureSet }

func (s *diskSource) Name() string { return "windows-disk" }

func (s *diskSource) Interval() time.Duration { return diskInterval }

func (s *diskSource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.disk != nil {
		return nil, errs.disk
	}
	return pdhPoints(snaps[len(snaps)-1].disk, "PhysicalDisk")
}

type networkSource struct{ set *fixtureSet }

func (s *networkSource) Name() string { return "windows-network" }

func (s *networkSource) Interval() time.Duration { return networkInterval }

func (s *networkSource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.network != nil {
		return nil, errs.network
	}
	return pdhPoints(snaps[len(snaps)-1].network, "Network Interface")
}

type filesystemSource struct{ set *fixtureSet }

func (s *filesystemSource) Name() string { return "windows-filesystem" }

func (s *filesystemSource) Interval() time.Duration { return filesystemInterval }

func (s *filesystemSource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.volumes != nil {
		return nil, errs.volumes
	}
	return filesystemPoints(snaps[len(snaps)-1].volumes), nil
}

type processSource struct{ set *fixtureSet }

func (s *processSource) Name() string { return "windows-process" }

func (s *processSource) Interval() time.Duration { return processInterval }

func (s *processSource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.system != nil {
		return nil, errs.system
	}
	last := snaps[len(snaps)-1]
	points, pdhErr := pdhPoints(last.system, "System")
	if errs.os != nil {
		return points, errors.Join(pdhErr, errs.os)
	}
	return append(points, schema.NewPoint(schema.SystemProcessLimit, last.maxProcs)), pdhErr
}

type inventorySource struct{ set *fixtureSet }

func (s *inventorySource) Name() string { return "windows-inventory" }

func (s *inventorySource) Interval() time.Duration { return inventoryInterval }

func (s *inventorySource) Collect(ctx context.Context) ([]schema.Point, error) {
	snaps, errs, err := s.set.load()
	if err != nil {
		return nil, err
	}
	if errs.processor != nil {
		return nil, errs.processor
	}
	if errs.os != nil {
		return nil, errs.os
	}
	last := snaps[len(snaps)-1]
	return inventoryPoints(cimInfo{cores: last.cores, logical: last.logical, totalVisibleMemory: last.memoryKB}), nil
}
