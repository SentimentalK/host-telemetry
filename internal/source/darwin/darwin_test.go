package darwin_test

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/darwin"
)

const fixtureRoot = "../../../testdata/darwin"

func loadFixtures(t *testing.T, dir string) fstest.MapFS {
	t.Helper()
	disk := os.DirFS(dir)
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

func realSynthetic(t *testing.T) fstest.MapFS {
	t.Helper()
	return loadFixtures(t, fixtureRoot)
}

func collect(t *testing.T, sources []source.Source, name string) ([]schema.Point, error) {
	t.Helper()
	for _, src := range sources {
		if src.Name() == name {
			return src.Collect(context.Background())
		}
	}
	t.Fatalf("no source named %q", name)
	return nil, nil
}

func pointWithAttr(points []schema.Point, name, key, value string) (schema.Point, bool) {
	for _, point := range points {
		if point.Name != name {
			continue
		}
		for _, attr := range point.Attrs {
			if attr.Key == key && attr.Value == value {
				return point, true
			}
		}
	}
	return schema.Point{}, false
}

func TestSourceNamesAndIntervals(t *testing.T) {
	sources := darwin.New(realSynthetic(t))
	want := map[string]time.Duration{
		"darwin-cpu":        15 * time.Second,
		"darwin-memory":     15 * time.Second,
		"darwin-uptime":     4 * time.Second,
		"darwin-inventory":  300 * time.Second,
		"darwin-disk":       15 * time.Second,
		"darwin-network":    15 * time.Second,
		"darwin-filesystem": 15 * time.Second,
		"darwin-process":    15 * time.Second,
		"darwin-sensors":    30 * time.Second,
	}
	if len(sources) != len(want) {
		t.Fatalf("got %d sources, want %d", len(sources), len(want))
	}
	for _, src := range sources {
		interval, ok := want[src.Name()]
		if !ok {
			t.Errorf("unexpected source %q", src.Name())
			continue
		}
		if src.Interval() != interval {
			t.Errorf("%s interval = %v, want %v", src.Name(), src.Interval(), interval)
		}
	}
}

func TestCPUUtilizationAndTime(t *testing.T) {
	points, err := collect(t, darwin.New(realSynthetic(t)), "darwin-cpu")
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]float64{"user": 0.4, "system": 0.2, "idle": 0.4}
	for mode, want := range modes {
		point, ok := pointWithAttr(points, schema.SystemCPUUtilization, schema.AttrCPUMode, mode)
		if !ok {
			t.Fatalf("%s{%s=%s} missing", schema.SystemCPUUtilization, schema.AttrCPUMode, mode)
		}
		if diff := point.Value - want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("%s{%s=%s} = %v, want %v", schema.SystemCPUUtilization, schema.AttrCPUMode, mode, point.Value, want)
		}
	}
	if got := countPoints(points, schema.SystemCPUUtilization); got != 3 {
		t.Errorf("%s points = %d, want 3", schema.SystemCPUUtilization, got)
	}
	times := map[string]float64{"user": 6060, "system": 3030, "idle": 12060}
	for mode, want := range times {
		point, ok := pointWithAttr(points, schema.SystemCPUTime, schema.AttrCPUMode, mode)
		if !ok {
			t.Fatalf("%s{%s=%s} missing", schema.SystemCPUTime, schema.AttrCPUMode, mode)
		}
		if point.Value != want {
			t.Errorf("%s{%s=%s} = %v, want %v", schema.SystemCPUTime, schema.AttrCPUMode, mode, point.Value, want)
		}
	}
}

