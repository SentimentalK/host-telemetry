package workload_test

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/workload"
)

const (
	fixtureRoot   = "../../testdata/linux/workload"
	fixtureCgroup = fixtureRoot + "/cgroup"
	fixtureProc   = fixtureRoot
)

func fixtureSource(t *testing.T, opts workload.Options) *pointsCollector {
	t.Helper()
	return collect(t, workload.New(os.DirFS(fixtureCgroup), os.DirFS(fixtureProc), opts))
}

type pointsCollector struct {
	t      *testing.T
	points []schema.Point
	err    error
}

func collect(t *testing.T, src interface {
	Collect(context.Context) ([]schema.Point, error)
}) *pointsCollector {
	t.Helper()
	points, err := src.Collect(context.Background())
	for _, point := range points {
		if validateErr := schema.Validate(point); validateErr != nil {
			t.Fatalf("invalid point: %v", validateErr)
		}
	}
	return &pointsCollector{t: t, points: points, err: err}
}

func (c *pointsCollector) find(name string, attrs ...schema.Attr) *schema.Point {
	c.t.Helper()
	for i := range c.points {
		point := &c.points[i]
		if point.Name != name || len(point.Attrs) != len(attrs) {
			continue
		}
		match := true
		for _, attr := range attrs {
			if !hasAttr(*point, attr) {
				match = false
				break
			}
		}
		if match {
			return point
		}
	}
	return nil
}

func hasAttr(point schema.Point, want schema.Attr) bool {
	for _, attr := range point.Attrs {
		if attr == want {
			return true
		}
	}
	return false
}

func id(value string) schema.Attr {
	return schema.Attr{Key: schema.AttrWorkloadID, Value: value}
}

func TestDisabledCollectsNothing(t *testing.T) {
	c := collect(t, workload.New(fstest.MapFS{}, fstest.MapFS{}, workload.Options{}))
	if c.err != nil {
		t.Fatalf("err = %v", c.err)
	}
	if len(c.points) != 0 {
		t.Fatalf("points = %v, want none", c.points)
	}
}

func TestCountIsZeroWhenNothingMatches(t *testing.T) {
	c := fixtureSource(t, workload.Options{Executables: []string{"absent"}})
	if c.err != nil {
		t.Fatalf("err = %v", c.err)
	}
	if len(c.points) != 1 {
		t.Fatalf("points = %v, want only count", c.points)
	}
	point := c.find(schema.WorkloadCount,
		schema.Attr{Key: schema.AttrWorkloadKind, Value: schema.WorkloadKindAgent},
		schema.Attr{Key: schema.AttrWorkloadState, Value: schema.WorkloadStateWorking})
	if point == nil || point.Value != 0 {
		t.Fatalf("count = %v, want 0", point)
	}
}

func TestCgroupScopePoints(t *testing.T) {
	c := collect(t, workload.New(os.DirFS(fixtureCgroup), fstest.MapFS{}, workload.Options{Cgroups: true}))
	if c.err != nil {
		t.Fatalf("err = %v", c.err)
	}
	orca := id("orca_pty_d_1")
	if point := c.find(schema.WorkloadMemoryUsage, orca); point == nil || point.Value != 104857600 {
		t.Errorf("orca memory = %v, want 104857600", point)
	}
	if point := c.find(schema.WorkloadCPUTime, orca, schema.Attr{Key: schema.AttrCPUMode, Value: schema.CPUModeUser}); point == nil || point.Value != 1 {
		t.Errorf("orca user cpu = %v, want 1", point)
	}
	if point := c.find(schema.WorkloadCPUTime, orca, schema.Attr{Key: schema.AttrCPUMode, Value: schema.CPUModeSystem}); point == nil || point.Value != 0.5 {
		t.Errorf("orca system cpu = %v, want 0.5", point)
	}
	if point := c.find(schema.WorkloadCPUTime, orca, schema.Attr{Key: schema.AttrCPUMode, Value: schema.CPUModeNice}); point == nil || point.Value != 0 {
		t.Errorf("orca nice cpu = %v, want 0", point)
	}
	if point := c.find(schema.WorkloadPressureUtilization, orca, schema.Attr{Key: schema.AttrSystemPressureResource, Value: schema.SystemPressureResourceMemory}); point == nil || point.Value != 0.025 {
		t.Errorf("orca memory pressure = %v, want 0.025", point)
	}
	count := c.find(schema.WorkloadCount,
		schema.Attr{Key: schema.AttrWorkloadKind, Value: schema.WorkloadKindAgent},
		schema.Attr{Key: schema.AttrWorkloadState, Value: schema.WorkloadStateWorking})
	if count == nil || count.Value != 2 {
		t.Errorf("count = %v, want 2", count)
	}
}

func TestCgroupExcludesProcessTrees(t *testing.T) {
	c := fixtureSource(t, workload.Options{Cgroups: true, Executables: []string{"agent", "excluded", "test-agent-1"}})
	if c.err != nil {
		t.Fatalf("err = %v", c.err)
	}
	if point := c.find(schema.WorkloadMemoryUsage, id("test-agent-1#200")); point != nil {
		t.Errorf("process tree test-agent-1#200 double-counted cgroup scope")
	}
	if point := c.find(schema.WorkloadMemoryUsage, id("excluded#102")); point != nil {
		t.Errorf("process tree excluded#102 emitted for cgroup pid")
	}
	agent := id("agent#100")
	if point := c.find(schema.WorkloadMemoryUsage, agent); point == nil || point.Value != float64((10240+4096)*1024) {
		t.Errorf("agent tree memory = %v, want %d", point, (10240+4096)*1024)
	}
	count := c.find(schema.WorkloadCount,
		schema.Attr{Key: schema.AttrWorkloadKind, Value: schema.WorkloadKindAgent},
		schema.Attr{Key: schema.AttrWorkloadState, Value: schema.WorkloadStateWorking})
	if count == nil || count.Value != 3 {
		t.Errorf("count = %v, want 3", count)
	}
}

