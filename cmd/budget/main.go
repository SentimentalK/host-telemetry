package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/SentimentalK/host-telemetry/internal/budget"
	"github.com/SentimentalK/host-telemetry/internal/source/fixtures"
)

func main() {
	fixturesDir := flag.String("fixtures", "testdata", "fixture root")
	iterations := flag.Int("iterations", 20, "collect iterations")
	flag.Parse()

	sources, err := fixtures.Load(*fixturesDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	report, err := budget.Measure(context.Background(), sources, *iterations)
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
