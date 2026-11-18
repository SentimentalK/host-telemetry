package schema

import "testing"

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
