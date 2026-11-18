package schema

type Kind uint8

const (
	KindGauge Kind = iota + 1
	KindCounter
	KindUpDownCounter
	KindHistogram
)

type Attr struct {
	Key   string
	Value string
}

type AttrDef struct {
	Name   string
	Values []string
}

type MetricDef struct {
	Name  string
	Unit  string
	Kind  Kind
	Attrs []AttrDef
}

type Point struct {
	Name  string
	Unit  string
	Value float64
	Attrs []Attr
}
