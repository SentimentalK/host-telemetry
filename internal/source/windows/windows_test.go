package windows_test

import (
	"context"
	"io/fs"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/windows"
)

const fixturesRoot = "../../../testdata/windows"

func TestSourceNamesAndIntervals(t *testing.T) {
	want := map[string]time.Duration{
		"windows-cpu":        15 * time.Second,
		"windows-memory":     15 * time.Second,
		"windows-uptime":     4 * time.Second,
		"windows-disk":       15 * time.Second,
		"windows-network":    15 * time.Second,
		"windows-filesystem": 15 * time.Second,
		"windows-process":    15 * time.Second,
		"windows-inventory":  300 * time.Second,
	}
	sources := windows.New(os.DirFS(fixturesRoot))
	if len(sources) != len(want) {
		t.Fatalf("sources = %d, want %d", len(sources), len(want))
	}
	for _, src := range sources {
		interval, ok := want[src.Name()]
		if !ok {
			t.Errorf("unexpected source %q", src.Name())
			continue
		}
		if src.Interval() != interval {
			t.Errorf("%s interval = %s, want %s", src.Name(), src.Interval(), interval)
		}
		delete(want, src.Name())
	}
	for name := range want {
		t.Errorf("missing source %q", name)
	}
}

func TestWindowsGoldenCoversMetrics(t *testing.T) {
	result := collectAll(t, windows.New(os.DirFS(fixturesRoot)))
	if len(result.Errors) != 0 {
		t.Fatalf("collect errors: %v", result.Errors)
	}
	metrics := []string{
		schema.SystemCPUTime,
		schema.SystemCPUUtilization,
		schema.SystemCPUPhysicalCount,
		schema.SystemCPULogicalCount,
		schema.SystemMemoryUsage,
		schema.SystemMemoryUtilization,
		schema.SystemMemoryLimit,
		schema.SystemPagingUsage,
		schema.SystemUptime,
		schema.SystemDiskIO,
		schema.SystemDiskOperations,
		schema.SystemNetworkIO,
		schema.SystemNetworkErrors,
		schema.SystemNetworkPacketCount,
		schema.SystemNetworkPacketDropped,
		schema.SystemFilesystemUsage,
		schema.SystemFilesystemUtilization,
		schema.SystemProcessCount,
		schema.SystemProcessLimit,
	}
	for _, name := range metrics {
		if len(pointsNamed(result.Points, name)) == 0 {
			t.Errorf("missing %s", name)
		}
	}
	utilizations := pointsNamed(result.Points, schema.SystemCPUUtilization)
	if len(utilizations) != 3 {
		t.Errorf("%s rows = %d, want 3", schema.SystemCPUUtilization, len(utilizations))
	}
	sum := 0.0
	for _, point := range utilizations {
		sum += point.Value
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Errorf("sum of %s = %v, want 1", schema.SystemCPUUtilization, sum)
	}
	for _, point := range result.Points {
		if point.Name == schema.SystemPressureStallTime || point.Name == schema.SystemPressureUtilization {
			t.Errorf("unexpected pressure point %s", point.Name)
		}
	}
}

func TestWindowsCPUKernelIncludesIdle(t *testing.T) {
	sources := windows.New(os.DirFS(fixturesRoot))
	cpu := sourceNamed(t, sources, "windows-cpu")
	points, err := cpu.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		schema.CPUModeIdle:   260,
		schema.CPUModeSystem: 130,
		schema.CPUModeUser:   130,
	}
	total := 0.0
	for _, point := range points {
		if point.Name != schema.SystemCPUTime {
			continue
		}
		mode := attrValue(t, point, schema.AttrCPUMode)
		if point.Value != want[mode] {
			t.Errorf("%s{%s} = %v, want %v", point.Name, mode, point.Value, want[mode])
		}
		total += point.Value
	}
	if total != 520 {
		t.Errorf("sum of %s = %v, want 520", schema.SystemCPUTime, total)
	}

	older := collectAll(t, windows.New(singleSnapshot(t, "real-synthetic-00")))
	newer := collectAll(t, windows.New(singleSnapshot(t, "real-synthetic-01")))
	if got := cpuTimeSum(t, newer.Points) - cpuTimeSum(t, older.Points); got != (300000000+900000000)/1e7 {
		t.Errorf("delta of %s sums = %v, want %v", schema.SystemCPUTime, got, 120.0)
	}

	utilizations := pointsNamed(points, schema.SystemCPUUtilization)
	if len(utilizations) != 3 {
		t.Fatalf("%s rows = %d, want 3", schema.SystemCPUUtilization, len(utilizations))
	}
	utilSum := 0.0
	for _, point := range utilizations {
		utilSum += point.Value
	}
	if math.Abs(utilSum-1) > 1e-9 {
		t.Errorf("sum of %s = %v, want 1", schema.SystemCPUUtilization, utilSum)
	}
}

