package sender

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/otlp"
	"github.com/SentimentalK/host-telemetry/internal/wal"
)

type Sender struct {
	Store    *wal.Store
	Client   *otlp.Client
	Interval time.Duration
}

func (s *Sender) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.round(ctx) {
				return
			}
		}
	}
}

func (s *Sender) round(ctx context.Context) bool {
	for _, batch := range s.Store.Pending() {
		if err := s.Client.Send(ctx, batch.Payload); err != nil {
			if errors.Is(err, otlp.ErrPermanent) {
				log.Printf("sender: dropping batch %d: %v", batch.Seq, err)
				if ackErr := s.Store.Discard(batch.Seq, batch.Samples); ackErr != nil {
					log.Printf("sender: discard batch %d: %v", batch.Seq, ackErr)
				}
				continue
			}
			log.Printf("sender: batch %d: %v", batch.Seq, err)
			return ctx.Err() == nil
		}
		if err := s.Store.Ack(batch.Seq); err != nil {
			log.Printf("sender: ack batch %d: %v", batch.Seq, err)
			return false
		}
	}
	return true
}