func TestProcessTreesOnlyWithoutCgroups(t *testing.T) {
	c := fixtureSource(t, workload.Options{Executables: []string{"agent"}})
	if c.err != nil {
		t.Fatalf("err = %v", c.err)
	}
	if point := c.find(schema.WorkloadMemoryUsage, id("agent#100")); point == nil {
		t.Fatalf("missing agent tree")
	}
	if point := c.find(schema.WorkloadMemoryUsage, id("test-agent-1")); point != nil {
		t.Errorf("cgroup point emitted with cgroups disabled")
	}
	count := c.find(schema.WorkloadCount,
		schema.Attr{Key: schema.AttrWorkloadKind, Value: schema.WorkloadKindAgent},
		schema.Attr{Key: schema.AttrWorkloadState, Value: schema.WorkloadStateWorking})
	if count == nil || count.Value != 1 {
		t.Errorf("count = %v, want 1", count)
	}
}

func TestEmitsOnlyWorkloadPoints(t *testing.T) {
	c := fixtureSource(t, workload.Options{Cgroups: true, Executables: []string{"agent"}})
	if c.err != nil {
		t.Fatalf("err = %v", c.err)
	}
	if len(c.points) == 0 {
		t.Fatal("no points")
	}
	for _, point := range c.points {
		if !strings.HasPrefix(point.Name, "workload.") {
			t.Errorf("workload source emitted %q", point.Name)
		}
		if point.Name == schema.SystemMemoryUsage {
			t.Errorf("workload source emitted %q", point.Name)
		}
	}
}

func TestPartialFailureKeepsOtherScopes(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureRoot)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "cgroup", "app.slice", "test-agent-1.scope", "memory.current")); err != nil {
		t.Fatal(err)
	}
	c := collect(t, workload.New(os.DirFS(filepath.Join(dir, "cgroup")), os.DirFS(filepath.Join(dir, "proc")), workload.Options{Cgroups: true}))
	if c.err == nil {
		t.Fatal("err = nil, want partial failure")
	}
	if point := c.find(schema.WorkloadMemoryUsage, id("orca_pty_d_1")); point == nil {
		t.Errorf("healthy scope missing after partial failure")
	}
	if point := c.find(schema.WorkloadMemoryUsage, id("test-agent-1")); point != nil {
		t.Errorf("broken scope emitted")
	}
}

func TestUsageMismatchWarnsOnce(t *testing.T) {
	root := fstest.MapFS{
		"app.slice/loud.scope/cgroup.events":  {Data: []byte("populated 1\n")},
		"app.slice/loud.scope/memory.current": {Data: []byte("1024\n")},
		"app.slice/loud.scope/cpu.stat":       {Data: []byte("user_usec 1000\nsystem_usec 0\nnice_usec 0\nusage_usec 5000\n")},
		"app.slice/loud.scope/cgroup.procs":   {Data: []byte("7\n")},
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)

	src := workload.New(root, fstest.MapFS{}, workload.Options{Cgroups: true})
	for range 2 {
		if _, err := src.Collect(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(output.String(), "usage_usec"); got != 1 {
		t.Fatalf("usage log lines = %d, want 1: %q", got, output.String())
	}

	output.Reset()
	root["app.slice/loud.scope/cpu.stat"] = &fstest.MapFile{Data: []byte("user_usec 1000\nsystem_usec 0\nnice_usec 0\nusage_usec 1001\n")}
	quiet := workload.New(root, fstest.MapFS{}, workload.Options{Cgroups: true})
	for range 2 {
		if _, err := quiet.Collect(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if output.Len() != 0 {
		t.Fatalf("logs for 1us mismatch = %q, want none", output.String())
	}
}

func TestCPUUsageMismatchLogsOncePerScope(t *testing.T) {
	dir := "cgroup/user.slice/user-1000.slice/user@1000.service/app.slice/bad.scope/"
	files := fstest.MapFS{
		dir + "cgroup.events":  &fstest.MapFile{Data: []byte("populated 1\nfrozen 0\n")},
		dir + "memory.current": &fstest.MapFile{Data: []byte("1024\n")},
		dir + "cpu.stat":       &fstest.MapFile{Data: []byte("usage_usec 1000000\nuser_usec 100000\nsystem_usec 0\nnice_usec 0\n")},
		dir + "cgroup.procs":   &fstest.MapFile{Data: []byte("")},
	}
	src := workload.New(files, fstest.MapFS{}, workload.Options{Cgroups: true})

	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(previous)

	for i := 0; i < 3; i++ {
		if _, err := src.Collect(context.Background()); err != nil {
			t.Fatalf("collect %d: %v", i, err)
		}
	}
	if got := strings.Count(buf.String(), "usage_usec"); got != 1 {
		t.Fatalf("log lines = %d, want 1:\n%s", got, buf.String())
	}
}