func TestMemoryUsageAndPaging(t *testing.T) {
	points, err := collect(t, darwin.New(realSynthetic(t)), "darwin-memory")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		key   string
		value string
		want  float64
	}{
		{schema.SystemMemoryUsage, schema.AttrSystemMemoryState, schema.SystemMemoryStateUsed, 10737418240},
		{schema.SystemMemoryUsage, schema.AttrSystemMemoryState, schema.SystemMemoryStateAvailable, 6442450944},
		{schema.SystemMemoryUtilization, schema.AttrSystemMemoryState, schema.SystemMemoryStateUsed, 0.625},
		{schema.SystemMemoryUtilization, schema.AttrSystemMemoryState, schema.SystemMemoryStateAvailable, 0.375},
		{schema.SystemPagingUsage, schema.AttrSystemPagingState, schema.SystemPagingStateUsed, 536870912},
		{schema.SystemPagingUsage, schema.AttrSystemPagingState, schema.SystemPagingStateFree, 1610612736},
		{schema.SystemPagingUtilization, schema.AttrSystemPagingState, schema.SystemPagingStateUsed, 0.25},
		{schema.SystemPagingUtilization, schema.AttrSystemPagingState, schema.SystemPagingStateFree, 0.75},
	}
	for _, tc := range cases {
		point, ok := pointWithAttr(points, tc.name, tc.key, tc.value)
		if !ok {
			t.Errorf("%s{%s=%s} missing", tc.name, tc.key, tc.value)
			continue
		}
		if point.Value != tc.want {
			t.Errorf("%s{%s=%s} = %v, want %v", tc.name, tc.key, tc.value, point.Value, tc.want)
		}
	}
}

func TestUptimeUsesLastSnapshot(t *testing.T) {
	points, err := collect(t, darwin.New(realSynthetic(t)), "darwin-uptime")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1", len(points))
	}
	if points[0].Name != schema.SystemUptime || points[0].Value != 12355 {
		t.Errorf("got %s = %v, want %s = 12355", points[0].Name, points[0].Value, schema.SystemUptime)
	}
}

func TestCPURejectsUnparsableHostStatistics(t *testing.T) {
	samples := realSynthetic(t)
	samples["real-synthetic-01/host_statistics.txt"] = &fstest.MapFile{
		Data: bytes.Replace(
			samples["real-synthetic-01/host_statistics.txt"].Data,
			[]byte("user_ticks: 606000"), []byte("user_ticks: botched"), 1,
		),
	}
	_, err := collect(t, darwin.New(samples), "darwin-cpu")
	if err == nil {
		t.Fatal("darwin-cpu accepted a non-numeric user_ticks")
	}
}

func TestMemoryRejectsMissingPageSize(t *testing.T) {
	samples := realSynthetic(t)
	data := samples["real-synthetic-01/vm_stat.txt"].Data
	line := []byte("Mach Virtual Memory Statistics: (page size of 16384 bytes)")
	samples["real-synthetic-01/vm_stat.txt"] = &fstest.MapFile{
		Data: bytes.Replace(data, line, []byte("Mach Virtual Memory Statistics:"), 1),
	}
	_, err := collect(t, darwin.New(samples), "darwin-memory")
	if err == nil {
		t.Fatal("darwin-memory accepted vm_stat.txt without a page size")
	}
}

