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

func Load(root string) ([]source.Source, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("fixtures: %w", err)
	}
	var sources []source.Source
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		src, err := newSource(name, filepath.Join(root, name))
		if err != nil {
			return nil, fmt.Errorf("%w in %s", err, root)
		}
		sources = append(sources, src)
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("fixtures: no platform directories in %s", root)
	}
	return sources, nil
}

func LoadPlatform(root, platform string) (source.Source, error) {
	return newSource(platform, filepath.Join(root, platform))
}

func newSource(platform, dir string) (source.Source, error) {
	switch platform {
	case "linux":
		return linux.New(os.DirFS(dir)), nil
	case "darwin":
		return darwin.New(os.DirFS(dir)), nil
	case "windows":
		return windows.New(os.DirFS(dir)), nil
	default:
		return nil, fmt.Errorf("fixtures: unknown platform %q", platform)
	}
}
