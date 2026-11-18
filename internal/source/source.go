package source

import (
	"context"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/schema"
)

type Source interface {
	Name() string
	Interval() time.Duration
	Collect(ctx context.Context) ([]schema.Point, error)
}
