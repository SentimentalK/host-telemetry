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
	for {
		batch, ok, err := s.Store.Next()
		if err != nil {
			log.Printf("sender: next: %v", err)
			return true
		}
		if !ok {
			return true
		}
		if err := s.Client.Send(ctx, batch.Payload); err != nil {
			age := time.Since(batch.At).Round(time.Second)
			if errors.Is(err, otlp.ErrPermanent) {
				log.Printf("sender: dropping batch %d after %s: %v", batch.Seq, age, err)
				if discardErr := s.Store.Discard(batch.Seq, batch.Samples); discardErr != nil {
					log.Printf("sender: discard batch %d: %v", batch.Seq, discardErr)
					return true
				}
				continue
			}
			log.Printf("sender: batch %d after %s: %v", batch.Seq, age, err)
			return ctx.Err() == nil
		}
		if err := s.Store.Ack(batch.Seq); err != nil {
			log.Printf("sender: ack batch %d: %v", batch.Seq, err)
			return true
		}
	}
}
