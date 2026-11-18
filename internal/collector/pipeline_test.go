package collector_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/linux"
)

func TestUnknownPointNameIsRejected(t *testing.T) {
	bad := stubSource{name: "bad-name", points: []schema.Point{{Name: "system.cpu.percent", Unit: "1", Value: 1}}}
	result := collector.New([]source.Source{bad}).CollectOnce(context.Background())

	if len(result.Errors) == 0 {
		t.Fatal("collector accepted system.cpu.percent")
	}
	named := false
	for _, sourceErr := range result.Errors {
		if strings.Contains(sourceErr.Err.Error(), "system.cpu.percent") {
			named = true
		}
	}
	if !named {
		t.Errorf("errors = %v, want one naming system.cpu.percent", result.Errors)
	}
	for _, point := range result.Points {
		if point.Name == "system.cpu.percent" {
			t.Errorf("system.cpu.percent survived collection")
		}
	}
}

func TestLinuxParserRejectsCorruptStatFixture(t *testing.T) {
	samples := copyTestdata(t, filepath.Join("..", "..", "testdata", "linux"))
	samples["sample-01/proc/stat"] = &fstest.MapFile{Data: []byte("cpu 1 2 3 broken 5 6 7 8\ncpu0 1 2 3 4 5 6 7 8\n")}

	_, err := linux.New(samples).Collect(context.Background())
	if err == nil {
		t.Fatal("linux parser accepted corrupt proc/stat")
	}
	for _, want := range []string{"sample-01/proc/stat", "broken"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}

func copyTestdata(t *testing.T, root string) fstest.MapFS {
	t.Helper()
	disk := os.DirFS(root)
	samples := fstest.MapFS{}
	err := fs.WalkDir(disk, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(disk, path)
		if err != nil {
			return err
		}
		samples[path] = &fstest.MapFile{Data: data}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := samples["sample-01/proc/stat"]; !ok {
		t.Fatalf("%s: missing sample-01/proc/stat", root)
	}
	return samples
}
