package windows_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/windows"
)

func TestWindowsRejectsFormattedRateCounters(t *testing.T) {
	samples := fstest.MapFS{
		"real-synthetic-00/pdh-disk.txt": &fstest.MapFile{Data: []byte(
			"\\PhysicalDisk(0 C:)\\Disk Read Bytes/sec\t0\t1048576\t0\n" +
				"\\PhysicalDisk(0 C:)\\Disk Write Bytes/sec\t272696576\t2097152\t0\n",
		)},
	}
	src := sourceNamed(t, windows.New(samples), "windows-disk")
	points, err := src.Collect(context.Background())
	if err == nil {
		t.Fatal("collect accepted a Disk Read Bytes/sec row with counter type 0")
	}
	write := false
	for _, point := range points {
		if point.Name != schema.SystemDiskIO {
			continue
		}
		direction := attrValue(t, point, schema.AttrDiskIODirection)
		if direction == schema.DiskIODirectionRead {
			t.Errorf("emitted %s for a rejected read row", point.Name)
		}
		if direction == schema.DiskIODirectionWrite {
			write = true
		}
	}
	if !write {
		t.Errorf("missing %s write series", schema.SystemDiskIO)
	}

	result := collector.New([]source.Source{src}).CollectOnce(context.Background())
	if _, ok := findPoint(result.Points, schema.CollectorSourceError, schema.AttrCollectorSource, "windows-disk"); !ok {
		t.Error("collector did not surface collector.source.error for windows-disk")
	}
}

func TestWindowsMissingCounterGroupIsNotAnError(t *testing.T) {
	samples := fstest.MapFS{
		"real-synthetic-00/pdh-disk.txt":    &fstest.MapFile{Data: []byte("# no PhysicalDisk counters\n")},
		"real-synthetic-00/pdh-network.txt": &fstest.MapFile{Data: []byte("# no Network Interface counters\n")},
	}
	all := windows.New(samples)
	sources := []source.Source{
		sourceNamed(t, all, "windows-disk"),
		sourceNamed(t, all, "windows-network"),
	}
	for _, src := range sources {
		points, err := src.Collect(context.Background())
		if err != nil {
			t.Errorf("%s: %v", src.Name(), err)
		}
		if len(points) != 0 {
			t.Errorf("%s: %d points, want 0", src.Name(), len(points))
		}
	}
	result := collector.New(sources).CollectOnce(context.Background())
	if len(result.Errors) != 0 {
		t.Errorf("collect errors: %v", result.Errors)
	}
}

func TestWindowsPartialFailurePerSource(t *testing.T) {
	cases := []struct {
		source  string
		file    string
		witness string
	}{
		{"windows-cpu", "real-synthetic-01/system-times.txt", "windows-uptime"},
		{"windows-memory", "real-synthetic-01/memory-status.txt", "windows-cpu"},
		{"windows-uptime", "real-synthetic-01/tick-count.txt", "windows-cpu"},
		{"windows-disk", "real-synthetic-01/pdh-disk.txt", "windows-network"},
		{"windows-network", "real-synthetic-01/pdh-network.txt", "windows-disk"},
		{"windows-filesystem", "real-synthetic-01/volumes.txt", "windows-cpu"},
		{"windows-process", "real-synthetic-01/pdh-system.txt", "windows-inventory"},
		{"windows-inventory", "real-synthetic-01/cim-processor.csv", "windows-process"},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			samples := copyFixtures(t)
			delete(samples, tc.file)
			sources := windows.New(samples)
			if _, err := sourceNamed(t, sources, tc.source).Collect(context.Background()); err == nil {
				t.Fatalf("%s accepted a missing %s", tc.source, tc.file)
			}
			points, err := sourceNamed(t, sources, tc.witness).Collect(context.Background())
			if err != nil {
				t.Fatalf("%s: %v", tc.witness, err)
			}
			if len(points) == 0 {
				t.Fatalf("%s: no points", tc.witness)
			}
		})
	}
}

func TestWindowsCounterMonotonicAcrossSnapshots(t *testing.T) {
	counters := []string{
		schema.SystemDiskIO,
		schema.SystemDiskOperations,
		schema.SystemNetworkIO,
		schema.SystemNetworkErrors,
		schema.SystemNetworkPacketCount,
		schema.SystemNetworkPacketDropped,
		schema.SystemProcessCount,
	}
	older := counterValues(t, collectAll(t, windows.New(singleSnapshot(t, "real-synthetic-00"))).Points, counters)
	newer := counterValues(t, collectAll(t, windows.New(singleSnapshot(t, "real-synthetic-01"))).Points, counters)
	if len(older) == 0 || len(newer) == 0 {
		t.Fatal("no counter series collected")
	}
	for key, value := range newer {
		previous, ok := older[key]
		if !ok {
			t.Errorf("missing older series %s", key)
			continue
		}
		if value < previous {
			t.Errorf("%s = %v, want >= %v", key, value, previous)
		}
	}
}

func TestWindowsRestartKeepsCounters(t *testing.T) {
	first := formatPoints(collectAll(t, windows.New(singleSnapshot(t, "real-synthetic-01"))).Points)
	second := formatPoints(collectAll(t, windows.New(singleSnapshot(t, "real-synthetic-01"))).Points)
	if first != second {
		t.Errorf("collect output depends on source instance state\n--- first ---\n%s--- second ---\n%s", first, second)
	}
}

func counterValues(t *testing.T, points []schema.Point, names []string) map[string]float64 {
	t.Helper()
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	values := make(map[string]float64)
	for _, point := range points {
		if !wanted[point.Name] {
			continue
		}
		key := point.Name
		for _, attribute := range point.Attrs {
			key += "|" + attribute.Key + "=" + attribute.Value
		}
		values[key] = point.Value
	}
	return values
}
