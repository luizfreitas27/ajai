package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/daemon"
	"github.com/luizfreitas27/aja/internal/launch"
	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/notify"
	"github.com/luizfreitas27/aja/internal/run"
)

// OpenBrowser is how aja run and aja login open Chrome; tests replace it.
var OpenBrowser browser.Opener = browser.Open

func (a *App) runCmd() *cobra.Command {
	var opts run.Options
	var terms, market string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Search LinkedIn and apply to Easy Apply jobs in your own Chrome",
		Long: `Opens Chrome on the aja profile (or settings.chrome_profile_dir), waits for you to sign
in when needed (sign in once beforehand with aja login), then walks every search term with
the configured filters, screens each job and applies. Pacing is on by default. On the aja
profile, Chrome is closed when the run ends.

--dry-run fills every form and discards it at the Review step instead of submitting, so you
can check the answers before applying for real.

With you at the terminal, a question nothing can answer pauses the job: aja shows it (and
sends a Telegram message when configured), you fill it in Chrome and press Enter, and what
you typed is remembered as an approved answer. The pause waits up to 15 minutes. With
search.pause_after_filters, aja also stops after applying each term's filters so you can
check them; type "off" there to stop pausing. With --unattended, --json, or when stdin or
stdout is not a terminal, aja never pauses: the job is skipped and the question waits for
you in "aja answers review".

With settings.run_non_stop, a run that visited every search term (or hit the per-run cap)
is followed, ten minutes later, by another, with the sort order and the date-posted filter
changed as settings.alternate_sort_by and settings.cycle_date_posted say, until LinkedIn's
daily limit, the hourly cap, --max, an error or Ctrl-C.

--json prints one JSON object per line for every event and each run's summary, and nothing
else on stdout.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			l, err := config.Load(a.paths, a.Env, marketFlag(market))
			a.openLog(logLevel(l)) // before the notifier, which logs its warnings through it
			// A run that cannot even start (config, Chrome, AI settings) says so on Telegram
			// too: an unattended run has nobody watching the terminal. The secrets come from
			// the environment, so they are there even when the config file is broken.
			secrets := a.secrets(l)
			telegram := notify.New(secrets.TelegramBotToken, secrets.TelegramChatID, a.logger)
			cannotStart := func(err error) error {
				run.ReportStartFailure(ctx, telegram, err)
				return err
			}
			if err := a.configProblem(l, err); err != nil {
				return cannotStart(err)
			}
			if err := opts.Validate(); err != nil {
				return err
			}
			if terms != "" {
				for _, t := range strings.Split(terms, ",") {
					if t = strings.TrimSpace(t); t != "" {
						opts.Terms = append(opts.Terms, t)
					}
				}
			}
			opts.Unattended = run.ResolveUnattended(opts.Unattended, a.eventsAsJSON, a.stdinIsTerminal(), a.stdoutIsTerminal())
			cfg := l.Config
			st, err := a.openStore()
			if err != nil {
				return cannotStart(err)
			}
			plan, err := launch.NewPlan(l, a.paths, a.logger)
			if err != nil {
				return cannotStart(err)
			}
			if !a.eventsAsJSON {
				a.ui.Banner(a.Version)
			}
			a.note(run.EventStart, "market", fmt.Sprintf("%s (%s, %s)", plan.Market.Name, plan.Market.Currency, plan.Market.SalaryPeriod))
			a.note(run.EventStart, "mode", runMode(opts, cfg))
			a.note(run.EventStart, "ai", aiMode(cfg, plan.AI != nil))
			for _, w := range l.Warnings {
				a.note(run.EventWarn, "config", w)
			}
			if plan.ResumeNote != "" {
				a.note(run.EventWarn, "resume", plan.ResumeNote)
			}

			opened, err := plan.Open(ctx, opts, launch.Deps{Origin: run.OriginTerminal, Paths: a.paths, Store: st, Logger: a.logger,
				Notifier: telegram, Events: a.report, OpenBrowser: OpenBrowser,
				Helper:       &terminalHelper{app: a, notify: telegram, timeout: defaultHelpTimeout},
				AfterFilters: a.filterPause(defaultHelpTimeout)})
			if err != nil {
				return cannotStart(err)
			}
			defer opened.Close()
			_, runErr := opened.Session(ctx)
			return runErr
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.DryRun, "dry-run", false, "fill every form and discard it at Review instead of submitting")
	f.BoolVar(&opts.Unattended, "unattended", false, "never pause for help; skip and remember instead")
	f.BoolVar(&opts.Fast, "fast", false, "shorten the long pauses (dry-run only)")
	f.IntVar(&opts.Max, "max", 0, "stop after this many applications (0: the pacing cap)")
	f.StringVar(&terms, "terms", "", "search terms for this run, comma-separated (overrides the config)")
	f.StringVar(&opts.Location, "location", "", "search location for this run (overrides the config)")
	f.StringVar(&market, "market", "", "market for this run: brasil or internacional")
	f.BoolVar(&a.eventsAsJSON, "json", false, "print events and summaries as JSON lines (implies --unattended)")
	return cmd
}

// configProblem says why a loaded configuration cannot run (no file, a parse
// error, a validation error), or nil.
func (a *App) configProblem(l *config.Loaded, err error) error {
	switch {
	case l == nil:
		return err
	case !l.FileExisted:
		return errors.New("no config file at " + a.paths.ConfigFile + "; run `aja init` first")
	case err != nil:
		return fmt.Errorf("fix the configuration first (`aja config validate`): %w", err)
	}
	return nil
}

// logLevel is the configured log level, or the default without a configuration.
func logLevel(l *config.Loaded) string {
	if l == nil {
		return config.Defaults().Settings.LogLevel
	}
	return l.Config.Settings.LogLevel
}

// marketFlag is the override for a run's market ("" keeps markets.active).
func marketFlag(market string) map[string]string {
	if market == "" {
		return map[string]string{}
	}
	return map[string]string{"markets.active": market}
}

func aiMode(c config.Config, on bool) string {
	if !on {
		return "off"
	}
	return c.AI.Provider + " " + c.AI.Model
}

func runMode(o run.Options, c config.Config) string {
	parts := []string{}
	if o.DryRun {
		parts = append(parts, "dry run")
	} else {
		parts = append(parts, "live")
	}
	if o.Unattended {
		parts = append(parts, "unattended")
	}
	if o.Fast {
		parts = append(parts, "fast")
	}
	if c.Settings.RunNonStop {
		parts = append(parts, "non-stop")
	}
	if o.PausesAfterFilters(c) {
		parts = append(parts, "pause after filters")
	}
	return strings.Join(parts, ", ")
}

func (a *App) stdoutIsTerminal() bool {
	f, ok := a.Stdout.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// stopReasonCLI is the ending reason of a run stopped with `aja stop`.
const stopReasonCLI = "stopped with aja stop"

func (a *App) stopCmd() *cobra.Command {
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the run in progress, wherever it was started",
		Long: `Asks the run in progress to end cleanly, as Ctrl-C in its terminal would: the job in
progress is finished or discarded, the run is recorded and its summary sent. Works for a run
started from any terminal.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			holder, err := run.RequestStop(a.paths.DataDir, stopReasonCLI)
			if errors.Is(err, run.ErrNoRun) {
				return errors.New("no run in progress")
			}
			if err != nil {
				return err
			}
			a.ui.Println("Asked " + holder.String() + " to stop.")
			if wait <= 0 {
				return nil
			}
			deadline := time.Now().Add(wait)
			for time.Now().Before(deadline) {
				_, held, err := run.ReadLock(a.paths.DataDir)
				if err != nil {
					return err
				}
				if !held {
					a.ui.OK("stopped", "")
					return nil
				}
				select {
				case <-cmd.Context().Done():
					return nil
				case <-time.After(stopWaitPoll):
				}
			}
			a.ui.Warn("still running", "it stops once the job in progress is done; check with `aja status`")
			return nil
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", 3*time.Minute, "how long to wait for the run to end (0: do not wait)")
	return cmd
}

