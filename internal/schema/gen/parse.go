package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type document struct {
	Path              string  `yaml:"-"`
	FileFormat        string  `yaml:"file_format"`
	Groups            []entry `yaml:"groups"`
	Metrics           []entry `yaml:"metrics"`
	Attributes        []entry `yaml:"attributes"`
	AttributeGroups   []entry `yaml:"attribute_groups"`
	MetricRefinements []entry `yaml:"metric_refinements"`
}

type entry struct {
	ID          string      `yaml:"id"`
	Name        string      `yaml:"name"`
	MetricName  string      `yaml:"metric_name"`
	Instrument  string      `yaml:"instrument"`
	Unit        string      `yaml:"unit"`
	Ref         string      `yaml:"ref"`
	RefGroup    string      `yaml:"ref_group"`
	Extends     string      `yaml:"extends"`
	Type        typeSpec    `yaml:"type"`
	Annotations annotations `yaml:"annotations"`
	Attributes  []entry     `yaml:"attributes"`
}

type annotations struct {
	EnumFallback any `yaml:"enum_fallback"`
}

type typeSpec struct {
	Scalar  string
	Members []member
	IsEnum  bool
}

type member struct {
	ID         string     `yaml:"id"`
	Value      string     `yaml:"value"`
	Deprecated *yaml.Node `yaml:"deprecated"`
}

func (t *typeSpec) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Decode(&t.Scalar)
	case yaml.MappingNode:
		var raw struct {
			Members []member `yaml:"members"`
		}
		if err := node.Decode(&raw); err != nil {
			return err
		}
		if raw.Members != nil {
			t.Members = raw.Members
			t.IsEnum = true
		}
	}
	return nil
}

func readDocument(path string) (*document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	doc.Path = path
	return &doc, nil
}
