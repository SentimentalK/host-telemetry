package otlp

import (
	"testing"
	"time"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestEncodeLogs(t *testing.T) {
	at := time.Date(2026, 11, 19, 12, 0, 0, 987654321, time.UTC)
	first := time.Date(2026, 11, 19, 11, 59, 0, 1, time.UTC)
	second := time.Date(2026, 11, 19, 11, 59, 30, 2, time.UTC)
	res := Resource{HostID: "host-123", ServiceVersion: "dev"}

	payload, err := EncodeLogs([]LogRecord{
		{Time: first, Name: "host.clock.skew", Attrs: []Attribute{{Key: "zeta", Value: "z"}, {Key: "alpha", Value: "a"}}},
		{Time: second, Name: "host.disk.full", Attrs: []Attribute{{Key: "mountpoint", Value: "/"}}},
	}, res, at)
	if err != nil {
		t.Fatal(err)
	}

	if len(payload) == 0 || payload[0] != 0x0a {
		t.Fatalf("payload does not start with the envelope tag: %x", payload)
	}
	field, typ, tagLen := protowire.ConsumeTag(payload)
	if field != 1 || typ != protowire.BytesType {
		t.Fatalf("envelope tag = (%d, %v), want (1, bytes)", field, typ)
	}
	body, bodyLen := protowire.ConsumeBytes(payload[tagLen:])
	if tagLen+bodyLen != len(payload) {
		t.Fatalf("envelope has %d trailing bytes", len(payload)-tagLen-bodyLen)
	}

	var logs logspb.ResourceLogs
	if err := proto.Unmarshal(body, &logs); err != nil {
		t.Fatal(err)
	}

	hostID := ""
	for _, attr := range logs.GetResource().GetAttributes() {
		if attr.Key == "host.id" {
			hostID = attr.GetValue().GetStringValue()
		}
	}
	if hostID != "host-123" {
		t.Fatalf("resource host.id = %q, want host-123", hostID)
	}

	scopeLogs := logs.GetScopeLogs()
	if len(scopeLogs) != 1 {
		t.Fatalf("scope logs = %d, want 1", len(scopeLogs))
	}
	if scope := scopeLogs[0].GetScope(); scope.GetName() != "host-telemetry" || scope.GetVersion() != "dev" {
		t.Fatalf("scope = %q/%q, want host-telemetry/dev", scope.GetName(), scope.GetVersion())
	}
	records := scopeLogs[0].GetLogRecords()
	if len(records) != 2 {
		t.Fatalf("log records = %d, want 2", len(records))
	}

	want := []struct {
		name  string
		time  time.Time
		attrs []Attribute
	}{
		{"host.clock.skew", first, []Attribute{{Key: "alpha", Value: "a"}, {Key: "zeta", Value: "z"}}},
		{"host.disk.full", second, []Attribute{{Key: "mountpoint", Value: "/"}}},
	}
	for i, record := range records {
		if record.GetEventName() != want[i].name {
			t.Errorf("record %d: EventName = %q, want %q", i, record.GetEventName(), want[i].name)
		}
		if got := int64(record.GetTimeUnixNano()); got != want[i].time.UnixNano() {
			t.Errorf("record %d: TimeUnixNano = %d, want %d", i, got, want[i].time.UnixNano())
		}
		if got := int64(record.GetObservedTimeUnixNano()); got != at.UnixNano() {
			t.Errorf("record %d: ObservedTimeUnixNano = %d, want %d", i, got, at.UnixNano())
		}
		if record.GetSeverityNumber() != logspb.SeverityNumber_SEVERITY_NUMBER_INFO {
			t.Errorf("record %d: severity = %v, want INFO", i, record.GetSeverityNumber())
		}
		if record.GetSeverityText() != "INFO" {
			t.Errorf("record %d: severity text = %q, want INFO", i, record.GetSeverityText())
		}
		attrs := record.GetAttributes()
		if len(attrs) != len(want[i].attrs) {
			t.Fatalf("record %d: attributes = %d, want %d", i, len(attrs), len(want[i].attrs))
		}
		for j, attr := range attrs {
			if attr.Key != want[i].attrs[j].Key || attr.GetValue().GetStringValue() != want[i].attrs[j].Value {
				t.Errorf("record %d: attribute %d = %s=%q, want %s=%q", i, j,
					attr.Key, attr.GetValue().GetStringValue(), want[i].attrs[j].Key, want[i].attrs[j].Value)
			}
		}
	}
}
