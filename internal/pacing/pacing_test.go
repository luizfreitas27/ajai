package pacing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/config"
)

func testPacer(t *testing.T, cfg config.Pacing) (*Pacer, *time.Time, *[]time.Duration) {
	t.Helper()
	p := New(cfg, false)
	clock := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	var slept []time.Duration
	p.now = func() time.Time { return clock }
	p.sleep = func(ctx context.Context, d time.Duration) error { slept = append(slept, d); return ctx.Err() }
	return p, &clock, &slept
}

func TestCapsPerRunAndPerRollingHour(t *testing.T) {
	cfg := config.Defaults().Pacing
	cfg.MaxPerHour, cfg.MaxPerRun = 2, 3
	p, clock, _ := testPacer(t, cfg)
	p.RecordApplication()
	p.RecordApplication()
	var le *LimitError
	if err := p.Allow(); !errors.As(err, &le) || le.Cap != "per hour" || !errors.Is(err, ErrLimit) {
		t.Errorf("%v", err)
	}
	*clock = clock.Add(61 * time.Minute) // the hour rolled
	if err := p.Allow(); err != nil {
		t.Errorf("%v", err)
	}
	p.RecordApplication()
	if err := p.Allow(); !errors.As(err, &le) || le.Cap != CapPerRun || p.Applied() != 3 {
		t.Errorf("%v", err)
	}
	// A new run starts the per-run count again, but the hourly history carries over.
	p.NewRun()
	if p.Applied() != 0 || p.Total() != 3 {
		t.Errorf("applied %d total %d", p.Applied(), p.Total())
	}
	if err := p.Allow(); err != nil {
		t.Errorf("one application in the last hour, cap 2: %v", err)
	}
	p.RecordApplication()
	if err := p.Allow(); !errors.As(err, &le) || le.Cap != CapPerHour {
		t.Errorf("the hourly cap must survive NewRun: %v", err)
	}
}

func TestPausesStayWithinTheConfiguredRangesAndFastShortensJobs(t *testing.T) {
	cfg := config.Defaults().Pacing
	cfg.BetweenJobsMin, cfg.BetweenJobsMax, cfg.BetweenActionsMin, cfg.BetweenActionsMax = 30, 90, 1, 4
	cfg.TypingDelayMinMs, cfg.TypingDelayMaxMs = 40, 160
	p, _, slept := testPacer(t, cfg)
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		_ = p.BetweenJobs(ctx)
		_ = p.BetweenActions(ctx)
		if d := p.TypingDelay(); d < 40*time.Millisecond || d > 1200*time.Millisecond {
			t.Errorf("typing delay %v", d)
		}
	}
	for i, d := range *slept {
		if i%2 == 0 && (d < 30*time.Second || d > 90*time.Second) {
			t.Errorf("between jobs %v", d)
		}
		if i%2 == 1 && (d < time.Second || d > 4*time.Second) {
			t.Errorf("between actions %v", d)
		}
	}
	fast := New(cfg, true)
	fast.sleep = func(ctx context.Context, d time.Duration) error { *slept = append(*slept, d); return nil }
	*slept = nil
	_ = fast.BetweenJobs(ctx)
	if d := (*slept)[0]; d < 3*time.Second || d > 9*time.Second {
		t.Errorf("fast between jobs %v", d)
	}
}

func TestACancelledContextEndsAPauseEarly(t *testing.T) {
	p := New(config.Defaults().Pacing, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.BetweenJobs(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("%v", err)
	}
}

func TestPauseSecondsStaysWithinItsBoundsAndIsExactWhenEqual(t *testing.T) {
	p, _, slept := testPacer(t, config.Defaults().Pacing)
	for i := 0; i < 200; i++ {
		if err := p.PauseSeconds(context.Background(), 90, 300); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range *slept {
		if d < 90*time.Second || d > 300*time.Second {
			t.Fatalf("pause %v outside 90s..300s", d)
		}
	}
	if err := p.PauseSeconds(context.Background(), 5, 5); err != nil {
		t.Fatal(err)
	}
	if got := (*slept)[len(*slept)-1]; got != 5*time.Second {
		t.Errorf("equal bounds paused %v, want 5s", got)
	}
}
