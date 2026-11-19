package otlp

import (
	"fmt"
	"sort"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"

	"github.com/SentimentalK/host-telemetry/internal/schema"
)

type Resource struct {
	HostID         string
	HostName       string
	HostArch       string
	OSType         string
	OSVersion      string
	OSDescription  string
	ServiceName    string
	ServiceVersion string
	SchemaVersion  string
}

func (r Resource) attrs() []*commonpb.KeyValue {
	pairs := []struct {
		key   string
		value string
	}{
		{"host.id", r.HostID},
		{"host.name", r.HostName},
		{"host.arch", r.HostArch},
		{"os.type", r.OSType},
		{"os.version", r.OSVersion},
		{"os.description", r.OSDescription},
		{"service.name", r.ServiceName},
		{"service.version", r.ServiceVersion},
		{"schema.version", r.SchemaVersion},
	}
	attrs := make([]*commonpb.KeyValue, 0, len(pairs))
	for _, pair := range pairs {
		if pair.value == "" {
			continue
		}
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   pair.key,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: pair.value}},
		})
	}
	return attrs
}

func Encode(points []schema.Point, at time.Time, start time.Time, res Resource) ([]byte, error) {
	grouped := make(map[string][]schema.Point)
	names := make([]string, 0)
	for _, point := range points {
		if _, ok := grouped[point.Name]; !ok {
			names = append(names, point.Name)
		}
		grouped[point.Name] = append(grouped[point.Name], point)
	}
	sort.Strings(names)

	metrics := make([]*metricspb.Metric, 0, len(names))
	for _, name := range names {
		group := grouped[name]
		def, ok := schema.Lookup(name)
		if !ok {
			return nil, fmt.Errorf("otlp: unknown metric %q", name)
		}
		metric := &metricspb.Metric{Name: name, Unit: group[0].Unit}
		startNano := uint64(start.UnixNano())
		switch def.Kind {
		case schema.KindGauge:
			metric.Data = &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: numberDataPoints(group, at, 0)}}
		case schema.KindCounter:
			metric.Data = &metricspb.Metric_Sum{Sum: &metricspb.Sum{
				DataPoints:             numberDataPoints(group, at, startNano),
				IsMonotonic:            true,
				AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
			}}
		case schema.KindUpDownCounter:
			metric.Data = &metricspb.Metric_Sum{Sum: &metricspb.Sum{
				DataPoints:             numberDataPoints(group, at, startNano),
				IsMonotonic:            false,
				AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
			}}
		default:
			return nil, fmt.Errorf("otlp: metric %q: unsupported kind %d", name, def.Kind)
		}
		metrics = append(metrics, metric)
	}

	rm := &metricspb.ResourceMetrics{
		Resource:     &resourcepb.Resource{Attributes: res.attrs()},
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: metrics}},
	}
	body, err := proto.Marshal(rm)
	if err != nil {
		return nil, fmt.Errorf("otlp: marshal: %w", err)
	}
	return envelope(body), nil
}

func numberDataPoints(points []schema.Point, at time.Time, startNano uint64) []*metricspb.NumberDataPoint {
	dataPoints := make([]*metricspb.NumberDataPoint, 0, len(points))
	for _, point := range points {
		attrs := append([]schema.Attr(nil), point.Attrs...)
		sort.Slice(attrs, func(i, j int) bool { return attrs[i].Key < attrs[j].Key })
		kvs := make([]*commonpb.KeyValue, 0, len(attrs))
		for _, attr := range attrs {
			kvs = append(kvs, &commonpb.KeyValue{
				Key:   attr.Key,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: attr.Value}},
			})
		}
		dataPoints = append(dataPoints, &metricspb.NumberDataPoint{
			Attributes:        kvs,
			StartTimeUnixNano: startNano,
			TimeUnixNano:      uint64(at.UnixNano()),
			Value:             &metricspb.NumberDataPoint_AsDouble{AsDouble: point.Value},
		})
	}
	return dataPoints
}
