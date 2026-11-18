package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildMetricExtends(t *testing.T) {
	doc := &document{
		Path: "host-metrics.yaml",
		Attributes: []entry{
			{ID: "hw.id"},
			{ID: "hw.name"},
		},
		Metrics: []entry{{
			Name:       "x.y",
			Instrument: "gauge",
			Unit:       "1",
			Extends:    "hardware.attributes.common",
		}},
		AttributeGroups: []entry{{
			ID:         "hardware.attributes.common",
			Attributes: []entry{{Ref: "hw.id"}, {Ref: "hw.name"}},
		}},
	}

	m, err := build([]*document{doc})
	if err != nil {
		t.Fatalf("build() = %v", err)
	}
	want := []string{"hw.id", "hw.name"}
	if got := m.metrics["x.y"].Attrs; !reflect.DeepEqual(got, want) {
		t.Errorf("x.y attrs = %v, want %v", got, want)
	}
}

func TestBuildRefGroup(t *testing.T) {
	doc := &document{
		Path: "v1.yaml",
		Groups: []entry{
			{
				ID:   "g",
				Type: typeSpec{Scalar: "attribute_group"},
				Attributes: []entry{{
					ID:   "cpu.mode",
					Type: typeSpec{IsEnum: true, Members: []member{{ID: "user"}}},
				}},
			},
			{
				ID:         "metric.a.b",
				Type:       typeSpec{Scalar: "metric"},
				MetricName: "a.b",
				Instrument: "counter",
				Unit:       "s",
				Attributes: []entry{{RefGroup: "g"}},
			},
		},
	}

	m, err := build([]*document{doc})
	if err != nil {
		t.Fatalf("build() = %v", err)
	}
	if got, want := m.metrics["a.b"].Attrs, []string{"cpu.mode"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a.b attrs = %v, want %v", got, want)
	}
	if got, want := m.attrs["cpu.mode"].Values, []string{"user"}; !reflect.DeepEqual(got, want) {
		t.Errorf("cpu.mode values = %v, want %v", got, want)
	}
}

func TestBuildEnumFallback(t *testing.T) {
	doc := &document{
		Path: "error.yaml",
		Attributes: []entry{{
			ID:          "error.type",
			Type:        typeSpec{IsEnum: true, Members: []member{{ID: "other", Value: "_OTHER"}}},
			Annotations: annotations{EnumFallback: "other"},
		}},
	}

	m, err := build([]*document{doc})
	if err != nil {
		t.Fatalf("build() = %v", err)
	}
	def := m.attrs["error.type"]
	if def.Values != nil {
		t.Errorf("error.type values = %v, want nil", def.Values)
	}
	if len(def.Members) != 1 {
		t.Errorf("error.type members = %d, want 1", len(def.Members))
	}
}

func TestBuildDuplicateMetric(t *testing.T) {
	first := &document{
		Path:    "first.yaml",
		Metrics: []entry{{Name: "x.y", Instrument: "gauge", Unit: "1"}},
	}
	second := &document{
		Path:    "second.yaml",
		Metrics: []entry{{Name: "x.y", Instrument: "gauge", Unit: "1"}},
	}

	_, err := build([]*document{first, second})
	if err == nil {
		t.Fatal("build() = nil, want duplicate metric error")
	}
	if !strings.Contains(err.Error(), "x.y") {
		t.Errorf("error = %q, want it to name x.y", err)
	}
}
