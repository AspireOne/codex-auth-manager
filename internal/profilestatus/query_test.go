package profilestatus

import (
	"errors"
	"testing"
	"time"

	"codex-manage/internal/codexapp"
)

func TestShortestWindowSelectsShortestUsableWindow(t *testing.T) {
	primaryPercent, secondaryPercent := 17.6, 42.4
	primaryMinutes, secondaryMinutes := int64(10080), int64(300)
	primaryReset, secondaryReset := int64(2_000_000_000), int64(1_900_000_000)
	window, err := shortestWindow(rateLimit{
		Primary:   &rateWindow{UsedPercent: &primaryPercent, WindowDurationMin: &primaryMinutes, ResetsAt: &primaryReset},
		Secondary: &rateWindow{UsedPercent: &secondaryPercent, WindowDurationMin: &secondaryMinutes, ResetsAt: &secondaryReset},
	})
	if err != nil {
		t.Fatal(err)
	}
	if window != nil && *window.UsedPercent != secondaryPercent {
		t.Fatalf("selected percent = %v, want %v", *window.UsedPercent, secondaryPercent)
	}
}

func TestStatusFromRateLimitsPrefersCodexBucketAndRoundsUsage(t *testing.T) {
	preferredPercent, fallbackPercent := 17.6, 99.0
	minutes := int64(300)
	preferredReset, fallbackReset := int64(1_900_000_000), int64(2_000_000_000)
	fetched := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	status, err := statusFromRateLimits(rateLimitsResponse{
		RateLimitsByLimitID: map[string]rateLimit{"codex": {Primary: &rateWindow{UsedPercent: &preferredPercent, WindowDurationMin: &minutes, ResetsAt: &preferredReset}}},
		RateLimits:          &rateLimit{Primary: &rateWindow{UsedPercent: &fallbackPercent, WindowDurationMin: &minutes, ResetsAt: &fallbackReset}},
	}, fetched)
	if err != nil {
		t.Fatal(err)
	}
	if status.UsedPercent == nil || *status.UsedPercent != 18 {
		t.Fatalf("used percent = %v, want 18", status.UsedPercent)
	}
	if status.ResetsAt == nil || status.ResetsAt.Unix() != preferredReset {
		t.Fatalf("reset = %v, want %d", status.ResetsAt, preferredReset)
	}
}

func TestStatusFromRateLimitsUsesCompatibleFallback(t *testing.T) {
	percent := 12.0
	minutes, reset := int64(300), int64(1_900_000_000)
	status, err := statusFromRateLimits(rateLimitsResponse{RateLimits: &rateLimit{
		Primary: &rateWindow{UsedPercent: &percent, WindowDurationMin: &minutes, ResetsAt: &reset},
	}}, time.Now())
	if err != nil || status.UsedPercent == nil || *status.UsedPercent != 12 {
		t.Fatalf("status/error = %#v/%v", status, err)
	}
}

func TestShortestWindowRejectsIncompleteLimits(t *testing.T) {
	percent := 10.0
	if _, err := shortestWindow(rateLimit{Primary: &rateWindow{UsedPercent: &percent}}); err == nil {
		t.Fatal("shortestWindow() error = nil, want unusable-window error")
	}
}

func TestClassifyErrorOnlyMapsExplicitAuthenticationFailures(t *testing.T) {
	if !errors.Is(classifyError(&codexapp.RPCError{Message: "ChatGPT authentication required"}), ErrSignInRequired) {
		t.Fatal("authentication rejection was not classified as sign-in required")
	}
	if errors.Is(classifyError(&codexapp.RPCError{Message: "backend unavailable"}), ErrSignInRequired) {
		t.Fatal("generic rejection was classified as sign-in required")
	}
}
