package schema

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	upstreamModelRoot = "model/upstream"
	projectModelPath  = "model/project.yaml"
)

var (
	metricNameLine = regexp.MustCompile(`^\s*metric_name:\s*(\S+)\s*$`)
	dashNameLine   = regexp.MustCompile(`^\s*-\s+name:\s*(\S+)\s*$`)
	unitLine       = regexp.MustCompile(`^\s*unit:\s*(?:"([^"]*)"|(\S+))\s*$`)
)

func TestGeneratedUnitsMatchRawModel(t *testing.T) {
	units := scanUpstreamUnits(t)
	project := scanProjectNames(t)

	for name, unit := range units {
		def, ok := Lookup(name)
		if !ok {
			t.Errorf("%s: scanned from upstream model but missing from the generated table", name)
			continue
		}
		if def.Unit != unit {
			t.Errorf("%s: generated unit %q, upstream model says %q", name, def.Unit, unit)
		}
	}
	for _, def := range All() {
		if _, ok := units[def.Name]; ok {
			continue
		}
		if project[def.Name] {
			continue
		}
		t.Errorf("%s: generated from neither the upstream model nor project.yaml", def.Name)
	}
}

func scanUpstreamUnits(t *testing.T) map[string]string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(upstreamModelRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("%s: no YAML files", upstreamModelRoot)
	}

	units := make(map[string]string)
	formats := make(map[string]int)
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := ""
		nameLine := 0
		for i, line := range strings.Split(string(data), "\n") {
			number := i + 1
			format := ""
			var match []string
			if match = metricNameLine.FindStringSubmatch(line); match != nil {
				format = "metric_name"
			} else if match = dashNameLine.FindStringSubmatch(line); match != nil {
				format = "dash_name"
			}
			if format != "" {
				if name != "" {
					t.Fatalf("%s:%d: metric %q has no unit line", path, nameLine, name)
				}
				name, nameLine = match[1], number
				formats[format]++
				continue
			}
			match = unitLine.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			if name == "" {
				t.Fatalf("%s:%d: unit line without a metric name", path, number)
			}
			if _, ok := units[name]; ok {
				t.Fatalf("%s:%d: metric %q appears more than once", path, nameLine, name)
			}
			units[name] = match[1] + match[2]
			name = ""
		}
		if name != "" {
			t.Fatalf("%s:%d: metric %q has no unit line", path, nameLine, name)
		}
	}
	if len(units) == 0 {
		t.Fatal("no metrics parsed from the upstream model")
	}
	for _, format := range []string{"metric_name", "dash_name"} {
		if formats[format] == 0 {
			t.Fatalf("no %s metrics parsed from the upstream model", format)
		}
	}
	return units
}

func scanProjectNames(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(projectModelPath)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		if match := dashNameLine.FindStringSubmatch(line); match != nil {
			names[match[1]] = true
		}
	}
	if len(names) == 0 {
		t.Fatalf("%s: no metric names", projectModelPath)
	}
	return names
}
