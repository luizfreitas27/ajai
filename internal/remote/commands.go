package remote

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/daemon"
	"github.com/luizfreitas27/aja/internal/run"
	"github.com/luizfreitas27/aja/internal/schedule"
	"github.com/luizfreitas27/aja/internal/store"
)

// status describes the run in progress (or the last one), the pending answers
// and the schedule.
func (l *Listener) status() string {
	var b strings.Builder
	holder, held, _ := run.ReadLock(l.dataDir)
	state, stateErr := run.ReadState(run.StatePath(l.dataDir))
	switch {
	case held:
		fmt.Fprintf(&b, "Running: %s.\n", holder)
		if stateErr == nil {
			mode := "live"
			if state.DryRun {
				mode = "dry run"
			}
			fmt.Fprintf(&b, "Market %s, %s.\n", state.Market, mode)
			if !state.NextRun.IsZero() {
				fmt.Fprintf(&b, "Non-stop session: next run at %s.\n", state.NextRun.Local().Format("15:04"))
			} else {
				if state.Term != "" {
					fmt.Fprintf(&b, "Searching: %s\n", state.Term)
				}
				fmt.Fprintf(&b, "Applied %d, external %d, failed %d, skipped %d, would apply %d.\n",
					state.Applied, state.External, state.Failed, state.Skipped, state.WouldApply)
			}
		}
	default:
		b.WriteString("No run in progress.\n")
		if runs, err := l.store.ListRuns(1); err == nil && len(runs) > 0 {
			r := runs[0]
			when := r.StartedAt
			if t, ok := store.ParseTime(r.StartedAt); ok {
				when = t.Local().Format("Mon 02 Jan 15:04")
			}
			fmt.Fprintf(&b, "Last run (%s): applied %d, external %d, failed %d, skipped %d; %s.\n",
				when, r.Applied, r.External, r.Failed, r.Skipped, r.Reason)
		}
	}
	if n, err := l.store.CountAnswers("pending"); err == nil {
		fmt.Fprintf(&b, "\nPending answers: %d\n", n)
	}
	if windows, err := l.store.ListWindows(); err == nil && len(windows) > 0 {
		now := l.now()
		if o, ok := schedule.Current(windows, now); ok {
			fmt.Fprintf(&b, "Window open now: %d until %s (%s)\n", o.Window.ID, o.Window.End, o.Window)
		}
		if o, ok := schedule.Next(windows, now); ok {
			fmt.Fprintf(&b, "Next window: %s, window %d (%s)\n", o.Start.Format("Mon 02 Jan 15:04"), o.Window.ID, o.Window)
		}
	}
	return strings.TrimSpace(b.String())
}

// stop asks the run in progress to end and, in the background, says when it has.
func (l *Listener) stop(ctx context.Context) {
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
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		deadline := time.Now().Add(l.StopConfirm) // real time: how long the user waits

		for {
			if _, held, _ := run.ReadLock(l.dataDir); !held {
				l.reply(ctx, "Stopped. The run's summary follows.")
				return
			}
			if !time.Now().Before(deadline) {
				l.reply(ctx, fmt.Sprintf("Still ending after %s; /status shows it.", l.StopConfirm))
				return
			}
			if l.Sleep(ctx, time.Second) != nil {
				return
			}
		}
	}()
}

// start runs /run [market] [dry].
func (l *Listener) start(ctx context.Context, args []string) {
	req := daemon.Request{Origin: run.OriginTelegram}
	markets := make([]string, len(config.MarketNames))
	for i, m := range config.MarketNames {
		markets[i] = string(m)
	}
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
		mode := "live"
		if req.DryRun {
			mode = "dry run"
		}
		l.reply(ctx, fmt.Sprintf("Started a run (market %s, %s). /status follows it, /stop ends it; its summary comes when it ends.", market, mode))
	}
}
