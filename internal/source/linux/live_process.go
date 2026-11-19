package linux

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

const processWorkers = 32

type processSource struct {
	root      fs.FS
	whitelist []string
	topN      int
}

func (s *processSource) Name() string { return "linux-process" }

func (s *processSource) Interval() time.Duration { return interval }

func (s *processSource) Collect(ctx context.Context) ([]schema.Point, error) {
	entries, err := fs.ReadDir(s.root, "proc")
	if err != nil {
		return nil, fmt.Errorf("proc: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		names = append(names, entry.Name())
	}
	states := make([]string, len(names))
	workers := processWorkers
	if len(names) < workers {
		workers = len(names)
	}
	var next atomic.Int64
	var readers sync.WaitGroup
	for range workers {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				index := int(next.Add(1)) - 1
				if index >= len(names) {
					return
				}
				states[index], _ = s.readState(names[index])
			}
		}()
	}
	readers.Wait()

	counts := make(map[string]int, 4)
	pids := make([]string, 0, len(names))
	for i, state := range states {
		if state == "" {
			continue
		}
		counts[state]++
		pids = append(pids, names[i])
	}

	var points []schema.Point
	for _, state := range []string{
		schema.ProcessStateRunning,
		schema.ProcessStateSleeping,
		schema.ProcessStateStopped,
		schema.ProcessStateDefunct,
	} {
		points = append(points, schema.NewPoint(schema.SystemProcessCount, float64(counts[state]), schema.Attr{Key: schema.AttrProcessState, Value: state}))
	}
	if limit, ok := s.readLimit(); ok {
		points = append(points, schema.NewPoint(schema.SystemProcessLimit, limit))
	}
	if len(s.whitelist) > 0 {
		points = append(points, s.memoryPoints(pids)...)
	}
	return points, nil
}

func (s *processSource) readState(pid string) (string, bool) {
	data, err := fs.ReadFile(s.root, "proc/"+pid+"/stat")
	if err != nil {
		return "", false
	}
	text := string(data)
	end := strings.LastIndex(text, ")")
	if end < 0 {
		return "", false
	}
	fields := strings.Fields(text[end+1:])
	if len(fields) == 0 {
		return "", false
	}
	switch fields[0] {
	case "R":
		return schema.ProcessStateRunning, true
	case "S", "D", "I":
		return schema.ProcessStateSleeping, true
	case "T", "t":
		return schema.ProcessStateStopped, true
	case "Z":
		return schema.ProcessStateDefunct, true
	default:
		return "", false
	}
}

func (s *processSource) readLimit() (float64, bool) {
	data, err := fs.ReadFile(s.root, "proc/self/limits")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] != "Max" || fields[1] != "processes" {
			continue
		}
		if fields[2] == "unlimited" {
			return 0, false
		}
		value, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return 0, false
		}
		return value, true
	}
	return 0, false
}

func (s *processSource) memoryPoints(pids []string) []schema.Point {
	type resident struct {
		pid   string
		bytes float64
	}
	sizes := make([]resident, 0, len(pids))
	for _, pid := range pids {
		data, err := fs.ReadFile(s.root, "proc/"+pid+"/statm")
		if err != nil {
			continue
		}
		fields := strings.Fields(string(data))
		if len(fields) < 2 {
			continue
		}
		pages, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		sizes = append(sizes, resident{pid: pid, bytes: pages * 4096})
	}
	sort.Slice(sizes, func(i, j int) bool {
		if sizes[i].bytes != sizes[j].bytes {
			return sizes[i].bytes > sizes[j].bytes
		}
		return sizes[i].pid < sizes[j].pid
	})
	if len(sizes) > s.topN {
		sizes = sizes[:s.topN]
	}
	points := make([]schema.Point, 0, len(sizes))
	for _, size := range sizes {
		points = append(points, schema.NewPoint(schema.ProcessMemoryUsage, size.bytes))
	}
	return points
}

var _ source.Source = (*processSource)(nil)
