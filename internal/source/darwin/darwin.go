package darwin

import (
	"context"
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

const interval = 15 * time.Second

var pageSizePattern = regexp.MustCompile(`page size of ([0-9]+) bytes`)

type darwinSource struct {
	samples fs.FS
	once    sync.Once
	points  []schema.Point
	err     error
}

func New(samples fs.FS) source.Source {
	return &darwinSource{samples: samples}
}

func (s *darwinSource) Name() string { return "darwin-sysctl" }

func (s *darwinSource) Interval() time.Duration { return interval }

func (s *darwinSource) Collect(ctx context.Context) ([]schema.Point, error) {
	s.once.Do(func() {
		s.points, s.err = s.load()
	})
	if s.err != nil {
		return nil, s.err
	}
	return s.points, nil
}

func (s *darwinSource) load() ([]schema.Point, error) {
	entries, err := fs.ReadDir(s.samples, ".")
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
	last := dirs[len(dirs)-1]
	return s.readSample(last)
}

func (s *darwinSource) readSample(dir string) ([]schema.Point, error) {
	sysctl, err := readFile(s.samples, dir, "sysctl.txt")
	if err != nil {
		return nil, err
	}
	sys, err := parseSysctl(sysctl, dir+"/sysctl.txt")
	if err != nil {
		return nil, err
	}
	logical, err := parseSysctlInt(sys, "hw.logicalcpu", dir+"/sysctl.txt")
	if err != nil {
		return nil, err
	}
	physical, err := parseSysctlInt(sys, "hw.physicalcpu", dir+"/sysctl.txt")
	if err != nil {
		return nil, err
	}
	swap, ok := sys["vm.swapusage"]
	if !ok {
		return nil, fmt.Errorf("%s/sysctl.txt: missing vm.swapusage", dir)
	}
	swapUsed, swapFree, err := parseSwapusage(swap, dir+"/sysctl.txt")
	if err != nil {
		return nil, err
	}
	vmstat, err := readFile(s.samples, dir, "vm_stat.txt")
	if err != nil {
		return nil, err
	}
	pageSize, pages, err := parseVMStat(vmstat, dir+"/vm_stat.txt")
	if err != nil {
		return nil, err
	}
	pageCount := func(key string) (float64, error) {
		value, ok := pages[key]
		if !ok {
			return 0, fmt.Errorf("%s/vm_stat.txt: missing %q", dir, key)
		}
		return value, nil
	}
	free, err := pageCount("Pages free")
	if err != nil {
		return nil, err
	}
	active, err := pageCount("Pages active")
	if err != nil {
		return nil, err
	}
	inactive, err := pageCount("Pages inactive")
	if err != nil {
		return nil, err
	}
	speculative, err := pageCount("Pages speculative")
	if err != nil {
		return nil, err
	}
	wired, err := pageCount("Pages wired down")
	if err != nil {
		return nil, err
	}
	compressor, err := pageCount("Pages occupied by compressor")
	if err != nil {
		return nil, err
	}
	boot, err := readEpoch(s.samples, dir, "boottime.txt")
	if err != nil {
		return nil, err
	}
	now, err := readEpoch(s.samples, dir, "now.txt")
	if err != nil {
		return nil, err
	}
	if now < boot {
		return nil, fmt.Errorf("%s/now.txt: now %v is before boot %v", dir, now, boot)
	}
	used := (active + wired + compressor) * pageSize
	available := (free + inactive + speculative) * pageSize
	return []schema.Point{
		schema.NewPoint(schema.SystemUptime, float64(now-boot)),
		schema.NewPoint(schema.SystemCPULogicalCount, logical),
		schema.NewPoint(schema.SystemCPUPhysicalCount, physical),
		schema.NewPoint(schema.SystemMemoryUsage, used, schema.Attr{Key: schema.AttrSystemMemoryState, Value: schema.SystemMemoryStateUsed}),
		schema.NewPoint(schema.SystemMemoryUsage, available, schema.Attr{Key: schema.AttrSystemMemoryState, Value: schema.SystemMemoryStateAvailable}),
		schema.NewPoint(schema.SystemPagingUsage, swapUsed, schema.Attr{Key: schema.AttrSystemPagingState, Value: schema.SystemPagingStateUsed}),
		schema.NewPoint(schema.SystemPagingUsage, swapFree, schema.Attr{Key: schema.AttrSystemPagingState, Value: schema.SystemPagingStateFree}),
	}, nil
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
