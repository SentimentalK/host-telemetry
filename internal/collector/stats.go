package collector

import (
	"sync"
	"time"
)

type Stats struct {
	mu        sync.Mutex
	durations map[string]time.Duration
	errors    map[string]uint64
}

func newStats() *Stats {
	return &Stats{
		durations: make(map[string]time.Duration),
		errors:    make(map[string]uint64),
	}
}

func (s *Stats) Record(source string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.durations == nil {
		s.durations = make(map[string]time.Duration)
	}
	s.durations[source] = d
}

func (s *Stats) Durations() map[string]time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]time.Duration, len(s.durations))
	for source, d := range s.durations {
		out[source] = d
	}
	return out
}

func (s *Stats) AddError(source string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errors == nil {
		s.errors = make(map[string]uint64)
	}
	s.errors[source]++
}

func (s *Stats) errorCount(source string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errors[source]
}
