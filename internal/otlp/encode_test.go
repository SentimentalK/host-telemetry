package otlp

import (
	"bytes"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/SentimentalK/host-telemetry/internal/schema"
)

func TestEnvelope(t *testing.T) {
	rm := []byte("resource-metrics")
	body := envelope(rm)
	if len(body) == 0 {
		t.Fatal("empty envelope")
	}
	if body[0] != 0x0a {
		t.Fatalf("first byte = %#x, want 0x0a", body[0])
	}
	field, typ, tagLen := protowire.ConsumeTag(body)
	if field != 1 || typ != protowire.BytesType {
		t.Fatalf("tag = (%d, %v), want (1, bytes)", field, typ)
	}
	value, valueLen := protowire.ConsumeBytes(body[tagLen:])
	if !bytes.Equal(value, rm) {
		t.Fatalf("envelope value = %q, want %q", value, rm)
	}
	if tagLen+valueLen != len(body) {
		t.Fatalf("consumed %d of %d bytes", tagLen+valueLen, len(body))
	}
}

func TestEncode(t *testing.T) {
	at := time.Unix(100, 5)
	start := time.Unix(50, 0)
	points := []schema.Point{
		schema.NewPoint(schema.SystemUptime, 12.5),
		schema.NewPoint(schema.SystemCPUTime, 1.5,
			schema.Attr{Key: schema.AttrCPUMode, Value: schema.CPUModeUser},
			schema.Attr{Key: schema.AttrCPULogicalNumber, Value: "0"},
		),
		schema.NewPoint(schema.CollectorSelfMemoryUsage, 2048),
		schema.NewPoint(schema.CollectorWALDropped, 3),
	}
	res := Resource{HostID: "host-1", HostName: "box", HostArch: "amd64", OSType: "linux", ServiceName: "host-telemetry", SchemaVersion: "v1.45.0"}

	body, err := Encode(points, at, start, res)
	if err != nil {
		t.Fatal(err)
	}
	_, _, tagLen := protowire.ConsumeTag(body)
	rmBytes, _ := protowire.ConsumeBytes(body[tagLen:])
	var rm metricspb.ResourceMetrics
	if err := proto.Unmarshal(rmBytes, &rm); err != nil {
		t.Fatal(err)
	}

	var hostID string
	for _, attr := range rm.Resource.Attributes {
		if attr.Key == "host.id" {
			hostID = attr.Value.GetStringValue()
		}
		if attr.Key == "os.description" {
			t.Fatalf("empty os.description was sent")
		}
	}
	if hostID != "host-1" {
		t.Fatalf("host.id = %q, want host-1", hostID)
	}
	if len(rm.ScopeMetrics) != 1 {
		t.Fatalf("scope metrics = %d, want 1", len(rm.ScopeMetrics))
	}
	metrics := rm.ScopeMetrics[0].Metrics
	wantNames := []string{schema.CollectorSelfMemoryUsage, schema.CollectorWALDropped, schema.SystemCPUTime, schema.SystemUptime}
	if len(metrics) != len(wantNames) {
		t.Fatalf("metrics = %d, want %d", len(metrics), len(wantNames))
	}
	for i, name := range wantNames {
		if metrics[i].Name != name {
			t.Errorf("metric %d = %q, want %q", i, metrics[i].Name, name)
		}
	}

	uptime := metrics[3]
	if uptime.Unit != "s" || uptime.GetGauge() == nil {
		t.Fatalf("system.uptime unit/kind = %q/%T", uptime.Unit, uptime.Data)
	}
	up := uptime.GetGauge().DataPoints[0]
	if up.TimeUnixNano != uint64(at.UnixNano()) || up.StartTimeUnixNano != 0 {
		t.Errorf("gauge timestamps = %d/%d", up.TimeUnixNano, up.StartTimeUnixNano)
	}
	if up.GetAsDouble() != 12.5 {
		t.Errorf("system.uptime value = %v", up.GetAsDouble())
	}

	cpu := metrics[2]
	if cpu.Unit != "s" || cpu.GetSum() == nil || !cpu.GetSum().IsMonotonic {
		t.Fatalf("system.cpu.time sum/monotonic = %v/%v", cpu.GetSum(), cpu.GetSum().GetIsMonotonic())
	}
	if cpu.GetSum().AggregationTemporality != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
		t.Errorf("temporality = %v", cpu.GetSum().AggregationTemporality)
	}
	dp := cpu.GetSum().DataPoints[0]
	if dp.TimeUnixNano != uint64(at.UnixNano()) || dp.StartTimeUnixNano != uint64(start.UnixNano()) {
		t.Errorf("sum timestamps = %d/%d", dp.TimeUnixNano, dp.StartTimeUnixNano)
	}
	if len(dp.Attributes) != 2 || dp.Attributes[0].Key != schema.AttrCPULogicalNumber || dp.Attributes[1].Key != schema.AttrCPUMode {
		t.Fatalf("cpu attrs = %v", attrKeys(dp.Attributes))
	}
	if dp.Attributes[0].Value.GetStringValue() != "0" || dp.Attributes[1].Value.GetStringValue() != schema.CPUModeUser {
		t.Errorf("cpu attr values = %q/%q", dp.Attributes[0].Value.GetStringValue(), dp.Attributes[1].Value.GetStringValue())
	}

	mem := metrics[0]
	if mem.GetSum() == nil || mem.GetSum().IsMonotonic {
		t.Errorf("updowncounter monotonic = %v", mem.GetSum().GetIsMonotonic())
	}
	if mem.GetSum().DataPoints[0].GetAsDouble() != 2048 {
		t.Errorf("memory value = %v", mem.GetSum().DataPoints[0].GetAsDouble())
	}
}

func attrKeys(attrs []*commonpb.KeyValue) []string {
	keys := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		keys = append(keys, attr.Key)
	}
	return keys
}
