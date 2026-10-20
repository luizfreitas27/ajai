package run

import (
	"context"
	"errors"
	"time"

	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/store"
)

// PendingOptions is what ajai messages send does.
type PendingOptions struct {
	// AppliedSince, when set, also backfills: every job applied to on or after it
	// with no hirer recorded has its job page read for one.
	AppliedSince time.Time
	DryRun       bool // read the hirers and say what would be sent; send and write nothing
}

// What SendPending did with one job, as SendLine.Outcome says it.
const (
	LineSent      = "sent"
	LineWouldSend = "would send"
	LineNoFree    = "no free message"
	LineMessaged  = "already messaged"
	LineCap       = "cap reached"
	LineFailed    = "failed"
)

// SendLine is one job's outcome in SendPending.
type SendLine struct {
	JobID, Title, Company string
	Outcome               string // one of the Line* constants
	Detail                string // the recipient, or why it failed
}

// PendingSummary counts what SendPending did.
type PendingSummary struct {
	Sent    int  // sent, or that would be on a dry run
	Left    int  // left at the daily cap, for another day
	Blocked bool // LinkedIn blocked a send, so sending stopped
}

// SendPending sends the approved messages and then, with AppliedSince, writes
// and sends a message for each applied job that has none, with the same checks
// a run makes: free messages only, one per recruiter, messages.max_per_day a
// local day and a pause before each send. It never searches or applies. A block
// or a cancelled context stops it.
func (r *Runner) SendPending(ctx context.Context, opts PendingOptions, report func(SendLine)) (PendingSummary, error) {
	r.defaults()
	r.sendsStopped = false
	var sum PendingSummary
	if !opts.DryRun {
		if n, err := r.Store.FailSendingMessages(store.InterruptedSend); err != nil {
			return sum, err
		} else if n > 0 {
			r.Logger.Warn("ended messages a dead process left sending", "messages", n)
		}
	}
	sentToday, err := r.Store.SentToday()
	if err != nil {
		return sum, err
	}
	if err := r.Site.EnsureLoggedIn(ctx, r.LoginTimeout, func() {
		r.emit(EventLogin, "Sign in to LinkedIn in the Chrome window; ajai continues when the feed appears.", nil)
	}); err != nil {
		return sum, err
	}
	line := func(j *store.Job, id, outcome, detail string) {
		l := SendLine{JobID: id, Outcome: outcome, Detail: detail}
		if j != nil {
			l.Title, l.Company = j.Title, j.Company
		}
		report(l)
	}
	atCap := func() bool { return sentToday+sum.Sent >= r.Cfg.Messages.MaxPerDay }
	// send sends m, approved, after the pause, and reports its outcome.
	send := func(j *store.Job, m store.Message, here bool) error {
		if err := r.Pacer.PauseSeconds(ctx, r.Cfg.Messages.BetweenMinSeconds, r.Cfg.Messages.BetweenMaxSeconds); err != nil {
			return err
		}
		marked, err := r.Store.MarkSending(m.ID) // before the send: a crash after this never sends it again
		if err != nil {
			return err
		}
		if !marked {
			line(j, m.JobID, LineFailed, "the message left the approved state before its send")
			return nil
		}
		keepGoing := r.sendOne(ctx, m, here)
		got, err := r.Store.GetMessage(m.ID)
		if err != nil || got == nil {
			return err
		}
		switch got.State {
		case store.MessageSent:
			sum.Sent++
			line(j, m.JobID, LineSent, m.RecipientName+" "+m.RecipientURL)
		case store.MessageNoRecipient:
			line(j, m.JobID, LineNoFree, got.Error)
		default:
			line(j, m.JobID, LineFailed, got.Error)
		}
		sum.Blocked = !keepGoing && got.Error == reasonBlocked
		return ctx.Err()
	}

	approved, err := r.Store.ListMessages(store.MessageApproved)
	if err != nil {
		return sum, err
	}
	for i, m := range approved {
		if ctx.Err() != nil || r.sendsStopped {
			return sum, ctx.Err()
		}
		j, err := r.Store.GetJob(m.JobID)
		if err != nil {
			return sum, err
		}
		if atCap() {
			sum.Left += len(approved) - i
			for _, m := range approved[i:] {
				line(nil, m.JobID, LineCap, "")
			}
			break
		}
		messaged, err := r.Store.HasMessaged(m.RecipientURL, m.ID)
		if err != nil {
			return sum, err
		}
		if messaged {
			if !opts.DryRun {
				if _, err := r.Store.DropApproved(m.ID, reasonMessaged); err != nil {
					return sum, err
				}
			}
			line(j, m.JobID, LineMessaged, m.RecipientURL)
			continue
		}
		if opts.DryRun {
			sum.Sent++
			line(j, m.JobID, LineWouldSend, m.RecipientName+" "+m.RecipientURL)
			continue
		}
		if err := send(j, m, false); err != nil {
			return sum, err
		}
	}
	if opts.AppliedSince.IsZero() || r.sendsStopped {
		return sum, ctx.Err()
	}

	jobs, err := r.Store.AppliedWithoutRecipient(opts.AppliedSince)
	if err != nil {
		return sum, err
	}
	for i := range jobs {
		j := &jobs[i]
		if ctx.Err() != nil || r.sendsStopped {
			return sum, ctx.Err()
		}
		if atCap() {
			sum.Left += len(jobs) - i
			for _, j := range jobs[i:] {
				line(&j, j.JobID, LineCap, "")
			}
			break
		}
		to, ok, err := r.Site.JobHiringTeam(ctx, j.JobID)
		if err != nil {
			if ctx.Err() != nil {
				return sum, ctx.Err()
			}
			if errors.Is(err, linkedin.ErrBlocked) {
				sum.Blocked = true
				line(j, j.JobID, LineFailed, reasonBlocked)
				return sum, nil
			}
			line(j, j.JobID, LineFailed, "could not read the hiring team: "+err.Error())
			continue
		}
		m := store.Message{JobID: j.JobID, State: store.MessageNoRecipient, Error: reasonNoRecipient}
		outcome, detail := LineNoFree, ""
		if ok && to.URL != "" { // a hirer without a profile URL cannot be keyed
			m.RecipientName, m.RecipientURL, detail = to.Name, to.URL, to.Name+" "+to.URL
			messaged, err := r.Store.HasMessaged(to.URL, 0)
			if err != nil {
				return sum, err
			}
			if messaged {
				m.Error, outcome = reasonMessaged, LineMessaged
			} else {
				template := r.templateFor(messages.DetectLanguage(j.Title + "\n" + j.Description))
				m.Text = messages.Render(template, to, messages.Job{Title: j.Title, Company: j.Company})
				if m.Text == "" {
					line(j, j.JobID, LineFailed, "the message template is blank")
					continue
				}
				m.State, m.Error, outcome = store.MessageApproved, "", LineWouldSend
			}
		}
		if opts.DryRun {
			if outcome == LineWouldSend {
				sum.Sent++
			}
			line(j, j.JobID, outcome, detail)
			continue
		}
		id, err := r.Store.InsertMessage(m)
		if err != nil {
			return sum, err
		}
		if outcome != LineWouldSend {
			line(j, j.JobID, outcome, detail)
			continue
		}
		if id == 0 {
			line(j, j.JobID, LineFailed, "a message to this hirer is already recorded for the job")
			continue
		}
		m.ID = id
		if err := send(j, m, true); err != nil { // on the job page JobHiringTeam opened
			return sum, err
		}
	}
	return sum, ctx.Err()
}
