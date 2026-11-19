package otlp

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSendSuccess(t *testing.T) {
	var bodies [][]byte
	var auth, contentEncoding, contentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		contentEncoding = r.Header.Get("Content-Encoding")
		contentType = r.Header.Get("Content-Type")
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("gzip: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(reader)
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	slept := 0
	client := &Client{
		Config: Config{Endpoint: server.URL, Token: "secret"},
		Sleep: func(context.Context, time.Duration) error {
			slept++
			return nil
		},
	}
	if err := client.Send(context.Background(), []byte("payload")); err != nil {
		t.Fatal(err)
	}
	if slept != 0 {
		t.Errorf("slept %d times, want 0", slept)
	}
	if auth != "Bearer secret" {
		t.Errorf("authorization = %q", auth)
	}
	if contentEncoding != "gzip" || contentType != "application/x-protobuf" {
		t.Errorf("headers = %q/%q", contentEncoding, contentType)
	}
	if len(bodies) != 1 || !bytes.Equal(bodies[0], []byte("payload")) {
		t.Fatalf("bodies = %q", bodies)
	}
}

func TestRetryAfter(t *testing.T) {
	base := time.Now().Truncate(time.Second)
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var sleeps []time.Duration
	client := &Client{
		Config: Config{Endpoint: server.URL},
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
		Now: func() time.Time { return base },
	}
	if err := client.Send(context.Background(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2", requests)
	}
	if len(sleeps) != 1 || sleeps[0] != 2*time.Second {
		t.Fatalf("sleeps = %v, want [2s]", sleeps)
	}

	t.Run("http-date", func(t *testing.T) {
		attempts := 0
		dateServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			if attempts == 1 {
				w.Header().Set("Retry-After", base.Add(3*time.Second).UTC().Format(http.TimeFormat))
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer dateServer.Close()

		var dates []time.Duration
		dateClient := &Client{
			Config: Config{Endpoint: dateServer.URL},
			Sleep: func(_ context.Context, d time.Duration) error {
				dates = append(dates, d)
				return nil
			},
			Now: func() time.Time { return base },
		}
		if err := dateClient.Send(context.Background(), []byte("x")); err != nil {
			t.Fatal(err)
		}
		if len(dates) != 1 || dates[0] != 3*time.Second {
			t.Fatalf("sleeps = %v, want [3s]", dates)
		}
	})
}

func TestBackoff(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests <= 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var sleeps []time.Duration
	client := &Client{
		Config: Config{Endpoint: server.URL},
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
		Rand: rand.New(rand.NewPCG(7, 11)),
	}
	if err := client.Send(context.Background(), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if requests != 4 {
		t.Fatalf("requests = %d, want 4", requests)
	}

	expectedRand := rand.New(rand.NewPCG(7, 11))
	backoff := time.Second
	var expected []time.Duration
	for range 3 {
		expected = append(expected, time.Duration(float64(backoff)*(0.8+0.4*expectedRand.Float64())))
		backoff *= 2
	}
	if len(sleeps) != len(expected) {
		t.Fatalf("sleeps = %v, want %v", sleeps, expected)
	}
	for i := range expected {
		if sleeps[i] != expected[i] {
			t.Fatalf("sleep[%d] = %v, want %v", i, sleeps[i], expected[i])
		}
	}
}

func TestPermanent(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	client := &Client{
		Config: Config{Endpoint: server.URL},
		Sleep: func(context.Context, time.Duration) error {
			t.Fatal("slept on a permanent error")
			return nil
		},
	}
	err := client.Send(context.Background(), []byte("x"))
	if !errors.Is(err, ErrPermanent) {
		t.Fatalf("err = %v, want ErrPermanent", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}
