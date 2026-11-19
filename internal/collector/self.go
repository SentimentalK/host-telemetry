package collector

import (
	"context"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

type WALStats interface {
	Size() int64
	Dropped() uint64
	PendingSamples() int
}

type selfSource struct {
	procfs fs.FS
	stats  *Stats
	wal    WALStats
	start  time.Time
}

func NewSelfSource(procfs fs.FS, stats *Stats, walStats WALStats, start time.Time) source.Source {
	return &selfSource{procfs: procfs, stats: stats, wal: walStats, start: start}
}

func (s *selfSource) Name() string { return "collector-self" }

func (s *selfSource) Interval() time.Duration { return 15 * time.Second }

func (s *selfSource) Collect(context.Context) ([]schema.Point, error) {
	points := make([]schema.Point, 0, 8)
	if cpu, ok := processCPUTime(s.procfs); ok {
		points = append(points, schema.NewPoint(schema.CollectorSelfCPUTime, cpu))
	}
	if memory, ok := processMemory(s.procfs); ok {
		points = append(points, schema.NewPoint(schema.CollectorSelfMemoryUsage, memory))
	}

	var size int64
	var dropped uint64
	var pending int
	if s.wal != nil {
		size = s.wal.Size()
		dropped = s.wal.Dropped()
		pending = s.wal.PendingSamples()
	}
	points = append(points,
		schema.NewPoint(schema.CollectorWALSize, float64(size)),
		schema.NewPoint(schema.CollectorWALDropped, float64(dropped)),
		schema.NewPoint(schema.CollectorSendQueueLength, float64(pending)),
	)

	for name, duration := range s.stats.Durations() {
		points = append(points, schema.NewPoint(schema.CollectorScrapeDuration, duration.Seconds(),
			schema.Attr{Key: schema.AttrCollectorSource, Value: name}))
	}
	return points, nil
}

func processCPUTime(procfs fs.FS) (float64, bool) {
	data, err := fs.ReadFile(procfs, "proc/self/stat")
	if err != nil {
		return 0, false
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return 0, false
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 13 {
		return 0, false
	}
	user, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return 0, false
	}
	system, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return 0, false
	}
	return float64(user+system) / 100, true
}

func processMemory(procfs fs.FS) (float64, bool) {
	data, err := fs.ReadFile(procfs, "proc/self/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, false
	}
	resident, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return float64(resident * 4096), true
}
