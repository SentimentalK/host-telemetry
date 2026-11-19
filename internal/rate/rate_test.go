package rate_test

import (
	"math"
	"testing"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/rate"
)

func TestRate(t *testing.T) {
	start := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		prev   float64
		cur    float64
		prevAt time.Time
		curAt  time.Time
		want   float64
		ok     bool
	}{
		{name: "normal increment", prev: 10, cur: 30, prevAt: start, curAt: start.Add(2 * time.Second), want: 10, ok: true},
		{name: "counter rollback", prev: 30, cur: 10, prevAt: start, curAt: start.Add(2 * time.Second), want: 0, ok: false},
		{name: "zero delta", prev: 10, cur: 20, prevAt: start, curAt: start, want: 0, ok: false},
		{name: "negative delta", prev: 10, cur: 20, prevAt: start, curAt: start.Add(-2 * time.Second), want: 0, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, ok := rate.Rate(tc.prev, tc.cur, tc.prevAt, tc.curAt)
			if ok != tc.ok {
				t.Fatalf("Rate() ok = %v, want %v", ok, tc.ok)
			}
			if math.Abs(value-tc.want) > 1e-9 {
				t.Fatalf("Rate() = %v, want %v", value, tc.want)
			}
		})
	}
}

func TestRateAcrossWallJumps(t *testing.T) {
	start := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	type clock struct {
		wall time.Time
		mono time.Time
	}
	cases := []struct {
		name string
		jump time.Duration
	}{
		{name: "positive jump", jump: 5 * time.Second},
		{name: "negative jump", jump: -5 * time.Second},
		{name: "zero jump", jump: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := clock{wall: start, mono: start}
			prevMono := c.mono
			c.wall = c.wall.Add(tc.jump + time.Second)
			c.mono = c.mono.Add(time.Second)

			value, ok := rate.Rate(100, 110, prevMono, c.mono)
			if !ok {
				t.Fatalf("Rate() ok = false, want true")
			}
			if math.Abs(value-10) > 1e-9 {
				t.Fatalf("Rate() = %v, want 10", value)
			}
		})
	}
}

func TestUtilization(t *testing.T) {
	start := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		prev   float64
		cur    float64
		prevAt time.Time
		curAt  time.Time
		units  float64
		want   float64
		ok     bool
	}{
		{name: "normal", prev: 10, cur: 12, prevAt: start, curAt: start.Add(2 * time.Second), units: 2, want: 0.5, ok: true},
		{name: "clamped", prev: 10, cur: 20, prevAt: start, curAt: start.Add(time.Second), units: 2, want: 1, ok: true},
		{name: "zero units", prev: 10, cur: 11, prevAt: start, curAt: start.Add(2 * time.Second), units: 0, want: 0.5, ok: true},
		{name: "counter rollback", prev: 20, cur: 10, prevAt: start, curAt: start.Add(2 * time.Second), units: 2, want: 0, ok: false},
		{name: "zero delta", prev: 10, cur: 20, prevAt: start, curAt: start, units: 2, want: 0, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, ok := rate.Utilization(tc.prev, tc.cur, tc.prevAt, tc.curAt, tc.units)
			if ok != tc.ok {
				t.Fatalf("Utilization() ok = %v, want %v", ok, tc.ok)
			}
			if math.Abs(value-tc.want) > 1e-9 {
				t.Fatalf("Utilization() = %v, want %v", value, tc.want)
			}
		})
	}
}
