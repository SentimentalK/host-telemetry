package darwin_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source/darwin"
)

func TestLiveCPUFallback(t *testing.T) {
	sysctl, err := os.ReadFile(filepath.Join(fixtureRoot, "real-synthetic-01", "sysctl.txt"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "sysctl" || len(args) != 1 || args[0] != "-a" {
			return nil, fmt.Errorf("unexpected command %s %v", name, args)
		}
		return sysctl, nil
	}
	sources := darwin.NewLive(darwin.Options{
		Run: run,
		CPUTicks: func() (darwin.CPUTicks, error) {
			return darwin.CPUTicks{}, errors.New("host_statistics unavailable")
		},
	})

	points, err := collect(t, sources, "darwin-cpu")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got := countPoints(points, schema.SystemCPUUtilization); got != 0 {
		t.Errorf("%s points = %d, want 0", schema.SystemCPUUtilization, got)
	}
	if len(points) != 0 {
		t.Errorf("points = %d, want 0", len(points))
	}
}

func TestLiveFromRunner(t *testing.T) {
	dir := filepath.Join(fixtureRoot, "real-synthetic-01")
	files := map[string]string{
		"sysctl -a": "sysctl.txt",
		"vm_stat":   "vm_stat.txt",
	}
	var calls []string
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		key := strings.Join(append([]string{name}, args...), " ")
		calls = append(calls, key)
		file, ok := files[key]
		if !ok {
			return nil, fmt.Errorf("unexpected command %q", key)
		}
		return os.ReadFile(filepath.Join(dir, file))
	}

	ticks := []darwin.CPUTicks{
		{User: 606000, System: 303000, Idle: 1206000},
		{User: 612000, System: 306000, Idle: 1212000},
	}
	var tickCalls int
	cpuTicks := func() (darwin.CPUTicks, error) {
		if tickCalls >= len(ticks) {
			tickCalls = len(ticks) - 1
		}
		value := ticks[tickCalls]
		tickCalls++
		return value, nil
	}

	base := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	var nowCalls int
	now := func() time.Time {
		at := base.Add(time.Duration(nowCalls) * 15 * time.Second)
		nowCalls++
		return at
	}

	sources := darwin.NewLive(darwin.Options{Run: run, Now: now, CPUTicks: cpuTicks})

	first, err := collect(t, sources, "darwin-cpu")
	if err != nil {
		t.Fatalf("first cpu collect: %v", err)
	}
	for mode, want := range map[string]float64{"user": 6060, "system": 3030, "idle": 12060} {
		point, ok := pointWithAttr(first, schema.SystemCPUTime, schema.AttrCPUMode, mode)
		if !ok {
			t.Fatalf("%s{%s=%s} missing", schema.SystemCPUTime, schema.AttrCPUMode, mode)
		}
		if point.Value != want {
			t.Errorf("%s{%s=%s} = %v, want %v", schema.SystemCPUTime, schema.AttrCPUMode, mode, point.Value, want)
		}
	}
	if got := countPoints(first, schema.SystemCPUUtilization); got != 0 {
		t.Errorf("first collect %s points = %d, want 0", schema.SystemCPUUtilization, got)
	}

	memory, err := collect(t, sources, "darwin-memory")
	if err != nil {
		t.Fatalf("memory collect: %v", err)
	}
	for _, tc := range []struct {
		name  string
		key   string
		value string
		want  float64
	}{
		{schema.SystemMemoryUsage, schema.AttrSystemMemoryState, schema.SystemMemoryStateUsed, 10737418240},
		{schema.SystemMemoryUsage, schema.AttrSystemMemoryState, schema.SystemMemoryStateAvailable, 6442450944},
		{schema.SystemMemoryUtilization, schema.AttrSystemMemoryState, schema.SystemMemoryStateUsed, 0.625},
		{schema.SystemMemoryUtilization, schema.AttrSystemMemoryState, schema.SystemMemoryStateAvailable, 0.375},
	} {
		point, ok := pointWithAttr(memory, tc.name, tc.key, tc.value)
		if !ok {
			t.Fatalf("%s{%s=%s} missing", tc.name, tc.key, tc.value)
		}
		if point.Value != tc.want {
			t.Errorf("%s{%s=%s} = %v, want %v", tc.name, tc.key, tc.value, point.Value, tc.want)
		}
	}

	second, err := collect(t, sources, "darwin-cpu")
	if err != nil {
		t.Fatalf("second cpu collect: %v", err)
	}
	for mode, want := range map[string]float64{"user": 0.4, "system": 0.2, "idle": 0.4} {
		point, ok := pointWithAttr(second, schema.SystemCPUUtilization, schema.AttrCPUMode, mode)
		if !ok {
			t.Fatalf("%s{%s=%s} missing", schema.SystemCPUUtilization, schema.AttrCPUMode, mode)
		}
		if diff := point.Value - want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("%s{%s=%s} = %v, want %v", schema.SystemCPUUtilization, schema.AttrCPUMode, mode, point.Value, want)
		}
	}

	for _, want := range []string{"sysctl -a", "vm_stat"} {
		found := false
		for _, call := range calls {
			if call == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("runner did not receive %q; calls = %v", want, calls)
		}
	}
}
