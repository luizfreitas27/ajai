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

	"github.com/luizfreitas27/aja/internal/ai"
	"github.com/luizfreitas27/aja/internal/apply"
	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/chrome"
	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/linkedin"
	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/markets"
	"github.com/luizfreitas27/aja/internal/memory"
	"github.com/luizfreitas27/aja/internal/notify"
	"github.com/luizfreitas27/aja/internal/pacing"
	"github.com/luizfreitas27/aja/internal/questions"
	"github.com/luizfreitas27/aja/internal/run"
)

// OpenBrowser is the seam the run command uses to get a browser; tests replace it.
var OpenBrowser = browser.Open

func (a *App) runCmd() *cobra.Command {
	var opts run.Options
	var terms, market string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Search LinkedIn and apply to Easy Apply jobs in your own Chrome",
		Long: `Attaches to your Chrome profile (close Chrome first, or point settings.chrome_profile_dir
at a profile aja can own), waits for you to sign in when needed, then walks every search
term with the configured filters, screens each job and applies. Pacing is on by default.

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
			flags := map[string]string{}
			if market != "" {
				flags["markets.active"] = market
			}
			ctx := cmd.Context()
			l, err := config.Load(a.paths, a.Env, flags)
			level := config.Defaults().Settings.LogLevel
			if l != nil {
				level = l.Config.Settings.LogLevel
			}
			a.openLog(level) // before the notifier, which logs its warnings through it
			// A run that cannot even start (config, Chrome, AI settings) says so on Telegram
			// too: an unattended run has nobody watching the terminal. The secrets come from
			// the environment, so they are there even when the config file is broken.
			secrets := a.secrets(l)
			telegram := notify.New(secrets.TelegramBotToken, secrets.TelegramChatID, a.logger)
			cannotStart := func(err error) error {
				run.ReportStartFailure(ctx, telegram, err)
				return err
			}
			if l == nil {
				return cannotStart(err)
			}
			if !l.FileExisted {
				return cannotStart(errors.New("no config file at " + a.paths.ConfigFile + "; run `aja init` first"))
			}
			if err != nil {
				return cannotStart(fmt.Errorf("fix the configuration first (`aja config validate`): %w", err))
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
			// --json is for scripts: nobody answers a pause there.
			opts.Unattended = run.ResolveUnattended(opts.Unattended || a.eventsAsJSON, a.stdinIsTerminal(), a.stdoutIsTerminal())
			cfg := l.Config
			st, err := a.openStore()
			if err != nil {
				return cannotStart(err)
			}
			resolved := markets.Resolve(cfg)
			aiClient, err := ai.FromConfig(cfg, l.Secrets, resolved, a.logger)
			if err != nil {
				return cannotStart(err)
			}
			pauseFilters := cfg.Search.PauseAfterFilters && !opts.Unattended
			if !a.eventsAsJSON {
				a.ui.Banner(a.Version)
			}
			a.note(run.EventStart, "market", fmt.Sprintf("%s (%s, %s)", resolved.Name, resolved.Currency, resolved.SalaryPeriod))
			a.note(run.EventStart, "mode", runMode(opts, cfg.Settings.RunNonStop, pauseFilters))
			a.note(run.EventStart, "ai", aiMode(cfg, aiClient != nil))
			for _, w := range l.Warnings {
				a.note(run.EventWarn, "config", w)
			}

			bin, err := chrome.Find(cfg.Settings.ChromeBinary)
			if err != nil {
				return cannotStart(err)
			}
			profile := cfg.Settings.ChromeProfileDir
			if profile == "" {
				profile = chrome.DefaultProfileDir()
			}
			br, err := OpenBrowser(ctx, browser.Options{Bin: bin, ProfileDir: profile, DisableExtensions: cfg.Settings.DisableExtensions,
				SmoothScroll: cfg.Settings.SmoothScroll, Logger: a.logger})
			if err != nil {
				return cannotStart(err)
			}
			defer func() { _ = br.Close() }()

			pacer := pacing.New(cfg.Pacing, opts.Fast)
			session := linkedin.NewSession(br.Main(), br, pacer, a.logger)
			session.CloseTabs = cfg.Settings.CloseTabs
			mem := memory.New(st, a.logger)
			answerer := questions.New(cfg, mem, nil, a.logger)
			if aiClient != nil {
				answerer.AI = aiClient
			}
			applier := &apply.Applier{Page: br.Main(), Pacer: pacer, Answerer: answerer, Logger: a.logger, ScreenshotDir: a.paths.ScreenshotDir}
			if !opts.Unattended {
				applier.Helper = &terminalHelper{app: a, notify: telegram, timeout: defaultHelpTimeout}
			}
			resume, note := run.ResolveResume(a.paths, resolved, cfg.Questions.Resume)
			if note != "" {
				a.note(run.EventWarn, "resume", note)
			}
			events := func(e run.Event) {
				a.report(e)
				if e.Kind == run.EventLogin {
					telegram.Send(ctx, "aja is waiting for you to sign in to LinkedIn in the Chrome window.")
				}
			}
			runner := &run.Runner{Cfg: cfg, Market: resolved, Site: session, Applier: applier, Store: st, Pacer: pacer, Logger: a.logger,
				Events: events, StatePath: run.StatePath(a.paths.DataDir), ResumePath: resume, Notifier: telegram}
			if pauseFilters {
				runner.AfterFilters = a.filterPause(defaultHelpTimeout)
			}
			_, runErr := runner.Session(ctx, opts)
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

func aiMode(c config.Config, on bool) string {
	if !on {
		return "off"
	}
	return c.AI.Provider + " " + c.AI.Model
}

func runMode(o run.Options, nonStop, pauseFilters bool) string {
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
	if nonStop {
		parts = append(parts, "non-stop")
	}
	if pauseFilters {
		parts = append(parts, "pause after filters")
	}
	return strings.Join(parts, ", ")
}

func (a *App) stdoutIsTerminal() bool {
	f, ok := a.Stdout.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// ------------------------------------------------------------------ status / logs

func (a *App) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether a run is in progress and the last runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := run.ReadState(run.StatePath(a.paths.DataDir))
			switch {
			case errors.Is(err, run.ErrNoState):
				a.ui.Println("No run in progress.")
			case err != nil:
				a.ui.Warn("state", err.Error())
			case state.Alive():
				mode := "live"
				if state.DryRun {
					mode = "dry run"
				}
				a.ui.OK("running", fmt.Sprintf("pid %d since %s (%s, %s)", state.PID, state.Started.Local().Format("15:04:05"), mode, state.Market))
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
	var follow bool
	var lines int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Print today's log; -f follows it like tail -f",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := logging.FileName(a.paths.LogDir, time.Now())
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
			return a.follow(cmd.Context(), path, int64(len(data)))
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing as the file grows")
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "print only the last N lines first (0: all)")
	return cmd
}

// follow prints what is appended to the day's log, surviving truncation (the
// offset resets) and midnight (the file name changes with the day).
func (a *App) follow(ctx context.Context, path string, offset int64) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(500 * time.Millisecond):
		}
		if today := logging.FileName(a.paths.LogDir, time.Now()); today != path {
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
