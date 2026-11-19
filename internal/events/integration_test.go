package events_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/events"
	"github.com/SentimentalK/host-telemetry/internal/otlp"
	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/wal"
)

func resourceLogs(payload []byte) (*logspb.ResourceLogs, error) {
	field, typ, tagLen := protowire.ConsumeTag(payload)
	if field != 1 || typ != protowire.BytesType {
		return nil, errors.New("missing envelope")
	}
	body, bodyLen := protowire.ConsumeBytes(payload[tagLen:])
	if tagLen+bodyLen != len(payload) {
		return nil, errors.New("trailing envelope bytes")
	}
	var logs logspb.ResourceLogs
	if err := proto.Unmarshal(body, &logs); err != nil {
		return nil, err
	}
	return &logs, nil
}

func TestPipelineSendsClockSkewAsOTLPLogs(t *testing.T) {
	dir := t.TempDir()
	store, err := wal.Open(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	type result struct {
		logs *logspb.ResourceLogs
		body []byte
		err  error
	}
	received := make(chan result, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			received <- result{err: err}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			received <- result{err: err}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		logs, err := resourceLogs(body)
		received <- result{logs: logs, body: body, err: err}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	now := time.Date(2026, 11, 19, 12, 0, 0, 500000000, time.UTC)
	resource := otlp.Resource{HostID: "host-123", ServiceName: "host-telemetry", ServiceVersion: "dev"}
	sink := &events.OTLPSink{Store: store, Resource: resource, Now: func() time.Time { return now }}
	pipeline := &events.Pipeline{
		Now:   func() time.Time { return now },
		Sinks: []events.Sink{sink},
		Logf:  func(string, ...any) {},
	}
	pipeline.Emit(events.Event{
		Time:    now,
		Name:    events.ClockSkew,
		Attrs:   []events.Attr{{Key: "jump_ms", Value: "2000"}},
		Subject: "clock",
	})

	batch, ok, err := store.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("wal has no batch after Emit")
	}
	if batch.Samples != 1 {
		t.Fatalf("batch samples = %d, want 1", batch.Samples)
	}

	requestPath := filepath.Join(t.TempDir(), "clock-skew-request.bin")
	if path := os.Getenv("HT_EVENT_DUMP"); path != "" {
		requestPath = path
	}
	if err := os.MkdirAll(filepath.Dir(requestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath, batch.Payload, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("clock skew request written to %s", requestPath)

	client := &otlp.Client{
		Config: otlp.Config{Endpoint: server.URL},
		Sleep:  func(context.Context, time.Duration) error { return nil },
	}
	if err := client.Send(context.Background(), batch.Payload); err != nil {
		t.Fatal(err)
	}

	var got result
	select {
	case got = <-received:
	case <-time.After(10 * time.Second):
		t.Fatal("server did not receive a request")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	if !bytes.Equal(got.body, batch.Payload) {
		t.Fatal("server body differs from wal payload")
	}

	records := got.logs.GetScopeLogs()
	if len(records) != 1 || len(records[0].GetLogRecords()) != 1 {
		t.Fatalf("log records = %+v, want 1", records)
	}
	record := records[0].GetLogRecords()[0]
	if record.GetEventName() != events.ClockSkew {
		t.Errorf("event_name = %q, want %q", record.GetEventName(), events.ClockSkew)
	}
	attrs := make(map[string]string)
	for _, attr := range record.GetAttributes() {
		attrs[attr.Key] = attr.GetValue().GetStringValue()
	}
	if attrs["jump_ms"] != "2000" {
		t.Errorf("jump_ms = %q, want 2000", attrs["jump_ms"])
	}
	hostID := ""
	for _, attr := range got.logs.GetResource().GetAttributes() {
		if attr.Key == "host.id" {
			hostID = attr.GetValue().GetStringValue()
		}
	}
	if hostID != "host-123" {
		t.Errorf("resource host.id = %q, want host-123", hostID)
	}
}

type staticSource struct {
	name   string
	points []schema.Point
}

func (s staticSource) Name() string {
	return s.name
}

func (s staticSource) Interval() time.Duration {
	return time.Minute
}

func (s staticSource) Collect(context.Context) ([]schema.Point, error) {
	return s.points, nil
}

func TestEventsStayOutOfMetrics(t *testing.T) {
	eventNames := []string{events.ClockSkew, events.OOMKill, events.DiskFull, events.ThermalThrottle}
	result := collector.New([]source.Source{
		staticSource{name: "static", points: []schema.Point{schema.NewPoint(schema.SystemUptime, 42)}},
	}).CollectOnce(context.Background())
	if len(result.Errors) != 0 {
		t.Fatalf("collect errors = %v", result.Errors)
	}
	for _, point := range result.Points {
		for _, name := range eventNames {
			if point.Name == name {
				t.Errorf("event %q leaked into collected points", name)
			}
		}
		if point.Name == schema.CollectorSourceError {
			t.Errorf("collector.source.error raised while only collecting metrics")
		}
	}

	recorder := httptest.NewRecorder()
	collector.MetricsHandler(func() []schema.Point { return result.Points })(
		recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", recorder.Code)
	}
	output := recorder.Body.String()
	for _, name := range eventNames {
		if strings.Contains(output, name) {
			t.Errorf("/metrics contains event name %q", name)
		}
	}
}

func TestSinkFailureKeepsNDJSONAndMetrics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	pipeline := &events.Pipeline{
		Now:   func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) },
		Sinks: []events.Sink{&events.OTLPSink{}, &recordingSink{err: errors.New("connection refused")}, &events.NDJSON{Path: path}},
		Logf:  func(string, ...any) {},
	}
	pipeline.Emit(events.Event{Time: time.Now(), Name: events.ClockSkew, Subject: "clock", Attrs: []events.Attr{{Key: "jump_ms", Value: "2000"}}})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSuffix(data, []byte("\n")), &record); err != nil {
		t.Fatalf("ndjson: %v\n%s", err, data)
	}

	recorder := httptest.NewRecorder()
	collector.MetricsHandler(func() []schema.Point { return nil })(
		recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", recorder.Code)
	}
}
