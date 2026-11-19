package workload_test

import (
	"context"
	"flag"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/workload"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files")

func TestGolden(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	src := workload.New(os.DirFS(fixtureCgroup), os.DirFS(fixtureProc), workload.Options{
		Cgroups:     true,
		Executables: []string{"agent", "excluded", "test-agent-1"},
		Now:         fixed,
	})
	first, err := src.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := src.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := formatPoints(second)
	if got != formatPoints(first) {
		t.Errorf("second collect differs\n--- first ---\n%s--- second ---\n%s", formatPoints(first), got)
	}
	path := "../../testdata/linux/workload-golden.txt"
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/workload -run TestGolden -update)", err)
	}
	if got != string(want) {
		t.Errorf("golden mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func formatPoints(points []schema.Point) string {
	lines := make([]string, 0, len(points))
	for _, point := range points {
		attrs := make([]string, 0, len(point.Attrs))
		for _, attr := range sortedAttrs(point.Attrs) {
			attrs = append(attrs, attr.Key+"="+attr.Value)
		}
		lines = append(lines, point.Name+"|"+strings.Join(attrs, ",")+"|"+strconv.FormatFloat(point.Value, 'f', -1, 64))
	}
	sort.SliceStable(lines, func(i, j int) bool {
		left, _, _ := strings.Cut(lines[i], "|")
		right, _, _ := strings.Cut(lines[j], "|")
		if left != right {
			return left < right
		}
		return lines[i] < lines[j]
	})
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func sortedAttrs(attrs []schema.Attr) []schema.Attr {
	out := append([]schema.Attr(nil), attrs...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Value < out[j].Value
	})
	return out
}
