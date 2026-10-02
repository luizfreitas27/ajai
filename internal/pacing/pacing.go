// Package pacing keeps a run at a human rhythm (ADR 0001): caps on applications
// per rolling hour and per run, jittered pauses between jobs and between form
// actions, and humanised typing delays. Reaching a cap ends the run cleanly.
package pacing

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/luizfreitas27/aja/internal/config"
)

// ErrLimit is returned by Allow when a cap is reached; the message says which.
var ErrLimit = errors.New("pacing limit reached")

// Pacer enforces the limits. Zero value is not usable; use New.
type Pacer struct {
	cfg     config.Pacing
	fast    bool // dry-run only: long pauses shortened
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
	rand    *rand.Rand
	applied []time.Time
}

// New builds a pacer from the config. fast shortens the between-jobs pauses to
// a tenth; callers allow it only together with --dry-run.
func New(cfg config.Pacing, fast bool) *Pacer {
	return &Pacer{cfg: cfg, fast: fast, now: time.Now, sleep: sleepCtx, rand: rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 7))}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// LimitError says which cap was reached.
type LimitError struct {
	Cap   string // "per hour" or "per run"
	Limit int
}

func (e *LimitError) Error() string { return "pacing limit reached: " + e.Cap }
func (e *LimitError) Unwrap() error { return ErrLimit }

// Allow reports whether one more application may be made now.
func (p *Pacer) Allow() error {
	if len(p.applied) >= p.cfg.MaxPerRun {
		return &LimitError{Cap: "per run", Limit: p.cfg.MaxPerRun}
	}
	cutoff := p.now().Add(-time.Hour)
	recent := 0
	for _, t := range p.applied {
		if t.After(cutoff) {
			recent++
		}
	}
	if recent >= p.cfg.MaxPerHour {
		return &LimitError{Cap: "per hour", Limit: p.cfg.MaxPerHour}
	}
	return nil
}

// RecordApplication counts one application (or, in a dry run, one that would have been made).
func (p *Pacer) RecordApplication() { p.applied = append(p.applied, p.now()) }

// Applied is how many applications this run made.
func (p *Pacer) Applied() int { return len(p.applied) }

func (p *Pacer) between(lo, hi int, unit time.Duration) time.Duration {
	if hi < lo {
		hi = lo
	}
	d := time.Duration(lo) * unit
	if hi > lo {
		d += time.Duration(p.rand.IntN(hi-lo+1)) * unit
	}
	return d
}

// BetweenJobs pauses before moving to the next job.
func (p *Pacer) BetweenJobs(ctx context.Context) error {
	d := p.between(p.cfg.BetweenJobsMin, p.cfg.BetweenJobsMax, time.Second)
	if p.fast {
		d /= 10
	}
	return p.sleep(ctx, d)
}

// BetweenActions pauses between two form actions.
func (p *Pacer) BetweenActions(ctx context.Context) error {
	return p.sleep(ctx, p.between(p.cfg.BetweenActionsMin, p.cfg.BetweenActionsMax, time.Second))
}

// TypingDelay is the pause before the next typed character; about one key in
// forty gets a longer "thinking" pause, as the Python human_type did.
func (p *Pacer) TypingDelay() time.Duration {
	d := p.between(p.cfg.TypingDelayMinMs, p.cfg.TypingDelayMaxMs, time.Millisecond)
	if p.rand.IntN(40) == 0 {
		d += time.Duration(600+p.rand.IntN(400)) * time.Millisecond
	}
	return d
}
