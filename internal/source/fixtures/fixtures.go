package fixtures

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/darwin"
	"github.com/SentimentalK/host-telemetry/internal/source/linux"
	"github.com/SentimentalK/host-telemetry/internal/source/windows"
)

func LoadPlatform(root, platform string) ([]source.Source, error) {
	return newSources(platform, filepath.Join(root, platform))
}

func newSources(platform, dir string) ([]source.Source, error) {
	switch platform {
	case "linux":
		return []source.Source{linux.New(os.DirFS(dir))}, nil
	case "darwin":
		return darwin.New(os.DirFS(dir)), nil
	case "windows":
		return []source.Source{windows.New(os.DirFS(dir))}, nil
	default:
		return nil, fmt.Errorf("fixtures: unknown platform %q", platform)
	}
}
