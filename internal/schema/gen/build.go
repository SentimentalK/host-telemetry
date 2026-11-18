package main

import (
	"fmt"
	"slices"
	"sort"
)

type attrDef struct {
	ID      string
	Values  []string
	Members []enumMember
}

type enumMember struct {
	Value string
	Const string
}

type metricDef struct {
	Name  string
	Unit  string
	Kind  string
	Attrs []string
}

type model struct {
	attrs   map[string]attrDef
	groups  map[string][]entry
	metrics map[string]metricDef
}

func build(docs []*document) (*model, error) {
	m := &model{
		attrs:   make(map[string]attrDef),
		groups:  make(map[string][]entry),
		metrics: make(map[string]metricDef),
	}

	for _, doc := range docs {
		for _, a := range doc.Attributes {
			if err := m.addAttr(doc, a); err != nil {
				return nil, err
			}
		}
		for _, g := range doc.Groups {
			if g.Type.Scalar != "attribute_group" {
				continue
			}
			if err := m.addGroup(doc, g); err != nil {
				return nil, err
			}
		}
		for _, g := range doc.AttributeGroups {
			if err := m.addGroup(doc, g); err != nil {
				return nil, err
			}
		}
	}

	for _, doc := range docs {
		for _, g := range doc.Groups {
			if g.Type.Scalar != "metric" {
				continue
			}
			name := g.MetricName
			if name == "" {
				name = g.Name
			}
			if err := m.addMetric(doc, name, g); err != nil {
				return nil, err
			}
		}
		for _, md := range doc.Metrics {
			if err := m.addMetric(doc, md.Name, md); err != nil {
				return nil, err
			}
		}
	}

	for _, doc := range docs {
		for _, ref := range doc.MetricRefinements {
			def, ok := m.metrics[ref.Ref]
			if !ok {
				return nil, fmt.Errorf("%s: metric_refinements: unknown metric %q", doc.Path, ref.Ref)
			}
			ids, err := m.expand(doc, ref.Attributes, nil)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				if _, ok := m.attrs[id]; !ok {
					return nil, fmt.Errorf("%s: metric_refinements %q: unknown attribute %q", doc.Path, ref.Ref, id)
				}
				if !slices.Contains(def.Attrs, id) {
					def.Attrs = append(def.Attrs, id)
				}
			}
			m.metrics[ref.Ref] = def
		}
	}

	return m, nil
}

func (m *model) addAttr(doc *document, a entry) error {
	if a.ID == "" {
		return fmt.Errorf("%s: attribute without id", doc.Path)
	}
	if _, ok := m.attrs[a.ID]; ok {
		return fmt.Errorf("%s: duplicate attribute %q", doc.Path, a.ID)
	}
	def := attrDef{ID: a.ID}
	if a.Type.IsEnum {
		for _, mem := range a.Type.Members {
			if mem.Deprecated != nil {
				continue
			}
			if mem.ID == "" {
				return fmt.Errorf("%s: attribute %q: member without id", doc.Path, a.ID)
			}
			value := mem.Value
			if value == "" {
				value = mem.ID
			}
			def.Members = append(def.Members, enumMember{Value: value, Const: ident(a.ID) + ident(mem.ID)})
			if a.Annotations.EnumFallback == nil {
				def.Values = append(def.Values, value)
			}
		}
		if len(def.Members) == 0 {
			return fmt.Errorf("%s: attribute %q: no non-deprecated members", doc.Path, a.ID)
		}
	}
	m.attrs[a.ID] = def
	return nil
}

func (m *model) addGroup(doc *document, g entry) error {
	if g.ID == "" {
		return fmt.Errorf("%s: attribute group without id", doc.Path)
	}
	if _, ok := m.groups[g.ID]; ok {
		return fmt.Errorf("%s: duplicate attribute group %q", doc.Path, g.ID)
	}
	var members []entry
	if g.Extends != "" {
		members = append(members, entry{RefGroup: g.Extends})
	}
	for _, a := range g.Attributes {
		if a.Ref == "" && a.RefGroup == "" && a.Extends == "" {
			if a.ID == "" {
				return fmt.Errorf("%s: attribute group %q: entry without ref or id", doc.Path, g.ID)
			}
			if err := m.addAttr(doc, a); err != nil {
				return err
			}
			members = append(members, entry{Ref: a.ID})
			continue
		}
		members = append(members, a)
	}
	m.groups[g.ID] = members
	return nil
}

func (m *model) addMetric(doc *document, name string, e entry) error {
	if name == "" {
		return fmt.Errorf("%s: metric without name", doc.Path)
	}
	if _, ok := m.metrics[name]; ok {
		return fmt.Errorf("%s: duplicate metric %q", doc.Path, name)
	}
	if e.Unit == "" {
		return fmt.Errorf("%s: metric %q: empty unit", doc.Path, name)
	}
	kind, err := kindOf(e.Instrument)
	if err != nil {
		return fmt.Errorf("%s: metric %q: %w", doc.Path, name, err)
	}
	uses := e.Attributes
	if e.Extends != "" {
		uses = append([]entry{{Extends: e.Extends}}, e.Attributes...)
	}
	ids, err := m.expand(doc, uses, nil)
	if err != nil {
		return err
	}
	attrs := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := m.attrs[id]; !ok {
			return fmt.Errorf("%s: metric %q: unknown attribute %q", doc.Path, name, id)
		}
		attrs = append(attrs, id)
	}
	m.metrics[name] = metricDef{Name: name, Unit: e.Unit, Kind: kind, Attrs: attrs}
	return nil
}

func (m *model) expand(doc *document, uses []entry, stack []string) ([]string, error) {
	ids := make([]string, 0, len(uses))
	for _, use := range uses {
		group := use.RefGroup
		if group == "" {
			group = use.Extends
		}
		if group != "" {
			members, ok := m.groups[group]
			if !ok {
				return nil, fmt.Errorf("%s: unknown attribute group %q", doc.Path, group)
			}
			for _, seen := range stack {
				if seen == group {
					return nil, fmt.Errorf("%s: attribute group cycle at %q", doc.Path, group)
				}
			}
			sub, err := m.expand(doc, members, append(stack, group))
			if err != nil {
				return nil, err
			}
			ids = append(ids, sub...)
			continue
		}
		if use.Ref == "" {
			return nil, fmt.Errorf("%s: attribute reference without ref", doc.Path)
		}
		ids = append(ids, use.Ref)
	}
	return ids, nil
}

func kindOf(instrument string) (string, error) {
	switch instrument {
	case "gauge":
		return "Gauge", nil
	case "counter":
		return "Counter", nil
	case "updowncounter":
		return "UpDownCounter", nil
	case "histogram":
		return "Histogram", nil
	default:
		return "", fmt.Errorf("instrument %q is not gauge, counter, updowncounter or histogram", instrument)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
