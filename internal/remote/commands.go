package remote

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/daemon"
	"github.com/luizfreitas27/ajai/internal/run"
	"github.com/luizfreitas27/ajai/internal/schedule"
	"github.com/luizfreitas27/ajai/internal/store"
)

// status describes the run in progress (or the last one), the pending answers
// and the schedule.
func (l *Listener) status() string {
	var b strings.Builder
	act, _ := run.ReadActivity(l.dataDir)
	state := act.State
	switch {
	case act.Held:
		fmt.Fprintf(&b, "Running: %s.\n", act.Holder)
		switch act.Kind {
		case run.Waiting:
			fmt.Fprintf(&b, "Market %s, %s.\n", state.Market, run.Mode(state.DryRun))
			fmt.Fprintf(&b, "Non-stop session: next run at %s.\n", state.NextRun.Local().Format("15:04"))
		case run.Running:
			fmt.Fprintf(&b, "Market %s, %s.\n", state.Market, run.Mode(state.DryRun))
			if state.Term != "" {
				fmt.Fprintf(&b, "Searching: %s\n", state.Term)
			}
			fmt.Fprintf(&b, "Applied %d, external %d, failed %d, skipped %d, would apply %d.\n",
				state.Applied, state.External, state.Failed, state.Skipped, state.WouldApply)
		}
	default:
		b.WriteString("No run in progress.\n")
		if runs, err := l.store.ListRuns(1); err == nil && len(runs) > 0 {
			r := runs[0]
			when := r.StartedAt
			if t, ok := store.ParseTime(r.StartedAt); ok {
				when = t.Local().Format("Mon 02 Jan 15:04")
			}
			reason := r.Reason
			if r.EndedAt == "" {
				reason = "it never recorded an end (ajai was killed)"
			}
			fmt.Fprintf(&b, "Last run (%s, market %s, %s): applied %d, external %d, failed %d, skipped %d; %s.\n",
				when, r.Market, run.Mode(r.DryRun), r.Applied, r.External, r.Failed, r.Skipped, reason)
		}
	}
	if n, err := l.store.CountAnswers("pending"); err == nil {
		fmt.Fprintf(&b, "\nPending answers: %d\n", n)
		if n > 0 {
			b.WriteString("/pending reviews them.\n")
		}
	}
	windows, err := l.store.ListWindows()
	switch {
	case err != nil:
	case len(windows) == 0:
		b.WriteString("No schedule windows (ajai schedule add).\n")
	default:
		now := l.now()
		if o, ok := schedule.Current(windows, now); ok {
			fmt.Fprintf(&b, "Window open now: %d until %s (%s)\n", o.Window.ID, o.Window.End, o.Window)
		}
		if o, ok := schedule.Next(windows, now); ok {
			fmt.Fprintf(&b, "Next window: %s, window %d (%s)\n", o.Start.Format("Mon 02 Jan 15:04"), o.Window.ID, o.Window)
		} else {
			b.WriteString("Every schedule window is disabled.\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// stop asks the run in progress to end and, in the background, says when it
// has. A /stop that waited while nobody listened is not for a run that started
// after it was sent (live, it always means the run in progress: clocks differ).
func (l *Listener) stop(ctx context.Context, sent time.Time, backlog bool) {
	if h, held, _ := run.ReadLock(l.dataDir); backlog && held && h.Started.After(sent) {
		l.reply(ctx, "Not stopping "+h.String()+": it started after your /stop. Send it again to stop this one.")
		return
	}
	holder, err := l.runs.Stop(ReasonStopped)
	switch {
	case errors.Is(err, run.ErrNoRun):
		l.reply(ctx, "No run in progress.")
		return
	case err != nil:
		l.reply(ctx, "Could not ask the run to stop: "+err.Error())
		return
	}
	l.reply(ctx, "Stopping "+holder.String()+"…")
	l.mu.Lock()
	already := l.confirming
	l.confirming = true
	l.mu.Unlock()
	if already {
		return // one "Stopped." is enough
	}
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		defer func() { l.mu.Lock(); l.confirming = false; l.mu.Unlock() }()
		deadline := time.Now().Add(l.stopConfirm) // real time: how long the user waits

		for {
			if _, held, _ := run.ReadLock(l.dataDir); !held {
				l.reply(ctx, "Stopped.")
				return
			}
			if !time.Now().Before(deadline) {
				l.reply(ctx, fmt.Sprintf("Still ending after %s; /status shows it.", l.stopConfirm))
				return
			}
			if l.sleep(ctx, time.Second) != nil {
				return
			}
		}
	}()
}

// start runs /run [market] [dry].
func (l *Listener) start(ctx context.Context, args []string) {
	req := daemon.Request{Origin: run.OriginTelegram}
	markets := config.MarketNameList()
	for _, a := range args {
		a = strings.ToLower(a)
		switch {
		case a == "dry" || a == "dry-run" || a == "dryrun":
			req.DryRun = true
		case slices.Contains(markets, a):
			req.Market = a
		default:
			l.reply(ctx, fmt.Sprintf("Not started: I do not know %q. Use /run [%s] [dry].", a, strings.Join(markets, "|")))
			return
		}
	}
	err := l.runs.Start(ctx, req)
	var held *run.HeldError
	switch {
	case errors.As(err, &held):
		l.reply(ctx, "Not started: "+held.Holder.String()+" is in progress. /stop ends it.")
	case err != nil:
		l.reply(ctx, "Not started: "+err.Error())
	default:
		market := req.Market
		if market == "" {
			market = "from your config"
		}
		l.reply(ctx, fmt.Sprintf("Started a run (market %s, %s). /status follows it, /stop ends it; its summary comes when it ends.", market, run.Mode(req.DryRun)))
	}
}

// help lists the commands.
func help() string {
	return `ajai listens to:
/status: the run in progress (or the last one), pending answers, the next window
/stop: stop the run in progress, wherever it was started
/run [` + strings.Join(config.MarketNameList(), "|") + `] [dry]: start an unattended run now
/pending: review the pending answers: Approve or Delete, or reply to a question with its answer
/help: this list`
}
