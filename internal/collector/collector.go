package collector

import (
	"context"
	"sort"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/clockwatch"
	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

type Collector struct {
	Sources []source.Source
	Now     func() time.Time
	Stats   *Stats
	Jump    func(source string, skew time.Duration)

	clock clockwatch.Watch
}

type Result struct {
	Points []schema.Point
	Errors []SourceError
}

type SourceError struct {
	Source string
	Err    error
}

func New(sources []source.Source) *Collector {
	return &Collector{Sources: sources, Now: time.Now, Stats: newStats(), clock: clockwatch.Watch{Threshold: time.Second}}
}

func (c *Collector) CollectOnce(ctx context.Context) Result {
	var result Result
	for _, src := range c.Sources {
		name := src.Name()
		points, err := src.Collect(ctx)
		if err != nil {
			result.Errors = append(result.Errors, SourceError{Source: name, Err: err})
			points = append(points, schema.NewPoint(schema.CollectorSourceError, 1, schema.Attr{Key: schema.AttrCollectorSource, Value: name}))
		}
		for _, point := range points {
			if err := schema.Validate(point); err != nil {
				result.Errors = append(result.Errors, SourceError{Source: name, Err: err})
				continue
			}
			result.Points = append(result.Points, normalize(point))
		}
	}
	sortPoints(result.Points)
	return result
}

func normalize(point schema.Point) schema.Point {
	attrs := append([]schema.Attr(nil), point.Attrs...)
	sort.Slice(attrs, func(i, j int) bool {
		if attrs[i].Key != attrs[j].Key {
			return attrs[i].Key < attrs[j].Key
		}
		return attrs[i].Value < attrs[j].Value
	})
	point.Attrs = attrs
	return point
}

func sortPoints(points []schema.Point) {
	sort.SliceStable(points, func(i, j int) bool {
		return less(points[i], points[j])
	})
}

func less(a, b schema.Point) bool {
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	for i := 0; i < len(a.Attrs) && i < len(b.Attrs); i++ {
		if a.Attrs[i].Key != b.Attrs[i].Key {
			return a.Attrs[i].Key < b.Attrs[i].Key
		}
		if a.Attrs[i].Value != b.Attrs[i].Value {
			return a.Attrs[i].Value < b.Attrs[i].Value
		}
	}
	return len(a.Attrs) < len(b.Attrs)
}
