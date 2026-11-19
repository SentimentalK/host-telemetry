package events_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/events"
)

func TestNDJSONWritesOneLinePerEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	sink := &events.NDJSON{Path: path}
	at := time.Date(2026, 11, 19, 12, 0, 0, 123456789, time.UTC)

	err := sink.Write([]events.Event{
		{Time: at, Name: events.ClockSkew, Attrs: []events.Attr{{Key: "jump_ms", Value: "2000"}}, Subject: "clock"},
		{Time: at.Add(time.Second), Name: events.DiskFull, Attrs: []events.Attr{
			{Key: "mountpoint", Value: "/"},
			{Key: "used_ratio", Value: "0.960000"},
		}, Subject: "/"},
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), data)
	}

	type record struct {
		Time       string            `json:"time"`
		Name       string            `json:"event.name"`
		Attributes map[string]string `json:"attributes"`
	}
	want := []struct {
		name  string
		attrs map[string]string
	}{
		{events.ClockSkew, map[string]string{"jump_ms": "2000"}},
		{events.DiskFull, map[string]string{"mountpoint": "/", "used_ratio": "0.960000"}},
	}
	for i, line := range lines {
		var got record
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if got.Name != want[i].name {
			t.Errorf("line %d: event.name = %q, want %q", i, got.Name, want[i].name)
		}
		if len(got.Attributes) != len(want[i].attrs) {
			t.Errorf("line %d: attributes = %v, want %v", i, got.Attributes, want[i].attrs)
		}
		for key, value := range want[i].attrs {
			if got.Attributes[key] != value {
				t.Errorf("line %d: attributes[%q] = %q, want %q", i, key, got.Attributes[key], value)
			}
		}
		if _, err := time.Parse(time.RFC3339Nano, got.Time); err != nil {
			t.Errorf("line %d: time %q: %v", i, got.Time, err)
		}
	}

	if first, second := strings.Index(lines[1], `"mountpoint"`), strings.Index(lines[1], `"used_ratio"`); first < 0 || second < 0 || first > second {
		t.Errorf("attributes not sorted by key: %s", lines[1])
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file mode = %04o, want 0600", perm)
	}
}

type recordingSink struct {
	mu     sync.Mutex
	events []events.Event
	err    error
}

func (s *recordingSink) Write(batch []events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, batch...)
	return nil
}

func (s *recordingSink) Close() error {
	return nil
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func TestPipelineDeduplicatesWithinWindow(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	current := now
	sink := &recordingSink{}
	pipeline := &events.Pipeline{
		Now:    func() time.Time { return current },
		Sinks:  []events.Sink{sink},
		Logf:   func(string, ...any) {},
		Window: 5 * time.Minute,
	}
	ev := events.Event{Time: now, Name: events.ClockSkew, Subject: "clock", Attrs: []events.Attr{{Key: "jump_ms", Value: "2000"}}}

	pipeline.Emit(ev)
	pipeline.Emit(ev)
	if got := sink.count(); got != 1 {
		t.Fatalf("events within window = %d, want 1", got)
	}

	current = now.Add(6 * time.Minute)
	pipeline.Emit(ev)
	if got := sink.count(); got != 2 {
		t.Fatalf("events after window = %d, want 2", got)
	}
}

func TestPipelineDeduplicatesByNameAndSubject(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	sink := &recordingSink{}
	pipeline := &events.Pipeline{
		Now:   func() time.Time { return now },
		Sinks: []events.Sink{sink},
		Logf:  func(string, ...any) {},
	}
	ev := events.Event{Time: now, Name: events.ClockSkew, Subject: "clock"}

	pipeline.Emit(ev)
	other := ev
	other.Subject = "other"
	pipeline.Emit(other)
	renamed := ev
	renamed.Name = events.DiskFull
	pipeline.Emit(renamed)
	if got := sink.count(); got != 3 {
		t.Fatalf("events with distinct name or subject = %d, want 3", got)
	}
}

func TestPipelineKeepsWritingAfterSinkFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	var logged int
	pipeline := &events.Pipeline{
		Now:   func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) },
		Sinks: []events.Sink{&recordingSink{err: errors.New("closed port")}, &events.NDJSON{Path: path}},
		Logf:  func(string, ...any) { logged++ },
	}

	pipeline.Emit(events.Event{Time: time.Now(), Name: events.ClockSkew, Subject: "clock", Attrs: []events.Attr{{Key: "jump_ms", Value: "2000"}}})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "\n"); lines != 1 {
		t.Fatalf("ndjson lines = %d, want 1:\n%s", lines, data)
	}
	if logged != 1 {
		t.Fatalf("sink failures logged = %d, want 1", logged)
	}
}

func TestOTLPSinkNilStoreIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	sink := &events.OTLPSink{}
	if err := sink.Write([]events.Event{{Time: time.Now(), Name: events.ClockSkew}}); err != nil {
		t.Fatalf("nil store Write: %v", err)
	}
	pipeline := &events.Pipeline{
		Now:   time.Now,
		Sinks: []events.Sink{sink, &events.NDJSON{Path: path}},
		Logf:  func(string, ...any) {},
	}
	pipeline.Emit(events.Event{Time: time.Now(), Name: events.ClockSkew, Subject: "clock"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "\n"); lines != 1 {
		t.Fatalf("ndjson lines = %d, want 1:\n%s", lines, data)
	}
}

func TestPipelineCloseAggregatesErrors(t *testing.T) {
	first := &closeSink{err: errors.New("first")}
	second := &closeSink{}
	err := (&events.Pipeline{Sinks: []events.Sink{first, second}}).Close()
	if !errors.Is(err, first.err) {
		t.Fatalf("Close = %v, want %v", err, first.err)
	}
	if !first.closed || !second.closed {
		t.Fatalf("closed = %v/%v, want both", first.closed, second.closed)
	}
}

type closeSink struct {
	err    error
	closed bool
}

func (s *closeSink) Write([]events.Event) error {
	return nil
}

func (s *closeSink) Close() error {
	s.closed = true
	return s.err
}
