package source_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/linux"
)

func liveDumpDir() string {
	return filepath.Join(testdataRoot, "linux", "real-00")
}

func liveSources(t *testing.T, dir string, now func() time.Time) []source.Source {
	t.Helper()
	statfs, err := linux.FixtureStatfs(os.DirFS(dir), "statfs.tsv")
	if err != nil {
		t.Fatal(err)
	}
	return linux.NewLive(os.DirFS(dir), linux.Options{Now: now, Statfs: statfs})
}

func TestLiveGolden(t *testing.T) {
	dir := liveDumpDir()
	base := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	calls := 0
	now := func() time.Time {
		at := base.Add(time.Duration(calls) * 15 * time.Second)
		calls++
		return at
	}
	sources := liveSources(t, dir, now)

	first := collector.New(sources).CollectOnce(context.Background())
	if len(first.Errors) != 0 {
		t.Fatalf("first collect errors: %v", first.Errors)
	}
	for _, point := range first.Points {
		if point.Name == schema.SystemCPUUtilization {
			t.Fatalf("first collect emitted %s before a second sample", schema.SystemCPUUtilization)
		}
	}
	time.Sleep(50 * time.Millisecond)

	second := collector.New(sources).CollectOnce(context.Background())
	if len(second.Errors) != 0 {
		t.Fatalf("second collect errors: %v", second.Errors)
	}
	got := formatPoints(second.Points)
	path := filepath.Join(dir, "golden.txt")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/source -run TestLiveGolden -update)", err)
	}
	if got != string(want) {
		t.Errorf("live golden mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestLiveDeterminism(t *testing.T) {
	dir := liveDumpDir()
	base := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	fixed := func() time.Time { return base }
	collect := func() string {
		result := collector.New(liveSources(t, dir, fixed)).CollectOnce(context.Background())
		if len(result.Errors) != 0 {
			t.Fatalf("collect errors: %v", result.Errors)
		}
		return formatPoints(result.Points)
	}
	first := collect()
	second := collect()
	if first != second {
		t.Errorf("live collect output differs between fresh sources\n--- first ---\n%s--- second ---\n%s", first, second)
	}
}
