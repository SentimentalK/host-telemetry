package schema

import (
	"fmt"
	"strings"
)

func Validate(p Point) error {
	def, ok := Lookup(p.Name)
	if !ok {
		return fmt.Errorf("schema: unknown metric %q", p.Name)
	}
	if p.Unit != def.Unit {
		return fmt.Errorf("schema: metric %q: unit %q not allowed, want %q", p.Name, p.Unit, def.Unit)
	}

	declared := make(map[string]AttrDef, len(def.Attrs))
	allowed := make([]string, 0, len(def.Attrs))
	for _, attr := range def.Attrs {
		declared[attr.Name] = attr
		allowed = append(allowed, attr.Name)
	}

	seen := make(map[string]bool, len(p.Attrs))
	for _, attr := range p.Attrs {
		def, ok := declared[attr.Key]
		if !ok {
			return fmt.Errorf("schema: metric %q: unknown attribute %q (allowed: %s)", p.Name, attr.Key, strings.Join(allowed, ", "))
		}
		if def.Values != nil && !contains(def.Values, attr.Value) {
			return fmt.Errorf("schema: metric %q: attribute %q value %q not allowed (allowed: %s)", p.Name, attr.Key, attr.Value, strings.Join(def.Values, ", "))
		}
		if seen[attr.Key] {
			return fmt.Errorf("schema: metric %q: duplicate attribute %q", p.Name, attr.Key)
		}
		seen[attr.Key] = true
	}
	return nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
