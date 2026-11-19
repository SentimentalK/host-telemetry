package clockwatch

import (
	"sync"
	"time"
)

const DefaultThreshold = time.Second

type Watch struct {
	Wall      func() time.Time
	Mono      func() time.Time
	Threshold time.Duration

	mu       sync.Mutex
	primed   bool
	prevWall time.Time
	prevMono time.Time
}

func (w *Watch) Step() (time.Duration, bool) {
	wall := w.wall()
	mono := w.mono()

	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.primed {
		w.prevWall, w.prevMono, w.primed = wall, mono, true
		return 0, false
	}

	skew := wall.Sub(w.prevWall) - mono.Sub(w.prevMono)
	w.prevWall, w.prevMono = wall, mono

	threshold := w.Threshold
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	return skew, abs(skew) > threshold
}

func (w *Watch) wall() time.Time {
	if w.Wall != nil {
		return w.Wall()
	}
	return time.Now()
}

func (w *Watch) mono() time.Time {
	if w.Mono != nil {
		return w.Mono()
	}
	return time.Now()
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