func TestWindowsMemoryAndPaging(t *testing.T) {
	result := collectAll(t, windows.New(os.DirFS(fixturesRoot)))
	if len(result.Errors) != 0 {
		t.Fatalf("collect errors: %v", result.Errors)
	}
	checks := []struct {
		name  string
		key   string
		value string
		want  float64
	}{
		{schema.SystemMemoryUsage, schema.AttrSystemMemoryState, schema.SystemMemoryStateAvailable, 6442450944},
		{schema.SystemMemoryUsage, schema.AttrSystemMemoryState, schema.SystemMemoryStateUsed, 10737418240},
		{schema.SystemMemoryUtilization, schema.AttrSystemMemoryState, schema.SystemMemoryStateAvailable, 0.375},
		{schema.SystemMemoryUtilization, schema.AttrSystemMemoryState, schema.SystemMemoryStateUsed, 0.625},
		{schema.SystemPagingUsage, schema.AttrSystemPagingState, schema.SystemPagingStateUsed, 4294967296},
		{schema.SystemPagingUsage, schema.AttrSystemPagingState, schema.SystemPagingStateFree, 4294967296},
	}
	for _, check := range checks {
		point, ok := findPoint(result.Points, check.name, check.key, check.value)
		if !ok {
			t.Errorf("missing %s{%s=%s}", check.name, check.key, check.value)
			continue
		}
		if point.Value != check.want {
			t.Errorf("%s{%s=%s} = %v, want %v", check.name, check.key, check.value, point.Value, check.want)
		}
	}
}

