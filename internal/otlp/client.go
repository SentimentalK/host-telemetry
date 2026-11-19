package otlp

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var ErrPermanent = errors.New("otlp: permanent error")

const (
	initialBackoff = time.Second
	maxBackoff     = 30 * time.Second
	maxRetryAfter  = 60 * time.Second
	defaultTimeout = 10 * time.Second
)

type Config struct {
	Endpoint string
	Token    string
	Timeout  time.Duration
}

type Client struct {
	Config
	HTTPClient *http.Client
	Sleep      func(context.Context, time.Duration) error
	Now        func() time.Time
	Rand       *rand.Rand
}

func (c *Client) Send(ctx context.Context, body []byte) error {
	compressed, err := gzipBytes(body)
	if err != nil {
		return err
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		timeout := c.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	random := c.Rand
	if random == nil {
		random = rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0))
	}

	backoff := initialBackoff
	for {
		delay, retryAfterSet, err := c.attempt(ctx, httpClient, compressed, now)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrPermanent) {
			return err
		}
		if ctx.Err() != nil {
			return err
		}
		wait := backoff
		if retryAfterSet {
			wait = delay
		} else {
			wait = time.Duration(float64(backoff) * (0.8 + 0.4*random.Float64()))
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (c *Client) attempt(ctx context.Context, httpClient *http.Client, body []byte, now func() time.Time) (time.Duration, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, false, fmt.Errorf("otlp: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "gzip")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("otlp: send: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return 0, false, nil
	case code == http.StatusRequestTimeout || code == http.StatusTooEarly || code == http.StatusTooManyRequests || code >= 500:
		delay, ok := retryAfter(resp.Header, now())
		return delay, ok, fmt.Errorf("otlp: status %d", code)
	default:
		return 0, false, fmt.Errorf("%w: status %d", ErrPermanent, code)
	}
}

func retryAfter(header http.Header, now time.Time) (time.Duration, bool) {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0, false
	}
	delay := time.Duration(0)
	if seconds, err := strconv.Atoi(value); err == nil {
		delay = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(value); err == nil {
		delay = at.Sub(now)
	} else {
		return 0, false
	}
	if delay < 0 {
		delay = 0
	}
	if delay > maxRetryAfter {
		delay = maxRetryAfter
	}
	return delay, true
}

func gzipBytes(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(body); err != nil {
		return nil, fmt.Errorf("otlp: gzip: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("otlp: gzip: %w", err)
	}
	return buf.Bytes(), nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
