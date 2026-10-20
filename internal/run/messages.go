package run

import (
	"context"
	"strings"

	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/store"
)

// queueMessage queues the recruiter message for a job just applied to, or records
// that the job had no hirer to message. A problem is logged and never fails the
// application: the job is already recorded as applied.
func (r *Runner) queueMessage(ctx context.Context, c linkedin.Card, runID int64) {
	if !r.Cfg.Messages.Enabled || strings.TrimSpace(r.Market.MessageTemplate) == "" {
		return
	}
	to, ok, err := r.Site.HiringTeam(ctx)
	if err != nil {
		r.Logger.Warn("could not read the hiring team", "job", c.ID, "error", err)
		return
	}
	m := store.Message{JobID: c.ID, RunID: runID, State: store.MessageNoRecipient}
	if ok && to.URL != "" { // a hirer without a profile URL cannot be keyed, so it is no recipient
		m = store.Message{JobID: c.ID, RunID: runID, RecipientName: to.Name, RecipientURL: to.URL,
			Text: messages.Render(r.Market.MessageTemplate, to, messages.Job{Title: c.Title, Company: c.Company})}
		if m.Text == "" {
			return
		}
	}
	if _, err := r.Store.QueueMessage(m); err != nil {
		r.Logger.Warn("could not queue the recruiter message", "job", c.ID, "error", err)
	}
}

// Why a message did not go out, as the store records it.
const (
	reasonNoRecipient = "the hirer changed, or only InMail or Premium is offered"
	reasonBlocked     = "LinkedIn blocked the message (restriction or CAPTCHA)"
)

// sendMessages sends the approved messages at the end of a live run, at most
// messages.max_per_day per local day (what was sent today counts), each after a
// jittered pause. A block ends sending for this run; so does a cancelled context.
// A message not sent stays approved for a later run, unless it was tried.
func (r *Runner) sendMessages(ctx context.Context) {
	if !r.Cfg.Messages.Enabled {
		return
	}
	sent, err := r.Store.SentToday()
	if err != nil {
		r.Logger.Warn("could not count the messages sent today", "error", err)
		return
	}
	remaining := r.Cfg.Messages.MaxPerDay - sent
	if remaining <= 0 {
		return
	}
	batch, err := r.Store.NextApproved(remaining)
	if err != nil {
		r.Logger.Warn("could not read the approved messages", "error", err)
		return
	}
	for _, m := range batch {
		if ctx.Err() != nil {
			return
		}
		if err := r.Pacer.PauseSeconds(ctx, r.Cfg.Messages.BetweenMinSeconds, r.Cfg.Messages.BetweenMaxSeconds); err != nil {
			return
		}
		marked, err := r.Store.MarkSending(m.ID) // before the send: a crash after this never sends it again
		if err != nil {
			r.Logger.Warn("could not start sending a message", "message", m.ID, "error", err)
			return
		}
		if !marked {
			r.Logger.Warn("message left the approved state before its send, skipped", "message", m.ID)
			continue
		}
		if !r.sendOne(ctx, m) {
			return
		}
	}
}

// sendOne sends one message that is marked sending and records the outcome. It
// reports false when sending must stop for this run.
func (r *Runner) sendOne(ctx context.Context, m store.Message) bool {
	to := messages.Recipient{Name: m.RecipientName, URL: m.RecipientURL}
	res, err := r.Site.SendMessage(ctx, to, m.JobID, m.Text)
	keepGoing := ctx.Err() == nil
	var serr error
	switch {
	case err != nil:
		_, serr = r.Store.MarkFailed(m.ID, err.Error())
	case res == linkedin.SendSent:
		_, serr = r.Store.MarkSent(m.ID)
		r.Logger.Info("recruiter message sent", "job", m.JobID)
	case res == linkedin.SendNoRecipient:
		_, serr = r.Store.MarkNoRecipient(m.ID, reasonNoRecipient)
	case res == linkedin.SendBlocked:
		_, serr = r.Store.MarkFailed(m.ID, reasonBlocked)
		keepGoing = false
		r.Logger.Warn("LinkedIn blocked recruiter messages; no more are sent in this run")
	default:
		_, serr = r.Store.MarkFailed(m.ID, "unexpected send result: "+string(res))
	}
	if serr != nil {
		r.Logger.Warn("could not record the message's outcome", "message", m.ID, "error", serr)
	}
	return keepGoing
}
