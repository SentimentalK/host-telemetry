package linux_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source/linux"
)

const realDump = "../../../testdata/linux/real-00"

func copyDump(t *testing.T) string {
	t.Helper()
	src := os.DirFS(realDump)
	dst := t.TempDir()
	err := fs.WalkDir(src, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, path)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func collectSource(t *testing.T, dir, name string) ([]schema.Point, error) {
	t.Helper()
	statfs, err := linux.FixtureStatfs(os.DirFS(dir), "statfs.tsv")
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range linux.NewLive(os.DirFS(dir), linux.Options{Statfs: statfs}) {
		if src.Name() == name {
			return src.Collect(context.Background())
		}
	}
	t.Fatalf("no source named %q", name)
	return nil, nil
}

func TestLivePartialFailure(t *testing.T) {
	t.Run("missing pressure", func(t *testing.T) {
		dir := copyDump(t)
		if err := os.RemoveAll(filepath.Join(dir, "proc/pressure")); err != nil {
			t.Fatal(err)
		}
		points, err := collectSource(t, dir, "linux-system")
		if err != nil {
			t.Fatalf("collect error: %v", err)
		}
		for _, point := range points {
			if strings.HasPrefix(point.Name, "system.pressure.") {
				t.Errorf("emitted %s without proc/pressure", point.Name)
			}
		}
	})

	t.Run("truncated proc/stat", func(t *testing.T) {
		dir := copyDump(t)
		path := filepath.Join(dir, "proc/stat")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		if err := os.WriteFile(path, []byte(lines[0]+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := collectSource(t, dir, "linux-system"); err == nil {
			t.Fatal("truncated proc/stat did not return an error")
		}
	})

	t.Run("unlimited process limit", func(t *testing.T) {
		dir := copyDump(t)
		path := filepath.Join(dir, "proc/self/limits")
		if err := os.WriteFile(path, []byte("Max processes unlimited unlimited processes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		points, err := collectSource(t, dir, "linux-process")
		if err != nil {
			t.Fatalf("collect error: %v", err)
		}
		for _, point := range points {
			if point.Name == schema.SystemProcessLimit {
				t.Errorf("emitted %s for an unlimited limit", schema.SystemProcessLimit)
			}
		}
	})

	t.Run("unknown filesystem type", func(t *testing.T) {
		dir := copyDump(t)
		path := filepath.Join(dir, "proc/self/mountinfo")
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("999 44 0:99 / /unknown rw - tmpfs tmpfs rw\n"); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		points, err := collectSource(t, dir, "linux-filesystem")
		if err != nil {
			t.Fatalf("collect error: %v", err)
		}
		if len(points) == 0 {
			t.Fatal("no filesystem points emitted")
		}
		for _, point := range points {
			for _, attr := range point.Attrs {
				if attr.Key == schema.AttrSystemFilesystemMountpoint && attr.Value == "/unknown" {
					t.Errorf("emitted %s for an unsupported filesystem type", point.Name)
				}
			}
		}
	})

	t.Run("empty hwmon", func(t *testing.T) {
		dir := copyDump(t)
		if err := os.RemoveAll(filepath.Join(dir, "sys/class/hwmon")); err != nil {
			t.Fatal(err)
		}
		points, err := collectSource(t, dir, "linux-hwmon")
		if err != nil {
			t.Fatalf("collect error: %v", err)
		}
		for _, point := range points {
			switch point.Name {
			case schema.HWTemperature, schema.HWFanSpeed, schema.HWPower:
				t.Errorf("emitted %s without hwmon sensors", point.Name)
			}
		}
	})
}
