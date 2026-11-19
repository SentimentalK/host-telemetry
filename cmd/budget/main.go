package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/SentimentalK/host-telemetry/internal/budget"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/fixtures"
	"github.com/SentimentalK/host-telemetry/internal/source/linux"
	"github.com/SentimentalK/host-telemetry/internal/workload"
)

func main() {
	fixturesDir := flag.String("fixtures", "", "fixture root; empty measures the live host")
	platform := flag.String("platform", "linux", "fixture platform")
	iterations := flag.Int("iterations", 20, "collect iterations")
	perSource := flag.Bool("per-source", false, "measure each source separately")
	workloadSource := flag.Bool("workload", false, "include the live workload source")
	workloadExecutables := flag.String("workload-executables", "", "comma-separated process executable whitelist")
	flag.Parse()

	newSources := func() ([]source.Source, error) {
		var sources []source.Source
		if *fixturesDir != "" {
			var err error
			sources, err = fixtures.LoadPlatform(*fixturesDir, *platform)
			if err != nil {
				return nil, err
			}
		} else {
			if runtime.GOOS != "linux" {
				return nil, fmt.Errorf("live collection supports linux only, running on %s", runtime.GOOS)
			}
			sources = linux.NewLive(os.DirFS("/"), linux.Options{})
		}
		if *workloadSource {
			sources = append(sources, workload.New(os.DirFS("/sys/fs/cgroup"), os.DirFS("/"), workload.Options{
				Cgroups:     true,
				Executables: splitExecutables(*workloadExecutables),
			}))
		}
		return sources, nil
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

func splitExecutables(value string) []string {
	list := make([]string, 0)
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			list = append(list, part)
		}
	}
	return list
}
