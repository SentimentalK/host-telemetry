package main

import (
	"runtime"
	"testing"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

func TestLoadSourcesRejectsMismatchedLivePlatform(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("mismatched live platform is only reachable on linux")
	}
	if _, err := loadSources("", "darwin", ""); err == nil {
		t.Fatal("loadSources accepted darwin live collection on linux")
	}
}

func TestLoadSourcesSensorFilter(t *testing.T) {
	plain, err := loadSources("../../testdata", "darwin", "")
	if err != nil {
		t.Fatal(err)
	}
	if hasSource(plain, "darwin-sensors") {
		t.Error("default sensors registered darwin-sensors")
	}
	enabled, err := loadSources("../../testdata", "darwin", "macmon")
	if err != nil {
		t.Fatal(err)
	}
	if !hasSource(enabled, "darwin-sensors") {
		t.Error("macmon sensors did not register darwin-sensors")
	}
}

func hasSource(sources []source.Source, name string) bool {
	for _, src := range sources {
		if src.Name() == name {
			return true
		}
	}
	return false
}

func TestSnapshotReplacesSourceSeries(t *testing.T) {
	snapshot := newSnapshot()
	used := schema.NewPoint(schema.SystemMemoryUsage, 1, schema.Attr{Key: schema.AttrSystemMemoryState, Value: schema.SystemMemoryStateUsed})
	available := schema.NewPoint(schema.SystemMemoryUsage, 2, schema.Attr{Key: schema.AttrSystemMemoryState, Value: schema.SystemMemoryStateAvailable})
	snapshot.update("linux-system", []schema.Point{used, available})
	if got := len(snapshot.list()); got != 2 {
		t.Fatalf("initial series = %d, want 2", got)
	}

	snapshot.update("linux-system", []schema.Point{used})
	list := snapshot.list()
	if len(list) != 1 {
		t.Fatalf("series after replacement = %d, want 1", len(list))
	}
	if list[0].Value != 1 {
		t.Fatalf("remaining series value = %v, want 1", list[0].Value)
	}

	snapshot.update("linux-uptime", []schema.Point{schema.NewPoint(schema.SystemUptime, 42)})
	if got := len(snapshot.list()); got != 2 {
		t.Fatalf("series after second source = %d, want 2", got)
	}
}
