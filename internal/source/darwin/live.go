package darwin

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/rate"
	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

type CPUTicks struct {
	User   float64
	System float64
	Idle   float64
}

type Options struct {
	Now      func() time.Time
	Run      func(ctx context.Context, name string, args ...string) ([]byte, error)
	CPUTicks func() (CPUTicks, error)
	Sensors  bool
}

func NewLive(opts Options) []source.Source {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Run == nil {
		opts.Run = execOutput
	}
	if opts.CPUTicks == nil {
		opts.CPUTicks = readCPUTicks
	}
	sources := []source.Source{
		&liveCPUSource{now: opts.Now, run: opts.Run, ticks: opts.CPUTicks},
		&liveMemorySource{run: opts.Run},
		&liveUptimeSource{now: opts.Now, run: opts.Run},
		&liveInventorySource{run: opts.Run},
		&liveDiskSource{run: opts.Run},
		&liveNetworkSource{run: opts.Run},
		&liveFilesystemSource{run: opts.Run},
		&liveProcessSource{run: opts.Run},
	}
	if opts.Sensors {
		sources = append(sources, &liveSensorsSource{run: opts.Run})
	}
	return sources
}

func execOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

type liveCPUSource struct {
	now   func() time.Time
	run   func(ctx context.Context, name string, args ...string) ([]byte, error)
	ticks func() (CPUTicks, error)

	prev     CPUTicks
	prevAt   time.Time
	havePrev bool
	warn     sync.Once
}

func (s *liveCPUSource) Name() string { return "darwin-cpu" }

func (s *liveCPUSource) Interval() time.Duration { return cpuInterval }

func (s *liveCPUSource) Collect(ctx context.Context) ([]schema.Point, error) {
	sysData, err := s.run(ctx, "sysctl", "-a")
	if err != nil {
		return nil, fmt.Errorf("sysctl -a: %w", err)
	}
	sys, err := parseSysctl(sysData, "sysctl -a")
	if err != nil {
		return nil, err
	}
	var clock snapshot
	if err := parseClockrate(&clock, sys, "sysctl -a"); err != nil {
		return nil, err
	}
	logical, err := parseSysctlInt(sys, "hw.logicalcpu", "sysctl -a")
	if err != nil {
		return nil, err
	}
	cur, err := s.ticks()
	if err != nil {
		s.warn.Do(func() { log.Printf("darwin-cpu: %v", err) })
		return nil, nil
	}
	now := s.now()
	points := cpuTimePoints(cur, clock.clockHz)
	if s.havePrev && logical > 0 {
		for _, tick := range []struct {
			mode string
			prev float64
			cur  float64
		}{
			{schema.CPUModeUser, s.prev.User, cur.User},
			{schema.CPUModeSystem, s.prev.System, cur.System},
			{schema.CPUModeIdle, s.prev.Idle, cur.Idle},
		} {
			value, ok := rate.Utilization(tick.prev, tick.cur, s.prevAt, now, clock.clockHz*logical)
			if !ok {
				continue
			}
			points = append(points, schema.NewPoint(schema.SystemCPUUtilization, value, cpuMode(tick.mode)))
		}
	}
	s.prev = cur
	s.prevAt = now
	s.havePrev = true
	return points, nil
}

func cpuTimePoints(ticks CPUTicks, hz float64) []schema.Point {
	return []schema.Point{
		schema.NewPoint(schema.SystemCPUTime, ticks.User/hz, cpuMode(schema.CPUModeUser)),
		schema.NewPoint(schema.SystemCPUTime, ticks.System/hz, cpuMode(schema.CPUModeSystem)),
		schema.NewPoint(schema.SystemCPUTime, ticks.Idle/hz, cpuMode(schema.CPUModeIdle)),
	}
}

