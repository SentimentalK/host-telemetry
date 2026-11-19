package main

import (
	"testing"

	"github.com/SentimentalK/host-telemetry/internal/schema"
)

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