func TestLegacyMissingHostStatisticsIsSilent(t *testing.T) {
	samples := fstest.MapFS{}
	disk := os.DirFS(filepath.Join(fixtureRoot, "sample-00"))
	entries, err := fs.ReadDir(disk, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(disk, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		samples["sample-00/"+entry.Name()] = &fstest.MapFile{Data: data}
	}
	sources := darwin.New(samples)

	points, err := collect(t, sources, "darwin-cpu")
	if err != nil {
		t.Fatalf("legacy cpu source returned an error: %v", err)
	}
	if len(points) != 0 {
		t.Fatalf("legacy cpu source returned %d points without host_statistics", len(points))
	}

	for _, name := range []string{"darwin-memory", "darwin-uptime", "darwin-inventory"} {
		if _, err := collect(t, sources, name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	for _, name := range []string{"darwin-disk", "darwin-network", "darwin-filesystem", "darwin-process", "darwin-sensors"} {
		points, err := collect(t, sources, name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(points) != 0 {
			t.Errorf("%s: got %d points without its fixture, want 0", name, len(points))
		}
	}
}

func TestDarwinMemoryUtilizationWithinLimit(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"real-synthetic": realSynthetic(t),
		"sample-00":      singleSample(t, "sample-00"),
	}
	for name, samples := range cases {
		t.Run(name, func(t *testing.T) {
			points, err := collect(t, darwin.New(samples), "darwin-memory")
			if err != nil {
				t.Fatal(err)
			}
			var sum float64
			for _, point := range points {
				if point.Name != schema.SystemMemoryUtilization {
					continue
				}
				if point.Value < 0 || point.Value > 1 {
					t.Errorf("%s%v = %v outside [0,1]", point.Name, point.Attrs, point.Value)
				}
				sum += point.Value
			}
			if sum > 1+1e-9 {
				t.Errorf("sum of %s states = %v, want <= 1", schema.SystemMemoryUtilization, sum)
			}
		})
	}
}

func singleSample(t *testing.T, name string) fstest.MapFS {
	t.Helper()
	disk := os.DirFS(filepath.Join(fixtureRoot, name))
	entries, err := fs.ReadDir(disk, ".")
	if err != nil {
		t.Fatal(err)
	}
	samples := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(disk, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		samples[name+"/"+entry.Name()] = &fstest.MapFile{Data: data}
	}
	return samples
}

func countPoints(points []schema.Point, name string) int {
	count := 0
	for _, point := range points {
		if point.Name == name {
			count++
		}
	}
	return count
}

func TestDarwinGoldenCoversMetrics(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(fixtureRoot, "golden.txt"))
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		name := line
		if index := strings.IndexAny(name, "{ "); index >= 0 {
			name = name[:index]
		}
		counts[name]++
	}
	want := []string{
		schema.SystemCPUTime,
		schema.SystemCPUUtilization,
		schema.SystemMemoryUsage,
		schema.SystemMemoryUtilization,
		schema.SystemPagingUsage,
		schema.SystemPagingUtilization,
		schema.SystemUptime,
		schema.SystemDiskIO,
		schema.SystemDiskOperations,
		schema.SystemDiskOperationTime,
		schema.SystemNetworkIO,
		schema.SystemNetworkErrors,
		schema.SystemNetworkPacketCount,
		schema.SystemNetworkPacketDropped,
		schema.SystemFilesystemUsage,
		schema.SystemFilesystemUtilization,
		schema.SystemProcessCount,
		schema.SystemProcessLimit,
		schema.SystemCPULogicalCount,
		schema.SystemCPUPhysicalCount,
		schema.SystemMemoryLimit,
		schema.HWTemperature,
		schema.HWFanSpeed,
		schema.HWFanSpeedLimit,
		schema.HWFanSpeedRatio,
		schema.HWPower,
	}
	for _, name := range want {
		if counts[name] == 0 {
			t.Errorf("golden has no %s row", name)
		}
	}
}

func TestDarwinCPUModes(t *testing.T) {
	points, err := collect(t, darwin.New(realSynthetic(t)), "darwin-cpu")
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	var sum float64
	for _, point := range points {
		if point.Name != schema.SystemCPUUtilization {
			continue
		}
		mode, ok := attrValue(point, schema.AttrCPUMode)
		if !ok {
			t.Fatalf("%s point without %s: %v", schema.SystemCPUUtilization, schema.AttrCPUMode, point.Attrs)
		}
		counts[mode]++
		sum += point.Value
	}
	if len(counts) != 3 || counts["user"] != 1 || counts["system"] != 1 || counts["idle"] != 1 {
		t.Errorf("cpu modes = %v, want one each of user, system, idle", counts)
	}
	if sum > 1+1e-9 {
		t.Errorf("sum of %s modes = %v, want <= 1", schema.SystemCPUUtilization, sum)
	}
}

func TestDarwinNoPSI(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(fixtureRoot, "golden.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "system.pressure.") {
		t.Error("darwin golden contains system.pressure metrics")
	}
}

func TestDarwinFilesystemUnknownType(t *testing.T) {
	points, err := collect(t, darwin.New(realSynthetic(t)), "darwin-filesystem")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		mountpoint string
		fstype     string
		hasType    bool
	}{
		{"/", "", false},
		{"/Volumes/Backup", schema.SystemFilesystemTypeHfsplus, true},
		{"/Volumes/USB", schema.SystemFilesystemTypeExfat, true},
	}
	for _, tc := range cases {
		var found bool
		for _, point := range points {
			if point.Name != schema.SystemFilesystemUsage {
				continue
			}
			if mount, _ := attrValue(point, schema.AttrSystemFilesystemMountpoint); mount != tc.mountpoint {
				continue
			}
			if state, _ := attrValue(point, schema.AttrSystemFilesystemState); state != schema.SystemFilesystemStateUsed {
				continue
			}
			found = true
			got, ok := attrValue(point, schema.AttrSystemFilesystemType)
			if ok != tc.hasType || got != tc.fstype {
				t.Errorf("%s %s type = %q (present %v), want %q (present %v)", tc.mountpoint, point.Name, got, ok, tc.fstype, tc.hasType)
			}
		}
		if !found {
			t.Errorf("no %s point for %s", schema.SystemFilesystemUsage, tc.mountpoint)
		}
	}
}

