package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/SentimentalK/host-telemetry/internal/budget"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/fixtures"
)

func main() {
	fixturesDir := flag.String("fixtures", "testdata", "fixture root")
	platform := flag.String("platform", "linux", "fixture platform")
	iterations := flag.Int("iterations", 20, "collect iterations")
	flag.Parse()

	newSources := func() ([]source.Source, error) {
		src, err := fixtures.LoadPlatform(*fixturesDir, *platform)
		if err != nil {
			return nil, err
		}
		return []source.Source{src}, nil
	}
	report, err := budget.Measure(context.Background(), newSources, *iterations)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(report.String())
	breaches := report.Breaches()
	if len(breaches) == 0 {
		return
	}
	for _, breach := range breaches {
		fmt.Fprintln(os.Stderr, breach)
	}
	os.Exit(1)
}
