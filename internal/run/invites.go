package run

import (
	"context"

	"github.com/luizfreitas27/ajai/internal/linkedin"
	"github.com/luizfreitas27/ajai/internal/messages"
	"github.com/luizfreitas27/ajai/internal/store"
)

// sendInvites sends the queued connection invites at the end of a live run, oldest
// first, at most messages.max_invites_per_day per local day (what was sent today
// counts), each after the pause a message waits. It returns how many were sent.
// A block ends sending for the rest of the run, as a blocked message does; the
// weekly limit ends the invites for this run only. A cancelled context sends
// nothing more. An invite that errors is marked failed and never retried.
func (r *Runner) sendInvites(ctx context.Context) int {
	if !r.Cfg.Messages.Connect || r.sendsStopped {
		return 0
	}
	queued, err := r.Store.QueuedInvites()
	if err != nil {
		r.Logger.Warn("could not read the queued invites", "error", err)
		return 0
	}
	sent := 0
invites:
	for _, inv := range queued {
		if ctx.Err() != nil {
			break
		}
		today, err := r.Store.InvitesSentToday()
		if err != nil {
			r.Logger.Warn("could not count the invites sent today", "error", err)
			break
		}
		if today >= r.Cfg.Messages.MaxInvitesPerDay {
			break
		}
		if err := r.Pacer.PauseSeconds(ctx, r.Cfg.Messages.BetweenMinSeconds, r.Cfg.Messages.BetweenMaxSeconds); err != nil {
			break
		}
		res, err := r.Site.Connect(ctx, messages.Recipient{Name: inv.RecipientName, URL: inv.RecipientURL})
		state, errText := store.InviteFailed, ""
		switch {
		case err != nil:
			errText = err.Error()
		case res == linkedin.ConnectSent:
			state = store.InviteSent
		case res == linkedin.ConnectAlready:
			state = store.InviteAlready
		case res == linkedin.ConnectUnavailable:
			state = store.InviteUnavailable
		case res == linkedin.ConnectLimited: // left queued for a later run
			r.Logger.Warn("LinkedIn's weekly invitation limit was reached; no more invites are sent in this run")
			break invites
		case res == linkedin.ConnectBlocked: // left queued; no more sends in this run
			r.Logger.Warn("LinkedIn blocked invites; no more are sent in this run")
			r.sendsStopped = true
			break invites
		default:
			errText = "unexpected connect result: " + string(res)
		}
		marked, serr := r.Store.MarkInvite(inv.ID, state, errText)
		if serr != nil || !marked {
			r.Logger.Warn("could not record the invite's outcome", "invite", inv.ID, "error", serr)
		}
		if state == store.InviteSent {
			sent++
		}
	}
	return sent
}
