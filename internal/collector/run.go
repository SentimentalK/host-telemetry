package collector

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/source"
)

func (c *Collector) Run(ctx context.Context, sink func(source string, points []schema.Point, err error, d time.Duration)) {
	stats := c.Stats
	if stats == nil {
		stats = newStats()
		c.Stats = stats
	}

	var wg sync.WaitGroup
	for _, src := range c.Sources {
		wg.Add(1)
		go func(src source.Source) {
			defer wg.Done()
			c.runSource(ctx, src, stats, sink)
		}(src)
	}
	wg.Wait()
}

func (c *Collector) runSource(ctx context.Context, src source.Source, stats *Stats, sink func(source string, points []schema.Point, err error, d time.Duration)) {
	interval := src.Interval()
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		c.collectSource(ctx, src, stats, sink)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkClock(src.Name())
		}
	}
}

func (c *Collector) checkClock(source string) {
	skew, jumped := c.clock.Step()
	if !jumped || c.Jump == nil {
		return
	}
	c.Jump(source, skew)
}

func (c *Collector) collectSource(ctx context.Context, src source.Source, stats *Stats, sink func(source string, points []schema.Point, err error, d time.Duration)) {
	collectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	start := time.Now()
	points, err := src.Collect(collectCtx)
	elapsed := time.Since(start)
	stats.Record(src.Name(), elapsed)

	if err != nil {
		stats.AddError(src.Name())
	}
	points = append(points, schema.NewPoint(schema.CollectorSourceError, float64(stats.errorCount(src.Name())),
		schema.Attr{Key: schema.AttrCollectorSource, Value: src.Name()}))

	valid := make([]schema.Point, 0, len(points))
	var invalid error
	for _, point := range points {
		if validateErr := schema.Validate(point); validateErr != nil {
			invalid = errors.Join(invalid, validateErr)
			continue
		}
		valid = append(valid, normalize(point))
	}
	sortPoints(valid)
	if invalid != nil {
		err = errors.Join(err, invalid)
	}
	sink(src.Name(), valid, err, elapsed)
}
