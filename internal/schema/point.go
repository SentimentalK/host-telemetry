package schema

import "fmt"

func NewPoint(name string, value float64, attrs ...Attr) Point {
	def, ok := Lookup(name)
	if !ok {
		panic(fmt.Sprintf("schema: unknown metric %q", name))
	}
	return Point{Name: name, Unit: def.Unit, Value: value, Attrs: attrs}
}