func TestDarwinDiskRejectsUnparsableBytes(t *testing.T) {
	samples := realSynthetic(t)
	path := "real-synthetic-01/ioreg.txt"
	data := samples[path].Data
	corrupt := bytes.Replace(data, []byte(`"Bytes (Read)"=368261685248`), []byte(`"Bytes (Read)"=botched`), 1)
	if bytes.Equal(corrupt, data) {
		t.Fatal("ioreg fixture has no Bytes (Read) counter to corrupt")
	}
	samples[path] = &fstest.MapFile{Data: corrupt}
	if _, err := collect(t, darwin.New(samples), "darwin-disk"); err == nil {
		t.Fatal("darwin-disk accepted a non-numeric Bytes (Read)")
	}
}

func TestDarwinNetworkRejectsUnparsableBytes(t *testing.T) {
	samples := realSynthetic(t)
	path := "real-synthetic-01/netstat.txt"
	data := samples[path].Data
	corrupt := bytes.Replace(data, []byte("1324567890"), []byte("botched"), 1)
	if bytes.Equal(corrupt, data) {
		t.Fatal("netstat fixture has no Ibytes counter to corrupt")
	}
	samples[path] = &fstest.MapFile{Data: corrupt}
	if _, err := collect(t, darwin.New(samples), "darwin-network"); err == nil {
		t.Fatal("darwin-network accepted a non-numeric Ibytes")
	}
}

func TestDarwinFilesystemRejectsMissingDF(t *testing.T) {
	samples := realSynthetic(t)
	for _, path := range []string{"real-synthetic-00/df.txt", "real-synthetic-01/df.txt"} {
		delete(samples, path)
	}
	if _, err := collect(t, darwin.New(samples), "darwin-filesystem"); err == nil {
		t.Fatal("darwin-filesystem accepted fixtures without df.txt")
	}
}

func TestDarwinProcessRejectsEmptyPS(t *testing.T) {
	samples := realSynthetic(t)
	samples["real-synthetic-01/ps.txt"] = &fstest.MapFile{}
	if _, err := collect(t, darwin.New(samples), "darwin-process"); err == nil {
		t.Fatal("darwin-process accepted an empty ps.txt")
	}
}

func TestDarwinSensorsRejectsBrokenJSON(t *testing.T) {
	samples := realSynthetic(t)
	samples["real-synthetic-01/macmon.json"] = &fstest.MapFile{Data: []byte(`{"temp":`)}
	if _, err := collect(t, darwin.New(samples), "darwin-sensors"); err == nil {
		t.Fatal("darwin-sensors accepted broken JSON")
	}
}

func attrValue(point schema.Point, key string) (string, bool) {
	for _, attr := range point.Attrs {
		if attr.Key == key {
			return attr.Value, true
		}
	}
	return "", false
}
