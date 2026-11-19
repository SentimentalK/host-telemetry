package linux

import (
	"io/fs"
	"syscall"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/source"
)

type Options struct {
	Now       func() time.Time
	Statfs    func(mountpoint string) (Statfs, error)
	Whitelist []string
	TopN      int
}

type Statfs struct {
	Blocks uint64
	Bfree  uint64
	Bavail uint64
	Bsize  uint64
}

func NewLive(root fs.FS, opts Options) []source.Source {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Statfs == nil {
		opts.Statfs = readStatfs
	}
	if opts.TopN <= 0 {
		opts.TopN = 32
	}
	return []source.Source{
		&systemSource{root: root, now: opts.Now},
		&filesystemSource{root: root, statfs: opts.Statfs},
		&processSource{root: root, whitelist: append([]string(nil), opts.Whitelist...), topN: opts.TopN},
		&hwmonSource{root: root},
		&inventorySource{root: root},
	}
}

func readStatfs(mountpoint string) (Statfs, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(mountpoint, &st); err != nil {
		return Statfs{}, err
	}
	return Statfs{
		Blocks: uint64(st.Blocks),
		Bfree:  uint64(st.Bfree),
		Bavail: uint64(st.Bavail),
		Bsize:  uint64(st.Bsize),
	}, nil
}
