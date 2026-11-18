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
		dir := filepath.Join(root, name)
		switch name {
		case "linux":
			sources = append(sources, linux.New(os.DirFS(dir)))
		case "darwin":
			sources = append(sources, darwin.New(os.DirFS(dir)))
		case "windows":
			sources = append(sources, windows.New(os.DirFS(dir)))
		default:
			return nil, fmt.Errorf("fixtures: unknown platform directory %q in %s", name, root)
		}
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("fixtures: no platform directories in %s", root)
	}
	return sources, nil
}
