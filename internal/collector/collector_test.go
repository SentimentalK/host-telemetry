package collector_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/clockwatch"
	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

type stubSource struct {
	name     string
	points   []schema.Point
	err      error
	interval time.Duration
}

func (s stubSource) Name() string { return s.name }

func (s stubSource) Interval() time.Duration {
	if s.interval > 0 {
		return s.interval
	}
	return time.Second
}

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

func TestRunEmitsClockJump(t *testing.T) {
	base := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	next := 0
	wall := func() time.Time {
		at := base.Add(time.Duration(next) * 10 * time.Millisecond)
		next++
		if next >= 2 {
			at = at.Add(5 * time.Second)
		}
		return at
	}
	mono := func() time.Time { return base.Add(time.Duration(next-1) * 10 * time.Millisecond) }

	col := collector.New([]source.Source{stubSource{name: "clock-stub", interval: 10 * time.Millisecond}})
	col.Clock = clockwatch.Watch{Wall: wall, Mono: mono, Threshold: time.Second}

	type jump struct {
		source string
		skew   time.Duration
	}
	jumps := make(chan jump, 16)
	col.Jump = func(source string, skew time.Duration) { jumps <- jump{source: source, skew: skew} }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		col.Run(ctx, func(string, []schema.Point, error, time.Duration) {})
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	select {
	case got := <-jumps:
		if got.source != "clock-stub" {
			t.Errorf("Jump source = %q, want clock-stub", got.source)
		}
		if got.skew <= time.Second {
			t.Errorf("Jump skew = %s, want > 1s", got.skew)
		}
	default:
		t.Fatal("Run did not report a detected clock jump")
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

func TestRunSchedulesSources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	good := stubSource{name: "run-good", points: []schema.Point{schema.NewPoint(schema.SystemUptime, 42)}}
	bad := stubSource{name: "run-bad", err: errors.New("boom")}
	col := collector.New([]source.Source{good, bad})

	type result struct {
		name   string
		points []schema.Point
		err    error
		d      time.Duration
	}
	results := make(chan result, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		col.Run(ctx, func(name string, points []schema.Point, err error, d time.Duration) {
			results <- result{name: name, points: points, err: err, d: d}
		})
	}()

	got := map[string]result{}
	for range 2 {
		select {
		case r := <-results:
			got[r.name] = r
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for source collection")
		}
	}

	if r, ok := got["run-good"]; !ok || r.err != nil {
		t.Fatalf("good source result = %+v", r)
	} else if _, ok := findPoint(r.points, schema.SystemUptime, "", ""); !ok {
		t.Errorf("good source missing %s", schema.SystemUptime)
	}
	if r, ok := got["run-bad"]; !ok || r.err == nil {
		t.Fatalf("bad source result = %+v", r)
	} else if _, ok := findPoint(r.points, schema.CollectorSourceError, schema.AttrCollectorSource, "run-bad"); !ok {
		t.Errorf("bad source missing %s", schema.CollectorSourceError)
	}

	durations := col.Stats.Durations()
	if len(durations) != 2 {
		t.Fatalf("durations = %v, want 2 sources", durations)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}

func TestSourceErrorIsCumulative(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bad := stubSource{name: "always-bad", interval: 5 * time.Millisecond, err: errors.New("boom")}
	good := stubSource{name: "always-good", interval: 5 * time.Millisecond, points: []schema.Point{schema.NewPoint(schema.SystemUptime, 1)}}
	col := collector.New([]source.Source{bad, good})

	type result struct {
		name   string
		points []schema.Point
	}
	results := make(chan result, 200)
	done := make(chan struct{})
	go func() {
		defer close(done)
		col.Run(ctx, func(name string, points []schema.Point, err error, d time.Duration) {
			results <- result{name: name, points: points}
		})
	}()

	values := map[string][]float64{}
	for len(values["always-bad"]) < 2 || len(values["always-good"]) < 2 {
		select {
		case r := <-results:
			point, ok := findPoint(r.points, schema.CollectorSourceError, schema.AttrCollectorSource, r.name)
			if !ok {
				t.Fatalf("source %s missing %s", r.name, schema.CollectorSourceError)
			}
			values[r.name] = append(values[r.name], point.Value)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for collections")
		}
	}
	cancel()
	<-done

	badValues := values["always-bad"]
	if badValues[0] != 1 || badValues[1] != 2 {
		t.Fatalf("failing source errors = %v, want 1 then 2", badValues)
	}
	for i, value := range values["always-good"] {
		if value != 0 {
			t.Fatalf("healthy source error[%d] = %v, want 0", i, value)
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