// stopWaitPoll is how often `aja stop` checks whether the run has ended.
var stopWaitPoll = 300 * time.Millisecond

// ------------------------------------------------------------------ status / logs

func (a *App) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether a run is in progress and the last runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			holder, held, lerr := run.ReadLock(a.paths.DataDir)
			if lerr != nil {
				a.ui.Warn("lock", lerr.Error())
			}
			state, err := run.ReadState(run.StatePath(a.paths.DataDir))
			switch {
			case errors.Is(err, run.ErrNoState) && held && holder.PID == 0:
				a.ui.OK("running", "a run is starting")
			case errors.Is(err, run.ErrNoState) && held:
				a.ui.OK("running", holder.String()+", starting")
			case errors.Is(err, run.ErrNoState):
				a.ui.Println("No run in progress.")
			case err != nil:
				a.ui.Warn("state", err.Error())
			case state.Alive() && !state.NextRun.IsZero():
				a.ui.OK("waiting", fmt.Sprintf("pid %d, non-stop session; next run at %s (%s)", state.PID, state.NextRun.Local().Format("15:04"), state.Market))
				if held {
					a.ui.KV("started from", holder.From())
				}
			case state.Alive():
				a.ui.OK("running", fmt.Sprintf("pid %d since %s (%s, %s)", state.PID, state.Started.Local().Format("15:04:05"), run.Mode(state.DryRun), state.Market))
				if held {
					a.ui.KV("started from", holder.From())
				}
				a.ui.KV("term", state.Term)
				a.ui.KV("applied", fmt.Sprintf("%d (external %d, failed %d, would apply %d)", state.Applied, state.External, state.Failed, state.WouldApply))
				a.ui.KV("skipped", fmt.Sprint(state.Skipped))
			default:
				a.ui.Warn("stale", fmt.Sprintf("pid %d is gone; the last run did not exit cleanly", state.PID))
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			windows, err := st.ListWindows()
			if err != nil {
				return err
			}
			if d, running, _ := daemon.ReadLock(a.paths.DataDir); running {
				a.ui.KV("daemon", "running ("+d.String()+")")
			} else if len(windows) > 0 {
				a.ui.KV("daemon", "not running: schedule windows start runs only while aja daemon runs")
			}
			if len(windows) > 0 {
				a.printNextOpening(windows)
			}
			runs, err := st.ListRuns(10)
			if err != nil {
				return err
			}
			if len(runs) == 0 {
				a.ui.Println("No runs recorded yet.")
				return nil
			}
			a.ui.Println()
			a.ui.Title("Last runs")
			for _, r := range runs {
				mode := "live"
				if r.DryRun {
					mode = "dry"
				}
				ended := r.EndedAt
				if ended == "" {
					ended = "in progress"
				}
				a.ui.Printf("#%-4d %-25s %-5s %-14s applied %d  ext %d  failed %d  skipped %d  pending %d  %s\n",
					r.ID, r.StartedAt, mode, r.Market, r.Applied, r.External, r.Failed, r.Skipped, r.PendingAnswers, firstNonEmpty(r.Reason, ended))
			}
			return nil
		},
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (a *App) logsCmd() *cobra.Command {
	var follow, ofDaemon bool
	var lines int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Print today's log; -f follows it like tail -f",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var name logging.FileNamer = logging.FileName
			if ofDaemon {
				name = logging.DaemonFileName // the daemon and the runs it starts
			}
			path := name(a.paths.LogDir, time.Now())
			data, err := os.ReadFile(path)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			text := string(data)
			if lines > 0 {
				all := strings.Split(strings.TrimRight(text, "\n"), "\n")
				if len(all) > lines {
					all = all[len(all)-lines:]
				}
				text = strings.Join(all, "\n") + "\n"
			}
			a.ui.Printf("%s", text)
			if !follow {
				return nil
			}
			return a.follow(cmd.Context(), name, path, int64(len(data)))
		},
	}
	cmd.Flags().BoolVar(&ofDaemon, "daemon", false, "the daemon's log (with the runs it started) instead of the terminal's")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing as the file grows")
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "print only the last N lines first (0: all)")
	return cmd
}

// follow prints what is appended to the day's log, surviving truncation (the
// offset resets) and midnight (the file name changes with the day).
func (a *App) follow(ctx context.Context, name logging.FileNamer, path string, offset int64) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(500 * time.Millisecond):
		}
		if today := name(a.paths.LogDir, time.Now()); today != path {
			path, offset = today, 0
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		if info, err := f.Stat(); err == nil && info.Size() < offset {
			offset = 0
		}
		if _, err := f.Seek(offset, 0); err == nil {
			buf := make([]byte, 64*1024)
			for {
				n, err := f.Read(buf)
				if n > 0 {
					a.ui.Printf("%s", buf[:n])
					offset += int64(n)
				}
				if err != nil {
					break
				}
			}
		}
		_ = f.Close()
	}
}

// secrets returns the loaded secrets, or, when the config file could not even be
// parsed, the ones the environment and the .env file hold.
func (a *App) secrets(l *config.Loaded) config.Secrets {
	if l != nil {
		return l.Secrets
	}
	env := a.Env
	_ = env.LoadDotenv(a.paths.EnvFile)
	return env.Secrets()
}
