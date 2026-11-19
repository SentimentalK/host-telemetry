//go:build linux

package linux

import (
	"io/fs"
	"syscall"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/source"
)

func NewLive(root fs.FS, opts Options) []source.Source {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Statfs == nil {
		opts.Statfs = readStatfs
	}
	return []source.Source{
		&systemSource{root: root, now: opts.Now},
		&uptimeSource{root: root},
		&filesystemSource{root: root, statfs: opts.Statfs},
		&processSource{root: root},
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
