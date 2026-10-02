package run

import (
	"context"
	"fmt"
	"time"

	"github.com/luizfreitas27/aja/internal/config"
)

// defaultNonStopWait is the pause between the runs of a non-stop session, the
// ten minutes the Python bot slept.
const defaultNonStopWait = 10 * time.Minute

// Session is one invocation of aja run: a single run, or, with
// settings.run_non_stop, run after run until one ends in a way that should not
// be retried (End.Continues), with the Python bot's filter changes between runs
// (FilterCycle) and NonStopWait in between. Each run is recorded and reported
// on its own. The configuration is restored when the session ends.
func (r *Runner) Session(ctx context.Context, opts Options) ([]Summary, error) {
	if !r.Cfg.Settings.RunNonStop {
		sum, err := r.Run(ctx, opts)
		return []Summary{sum}, err
	}
	if r.NonStopWait == 0 {
		r.NonStopWait = defaultNonStopWait
	}
	if r.Sleep == nil {
		r.Sleep = sleep
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
		next := cycle.Peek()
		r.emit(EventWait, fmt.Sprintf("non-stop: next run at %s (sort %q, date posted %q)",
			r.now().Add(r.NonStopWait).Format("15:04"), next.SortBy, next.DatePosted), nil)
		if err := r.Sleep(ctx, r.NonStopWait); err != nil {
			return all, nil // Ctrl-C during the wait ends the session cleanly
		}
	}
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// datePostedSteps is LinkedIn's date-posted filter, widest first.
var datePostedSteps = []string{"Any time", "Past month", "Past week", "Past 24 hours"}

// FilterCycle yields the search filters for each run of a non-stop session, the
// way the Python bot changed them between runs:
//   - the first run uses the configured filters;
//   - before each later round, with cycle_date_posted, the date-posted filter
//     narrows one step towards "Past 24 hours" and stays there (the Python
//     default, stop_date_cycle_at_24hr);
//   - with alternate_sort_by, each round is two runs: first with the other sort
//     order, then with the configured one ("" is LinkedIn's default, "Most
//     relevant").
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
	if !c.started {
		c.started = true
		return c.current
	}
	if len(c.queue) == 0 {
		c.fill()
	}
	next := c.queue[0]
	c.queue = c.queue[1:]
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
		c.current.DatePosted = narrower(c.current.DatePosted)
	}
	if c.alternate {
		other := c.current
		other.SortBy = otherSort(c.current.SortBy)
		c.queue = append(c.queue, other)
	}
	c.queue = append(c.queue, c.current)
}

func narrower(date string) string {
	for i, step := range datePostedSteps {
		if step == date && i+1 < len(datePostedSteps) {
			return datePostedSteps[i+1]
		}
	}
	if date == "" {
		return datePostedSteps[1] // no filter is "Any time"
	}
	return date // already the narrowest, or a value LinkedIn does not list
}

func otherSort(sort string) string {
	if sort == "Most recent" {
		return "Most relevant"
	}
	return "Most recent"
}
