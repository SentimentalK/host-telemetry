package clockwatch_test

import (
	"testing"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/clockwatch"
)

func TestStep(t *testing.T) {
	base := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		wall     time.Duration
		mono     time.Duration
		wantSkew time.Duration
		wantJump bool
	}{
		{name: "wall forward", wall: 5 * time.Second, mono: time.Second, wantSkew: 4 * time.Second, wantJump: true},
		{name: "wall backward", wall: -5 * time.Second, mono: time.Second, wantSkew: -6 * time.Second, wantJump: true},
		{name: "no jump", wall: 2 * time.Second, mono: 2 * time.Second, wantSkew: 0, wantJump: false},
		{name: "threshold boundary", wall: 2 * time.Second, mono: time.Second, wantSkew: time.Second, wantJump: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			watch := &clockwatch.Watch{
				Wall:      func() time.Time { return base.Add(tc.wall * time.Duration(calls)) },
				Mono:      func() time.Time { return base.Add(tc.mono * time.Duration(calls)) },
				Threshold: time.Second,
			}

			skew, jumped := watch.Step()
			if skew != 0 || jumped {
				t.Fatalf("first Step() = (%s, %v), want (0, false)", skew, jumped)
			}

			calls = 1
			skew, jumped = watch.Step()
			if skew != tc.wantSkew || jumped != tc.wantJump {
				t.Fatalf("Step() = (%s, %v), want (%s, %v)", skew, jumped, tc.wantSkew, tc.wantJump)
			}
		})
	}
}

func TestDefaultThreshold(t *testing.T) {
	base := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)

	within := &clockwatch.Watch{
		Wall: func() time.Time { return base },
		Mono: func() time.Time { return base },
	}
	within.Step()
	if skew, jumped := within.Step(); skew != 0 || jumped {
		t.Fatalf("Step() = (%s, %v) without a configured threshold, want (0, false)", skew, jumped)
	}

	calls := 0
	jumped := &clockwatch.Watch{
		Wall: func() time.Time { return base.Add(time.Duration(calls) * 3 * time.Second) },
		Mono: func() time.Time { return base.Add(time.Duration(calls) * time.Second) },
	}
	jumped.Step()
	calls = 1
	if skew, wasJumped := jumped.Step(); skew != 2*time.Second || !wasJumped {
		t.Fatalf("Step() = (%s, %v), want (2s, true)", skew, wasJumped)
	}
}
