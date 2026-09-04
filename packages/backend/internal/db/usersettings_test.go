package db

import (
	"testing"
	"time"
)

// TestTierLimits locks down the tank/compilation quota per tier — item 260's
// submitVersion fix gates on this before ever creating a version record, so a
// regression here would silently change who gets 429'd.
func TestTierLimits(t *testing.T) {
	cases := []struct {
		tier         string
		wantTanks    int
		wantCompiles int
	}{
		{TierFree, 2, 10},
		{TierBuilder, 5, 50},
		{TierPro, 15, 200},
		{"", 2, 10},      // unset tier defaults to Free
		{"bogus", 2, 10}, // unrecognized tier defaults to Free
	}
	for _, c := range cases {
		tanks, compiles := TierLimits(c.tier)
		if tanks != c.wantTanks || compiles != c.wantCompiles {
			t.Errorf("TierLimits(%q) = (%d, %d), want (%d, %d)", c.tier, tanks, compiles, c.wantTanks, c.wantCompiles)
		}
	}
}

// TestResetWindowIfExpired covers the lazy 30-day compile-window reset that
// item 260's quota check relies on before comparing CompilationsThisWindow
// against the tier limit.
func TestResetWindowIfExpired(t *testing.T) {
	t.Run("no windowStart yet — no-op", func(t *testing.T) {
		us := UserSettings{CompilationsThisWindow: 3, WindowStart: ""}
		got, reset := ResetWindowIfExpired(us)
		if reset {
			t.Fatalf("got reset=true, want false")
		}
		if got.CompilationsThisWindow != 3 || got.WindowStart != "" {
			t.Fatalf("got %+v, want unchanged", got)
		}
	})

	t.Run("unparseable windowStart — no-op", func(t *testing.T) {
		us := UserSettings{CompilationsThisWindow: 3, WindowStart: "not-a-timestamp"}
		got, reset := ResetWindowIfExpired(us)
		if reset {
			t.Fatalf("got reset=true, want false")
		}
		if got.CompilationsThisWindow != 3 {
			t.Fatalf("got CompilationsThisWindow=%d, want unchanged 3", got.CompilationsThisWindow)
		}
	})

	t.Run("window still active (< 30 days old) — no-op", func(t *testing.T) {
		us := UserSettings{
			CompilationsThisWindow: 9,
			WindowStart:            time.Now().Add(-29 * 24 * time.Hour).Format(time.RFC3339),
		}
		got, reset := ResetWindowIfExpired(us)
		if reset {
			t.Fatalf("got reset=true, want false")
		}
		if got.CompilationsThisWindow != 9 {
			t.Fatalf("got CompilationsThisWindow=%d, want unchanged 9", got.CompilationsThisWindow)
		}
	})

	t.Run("window expired (>= 30 days old) — resets counter and clears windowStart", func(t *testing.T) {
		us := UserSettings{
			CompilationsThisWindow: 10,
			WindowStart:            time.Now().Add(-31 * 24 * time.Hour).Format(time.RFC3339),
		}
		got, reset := ResetWindowIfExpired(us)
		if !reset {
			t.Fatalf("got reset=false, want true")
		}
		if got.CompilationsThisWindow != 0 {
			t.Fatalf("got CompilationsThisWindow=%d, want 0", got.CompilationsThisWindow)
		}
		if got.WindowStart != "" {
			t.Fatalf("got WindowStart=%q, want empty", got.WindowStart)
		}
	})

	t.Run("window exactly at the 30-day boundary — resets", func(t *testing.T) {
		us := UserSettings{
			CompilationsThisWindow: 5,
			WindowStart:            time.Now().Add(-30 * 24 * time.Hour).Add(-time.Second).Format(time.RFC3339),
		}
		got, reset := ResetWindowIfExpired(us)
		if !reset {
			t.Fatalf("got reset=false, want true")
		}
		if got.CompilationsThisWindow != 0 {
			t.Fatalf("got CompilationsThisWindow=%d, want 0", got.CompilationsThisWindow)
		}
	})
}
