package linux

import "time"

type Options struct {
	Now    func() time.Time
	Statfs func(mountpoint string) (Statfs, error)
}

type Statfs struct {
	Blocks uint64
	Bfree  uint64
	Bavail uint64
	Bsize  uint64
}
