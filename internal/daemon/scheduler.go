package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/luizfreitas27/aja/internal/run"
	"github.com/luizfreitas27/aja/internal/schedule"
	"github.com/luizfreitas27/aja/internal/store"
)

// TickEvery is how often the scheduler looks at the clock.
const TickEvery = 30 * time.Second

// The reasons the scheduler stops its run, recorded as the run's ending reason.
const (
	ReasonWindowClosed   = "schedule window closed"
	ReasonWindowDisabled = "schedule window disabled"
	ReasonWindowRemoved  = "schedule window removed"
)

// Scheduler starts and stops runs on the schedule windows.
type Scheduler struct {
	Store    *store.Store
	Sup      *Supervisor
	Notifier run.Notifier // nil: no Telegram notices
	Logger   *slog.Logger
	Now      func() time.Time

	wake chan struct{}
}

// Wake asks for a tick now (a run just ended: the next opening may be waiting).
func (s *Scheduler) Wake() {
	if s.wake == nil {
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Loop ticks at once, every interval, and when woken, until ctx ends.
func (s *Scheduler) Loop(ctx context.Context, every time.Duration) {
	s.wake = make(chan struct{}, 1)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Error("scheduler tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
	}
}

// Tick makes the decisions for this moment:
//   - the scheduler's own run is stopped when its window has closed (or was
//     disabled or removed meanwhile);
//   - an opening that is due gets its run, unless another run is in progress:
//     then it is skipped (recorded, notified) and never retried. While the
//     scheduler's own run of the window before is still ending, the next opening
//     waits for it instead (back-to-back windows hand over).
func (s *Scheduler) Tick(ctx context.Context) error {
	now := s.Now()
	windows, err := s.Store.ListWindows()
	if err != nil {
		return err
	}
	if info, ok := s.Sup.Current(); ok && info.Request.Origin == run.OriginSchedule && info.Stopping == "" {
		if reason := closedReason(windows, info.Request.Opening, now); reason != "" {
			_, _ = s.Sup.Stop(reason)
		}
	}
	handled, err := s.Store.HandledOpenings(now.Format(time.DateOnly))
	if err != nil {
		return err
	}
	due, ok := schedule.Due(windows, handled, now)
	if !ok {
		return nil
	}
	if info, busy := s.Sup.Current(); busy && info.Request.Origin == run.OriginSchedule && info.Stopping != "" {
		return nil // our run of the window before is ending; this opening starts after it
	}
	w := due.Window
	err = s.Sup.Start(ctx, Request{Origin: run.OriginSchedule, Market: w.Market, DryRun: w.DryRun, Opening: due.Key()})
	var held *run.HeldError
	switch {
	case errors.As(err, &held):
		s.Logger.Info("schedule window skipped", "window", w.ID, "holder", held.Holder.String())
		s.notify(fmt.Sprintf("aja skipped schedule window %d (%s) today: %s was in progress.", w.ID, w, held.Holder))
		return s.Store.RecordOpening(due.Key(), schedule.OutcomeSkipped, held.Holder.String(), 0)
	case err != nil:
		if ctx.Err() != nil {
			return nil // the daemon is stopping: this opening is not handled
		}
		s.Logger.Error("schedule window could not start", "window", w.ID, "error", err)
		s.notify(fmt.Sprintf("aja could not start the run for schedule window %d (%s).\n\n%s", w.ID, w, err))
		return s.Store.RecordOpening(due.Key(), schedule.OutcomeFailed, err.Error(), 0)
	}
	s.notify(fmt.Sprintf("aja started a run for schedule window %d: %s.", w.ID, w))
	return s.Store.RecordOpening(due.Key(), schedule.OutcomeStarted, "", 0)
}

// RunEnded records the run id of a scheduled run once it has ended, and wakes
// the loop: an opening may be waiting for this run (Supervisor.OnEnd).
func (s *Scheduler) RunEnded(e Ended) {
	if e.Info.Request.Origin == run.OriginSchedule && len(e.Summaries) > 0 && e.Summaries[0].RunID != 0 {
		if err := s.Store.RecordOpening(e.Info.Request.Opening, schedule.OutcomeStarted, "", e.Summaries[0].RunID); err != nil {
			s.Logger.Warn("could not record the run of an opening", "error", err)
		}
	}
	s.Wake()
}

// closedReason is why the scheduler's run of opening key must stop now, or "".
func closedReason(windows []schedule.Window, key schedule.Key, now time.Time) string {
	if cur, ok := schedule.Current(windows, now); ok && cur.Key() == key {
		return ""
	}
	for _, w := range windows {
		if w.ID == key.WindowID {
			if !w.Enabled {
				return ReasonWindowDisabled
			}
			return ReasonWindowClosed
		}
	}
	return ReasonWindowRemoved
}

func (s *Scheduler) notify(text string) {
	if s.Notifier != nil {
		s.Notifier.Send(context.Background(), text)
	}
}
