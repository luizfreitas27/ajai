package run

import (
	"context"
	"strings"

	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/store"
)

// queueRecruiter reads the hirer of a job just applied to, then queues what the
// settings ask for: the recruiter message (messages.enabled) and the connection
// invite (messages.connect). A problem is logged and never fails the application:
// the job is already recorded as applied.
func (r *Runner) queueRecruiter(ctx context.Context, c linkedin.Card, description string, runID int64) {
	if !r.Cfg.Messages.Enabled && !r.Cfg.Messages.Connect {
		return
	}
	to, ok, err := r.Site.HiringTeam(ctx)
	if err != nil {
		r.Logger.Warn("could not read the hiring team", "job", c.ID, "error", err)
		return
	}
	if r.Cfg.Messages.Connect && ok && to.URL != "" { // a hirer without a profile URL cannot be keyed
		r.queueInvite(c, to, runID)
	}
	if r.Cfg.Messages.Enabled {
		r.queueMessage(ctx, c, description, runID, to, ok)
	}
}

// queueInvite queues a connection invite to the hirer of a job just applied to.
// A recruiter is invited once, ever, so one already invited is left alone.
func (r *Runner) queueInvite(c linkedin.Card, to messages.Recipient, runID int64) {
	inserted, err := r.Store.QueueInvite(store.Invite{JobID: c.ID, RunID: runID, RecipientName: to.Name, RecipientURL: to.URL})
	if err != nil {
		r.Logger.Warn("could not queue the connection invite", "job", c.ID, "error", err)
		return
	}
	if inserted {
		r.Logger.Info("invite queued", "job", c.ID, "recipient", to.Name)
	}
}

// queueMessage queues the recruiter message for a job just applied to, or records
// that the job had no hirer to message. to and ok are the hirer read by queueRecruiter.
func (r *Runner) queueMessage(ctx context.Context, c linkedin.Card, description string, runID int64, to messages.Recipient, ok bool) {
	template := r.templateFor(messages.DetectLanguage(c.Title + "\n" + description))
	if strings.TrimSpace(template) == "" {
		return
	}
	m := store.Message{JobID: c.ID, RunID: runID, State: store.MessageNoRecipient}
	if ok && to.URL != "" { // a hirer without a profile URL cannot be keyed, so it is no recipient
		m = store.Message{JobID: c.ID, RunID: runID, RecipientName: to.Name, RecipientURL: to.URL,
			Text: messages.Render(template, to, messages.Job{Title: c.Title, Company: c.Company})}
		if m.Text == "" {
			return
		}
		messaged, err := r.Store.HasMessaged(m.RecipientURL, 0)
		if err != nil {
			r.Logger.Warn("could not check the recruiter's earlier messages", "job", c.ID, "error", err)
			return
		}
		if messaged { // one message per recruiter, ever: a staffing recruiter hires for many jobs
			m.State, m.Error = store.MessageNoRecipient, reasonMessaged
		}
	}
	auto := r.Cfg.Messages.AutoSend && m.State == ""
	if auto { // the user approved the template in advance
		m.State = store.MessageApproved
	}
	id, err := r.Store.InsertMessage(m)
	if err != nil {
		r.Logger.Warn("could not queue the recruiter message", "job", c.ID, "error", err)
		return
	}
	if auto && id > 0 {
		m.ID = id
		r.sendNow(ctx, m)
	}
}

// sendNow sends an auto-sent message on the page of the job just applied to,
// with no pause of its own: the application pacing already spaces them. At the
// daily cap it stays approved, for the end of this run or a later one.
func (r *Runner) sendNow(ctx context.Context, m store.Message) {
	if r.sendsStopped || ctx.Err() != nil {
		return
	}
	sent, err := r.Store.SentToday()
	if err != nil {
		r.Logger.Warn("could not count the messages sent today", "error", err)
		return
	}
	if sent >= r.Cfg.Messages.MaxPerDay {
		return
	}
	marked, err := r.Store.MarkSending(m.ID) // before the send: a crash after this never sends it again
	if err != nil || !marked {
		r.Logger.Warn("could not start sending a message", "message", m.ID, "error", err)
		return
	}
	r.sendOne(ctx, m, true)
}

// templateFor is the template for a posting in lang ("en", "pt", "es"): that
// language's messages.templates entry when it is not blank, else the active
// market's message_template. "" (unsure) always takes the market's.
func (r *Runner) templateFor(lang string) string {
	t := r.Cfg.Messages.Templates
	own := ""
	switch lang {
	case "en":
		own = t.En
	case "pt":
		own = t.Pt
	case "es":
		own = t.Es
	}
	if strings.TrimSpace(own) != "" {
		return own
	}
	return r.Market.MessageTemplate
}

// Why a message did not go out, as the store records it.
const (
	reasonNoRecipient = "the hirer changed, or only Premium or a paid InMail is offered"
	reasonBlocked     = "LinkedIn blocked the message (restriction or CAPTCHA)"
	reasonMessaged    = "already messaged this recruiter"
)

// sendMessages sends the approved messages at the end of a live run, at most
// messages.max_per_day per local day (what was sent today counts), each after a
// jittered pause. A block ends sending for this run; so does a cancelled context.
// A message not sent stays approved for a later run, unless it was tried.
func (r *Runner) sendMessages(ctx context.Context) {
	if !r.Cfg.Messages.Enabled || r.sendsStopped {
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
		messaged, err := r.Store.HasMessaged(m.RecipientURL, m.ID)
		if err != nil {
			r.Logger.Warn("could not check the recruiter's earlier messages", "message", m.ID, "error", err)
			return
		}
		if messaged {
			if _, err := r.Store.DropApproved(m.ID, reasonMessaged); err != nil {
				r.Logger.Warn("could not record the message's outcome", "message", m.ID, "error", err)
			}
			continue
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
		if !r.sendOne(ctx, m, false) {
			return
		}
	}
}

// sendOne sends one message that is marked sending, on the page shown when
// here (SendHere) or on its job page, and records the outcome. It reports
// false, and stops sending for the rest of the run, when sending must stop.
func (r *Runner) sendOne(ctx context.Context, m store.Message, here bool) bool {
	to := messages.Recipient{Name: m.RecipientName, URL: m.RecipientURL}
	var res linkedin.SendResult
	var err error
	if here {
		res, err = r.Site.SendHere(ctx, to, m.Text)
	} else {
		res, err = r.Site.SendMessage(ctx, to, m.JobID, m.Text)
	}
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
	r.sendsStopped = !keepGoing
	return keepGoing
}
