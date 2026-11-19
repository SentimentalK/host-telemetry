package linux

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

type inventorySource struct {
	root fs.FS
}

func (s *inventorySource) Name() string { return "linux-inventory" }

func (s *inventorySource) Interval() time.Duration { return 300 * time.Second }

func (s *inventorySource) Collect(ctx context.Context) ([]schema.Point, error) {
	stat, err := fs.ReadFile(s.root, "proc/stat")
	if err != nil {
		return nil, fmt.Errorf("proc/stat: %w", err)
	}
	logical := 0
	for _, line := range strings.Split(string(stat), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		number := strings.TrimPrefix(fields[0], "cpu")
		if number != "" && strings.Trim(number, "0123456789") == "" {
			logical++
		}
	}
	if logical == 0 {
		return nil, fmt.Errorf("proc/stat: missing per-cpu lines")
	}
	mem, err := readMeminfo(s.root)
	if err != nil {
		return nil, err
	}
	points := []schema.Point{
		schema.NewPoint(schema.SystemCPULogicalCount, float64(logical)),
		schema.NewPoint(schema.SystemMemoryLimit, mem["MemTotal"]),
	}
	if cpuinfo, err := fs.ReadFile(s.root, "proc/cpuinfo"); err == nil {
		if physical := parsePhysicalCount(cpuinfo); physical > 0 {
			points = append(points, schema.NewPoint(schema.SystemCPUPhysicalCount, float64(physical)))
		}
	}
	return points, nil
}

func parsePhysicalCount(data []byte) int {
	pairs := make(map[string]bool)
	physical := ""
	cores := ""
	flush := func() {
		if physical != "" && cores != "" {
			pairs[physical+"/"+cores] = true
		}
		physical = ""
		cores = ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "physical id":
			physical = strings.TrimSpace(value)
		case "cpu cores":
			cores = strings.TrimSpace(value)
		}
	}
	flush()
	return len(pairs)
}

var _ source.Source = (*inventorySource)(nil)
