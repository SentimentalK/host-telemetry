package collector_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

type stubSource struct {
	name   string
	points []schema.Point
	err    error
}

func (s stubSource) Name() string { return s.name }

func (s stubSource) Interval() time.Duration { return time.Second }

func (s stubSource) Collect(context.Context) ([]schema.Point, error) {
	return s.points, s.err
}

func TestPartialFailure(t *testing.T) {
	good := stubSource{name: "good", points: []schema.Point{schema.NewPoint(schema.SystemUptime, 42)}}
	bad := stubSource{name: "broken-stub", err: errors.New("fixture unreadable")}
	result := collector.New([]source.Source{good, bad}).CollectOnce(context.Background())

	if len(result.Errors) != 1 || result.Errors[0].Source != "broken-stub" {
		t.Fatalf("errors = %v, want one for broken-stub", result.Errors)
	}
	if _, ok := findPoint(result.Points, schema.SystemUptime, "", ""); !ok {
		t.Errorf("missing surviving %s point", schema.SystemUptime)
	}
	point, ok := findPoint(result.Points, schema.CollectorSourceError, schema.AttrCollectorSource, "broken-stub")
	if !ok {
		t.Fatalf("missing %s{%s=broken-stub}", schema.CollectorSourceError, schema.AttrCollectorSource)
	}
	if point.Value != 1 {
		t.Errorf("%s = %v, want 1", schema.CollectorSourceError, point.Value)
	}
}

func TestMetricsText(t *testing.T) {
	points := []schema.Point{
		schema.NewPoint(schema.SystemUptime, 42.5),
		schema.NewPoint(schema.SystemCPUUtilization, 0.25, schema.Attr{Key: schema.AttrCPUMode, Value: schema.CPUModeUser}),
	}
	handler := collector.MetricsHandler(func() []schema.Point { return points })
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()

	for _, want := range []string{
		"# HELP system_uptime OTel metric system.uptime; unit s\n",
		"# TYPE system_uptime gauge\n",
		"system_uptime 42.5\n",
		"# HELP system_cpu_utilization OTel metric system.cpu.utilization; unit 1\n",
		"# TYPE system_cpu_utilization gauge\n",
		"system_cpu_utilization{cpu_mode=\"user\"} 0.25\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q\n%s", want, body)
		}
	}
}

func findPoint(points []schema.Point, name, attrKey, attrValue string) (schema.Point, bool) {
	for _, point := range points {
		if point.Name != name {
			continue
		}
		if attrKey == "" {
			return point, true
		}
		for _, attr := range point.Attrs {
			if attr.Key == attrKey && (attrValue == "" || attr.Value == attrValue) {
				return point, true
			}
		}
	}
	return schema.Point{}, false
}
