package collector

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/SentimentalK/host-telemetry/internal/schema"
)

func WriteMetrics(w io.Writer, points []schema.Point) error {
	groups := make(map[string][]schema.Point)
	kinds := make(map[string]schema.Kind)
	names := make([]string, 0, len(points))
	for _, point := range points {
		def, ok := schema.Lookup(point.Name)
		if !ok {
			return fmt.Errorf("metrics: unknown metric %q", point.Name)
		}
		if def.Kind != schema.KindGauge && def.Kind != schema.KindCounter && def.Kind != schema.KindUpDownCounter {
			return fmt.Errorf("metrics: metric %q: unsupported kind %d", point.Name, def.Kind)
		}
		sample := sampleName(point.Name, def.Kind)
		if _, ok := groups[sample]; !ok {
			names = append(names, sample)
			kinds[sample] = def.Kind
		}
		groups[sample] = append(groups[sample], point)
	}
	sort.Strings(names)

	out := bufio.NewWriter(w)
	for _, sample := range names {
		points := groups[sample]
		sort.Slice(points, func(i, j int) bool { return lessLabels(points[i], points[j]) })
		if _, err := fmt.Fprintf(out, "# HELP %s OTel metric %s; unit %s\n", sample, points[0].Name, points[0].Unit); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "# TYPE %s %s\n", sample, prometheusType(kinds[sample])); err != nil {
			return err
		}
		for _, point := range points {
			if _, err := out.WriteString(formatSample(sample, point)); err != nil {
				return err
			}
		}
	}
	return out.Flush()
}

func sampleName(name string, kind schema.Kind) string {
	sample := strings.ReplaceAll(name, ".", "_")
	if kind == schema.KindCounter {
		sample += "_total"
	}
	return sample
}

func prometheusType(kind schema.Kind) string {
	if kind == schema.KindCounter {
		return "counter"
	}
	return "gauge"
}

func formatSample(sample string, point schema.Point) string {
	var b strings.Builder
	b.WriteString(sample)
	if len(point.Attrs) > 0 {
		b.WriteByte('{')
		for i, attr := range point.Attrs {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(labelName(attr.Key))
			b.WriteString(`="`)
			b.WriteString(escapeLabel(attr.Value))
			b.WriteString(`"`)
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(point.Value, 'g', -1, 64))
	b.WriteByte('\n')
	return b.String()
}

func labelName(key string) string {
	return strings.ReplaceAll(key, ".", "_")
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escapeLabel(value string) string {
	return labelEscaper.Replace(value)
}

func lessLabels(a, b schema.Point) bool {
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
