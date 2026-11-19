package windows

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

type Options struct{}

type pdhQuery interface {
	collect() ([]pdhRaw, error)
	close()
}

var (
	diskCounters = []string{
		`\PhysicalDisk(*)\Disk Read Bytes/sec`,
		`\PhysicalDisk(*)\Disk Write Bytes/sec`,
		`\PhysicalDisk(*)\Disk Reads/sec`,
		`\PhysicalDisk(*)\Disk Writes/sec`,
	}
	networkCounters = []string{
		`\Network Interface(*)\Bytes Received/sec`,
		`\Network Interface(*)\Bytes Sent/sec`,
		`\Network Interface(*)\Packets Received Errors`,
		`\Network Interface(*)\Packets Outbound Errors`,
		`\Network Interface(*)\Packets Received/sec`,
		`\Network Interface(*)\Packets Sent/sec`,
		`\Network Interface(*)\Packets Received Discarded`,
		`\Network Interface(*)\Packets Outbound Discarded`,
	}
	systemCounters = []string{`\System\Processes`}
)

func NewLive(opts Options) []source.Source {
	cim := &cimCache{}
	return []source.Source{
		&liveCPUSource{},
		&liveMemorySource{},
		&liveUptimeSource{},
		&liveDiskSource{query: &pdhSource{class: "PhysicalDisk", templates: diskCounters}},
		&liveNetworkSource{query: &pdhSource{class: "Network Interface", templates: networkCounters}},
		&liveFilesystemSource{},
		&liveProcessSource{query: &pdhSource{class: "System", templates: systemCounters}, cim: cim},
		&liveInventorySource{cim: cim},
	}
}

type liveCPUSource struct {
	mu       sync.Mutex
	previous *cpuTimes
}

func (s *liveCPUSource) Name() string { return "windows-cpu" }

func (s *liveCPUSource) Interval() time.Duration { return cpuInterval }

func (s *liveCPUSource) Collect(ctx context.Context) ([]schema.Point, error) {
	current, err := systemTimes()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	points := cpuPoints(s.previous, current)
	s.previous = &current
	return points, nil
}

type liveMemorySource struct{}

func (s *liveMemorySource) Name() string { return "windows-memory" }

func (s *liveMemorySource) Interval() time.Duration { return memoryInterval }

func (s *liveMemorySource) Collect(ctx context.Context) ([]schema.Point, error) {
	counters, err := memoryStatus()
	if err != nil {
		return nil, err
	}
	return memoryPoints(counters), nil
}

type liveUptimeSource struct{}

func (s *liveUptimeSource) Name() string { return "windows-uptime" }

func (s *liveUptimeSource) Interval() time.Duration { return uptimeInterval }

func (s *liveUptimeSource) Collect(ctx context.Context) ([]schema.Point, error) {
	milliseconds, err := tickCountMilliseconds()
	if err != nil {
		return nil, err
	}
	return uptimePoints(milliseconds), nil
}

type pdhSource struct {
	class     string
	templates []string

	mu    sync.Mutex
	query pdhQuery
}

func (s *pdhSource) collect() ([]schema.Point, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.query == nil {
		query, err := pdhOpen(s.templates)
		if err != nil {
			return nil, err
		}
		s.query = query
	}
	raws, err := s.query.collect()
	if err != nil {
		s.query.close()
		s.query = nil
		return nil, err
	}
	return pdhPoints(raws, s.class)
}

type liveDiskSource struct{ query *pdhSource }

func (s *liveDiskSource) Name() string { return "windows-disk" }

func (s *liveDiskSource) Interval() time.Duration { return diskInterval }

func (s *liveDiskSource) Collect(ctx context.Context) ([]schema.Point, error) {
	return s.query.collect()
}

type liveNetworkSource struct{ query *pdhSource }

func (s *liveNetworkSource) Name() string { return "windows-network" }

func (s *liveNetworkSource) Interval() time.Duration { return networkInterval }

func (s *liveNetworkSource) Collect(ctx context.Context) ([]schema.Point, error) {
	return s.query.collect()
}

type liveFilesystemSource struct{}

func (s *liveFilesystemSource) Name() string { return "windows-filesystem" }

func (s *liveFilesystemSource) Interval() time.Duration { return filesystemInterval }

func (s *liveFilesystemSource) Collect(ctx context.Context) ([]schema.Point, error) {
	volumes, err := listVolumes()
	if err != nil {
		return nil, err
	}
	return filesystemPoints(volumes), nil
}

type liveProcessSource struct {
	query *pdhSource
	cim   *cimCache
}

func (s *liveProcessSource) Name() string { return "windows-process" }

func (s *liveProcessSource) Interval() time.Duration { return processInterval }

func (s *liveProcessSource) Collect(ctx context.Context) ([]schema.Point, error) {
	points, pdhErr := s.query.collect()
	info, cimErr := s.cim.get(ctx)
	if cimErr != nil {
		return points, errors.Join(pdhErr, cimErr)
	}
	return append(points, schema.NewPoint(schema.SystemProcessLimit, info.maxProcesses)), pdhErr
}

type liveInventorySource struct{ cim *cimCache }

func (s *liveInventorySource) Name() string { return "windows-inventory" }

func (s *liveInventorySource) Interval() time.Duration { return inventoryInterval }

func (s *liveInventorySource) Collect(ctx context.Context) ([]schema.Point, error) {
	info, err := s.cim.get(ctx)
	if err != nil {
		return nil, err
	}
	return inventoryPoints(info), nil
}

type cimCache struct {
	mu   sync.Mutex
	at   time.Time
	info cimInfo
}

func (c *cimCache) get(ctx context.Context) (cimInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < inventoryInterval {
		return c.info, nil
	}
	info, err := cimInventory(ctx)
	if err != nil {
		return cimInfo{}, err
	}
	c.at = time.Now()
	c.info = info
	return info, nil
}
