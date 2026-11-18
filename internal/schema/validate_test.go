package schema

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name  string
		point Point
	}{
		{"uptime", Point{Name: SystemUptime, Unit: "s", Value: 42}},
		{"cpu utilization", Point{
			Name:  SystemCPUUtilization,
			Unit:  "1",
			Value: 0.5,
			Attrs: []Attr{
				{Key: AttrCPUMode, Value: CPUModeUser},
				{Key: AttrCPULogicalNumber, Value: "0"},
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.point); err != nil {
				t.Fatalf("Validate(%s) = %v", tc.name, err)
			}
		})
	}
}

func TestUnknownMetric(t *testing.T) {
	err := Validate(Point{Name: "system.cpu.percent", Unit: "1", Value: 1})
	if err == nil {
		t.Fatal("Validate accepted unknown metric system.cpu.percent")
	}
	for _, want := range []string{"system.cpu.percent", "unknown metric"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}

func TestWrongUnit(t *testing.T) {
	err := Validate(Point{Name: SystemUptime, Unit: "ms", Value: 1})
	if err == nil {
		t.Fatalf("Validate accepted system.uptime with unit ms; expected error text %q is missing", `not allowed, want "s"`)
	}
	for _, want := range []string{SystemUptime, `"ms"`, `"s"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}

func TestUnknownAttribute(t *testing.T) {
	err := Validate(Point{
		Name:  SystemCPUTime,
		Unit:  "s",
		Value: 1,
		Attrs: []Attr{{Key: "cpu.bogus", Value: "user"}},
	})
	if err == nil {
		t.Fatal("Validate accepted unknown attribute cpu.bogus")
	}
	for _, want := range []string{"cpu.bogus", AttrCPUMode, AttrCPULogicalNumber} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}

func TestEnumValueRejected(t *testing.T) {
	err := Validate(Point{
		Name:  SystemCPUTime,
		Unit:  "s",
		Value: 1,
		Attrs: []Attr{{Key: AttrCPUMode, Value: "bogus"}},
	})
	if err == nil {
		t.Fatal("Validate accepted cpu.mode=bogus")
	}
	for _, want := range []string{"bogus", AttrCPUMode, CPUModeUser, CPUModeSteal} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}

func TestDuplicateAttribute(t *testing.T) {
	err := Validate(Point{
		Name:  SystemCPUTime,
		Unit:  "s",
		Value: 1,
		Attrs: []Attr{
			{Key: AttrCPUMode, Value: CPUModeUser},
			{Key: AttrCPUMode, Value: CPUModeIdle},
		},
	})
	if err == nil {
		t.Fatal("Validate accepted duplicate cpu.mode attributes")
	}
	for _, want := range []string{"duplicate attribute", AttrCPUMode} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}
