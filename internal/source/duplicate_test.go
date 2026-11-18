package source_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/fixtures"
)

func TestSinglePlatformHasNoDuplicateSeries(t *testing.T) {
	src, err := fixtures.LoadPlatform(testdataRoot, "linux")
	if err != nil {
		t.Fatal(err)
	}
	result := collector.New([]source.Source{src}).CollectOnce(context.Background())
	if len(result.Errors) != 0 {
		t.Fatalf("collect errors: %v", result.Errors)
	}
	var out bytes.Buffer
	if err := collector.WriteMetrics(&out, result.Points); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]int)
	var duplicates []string
	for _, line := range strings.Split(out.String(), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key := seriesKey(line)
		seen[key]++
		if seen[key] == 2 {
			duplicates = append(duplicates, key)
		}
	}
	if len(duplicates) > 0 {
		t.Errorf("duplicate series rendered: %s", strings.Join(duplicates, "; "))
	}
}

func seriesKey(line string) string {
	return line[:strings.LastIndex(line, " ")]
}
