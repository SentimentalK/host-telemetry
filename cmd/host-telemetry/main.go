package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/SentimentalK/host-telemetry/internal/schema"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "print":
		if len(os.Args) != 2 {
			usage()
			os.Exit(2)
		}
		printMetrics(os.Stdout)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: host-telemetry print")
}

func printMetrics(w io.Writer) {
	defs := append([]schema.MetricDef(nil), schema.All()...)
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	for _, def := range defs {
		names := make([]string, 0, len(def.Attrs))
		for _, attr := range def.Attrs {
			names = append(names, attr.Name)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", def.Name, def.Unit, kindName(def.Kind), strings.Join(names, ","))
	}
}

func kindName(kind schema.Kind) string {
	switch kind {
	case schema.KindGauge:
		return "KindGauge"
	case schema.KindCounter:
		return "KindCounter"
	case schema.KindUpDownCounter:
		return "KindUpDownCounter"
	case schema.KindHistogram:
		return "KindHistogram"
	default:
		return "Kind(0)"
	}
}