type liveMemorySource struct {
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (s *liveMemorySource) Name() string { return "darwin-memory" }

func (s *liveMemorySource) Interval() time.Duration { return memoryInterval }

func (s *liveMemorySource) Collect(ctx context.Context) ([]schema.Point, error) {
	sysData, err := s.run(ctx, "sysctl", "-a")
	if err != nil {
		return nil, fmt.Errorf("sysctl -a: %w", err)
	}
	sys, err := parseSysctl(sysData, "sysctl -a")
	if err != nil {
		return nil, err
	}
	var snap snapshot
	if err := parseSysctlSnapshot(&snap, sys, "sysctl -a"); err != nil {
		return nil, err
	}
	vmData, err := s.run(ctx, "vm_stat")
	if err != nil {
		return nil, fmt.Errorf("vm_stat: %w", err)
	}
	if err := parseVMStatSnapshot(&snap, vmData, "vm_stat"); err != nil {
		return nil, err
	}
	return memoryPoints(snap), nil
}

type liveUptimeSource struct {
	now func() time.Time
	run func(ctx context.Context, name string, args ...string) ([]byte, error)

	mu       sync.Mutex
	boot     int64
	haveBoot bool
}

func (s *liveUptimeSource) Name() string { return "darwin-uptime" }

func (s *liveUptimeSource) Interval() time.Duration { return uptimeInterval }

func (s *liveUptimeSource) Collect(ctx context.Context) ([]schema.Point, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveBoot {
		data, err := s.run(ctx, "sysctl", "-b", "kern.boottime")
		if err != nil {
			return nil, fmt.Errorf("sysctl -b kern.boottime: %w", err)
		}
		if len(data) < 8 {
			return nil, fmt.Errorf("sysctl -b kern.boottime: %d bytes, want at least 8", len(data))
		}
		s.boot = int64(binary.LittleEndian.Uint64(data[:8]))
		s.haveBoot = true
	}
	return []schema.Point{schema.NewPoint(schema.SystemUptime, float64(s.now().Unix()-s.boot))}, nil
}

type liveInventorySource struct {
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (s *liveInventorySource) Name() string { return "darwin-inventory" }

func (s *liveInventorySource) Interval() time.Duration { return inventoryInterval }

func (s *liveInventorySource) Collect(ctx context.Context) ([]schema.Point, error) {
	sysData, err := s.run(ctx, "sysctl", "-a")
	if err != nil {
		return nil, fmt.Errorf("sysctl -a: %w", err)
	}
	sys, err := parseSysctl(sysData, "sysctl -a")
	if err != nil {
		return nil, err
	}
	var snap snapshot
	if err := parseSysctlSnapshot(&snap, sys, "sysctl -a"); err != nil {
		return nil, err
	}
	return inventoryPoints(snap), nil
}

type liveDiskSource struct {
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (s *liveDiskSource) Name() string { return "darwin-disk" }

func (s *liveDiskSource) Interval() time.Duration { return diskInterval }

func (s *liveDiskSource) Collect(ctx context.Context) ([]schema.Point, error) {
	data, err := s.run(ctx, "ioreg", "-c", "IOBlockStorageDriver", "-r", "-l", "-w", "0")
	if err != nil {
		return nil, fmt.Errorf("ioreg: %w", err)
	}
	disks, err := parseIOreg(data, "ioreg")
	if err != nil {
		return nil, err
	}
	return diskPoints(disks), nil
}

type liveNetworkSource struct {
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (s *liveNetworkSource) Name() string { return "darwin-network" }

func (s *liveNetworkSource) Interval() time.Duration { return networkInterval }

func (s *liveNetworkSource) Collect(ctx context.Context) ([]schema.Point, error) {
	data, err := s.run(ctx, "netstat", "-ibdnW")
	if err != nil {
		return nil, fmt.Errorf("netstat -ibdnW: %w", err)
	}
	stats, err := parseNetstat(data, "netstat -ibdnW")
	if err != nil {
		return nil, err
	}
	return networkPoints(stats), nil
}

type liveFilesystemSource struct {
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (s *liveFilesystemSource) Name() string { return "darwin-filesystem" }

func (s *liveFilesystemSource) Interval() time.Duration { return filesystemInterval }

func (s *liveFilesystemSource) Collect(ctx context.Context) ([]schema.Point, error) {
	mountData, err := s.run(ctx, "mount")
	if err != nil {
		return nil, fmt.Errorf("mount: %w", err)
	}
	dfData, err := s.run(ctx, "df", "-kP")
	if err != nil {
		return nil, fmt.Errorf("df -kP: %w", err)
	}
	stats, err := parseFilesystemsFrom(mountData, dfData, "mount", "df -kP")
	if err != nil {
		return nil, err
	}
	return filesystemPoints(stats), nil
}

type liveProcessSource struct {
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (s *liveProcessSource) Name() string { return "darwin-process" }

func (s *liveProcessSource) Interval() time.Duration { return processInterval }

func (s *liveProcessSource) Collect(ctx context.Context) ([]schema.Point, error) {
	data, err := s.run(ctx, "ps", "-axo", "stat=")
	if err != nil {
		return nil, fmt.Errorf("ps -axo stat=: %w", err)
	}
	stat, err := parseProcesses(data, "ps -axo stat=")
	if err != nil {
		return nil, err
	}
	if sysData, runErr := s.run(ctx, "sysctl", "-a"); runErr == nil {
		if sys, parseErr := parseSysctl(sysData, "sysctl -a"); parseErr == nil {
			if limit, limitErr := parseSysctlInt(sys, "kern.maxproc", "sysctl -a"); limitErr == nil && limit > 0 {
				stat.limit = limit
				stat.hasLimit = true
			}
		}
	}
	return processPoints(stat), nil
}

type liveSensorsSource struct {
	run  func(ctx context.Context, name string, args ...string) ([]byte, error)
	warn sync.Once
}

func (s *liveSensorsSource) Name() string { return "darwin-sensors" }

func (s *liveSensorsSource) Interval() time.Duration { return sensorsInterval }

func (s *liveSensorsSource) Collect(ctx context.Context) ([]schema.Point, error) {
	data, err := s.run(ctx, "macmon", "pipe", "-s", "1", "-i", "1000")
	if err != nil {
		s.warn.Do(func() { log.Printf("darwin-sensors: %v", err) })
		return nil, nil
	}
	stat, err := parseSensors(data, "macmon pipe")
	if err != nil {
		return nil, err
	}
	return sensorsPoints(stat), nil
}

var _ source.Source = (*liveCPUSource)(nil)
var _ source.Source = (*liveMemorySource)(nil)
var _ source.Source = (*liveUptimeSource)(nil)
var _ source.Source = (*liveInventorySource)(nil)
var _ source.Source = (*liveDiskSource)(nil)
var _ source.Source = (*liveNetworkSource)(nil)
var _ source.Source = (*liveFilesystemSource)(nil)
var _ source.Source = (*liveProcessSource)(nil)
var _ source.Source = (*liveSensorsSource)(nil)
