package source_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/fixtures"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files")

const testdataRoot = "../../testdata"

func platforms(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(testdataRoot)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		t.Fatalf("%s: no platform directories", testdataRoot)
	}
	return names
}

func collectPlatform(t *testing.T, platform string) collector.Result {
	t.Helper()
	sources, err := fixtures.Load(testdataRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range sources {
		if strings.HasPrefix(src.Name(), platform) {
			return collector.New([]source.Source{src}).CollectOnce(context.Background())
		}
	}
	t.Fatalf("%s: no source named %s-* in %s", platform, platform, testdataRoot)
	return collector.Result{}
}

func TestGolden(t *testing.T) {
	for _, platform := range platforms(t) {
		t.Run(platform, func(t *testing.T) {
			result := collectPlatform(t, platform)
			if len(result.Errors) != 0 {
				t.Fatalf("%s: collect errors: %v", platform, result.Errors)
			}
			got := formatPoints(result.Points)
			path := filepath.Join(testdataRoot, platform, "golden.txt")
			if *updateGolden {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: %v (run go test ./internal/source -run TestGolden -update)", platform, err)
			}
			if got != string(want) {
				t.Errorf("%s: golden mismatch\n--- got ---\n%s--- want ---\n%s", platform, got, want)
			}
		})
	}
}

func TestDeterminism(t *testing.T) {
	for _, platform := range platforms(t) {
		t.Run(platform, func(t *testing.T) {
			first := formatPoints(collectFresh(t, platform))
			second := formatPoints(collectFresh(t, platform))
			if first != second {
				t.Errorf("%s: collect output differs between runs\n--- first ---\n%s--- second ---\n%s", platform, first, second)
			}
		})
	}
}

func collectFresh(t *testing.T, platform string) []schema.Point {
	t.Helper()
	src, err := fixtures.LoadPlatform(testdataRoot, platform)
	if err != nil {
		t.Fatal(err)
	}
	return collector.New([]source.Source{src}).CollectOnce(context.Background()).Points
}

func formatPoints(points []schema.Point) string {
	var b strings.Builder
	for _, point := range points {
		b.WriteString(point.Name)
		if len(point.Attrs) > 0 {
			b.WriteString("{")
			for i, attr := range point.Attrs {
				if i > 0 {
					b.WriteString(",")
				}
				fmt.Fprintf(&b, "%s=%s", attr.Key, attr.Value)
			}
			b.WriteString("}")
		}
		fmt.Fprintf(&b, " unit=%s value=%s\n", point.Unit, strconv.FormatFloat(point.Value, 'g', -1, 64))
	}
	return b.String()
}

func formatAttrs(attrs []schema.Attr) string {
	parts := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		parts = append(parts, attr.Key+"="+attr.Value)
	}
	return strings.Join(parts, ",")
}
