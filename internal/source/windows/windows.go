package windows

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

const interval = 15 * time.Second

type column struct {
	match   string
	name    string
	attr    schema.Attr
	percent bool
}

var columns = []column{
	{"% user time", schema.SystemCPUUtilization, schema.Attr{Key: schema.AttrCPUMode, Value: schema.CPUModeUser}, true},
	{"% privileged time", schema.SystemCPUUtilization, schema.Attr{Key: schema.AttrCPUMode, Value: schema.CPUModeSystem}, true},
	{"% idle time", schema.SystemCPUUtilization, schema.Attr{Key: schema.AttrCPUMode, Value: schema.CPUModeIdle}, true},
	{`\memory\available bytes`, schema.SystemMemoryUsage, schema.Attr{Key: schema.AttrSystemMemoryState, Value: schema.SystemMemoryStateAvailable}, false},
	{`\memory\committed bytes`, schema.SystemMemoryUsage, schema.Attr{Key: schema.AttrSystemMemoryState, Value: schema.SystemMemoryStateUsed}, false},
}

type windowsSource struct {
	samples fs.FS
	once    sync.Once
	points  []schema.Point
	err     error
}

func New(samples fs.FS) source.Source {
	return &windowsSource{samples: samples}
}

func (s *windowsSource) Name() string { return "windows-pdh" }

func (s *windowsSource) Interval() time.Duration { return interval }

func (s *windowsSource) Collect(ctx context.Context) ([]schema.Point, error) {
	s.once.Do(func() {
		s.points, s.err = s.load()
	})
	if s.err != nil {
		return nil, s.err
	}
	return s.points, nil
}

func (s *windowsSource) load() ([]schema.Point, error) {
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
	return s.readSample(dirs[len(dirs)-1])
}

func (s *windowsSource) readSample(dir string) ([]schema.Point, error) {
	typeperf, err := readFile(s.samples, dir, "typeperf.csv")
	if err != nil {
		return nil, err
	}
	counters, err := parseTypeperf(typeperf, dir+"/typeperf.csv")
	if err != nil {
		return nil, err
	}
	wmic, err := readFile(s.samples, dir, "wmic-cpu.csv")
	if err != nil {
		return nil, err
	}
	cores, logical, err := parseWMIC(wmic, dir+"/wmic-cpu.csv")
	if err != nil {
		return nil, err
	}
	points := []schema.Point{
		schema.NewPoint(schema.SystemCPUPhysicalCount, cores),
		schema.NewPoint(schema.SystemCPULogicalCount, logical),
	}
	for _, counter := range counters {
		points = append(points, schema.NewPoint(counter.name, counter.value, counter.attr))
	}
	return points, nil
}

func readFile(samples fs.FS, dir, name string) ([]byte, error) {
	data, err := fs.ReadFile(samples, dir+"/"+name)
	if err != nil {
		return nil, fmt.Errorf("%s/%s: %w", dir, name, err)
	}
	return data, nil
}

type counter struct {
	name  string
	value float64
	attr  schema.Attr
}

func parseTypeperf(data []byte, file string) ([]counter, error) {
	records, err := readCSV(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("%s: want a header and at least one sample row, got %d rows", file, len(records))
	}
	header := records[0]
	last := records[len(records)-1]
	if len(last) != len(header) {
		return nil, fmt.Errorf("%s: sample row has %d fields, header has %d", file, len(last), len(header))
	}
	var out []counter
	found := make([]bool, len(columns))
	for i, name := range header[1:] {
		lower := strings.ToLower(name)
		for j := range columns {
			if !strings.Contains(lower, columns[j].match) {
				continue
			}
			value, err := strconv.ParseFloat(last[i+1], 64)
			if err != nil {
				return nil, fmt.Errorf("%s: column %q: value %q: %w", file, name, last[i+1], err)
			}
			if columns[j].percent {
				value /= 100
			}
			out = append(out, counter{name: columns[j].name, value: value, attr: columns[j].attr})
			found[j] = true
		}
	}
	for j := range columns {
		if !found[j] {
			return nil, fmt.Errorf("%s: missing column matching %q", file, columns[j].match)
		}
	}
	return out, nil
}

func parseWMIC(data []byte, file string) (float64, float64, error) {
	records, err := readCSV(bytes.NewReader(data))
	if err != nil {
		return 0, 0, fmt.Errorf("%s: %w", file, err)
	}
	if len(records) < 2 {
		return 0, 0, fmt.Errorf("%s: want a header and at least one sample row, got %d rows", file, len(records))
	}
	header := records[0]
	coresIndex, logicalIndex := -1, -1
	for i, name := range header {
		switch strings.TrimSpace(name) {
		case "NumberOfCores":
			coresIndex = i
		case "NumberOfLogicalProcessors":
			logicalIndex = i
		}
	}
	if coresIndex < 0 {
		return 0, 0, fmt.Errorf("%s: missing NumberOfCores column", file)
	}
	if logicalIndex < 0 {
		return 0, 0, fmt.Errorf("%s: missing NumberOfLogicalProcessors column", file)
	}
	last := records[len(records)-1]
	if coresIndex >= len(last) || logicalIndex >= len(last) {
		return 0, 0, fmt.Errorf("%s: sample row has %d fields, want at least %d", file, len(last), max(coresIndex, logicalIndex)+1)
	}
	cores, err := strconv.ParseFloat(strings.TrimSpace(last[coresIndex]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("%s: NumberOfCores %q: %w", file, last[coresIndex], err)
	}
	logical, err := strconv.ParseFloat(strings.TrimSpace(last[logicalIndex]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("%s: NumberOfLogicalProcessors %q: %w", file, last[logicalIndex], err)
	}
	return cores, logical, nil
}

func readCSV(r io.Reader) ([][]string, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
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
