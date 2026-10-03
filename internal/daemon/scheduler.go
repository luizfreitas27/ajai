package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/luizfreitas27/ajai/internal/logging"
	"github.com/luizfreitas27/ajai/internal/run"
	"github.com/luizfreitas27/ajai/internal/schedule"
	"github.com/luizfreitas27/ajai/internal/store"
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
	store      *store.Store
	supervisor *Supervisor
	notifier   run.Notifier // nil: no Telegram notices
	logger     *slog.Logger
	now        func() time.Time
	wake       chan struct{}
}

// NewScheduler makes the scheduler and has it told when the supervisor's runs
// end (Supervisor.OnEnd).
func NewScheduler(st *store.Store, sup *Supervisor, notifier run.Notifier, logger *slog.Logger, now func() time.Time) *Scheduler {
	if logger == nil {
		logger = logging.Discard
	}
	s := &Scheduler{store: st, supervisor: sup, notifier: notifier, logger: logger, now: now, wake: make(chan struct{}, 1)}
	sup.OnEnd = s.RunEnded
	return s
}

// Wake asks for a tick now (a run just ended: the next opening may be waiting).
func (s *Scheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Loop ticks at once, every interval, and when woken, until ctx ends.
func (s *Scheduler) Loop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.logger.Error("scheduler tick failed", "error", err)
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
	now := s.now()
	windows, err := s.store.ListWindows()
	if err != nil {
		return err
	}
	if info, ok := s.supervisor.Current(); ok && info.Request.Origin == run.OriginSchedule && info.Stopping == "" {
		if reason := closedReason(windows, info.Request.Opening, now); reason != "" {
			opening := info.Request.Opening
			s.supervisor.StopIf(reason, func(i Info) bool {
				return i.Request.Origin == run.OriginSchedule && i.Request.Opening == opening
			})
		}
	}
	handled, err := s.store.HandledOpenings(now.Format(time.DateOnly))
	if err != nil {
		return err
	}
	due, ok := schedule.Due(windows, handled, now)
	if !ok {
		return nil
	}
	if info, busy := s.supervisor.Current(); busy && info.Request.Origin == run.OriginSchedule && info.Stopping != "" {
		return nil // our run of the window before is ending; this opening starts after it
	}
	w := due.Window
	err = s.supervisor.Start(ctx, Request{Origin: run.OriginSchedule, Market: w.Market, DryRun: w.DryRun, Opening: due.Key()})
	var held *run.HeldError
	switch {
	case errors.As(err, &held):
		s.logger.Info("schedule window skipped", "window", w.ID, "holder", held.Holder.String())
		s.notify(fmt.Sprintf("ajai skipped schedule window %d (%s) today: %s was in progress.", w.ID, w, held.Holder))
		return s.store.RecordOpening(due.Key(), schedule.OutcomeSkipped, held.Holder.String(), 0)
	case err != nil:
		if ctx.Err() != nil {
			return nil // the daemon is stopping: this opening is not handled
		}
		s.logger.Error("schedule window could not start", "window", w.ID, "error", err)
		s.notify(fmt.Sprintf("ajai could not start the run for schedule window %d (%s).\n\n%s", w.ID, w, err))
		return s.store.RecordOpening(due.Key(), schedule.OutcomeFailed, err.Error(), 0)
	}
	s.notify(fmt.Sprintf("ajai started a run for schedule window %d: %s.", w.ID, w))
	return s.store.RecordOpening(due.Key(), schedule.OutcomeStarted, "", 0)
}

// RunEnded is told of every run the supervisor ran (Supervisor.OnEnd). For a
// scheduled run it records the run id on the opening, or, when the daemon's own
// stop cut the run short (a reboot, a logout), forgets the opening, so the
// window resumes when the daemon is back within it. Then it wakes the loop: an
// opening may be waiting for this run.
func (s *Scheduler) RunEnded(e Ended) {
	if e.Info.Request.Origin == run.OriginSchedule {
		key := e.Info.Request.Opening
		var err error
		switch {
		case e.Shutdown:
			err = s.store.ForgetOpening(key)
			s.logger.Info("schedule window interrupted by the daemon stopping; it resumes when the daemon is back", "window", key.WindowID)
		case len(e.Summaries) > 0 && e.Summaries[0].RunID != 0:
			err = s.store.RecordOpening(key, schedule.OutcomeStarted, "", e.Summaries[0].RunID)
		}
		if err != nil {
			s.logger.Warn("could not record the end of a scheduled run", "error", err)
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
	if s.notifier != nil {
		s.notifier.Send(context.Background(), text)
	}
}
