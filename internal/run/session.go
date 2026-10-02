package run

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/pacing"
)

// defaultNonStopWait is the pause between the runs of a non-stop session, the
// ten minutes the Python bot slept.
const defaultNonStopWait = 10 * time.Minute

// Session is one invocation of aja run: a single run, or, with
// settings.run_non_stop, run after run until one ends in a way that should not
// be retried (End.Continues), with the Python bot's filter changes between runs
// (FilterCycle) and NonStopWait in between. Each run is recorded and reported
// on its own. The configuration is restored when the session ends. A context
// cancelled with a *StopRequested cause (Lock.WatchStops) records its reason.
func (r *Runner) Session(ctx context.Context, opts Options) ([]Summary, error) {
	r.defaults()
	r.filtersPaused = false
	if !r.Cfg.Settings.RunNonStop {
		sum, err := r.Run(ctx, opts)
		return []Summary{sum}, err
	}
	base := r.Cfg.Search
	defer func() { r.Cfg.Search = base }()
	cycle := NewFilterCycle(base, r.Cfg.Settings.AlternateSortBy, r.Cfg.Settings.CycleDatePosted)
	var all []Summary
	for {
		r.Cfg.Search = cycle.Next()
		r.Pacer.NewRun()
		sum, err := r.Run(ctx, opts)
		all = append(all, sum)
		if err != nil || !sum.End.Continues() {
			return all, err
		}
		next, at := cycle.Peek(), r.Now().Add(r.NonStopWait)
		r.emit(EventWait, fmt.Sprintf("non-stop: next run at %s (sort %q, date posted %q)", at.Format("15:04"), next.SortBy, next.DatePosted), nil)
		r.saveWaiting(sum, opts, at)
		err = r.Sleep(ctx, r.NonStopWait)
		if r.StatePath != "" {
			RemoveState(r.StatePath)
		}
		if err != nil {
			return all, nil // Ctrl-C during the wait ends the session cleanly
		}
	}
}

// saveWaiting records, for aja status, that a non-stop session is between runs.
func (r *Runner) saveWaiting(last Summary, opts Options, next time.Time) {
	if r.StatePath == "" {
		return
	}
	st := State{PID: os.Getpid(), Started: r.started, DryRun: opts.DryRun, Market: last.Market, NextRun: next}
	if err := WriteState(r.StatePath, st); err != nil {
		r.Logger.Warn("could not write the run state", "error", err)
	}
}

// defaults fills what a Runner may leave unset.
func (r *Runner) defaults() {
	if r.Logger == nil {
		r.Logger = slog.New(slog.DiscardHandler)
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.LoginTimeout == 0 {
		r.LoginTimeout = 10 * time.Minute
	}
	if r.NonStopWait == 0 {
		r.NonStopWait = defaultNonStopWait
	}
	if r.Sleep == nil {
		r.Sleep = pacing.Sleep
	}
}

// FilterCycle yields the search filters for each run of a non-stop session,
// reproducing what the Python bot did at run time:
//   - the first run uses the configured filters;
//   - before each later round, with cycle_date_posted, the date-posted filter
//     becomes "Past 24 hours" (the Python bot's code, with its default
//     stop_date_cycle_at_24hr, always landed there) and stays;
//   - with alternate_sort_by, each round is two runs: the sort is flipped, a run,
//     flipped again, a run. Python's flip is `"Most recent" if sort == "Most
//     relevant" else "Most relevant"`, so a configured "" (LinkedIn's default)
//     becomes "Most relevant", then "Most recent", and so on.
type FilterCycle struct {
	current           config.Search
	alternate, narrow bool
	queue             []config.Search
	started           bool
}

// NewFilterCycle starts a cycle from the configured filters.
func NewFilterCycle(base config.Search, alternateSort, cycleDate bool) *FilterCycle {
	return &FilterCycle{current: base, alternate: alternateSort, narrow: cycleDate}
}

// Next returns the filters for the next run.
func (c *FilterCycle) Next() config.Search {
	next := c.Peek()
	if !c.started {
		c.started = true
	} else {
		c.queue = c.queue[1:]
	}
	return next
}

// Peek returns what Next will return, without advancing.
func (c *FilterCycle) Peek() config.Search {
	if !c.started {
		return c.current
	}
	if len(c.queue) == 0 {
		c.fill()
	}
	return c.queue[0]
}

func (c *FilterCycle) fill() {
	if c.narrow {
		c.current.DatePosted = lastDateStep
	}
	if !c.alternate {
		c.queue = append(c.queue, c.current)
		return
	}
	c.current.SortBy = pythonFlip(c.current.SortBy)
	c.queue = append(c.queue, c.current)
	c.current.SortBy = pythonFlip(c.current.SortBy)
	c.queue = append(c.queue, c.current)
}

// lastDateStep is where the Python bot's date cycle always ended up.
const lastDateStep = "Past 24 hours"

func pythonFlip(sort string) string {
	if sort == "Most relevant" {
		return "Most recent"
	}
	return "Most relevant"
}
