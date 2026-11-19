package workload

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"sort"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/workload/cgroup"
	"github.com/SentimentalK/host-telemetry/internal/workload/process"
)

type Options struct {
	Cgroups     bool
	Executables []string
	TopN        int
	Now         func() time.Time
	Interval    time.Duration
}

type workloadSource struct {
	cgroupRoot fs.FS
	procRoot   fs.FS
	options    Options
	tracker    *process.Tracker
	warned     map[string]bool
}

func New(cgroupRoot, procRoot fs.FS, opts Options) source.Source {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.TopN <= 0 {
		opts.TopN = 32
	}
	return &workloadSource{
		cgroupRoot: cgroupRoot,
		procRoot:   procRoot,
		options:    opts,
		tracker:    &process.Tracker{},
		warned:     make(map[string]bool),
	}
}

func (s *workloadSource) Name() string { return "workload" }

func (s *workloadSource) Interval() time.Duration { return s.options.Interval }

func (s *workloadSource) Collect(ctx context.Context) ([]schema.Point, error) {
	if !s.options.Cgroups && len(s.options.Executables) == 0 {
		return nil, nil
	}

	type scanResult struct {
		snapshot *process.Snapshot
		err      error
	}
	var scanned chan scanResult
	if len(s.options.Executables) > 0 {
		scanned = make(chan scanResult, 1)
		go func() {
			snapshot, err := process.Scan(s.procRoot)
			scanned <- scanResult{snapshot: snapshot, err: err}
		}()
	}

	var points []schema.Point
	var errs error
	var scopes []cgroup.Scope
	if s.options.Cgroups {
		var err error
		scopes, err = cgroup.Discover(s.cgroupRoot)
		errs = errors.Join(errs, err)
		for _, scope := range scopes {
			points = append(points, s.scopePoints(scope)...)
		}
	}

	exclude := make(map[int]bool)
	for _, scope := range scopes {
		for _, pid := range scope.PIDs {
			exclude[pid] = true
		}
	}

	var trees []process.Tree
	if scanned != nil {
		result := <-scanned
		errs = errors.Join(errs, result.err)
		if result.snapshot != nil {
			var err error
			trees, err = result.snapshot.Match(s.options.Executables, exclude, s.tracker)
			errs = errors.Join(errs, err)
		}
		sort.SliceStable(trees, func(i, j int) bool {
			if trees[i].RSSBytes != trees[j].RSSBytes {
				return trees[i].RSSBytes > trees[j].RSSBytes
			}
			return trees[i].RootPID < trees[j].RootPID
		})
		if len(trees) > s.options.TopN {
			trees = trees[:s.options.TopN]
		}
		for _, tree := range trees {
			points = append(points, treePoints(tree)...)
		}
	}

	points = append(points, schema.NewPoint(schema.WorkloadCount, float64(len(scopes)+len(trees)),
		schema.Attr{Key: schema.AttrWorkloadKind, Value: schema.WorkloadKindAgent},
		schema.Attr{Key: schema.AttrWorkloadState, Value: schema.WorkloadStateWorking},
	))
	return points, errs
}

func (s *workloadSource) scopePoints(scope cgroup.Scope) []schema.Point {
	id := schema.Attr{Key: schema.AttrWorkloadID, Value: scope.ID}
	points := []schema.Point{
		schema.NewPoint(schema.WorkloadMemoryUsage, float64(scope.MemoryCurrent), id),
		schema.NewPoint(schema.WorkloadCPUTime, float64(scope.CPU.User)/1e6, id, schema.Attr{Key: schema.AttrCPUMode, Value: schema.CPUModeUser}),
		schema.NewPoint(schema.WorkloadCPUTime, float64(scope.CPU.System)/1e6, id, schema.Attr{Key: schema.AttrCPUMode, Value: schema.CPUModeSystem}),
		schema.NewPoint(schema.WorkloadCPUTime, float64(scope.CPU.Nice)/1e6, id, schema.Attr{Key: schema.AttrCPUMode, Value: schema.CPUModeNice}),
	}
	for _, resource := range []struct {
		name  string
		value string
	}{
		{name: "cpu", value: schema.SystemPressureResourceCPU},
		{name: "memory", value: schema.SystemPressureResourceMemory},
		{name: "io", value: schema.SystemPressureResourceIO},
	} {
		value, ok := scope.Pressure[resource.name]
		if !ok {
			continue
		}
		points = append(points, schema.NewPoint(schema.WorkloadPressureUtilization, value, id,
			schema.Attr{Key: schema.AttrSystemPressureResource, Value: resource.value}))
	}
	diff := int64(scope.CPU.Usage) - int64(scope.CPU.User+scope.CPU.System+scope.CPU.Nice)
	if diff < 0 {
		diff = -diff
	}
	if scope.CPU.Usage > 0 && diff*100 > int64(scope.CPU.Usage) && !s.warned[scope.ID] {
		s.warned[scope.ID] = true
		log.Printf("workload: cgroup %s usage_usec=%d != user+system+nice=%d",
			scope.ID, scope.CPU.Usage, scope.CPU.User+scope.CPU.System+scope.CPU.Nice)
	}
	return points
}

func treePoints(tree process.Tree) []schema.Point {
	id := schema.Attr{Key: schema.AttrWorkloadID, Value: fmt.Sprintf("%s#%d", tree.Exe, tree.RootPID)}
	return []schema.Point{
		schema.NewPoint(schema.WorkloadMemoryUsage, float64(tree.RSSBytes), id),
		schema.NewPoint(schema.WorkloadCPUTime, tree.UserSeconds, id, schema.Attr{Key: schema.AttrCPUMode, Value: schema.CPUModeUser}),
		schema.NewPoint(schema.WorkloadCPUTime, tree.SystemSeconds, id, schema.Attr{Key: schema.AttrCPUMode, Value: schema.CPUModeSystem}),
	}
}
