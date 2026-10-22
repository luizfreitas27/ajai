package run

import (
	"context"
	"errors"

	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/store"
)

// BackfillSummary is what Backfill did.
type BackfillSummary struct {
	Checked  int    // unreached jobs whose job page was read
	Hirers   int    // jobs whose page showed a hirer with a profile
	NoHirer  int    // jobs whose page showed none
	Messages int    // recruiter messages sent
	Invites  int    // connection invites sent
	Left     int    // approved messages still unsent, for a later run or day
	Stopped  string // why the backfill ended before its last job or send; "" when it did not
}

// Stop reasons Backfill records in BackfillSummary.Stopped.
const (
	stopCheckpoint = "LinkedIn showed a checkpoint page; nothing more is read or sent"
	stopBlocked    = "LinkedIn blocked a send; nothing more is sent"
	stopCap        = "daily message cap reached; the rest waits for tomorrow"
	stopUser       = "stopped by user"
)

// Backfill writes and sends the recruiter messages and invites that applied jobs
// never got: up to limit jobs (0 for all), newest applied first, that have no
// message to their hirer in a state that goes out and no invite sent. Each job
// page is opened for its hirer, and that hirer is queued by the same path a run
// uses (queueHirer), approved for sending. The approved messages and the queued
// invites then go out as a run's end sends them: the daily caps, a pause before
// each send, one message per recruiter ever. It never searches or applies. A
// checkpoint page or a block stops the backfill, a cancelled context stops it too.
func (r *Runner) Backfill(ctx context.Context, limit int) (BackfillSummary, error) {
	r.defaults()
	r.sendsStopped = false
	var sum BackfillSummary
	jobs, err := r.Store.UnreachedApplied(limit)
	if err != nil {
		return sum, err
	}
	if err := r.Site.EnsureLoggedIn(ctx, r.LoginTimeout, func() {
		r.emit(EventLogin, "Sign in to LinkedIn in the Chrome window; ajai continues when the feed appears.", nil)
	}); err != nil {
		return sum, err
	}
	sentBefore, err := r.Store.SentToday()
	if err != nil {
		return sum, err
	}
	for i, j := range jobs {
		if ctx.Err() != nil || r.sendsStopped {
			break
		}
		if i > 0 {
			if err := r.Pacer.BetweenJobs(ctx); err != nil {
				break
			}
		}
		sum.Checked++
		to, ok, err := r.Site.JobHiringTeam(ctx, j.JobID)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if errors.Is(err, linkedin.ErrBlocked) {
				r.Logger.Warn("LinkedIn showed a checkpoint page; the backfill stops", "job", j.JobID)
				sum.Stopped, r.sendsStopped = stopCheckpoint, true
				break
			}
			r.Logger.Warn("could not read the hiring team", "job", j.JobID, "error", err)
			continue
		}
		if ok && to.URL != "" {
			sum.Hirers++
		} else {
			sum.NoHirer++
		}
		r.Logger.Info("backfill: job read", "job", j.JobID, "hirer", to.Name)
		r.queueHirer(ctx, linkedin.Card{ID: j.JobID, Title: j.Title, Company: j.Company}, j.Description, 0, to, ok, true)
	}
	if ctx.Err() == nil {
		r.sendMessages(ctx)
		sum.Invites = r.sendInvites(ctx)
	}
	sentAfter, err := r.Store.SentToday()
	if err != nil {
		return sum, err
	}
	sum.Messages = sentAfter - sentBefore
	approved, err := r.Store.ListMessages(store.MessageApproved)
	if err != nil {
		return sum, err
	}
	sum.Left = len(approved)
	switch {
	case ctx.Err() != nil && sum.Stopped == "":
		sum.Stopped = stopUser
	case r.sendsStopped && sum.Stopped == "":
		sum.Stopped = stopBlocked
	case r.Cfg.Messages.Enabled && sum.Left > 0 && sentAfter >= r.Cfg.Messages.MaxPerDay:
		sum.Stopped = stopCap
	}
	r.Logger.Info("backfill done", "checked", sum.Checked, "hirers", sum.Hirers, "no_hirer", sum.NoHirer,
		"messages", sum.Messages, "invites", sum.Invites, "left", sum.Left, "stopped", sum.Stopped)
	return sum, ctx.Err()
}
