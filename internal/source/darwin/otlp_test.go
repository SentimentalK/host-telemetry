package darwin_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/otlp"
	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source/darwin"
)

func TestDarwinOTLPRoundTrip(t *testing.T) {
	result := collector.New(darwin.New(realSynthetic(t))).CollectOnce(context.Background())
	if len(result.Errors) != 0 {
		t.Fatalf("collect errors: %v", result.Errors)
	}
	points := result.Points

	at := time.Unix(1763412355, 0)
	start := at.Add(-time.Hour)
	body, err := otlp.Encode(points, at, start, otlp.Resource{
		HostID:        "host-1",
		ServiceName:   "host-telemetry",
		SchemaVersion: schema.Version,
	})
	if err != nil {
		t.Fatal(err)
	}

	field, typ, tagLen := protowire.ConsumeTag(body)
	if field != 1 || typ != protowire.BytesType {
		t.Fatalf("envelope tag = (%d, %v), want (1, bytes)", field, typ)
	}
	rmBytes, _ := protowire.ConsumeBytes(body[tagLen:])
	var rm metricspb.ResourceMetrics
	if err := proto.Unmarshal(rmBytes, &rm); err != nil {
		t.Fatal(err)
	}
	if len(rm.ScopeMetrics) != 1 {
		t.Fatalf("scope metrics = %d, want 1", len(rm.ScopeMetrics))
	}

	units := make(map[string]string)
	want := make(map[string]float64)
	for _, point := range points {
		units[point.Name] = point.Unit
		want[otlpSeriesKey(point.Name, otlpAttrs(point.Attrs))] = point.Value
	}

	got := make(map[string]float64)
	for _, metric := range rm.ScopeMetrics[0].Metrics {
		if metric.Unit != units[metric.Name] {
			t.Errorf("%s unit = %q, want %q", metric.Name, metric.Unit, units[metric.Name])
		}
		var dataPoints []*metricspb.NumberDataPoint
		switch {
		case metric.GetGauge() != nil:
			dataPoints = metric.GetGauge().DataPoints
		case metric.GetSum() != nil:
			dataPoints = metric.GetSum().DataPoints
		default:
			t.Errorf("%s has no data points", metric.Name)
			continue
		}
		for _, dataPoint := range dataPoints {
			attrs := make(map[string]string)
			for _, kv := range dataPoint.Attributes {
				attrs[kv.Key] = kv.GetValue().GetStringValue()
			}
			got[otlpSeriesKey(metric.Name, attrs)] = dataPoint.GetAsDouble()
		}
	}
	if len(got) != len(want) {
		t.Fatalf("decoded series = %d, want %d", len(got), len(want))
	}
	for key, value := range want {
		decoded, ok := got[key]
		if !ok {
			t.Errorf("decoded series missing %s", key)
			continue
		}
		if decoded != value {
			t.Errorf("%s = %v, want %v", key, decoded, value)
		}
	}

	if _, ok := pointWithAttr(points, schema.SystemCPUUtilization, schema.AttrCPUMode, schema.CPUModeUser); !ok {
		t.Errorf("%s with %s=user missing", schema.SystemCPUUtilization, schema.AttrCPUMode)
	}
	if _, ok := pointWithAttr(points, schema.HWPower, schema.AttrHWID, "gpu"); !ok {
		t.Errorf("%s for gpu missing", schema.HWPower)
	}
	if got := countPoints(points, schema.SystemPagingUtilization); got == 0 {
		t.Errorf("%s points = 0", schema.SystemPagingUtilization)
	}
	foundNoType, foundType := false, false
	for _, point := range points {
		if point.Name != schema.SystemFilesystemUsage {
			continue
		}
		mountpoint, _ := attrValue(point, schema.AttrSystemFilesystemMountpoint)
		fstype, hasType := attrValue(point, schema.AttrSystemFilesystemType)
		if mountpoint == "/" && !hasType {
			foundNoType = true
		}
		if mountpoint == "/Volumes/Backup" && hasType && fstype == schema.SystemFilesystemTypeHfsplus {
			foundType = true
		}
	}
	if !foundNoType {
		t.Errorf("%s for / without %s missing", schema.SystemFilesystemUsage, schema.AttrSystemFilesystemType)
	}
	if !foundType {
		t.Errorf("%s for /Volumes/Backup with hfsplus missing", schema.SystemFilesystemUsage)
	}
}

func otlpAttrs(attrs []schema.Attr) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		out[attr.Key] = attr.Value
	}
	return out
}

func otlpSeriesKey(name string, attrs map[string]string) string {
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	for _, key := range keys {
		b.WriteByte('|')
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(attrs[key])
	}
	return b.String()
}
