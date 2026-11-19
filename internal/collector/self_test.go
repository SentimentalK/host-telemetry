package collector_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/schema"
)

type fakeWAL struct {
	size    int64
	dropped uint64
	pending int
}

func (w fakeWAL) Size() int64         { return w.size }
func (w fakeWAL) Dropped() uint64     { return w.dropped }
func (w fakeWAL) PendingSamples() int { return w.pending }

func TestSelfSource(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, "proc/self/stat", "123 (host telemetry) S 0 0 0 0 0 0 0 0 0 0 250 50 0 0\n")
	writeProc(t, root, "proc/self/statm", "1000 250 100 1 0 200 0\n")

	var stats collector.Stats
	stats.Record("linux-procfs", 1500*time.Millisecond)

	src := collector.NewSelfSource(os.DirFS(root), &stats, fakeWAL{size: 4096, dropped: 7, pending: 9}, time.Now())
	points, err := src.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{}
	for _, point := range points {
		values[point.Name] = point.Value
	}
	want := map[string]float64{
		schema.CollectorSelfCPUTime:     3,
		schema.CollectorSelfMemoryUsage: 250 * 4096,
		schema.CollectorWALSize:         4096,
		schema.CollectorWALDropped:      7,
		schema.CollectorSendQueueLength: 9,
		schema.CollectorScrapeDuration:  1.5,
	}
	for name, value := range want {
		got, ok := values[name]
		if !ok {
			t.Errorf("missing %s", name)
			continue
		}
		if got != value {
			t.Errorf("%s = %v, want %v", name, got, value)
		}
	}
	if len(points) != len(want) {
		t.Errorf("points = %d, want %d", len(points), len(want))
	}

	empty := collector.NewSelfSource(os.DirFS(t.TempDir()), &stats, nil, time.Now())
	points, err = empty.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range points {
		if point.Name == schema.CollectorSelfCPUTime || point.Name == schema.CollectorSelfMemoryUsage {
			t.Errorf("emitted %s without procfs", point.Name)
		}
	}
	if len(points) != 4 {
		t.Errorf("points without procfs = %d, want 4", len(points))
	}
}

func writeProc(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
