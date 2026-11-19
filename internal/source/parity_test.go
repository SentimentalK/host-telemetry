package source_test

import (
	"testing"

	"github.com/SentimentalK/host-telemetry/internal/schema"
)

func TestCrossPlatformParity(t *testing.T) {
	platforms := []string{"linux", "darwin", "windows"}
	points := make(map[string][]schema.Point, len(platforms))
	for _, platform := range platforms {
		result := collectPlatform(t, platform)
		points[platform] = result.Points
	}

	for _, platform := range platforms {
		if _, ok := findPoint(points[platform], schema.SystemCPULogicalCount, "", ""); !ok {
			t.Errorf("%s: missing %s", platform, schema.SystemCPULogicalCount)
		}
		if _, ok := findPoint(points[platform], schema.SystemMemoryUsage, schema.AttrSystemMemoryState, ""); !ok {
			t.Errorf("%s: missing %s with %s", platform, schema.SystemMemoryUsage, schema.AttrSystemMemoryState)
		}
	}

	for _, platform := range platforms {
		utilizations := findAll(points[platform], schema.SystemCPUUtilization)
		if len(utilizations) == 0 {
			t.Errorf("%s: missing %s", platform, schema.SystemCPUUtilization)
			continue
		}
		for _, point := range utilizations {
			if _, ok := attr(point, schema.AttrCPUMode); !ok {
				t.Errorf("%s: %s missing %s", platform, schema.SystemCPUUtilization, schema.AttrCPUMode)
			}
			if point.Value < 0 || point.Value > 1 {
				t.Errorf("%s: %s{%s} = %v outside [0,1]", platform, point.Name, formatAttrs(point.Attrs), point.Value)
			}
		}
		sum := 0.0
		for _, point := range utilizations {
			sum += point.Value
		}
		if sum > 1.0+1e-9 {
			t.Errorf("%s: sum of %s modes = %v, want <= 1", platform, schema.SystemCPUUtilization, sum)
		}
	}

	darwinModes := make(map[string]int)
	for _, point := range findAll(points["darwin"], schema.SystemCPUUtilization) {
		mode, _ := attr(point, schema.AttrCPUMode)
		darwinModes[mode]++
	}
	for _, mode := range []string{"user", "system", "idle"} {
		if darwinModes[mode] != 1 {
			t.Errorf("darwin: %s mode %q rows = %d, want 1", schema.SystemCPUUtilization, mode, darwinModes[mode])
		}
	}
	if len(darwinModes) != 3 {
		t.Errorf("darwin: %s modes = %v, want user, system, idle", schema.SystemCPUUtilization, darwinModes)
	}

	for _, platform := range []string{"linux", "darwin"} {
		point, ok := findPoint(points[platform], schema.SystemUptime, "", "")
		if !ok {
			t.Errorf("%s: missing %s", platform, schema.SystemUptime)
			continue
		}
		if point.Unit != "s" {
			t.Errorf("%s: %s unit = %q, want %q", platform, schema.SystemUptime, point.Unit, "s")
		}
	}

	for _, platform := range platforms {
		for _, point := range points[platform] {
			def, ok := schema.Lookup(point.Name)
			if !ok {
				t.Errorf("%s: %s is not in the schema package", platform, point.Name)
				continue
			}
			if point.Unit != def.Unit {
				t.Errorf("%s: %s unit = %q, schema package has %q", platform, point.Name, point.Unit, def.Unit)
			}
		}
	}
}

func findPoint(points []schema.Point, name, attrKey, attrValue string) (schema.Point, bool) {
	for _, point := range points {
		if point.Name != name {
			continue
		}
		if attrKey == "" {
			return point, true
		}
		if value, ok := attr(point, attrKey); ok && (attrValue == "" || value == attrValue) {
			return point, true
		}
	}
	return schema.Point{}, false
}

func findAll(points []schema.Point, name string) []schema.Point {
	var out []schema.Point
	for _, point := range points {
		if point.Name == name {
			out = append(out, point)
		}
	}
	return out
}

func attr(point schema.Point, key string) (string, bool) {
	for _, attr := range point.Attrs {
		if attr.Key == key {
			return attr.Value, true
		}
	}
	return "", false
}