func TestWindowsFilesystemTypes(t *testing.T) {
	result := collectAll(t, windows.New(os.DirFS(fixturesRoot)))
	if len(result.Errors) != 0 {
		t.Fatalf("collect errors: %v", result.Errors)
	}
	cases := []struct {
		drive    string
		mode     string
		fsType   string
		wantType bool
	}{
		{"C:", "rw", schema.SystemFilesystemTypeNtfs, true},
		{"D:", "rw", schema.SystemFilesystemTypeExfat, true},
		{"E:", "ro", "", false},
	}
	for _, tc := range cases {
		var usage int
		for _, point := range pointsNamed(result.Points, schema.SystemFilesystemUsage) {
			if attrValue(t, point, schema.AttrSystemDeviceName) != tc.drive {
				continue
			}
			usage++
			if got := attrValue(t, point, schema.AttrSystemFilesystemMode); got != tc.mode {
				t.Errorf("%s mode = %q, want %q", tc.drive, got, tc.mode)
			}
			fsType, ok := attr(point, schema.AttrSystemFilesystemType)
			if ok != tc.wantType || fsType != tc.fsType {
				t.Errorf("%s type = %q (present %v), want %q (present %v)", tc.drive, fsType, ok, tc.fsType, tc.wantType)
			}
			if got := attrValue(t, point, schema.AttrSystemFilesystemMountpoint); got != tc.drive+`\` {
				t.Errorf("%s mountpoint = %q, want %q", tc.drive, got, tc.drive+`\`)
			}
		}
		if usage != 2 {
			t.Errorf("%s: %s rows = %d, want 2", tc.drive, schema.SystemFilesystemUsage, usage)
		}
		utilization := 0
		for _, point := range pointsNamed(result.Points, schema.SystemFilesystemUtilization) {
			if attrValue(t, point, schema.AttrSystemDeviceName) == tc.drive {
				utilization++
			}
		}
		if utilization != 2 {
			t.Errorf("%s: %s rows = %d, want 2", tc.drive, schema.SystemFilesystemUtilization, utilization)
		}
	}
}

func TestWindowsProcessInventoryUptime(t *testing.T) {
	result := collectAll(t, windows.New(singleSnapshot(t, "real-synthetic-00")))
	if len(result.Errors) != 0 {
		t.Fatalf("collect errors: %v", result.Errors)
	}
	checks := []struct {
		name string
		want float64
	}{
		{schema.SystemProcessCount, 312},
		{schema.SystemProcessLimit, 4294967295},
		{schema.SystemUptime, 86400},
		{schema.SystemCPULogicalCount, 16},
		{schema.SystemCPUPhysicalCount, 8},
		{schema.SystemMemoryLimit, 17179869184},
	}
	for _, check := range checks {
		point, ok := findPoint(result.Points, check.name, "", "")
		if !ok {
			t.Errorf("missing %s", check.name)
			continue
		}
		if point.Value != check.want {
			t.Errorf("%s = %v, want %v", check.name, point.Value, check.want)
		}
	}
}

func collectAll(t *testing.T, sources []source.Source) collector.Result {
	t.Helper()
	return collector.New(sources).CollectOnce(context.Background())
}

func sourceNamed(t *testing.T, sources []source.Source, name string) source.Source {
	t.Helper()
	for _, src := range sources {
		if src.Name() == name {
			return src
		}
	}
	t.Fatalf("missing source %q", name)
	return nil
}

func singleSnapshot(t *testing.T, name string) fstest.MapFS {
	t.Helper()
	disk := os.DirFS(fixturesRoot)
	samples := fstest.MapFS{}
	err := fs.WalkDir(disk, name, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(disk, path)
		if err != nil {
			return err
		}
		samples[path] = &fstest.MapFile{Data: data}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) == 0 {
		t.Fatalf("%s: no fixture files", name)
	}
	return samples
}

func copyFixtures(t *testing.T) fstest.MapFS {
	t.Helper()
	disk := os.DirFS(fixturesRoot)
	samples := fstest.MapFS{}
	err := fs.WalkDir(disk, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(disk, path)
		if err != nil {
			return err
		}
		samples[path] = &fstest.MapFile{Data: data}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return samples
}

func pointsNamed(points []schema.Point, name string) []schema.Point {
	var out []schema.Point
	for _, point := range points {
		if point.Name == name {
			out = append(out, point)
		}
	}
	return out
}

func findPoint(points []schema.Point, name, attrKey, attrValue string) (schema.Point, bool) {
	for _, point := range points {
		if point.Name != name {
			continue
		}
		if attrKey == "" {
			return point, true
		}
		if value, ok := attr(point, attrKey); ok && value == attrValue {
			return point, true
		}
	}
	return schema.Point{}, false
}

func attr(point schema.Point, key string) (string, bool) {
	for _, attribute := range point.Attrs {
		if attribute.Key == key {
			return attribute.Value, true
		}
	}
	return "", false
}

func attrValue(t *testing.T, point schema.Point, key string) string {
	t.Helper()
	value, ok := attr(point, key)
	if !ok {
		t.Fatalf("%s missing %s", point.Name, key)
	}
	return value
}

func cpuTimeSum(t *testing.T, points []schema.Point) float64 {
	t.Helper()
	sum := 0.0
	for _, point := range pointsNamed(points, schema.SystemCPUTime) {
		sum += point.Value
	}
	return sum
}

func formatPoints(points []schema.Point) string {
	var b strings.Builder
	for _, point := range points {
		b.WriteString(point.Name)
		b.WriteByte('{')
		parts := make([]string, 0, len(point.Attrs))
		for _, attribute := range point.Attrs {
			parts = append(parts, attribute.Key+"="+attribute.Value)
		}
		b.WriteString(strings.Join(parts, ","))
		b.WriteString("}=")
		b.WriteString(strconv.FormatFloat(point.Value, 'g', -1, 64))
		b.WriteByte('\n')
	}
	return b.String()
}
