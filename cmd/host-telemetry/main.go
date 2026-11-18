package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source/fixtures"
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
	case "run":
		run(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: host-telemetry print|run [flags]")
}

func run(args []string) {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	fixturesDir := flags.String("fixtures", "testdata", "fixture root")
	listen := flags.String("listen", "127.0.0.1:9101", "listen address")
	if err := flags.Parse(args); err != nil {
		os.Exit(2)
	}
	sources, err := fixtures.Load(*fixturesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "host-telemetry: %v\n", err)
		os.Exit(1)
	}
	col := collector.New(sources)
	var mu sync.RWMutex
	points := collect(col)
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			next := collect(col)
			mu.Lock()
			points = next
			mu.Unlock()
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", collector.MetricsHandler(func() []schema.Point {
		mu.RLock()
		defer mu.RUnlock()
		return points
	}))
	log.Printf("listening on %s", *listen)
	if err := http.ListenAndServe(*listen, mux); err != nil {
		fmt.Fprintf(os.Stderr, "host-telemetry: %v\n", err)
		os.Exit(1)
	}
}

func collect(c *collector.Collector) []schema.Point {
	result := c.CollectOnce(context.Background())
	for _, sourceErr := range result.Errors {
		log.Printf("source %s: %v", sourceErr.Source, sourceErr.Err)
	}
	return result.Points
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
