package collector

import (
	"sync"
	"time"
)

type Stats struct {
	mu        sync.Mutex
	durations map[string]time.Duration
}

func newStats() *Stats {
	return &Stats{durations: make(map[string]time.Duration)}
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
