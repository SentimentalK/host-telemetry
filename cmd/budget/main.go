package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/SentimentalK/host-telemetry/internal/budget"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/fixtures"
	"github.com/SentimentalK/host-telemetry/internal/source/linux"
)

func main() {
	fixturesDir := flag.String("fixtures", "", "fixture root; empty measures the live host")
	platform := flag.String("platform", "linux", "fixture platform")
	iterations := flag.Int("iterations", 20, "collect iterations")
	perSource := flag.Bool("per-source", false, "measure each source separately")
	flag.Parse()

	newSources := func() ([]source.Source, error) {
		if *fixturesDir != "" {
			src, err := fixtures.LoadPlatform(*fixturesDir, *platform)
			if err != nil {
				return nil, err
			}
			return []source.Source{src}, nil
		}
		if runtime.GOOS != "linux" {
			return nil, fmt.Errorf("live collection supports linux only, running on %s", runtime.GOOS)
		}
		return linux.NewLive(os.DirFS("/"), linux.Options{}), nil
	}
	report, err := budget.Measure(context.Background(), newSources, *iterations)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(report.String())
	breaches := report.Breaches()
	if *perSource {
		reports, err := budget.MeasurePerSource(context.Background(), newSources, *iterations)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, sourceReport := range reports {
			fmt.Print(sourceReport.String())
			breaches = append(breaches, sourceReport.Breaches()...)
		}
	}
	if len(breaches) == 0 {
		return
	}
	for _, breach := range breaches {
		fmt.Fprintln(os.Stderr, breach)
	}
	os.Exit(1)
}
