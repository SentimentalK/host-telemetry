package budget

import (
	"context"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

const (
	MaxCollect     = 30 * time.Millisecond
	MaxRSS         = 80 << 20
	MaxCPUFraction = 0.01

	clockTicks    = 100
	minMeasure    = 250 * time.Millisecond
	unavailable   = "unavailable"
	cpuStatPath   = "/proc/self/stat"
	rssStatmPath  = "/proc/self/statm"
	rssStatusPath = "/proc/self/status"
)

type Report struct {
	Iterations    int
	CollectMedian time.Duration
	CollectP95    time.Duration
	CollectMax    time.Duration
	RSSBytes      uint64
	RSSSource     string
	CPUFraction   float64
	CPUSource     string

	interval     time.Duration
	cpuUntrusted bool
}

func Measure(ctx context.Context, newSources func() ([]source.Source, error), iterations int) (Report, error) {
	if iterations < 1 {
		return Report{}, fmt.Errorf("budget: iterations must be at least 1, got %d", iterations)
	}
	if newSources == nil {
		return Report{}, fmt.Errorf("budget: nil source factory")
	}

	startTicks, cpuStartErr := readCPUTicks()
	start := time.Now()
	durations := make([]time.Duration, 0, iterations)
	var interval time.Duration
	for len(durations) < iterations || time.Since(start) < minMeasure {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		sources, err := newSources()
		if err != nil {
			return Report{}, fmt.Errorf("budget: source factory: %w", err)
		}
		current := minInterval(sources)
		if current > 0 && (interval == 0 || current < interval) {
			interval = current
		}
		col := collector.New(sources)
		callStart := time.Now()
		col.CollectOnce(ctx)
		durations = append(durations, time.Since(callStart))
	}
	endTicks, cpuEndErr := readCPUTicks()

	rss, rssSource := readRSSPeak()
	report := Report{
		Iterations:    len(durations),
		CollectMedian: percentile(durations, 0.5),
		CollectP95:    percentile(durations, 0.95),
		CollectMax:    percentile(durations, 1),
		RSSBytes:      rss,
		RSSSource:     rssSource,
		CPUFraction:   -1,
		CPUSource:     unavailable,
		interval:      interval,
	}
	if cpuStartErr == nil && cpuEndErr == nil && interval > 0 {
		cpuSeconds := (endTicks - startTicks) / clockTicks
		report.CPUFraction = (cpuSeconds / float64(len(durations))) / interval.Seconds()
		report.CPUSource = cpuStatPath
		report.cpuUntrusted = report.CPUFraction < 0 || report.CPUFraction > 1
	}
	return report, nil
}

func minInterval(sources []source.Source) time.Duration {
	var shortest time.Duration
	for _, src := range sources {
		interval := src.Interval()
		if interval <= 0 {
			continue
		}
		if shortest == 0 || interval < shortest {
			shortest = interval
		}
	}
	return shortest
}

func (r Report) Breaches() []string {
	var breaches []string
	if r.CollectMax > MaxCollect {
		breaches = append(breaches, fmt.Sprintf("collect max %s exceeds limit %s", r.CollectMax, MaxCollect))
	}
	if r.RSSBytes > MaxRSS {
		breaches = append(breaches, fmt.Sprintf("rss %d By exceeds limit %d By", r.RSSBytes, MaxRSS))
	}
	switch {
	case r.CPUFraction < 0:
		breaches = append(breaches, fmt.Sprintf("cpu measurement unavailable (source %s): 1%% budget not proven", r.CPUSource))
	case r.cpuUntrusted:
		breaches = append(breaches, fmt.Sprintf("cpu fraction %.4f outside [0,1], measurement untrusted (source %s)", r.CPUFraction, r.CPUSource))
	case r.CPUFraction > MaxCPUFraction:
		breaches = append(breaches, fmt.Sprintf("cpu fraction %.4f exceeds limit %.4f", r.CPUFraction, MaxCPUFraction))
	}
	return breaches
}

func (r Report) String() string {
	var out strings.Builder
	fmt.Fprintf(&out, "budget: iterations %d; measurement=fresh source per iteration (parse+normalize included)\n", r.Iterations)
	fmt.Fprintf(&out, "budget: collect median=%s p95=%s max=%s (limit %s)\n", r.CollectMedian, r.CollectP95, r.CollectMax, MaxCollect)
	fmt.Fprintf(&out, "budget: rss peak=%s; limit %s; source %s\n", formatBytes(r.RSSBytes), formatBytes(MaxRSS), r.RSSSource)
	if r.CPUFraction < 0 {
		fmt.Fprintf(&out, "budget: cpu=%s (limit %s; source %s; formula=cpu_per_collect/interval)\n", unavailable, formatPercent(MaxCPUFraction), r.CPUSource)
		return out.String()
	}
	trust := ""
	if r.cpuUntrusted {
		trust = "; untrusted=true"
	}
	fmt.Fprintf(&out, "budget: cpu=%s of one core per %s interval (limit %s; source %s; formula=cpu_per_collect/interval%s)\n",
		formatPercent(r.CPUFraction), r.interval, formatPercent(MaxCPUFraction), r.CPUSource, trust)
	return out.String()
}

func percentile(durations []time.Duration, q float64) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	if q == 0.5 && len(sorted)%2 == 0 {
		mid := len(sorted) / 2
		return (sorted[mid-1] + sorted[mid]) / 2
	}
	index := int(math.Ceil(q*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	return sorted[index]
}

func readCPUTicks() (float64, error) {
	data, err := os.ReadFile(cpuStatPath)
	if err != nil {
		return 0, err
	}
	text := string(data)
	commEnd := strings.LastIndex(text, ")")
	if commEnd < 0 || commEnd+2 > len(text) {
		return 0, fmt.Errorf("budget: malformed %s", cpuStatPath)
	}
	fields := strings.Fields(text[commEnd+1:])
	if len(fields) < 13 {
		return 0, fmt.Errorf("budget: %s has %d fields after comm, want at least 13", cpuStatPath, len(fields))
	}
	utime, err := strconv.ParseFloat(fields[11], 64)
	if err != nil {
		return 0, fmt.Errorf("budget: %s utime: %w", cpuStatPath, err)
	}
	stime, err := strconv.ParseFloat(fields[12], 64)
	if err != nil {
		return 0, fmt.Errorf("budget: %s stime: %w", cpuStatPath, err)
	}
	return utime + stime, nil
}

func readRSSPeak() (uint64, string) {
	if data, err := os.ReadFile(rssStatusPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "VmHWM:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				break
			}
			kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				break
			}
			return kilobytes * 1024, rssStatusPath + " VmHWM"
		}
	}
	if data, err := os.ReadFile(rssStatmPath); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 2 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				return pages * uint64(os.Getpagesize()), rssStatmPath + " resident"
			}
		}
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.Sys, "runtime.MemStats Sys"
}

func formatBytes(bytes uint64) string {
	return fmt.Sprintf("%.1f MiB (%d By)", float64(bytes)/(1<<20), bytes)
}

func formatPercent(fraction float64) string {
	text := strconv.FormatFloat(fraction*100, 'f', 6, 64)
	text = strings.TrimRight(text, "0")
	text = strings.TrimRight(text, ".")
	return text + "%"
}
