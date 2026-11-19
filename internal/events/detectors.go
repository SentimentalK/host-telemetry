package events

import (
	"io/fs"
	"time"
)

type Detector interface {
	Name() string
	Poll(now time.Time) []Event
}

type Statfs struct {
	Blocks uint64
	Bfree  uint64
}

type DetectorOptions struct {
	FS              fs.FS
	Journal         func(since time.Time) ([]byte, error)
	Statfs          func(string) (Statfs, error)
	DiskFullRatio   float64
	JournalLookback time.Duration
}
