//go:build !linux

package linux

import (
	"io/fs"

	"github.com/SentimentalK/host-telemetry/internal/source"
)

func NewLive(root fs.FS, opts Options) []source.Source { return nil }
