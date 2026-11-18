package source_test

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/fixtures"
	"github.com/SentimentalK/host-telemetry/internal/source/linux"
)

func TestLinuxRejectsUnsupportedMeminfoUnit(t *testing.T) {
	samples := copyFixtures(t, filepath.Join(testdataRoot, "linux"))
	meminfo := samples["sample-01/proc/meminfo"].Data
	corrupt := bytes.Replace(meminfo, []byte(" kB"), []byte(" MB"), 1)
	if bytes.Equal(corrupt, meminfo) {
		t.Fatal("sample-01/proc/meminfo has no kB unit to corrupt")
	}
	samples["sample-01/proc/meminfo"] = &fstest.MapFile{Data: corrupt}

	_, err := linux.New(samples).Collect(context.Background())
	if err == nil {
		t.Fatal("linux parser accepted MemTotal in MB")
	}
	for _, want := range []string{"sample-01/proc/meminfo", `"MB"`, "MemTotal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}

func TestRenderedMetricsHaveRequiredNames(t *testing.T) {
	const (
		uptimeHelp = "# HELP system_uptime OTel metric system.uptime; unit s"
		cpuHelp    = "# HELP system_cpu_utilization OTel metric system.cpu.utilization; unit 1"
	)
	cases := []struct {
		platform string
		wants    []string
	}{
		{"linux", []string{uptimeHelp, cpuHelp}},
		{"darwin", []string{uptimeHelp}},
		{"windows", []string{cpuHelp}},
	}
	for _, tc := range cases {
		t.Run(tc.platform, func(t *testing.T) {
			src, err := fixtures.LoadPlatform(testdataRoot, tc.platform)
			if err != nil {
				t.Fatal(err)
			}
			result := collector.New([]source.Source{src}).CollectOnce(context.Background())
			if len(result.Errors) != 0 {
				t.Fatalf("%s: collect errors: %v", tc.platform, result.Errors)
			}
			var out bytes.Buffer
			if err := collector.WriteMetrics(&out, result.Points); err != nil {
				t.Fatal(err)
			}
			text := out.String()
			for _, want := range tc.wants {
				if !strings.Contains(text, want) {
					t.Errorf("%s: rendered metrics missing %q", tc.platform, want)
				}
			}
			for _, line := range strings.Split(text, "\n") {
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if strings.Contains(line, "NaN") || strings.Contains(line, "+Inf") {
					t.Errorf("%s: sample line is not finite: %q", tc.platform, line)
				}
			}
		})
	}
}

func copyFixtures(t *testing.T, root string) fstest.MapFS {
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
	if _, ok := samples["sample-01/proc/meminfo"]; !ok {
		t.Fatalf("%s: missing sample-01/proc/meminfo", root)
	}
	return samples
}
