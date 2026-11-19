package events

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/otlp"
	"github.com/SentimentalK/host-telemetry/internal/wal"
)

const (
	ClockSkew       = "host.clock.skew"
	OOMKill         = "host.oom.kill"
	DiskFull        = "host.disk.full"
	ThermalThrottle = "host.thermal.throttle"
)

type Attr struct {
	Key   string
	Value string
}

type Event struct {
	Time    time.Time
	Name    string
	Attrs   []Attr
	Subject string
}

type Sink interface {
	Write([]Event) error
	Close() error
}

type NDJSON struct {
	Path string
}

type ndjsonRecord struct {
	Time       string            `json:"time"`
	Name       string            `json:"event.name"`
	Attributes map[string]string `json:"attributes"`
}

func (n *NDJSON) Write(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	lines := make([]byte, 0, 256*len(events))
	for _, ev := range events {
		attributes := make(map[string]string, len(ev.Attrs))
		for _, attr := range ev.Attrs {
			attributes[attr.Key] = attr.Value
		}
		line, err := json.Marshal(ndjsonRecord{
			Time:       ev.Time.Format(time.RFC3339Nano),
			Name:       ev.Name,
			Attributes: attributes,
		})
		if err != nil {
			return err
		}
		lines = append(lines, line...)
		lines = append(lines, '\n')
	}

	file, err := os.OpenFile(n.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(lines); err != nil {
		return err
	}
	return file.Sync()
}

func (n *NDJSON) Close() error {
	return nil
}

type OTLPSink struct {
	Store    *wal.Store
	Resource otlp.Resource
	Now      func() time.Time
}

func (s *OTLPSink) Write(events []Event) error {
	if s.Store == nil || len(events) == 0 {
		return nil
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	at := now()
	records := make([]otlp.LogRecord, 0, len(events))
	for _, ev := range events {
		attrs := make([]otlp.Attribute, 0, len(ev.Attrs))
		for _, attr := range ev.Attrs {
			attrs = append(attrs, otlp.Attribute{Key: attr.Key, Value: attr.Value})
		}
		records = append(records, otlp.LogRecord{Time: ev.Time, Name: ev.Name, Attrs: attrs})
	}
	payload, err := otlp.EncodeLogs(records, s.Resource, at)
	if err != nil {
		return err
	}
	_, err = s.Store.Append(at, len(events), payload)
	return err
}

func (s *OTLPSink) Close() error {
	return nil
}

type Pipeline struct {
	Now    func() time.Time
	Window time.Duration
	Sinks  []Sink
	Logf   func(string, ...any)

	mu   sync.Mutex
	last map[string]time.Time
}

func (p *Pipeline) Emit(ev Event) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.last == nil {
		p.last = make(map[string]time.Time)
	}
	now := p.Now
	if now == nil {
		now = time.Now
	}
	at := now()
	window := p.Window
	if window <= 0 {
		window = 5 * time.Minute
	}
	key := ev.Name + "\x00" + ev.Subject
	if last, ok := p.last[key]; ok && at.Sub(last) < window {
		return
	}
	p.last[key] = at

	logf := p.Logf
	if logf == nil {
		logf = log.Printf
	}
	batch := []Event{ev}
	for _, sink := range p.Sinks {
		if err := sink.Write(batch); err != nil {
			logf("events: sink: %v", err)
		}
	}
}

func (p *Pipeline) Close() error {
	errs := make([]error, 0)
	for _, sink := range p.Sinks {
		if err := sink.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
