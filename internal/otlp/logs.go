package otlp

import (
	"fmt"
	"sort"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

type Attribute struct {
	Key   string
	Value string
}

type LogRecord struct {
	Time  time.Time
	Name  string
	Attrs []Attribute
}

func EncodeLogs(records []LogRecord, res Resource, at time.Time) ([]byte, error) {
	logRecords := make([]*logspb.LogRecord, 0, len(records))
	for _, record := range records {
		attrs := append([]Attribute(nil), record.Attrs...)
		sort.Slice(attrs, func(i, j int) bool { return attrs[i].Key < attrs[j].Key })
		kvs := make([]*commonpb.KeyValue, 0, len(attrs))
		for _, attr := range attrs {
			kvs = append(kvs, &commonpb.KeyValue{
				Key:   attr.Key,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: attr.Value}},
			})
		}
		logRecords = append(logRecords, &logspb.LogRecord{
			TimeUnixNano:         uint64(record.Time.UnixNano()),
			ObservedTimeUnixNano: uint64(at.UnixNano()),
			SeverityNumber:       logspb.SeverityNumber_SEVERITY_NUMBER_INFO,
			SeverityText:         "INFO",
			EventName:            record.Name,
			Attributes:           kvs,
		})
	}

	rl := &logspb.ResourceLogs{
		Resource: &resourcepb.Resource{Attributes: res.attrs()},
		ScopeLogs: []*logspb.ScopeLogs{{
			Scope:      &commonpb.InstrumentationScope{Name: "host-telemetry", Version: res.ServiceVersion},
			LogRecords: logRecords,
		}},
	}
	body, err := proto.Marshal(rl)
	if err != nil {
		return nil, fmt.Errorf("otlp: marshal logs: %w", err)
	}
	return envelope(body), nil
}
