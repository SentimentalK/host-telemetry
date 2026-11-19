package events_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/events"
)

func TestAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	writer := &events.Writer{Path: path}
	at := time.Date(2026, 11, 19, 12, 0, 0, 123456789, time.UTC)

	if err := writer.Append(events.Event{Time: at, Name: events.ClockSkew, JumpMS: 4123}); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	if err := writer.Append(events.Event{Time: at.Add(time.Second), Name: events.ClockSkew, JumpMS: -6000}); err != nil {
		t.Fatalf("second Append: %v", err)
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
		Time   string `json:"time"`
		Name   string `json:"event.name"`
		JumpMS int64  `json:"jump_ms"`
	}
	want := []int64{4123, -6000}
	for i, line := range lines {
		var got record
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if got.Name != events.ClockSkew {
			t.Errorf("line %d: event.name = %q, want %q", i, got.Name, events.ClockSkew)
		}
		if got.JumpMS != want[i] {
			t.Errorf("line %d: jump_ms = %d, want %d", i, got.JumpMS, want[i])
		}
		if _, err := time.Parse(time.RFC3339Nano, got.Time); err != nil {
			t.Errorf("line %d: time %q: %v", i, got.Time, err)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file mode = %04o, want 0600", perm)
	}
}
