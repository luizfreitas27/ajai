package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/chrome"
	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/linkedin"
	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/markets"
	"github.com/luizfreitas27/aja/internal/pacing"
	"github.com/luizfreitas27/aja/internal/run"
)

// pidFile records the run in progress, for `aja status`.
type pidFile struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	RunID   int64     `json:"run_id"`
	DryRun  bool      `json:"dry_run"`
	Market  string    `json:"market"`
}

func (a *App) pidPath() string { return filepath.Join(a.paths.DataDir, "run.pid") }

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

--dry-run lists the jobs it would apply to without opening any form. Until the Easy Apply
loop lands (F1-5), --dry-run is required.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := map[string]string{}
			if market != "" {
				flags["markets.active"] = market
			}
			l, err := config.Load(a.paths, a.Env, flags)
			if l == nil {
				return err
			}
			if !l.FileExisted {
				return errors.New("no config file at " + a.paths.ConfigFile + "; run `aja init` first")
			}
			if err != nil {
				return fmt.Errorf("fix the configuration first (`aja config validate`): %w", err)
			}
			if opts.Fast && !opts.DryRun {
				return errors.New("--fast is allowed only together with --dry-run")
			}
			if terms != "" {
				for _, t := range strings.Split(terms, ",") {
					if t = strings.TrimSpace(t); t != "" {
						opts.Terms = append(opts.Terms, t)
					}
				}
			}
			if !opts.Unattended && !a.stdoutIsTerminal() {
				opts.Unattended = true
			}
			cfg := l.Config
			a.openLog(cfg.Settings.LogLevel)
			st, err := a.openStore()
			if err != nil {
				return err
			}
			resolved := markets.Resolve(cfg)
			a.ui.Banner(a.Version)
			a.ui.KV("market", fmt.Sprintf("%s (%s, %s)", resolved.Name, resolved.Currency, resolved.SalaryPeriod))
			a.ui.KV("mode", runMode(opts))

			bin, err := chrome.Find(cfg.Settings.ChromeBinary)
			if err != nil {
				return err
			}
			profile := cfg.Settings.ChromeProfileDir
			if profile == "" {
				profile = chrome.DefaultProfileDir()
			}
			ctx := cmd.Context()
			br, err := OpenBrowser(ctx, browser.Options{Bin: bin, ProfileDir: profile, DisableExtensions: cfg.Settings.DisableExtensions, Logger: a.logger})
			if err != nil {
				return err
			}
			defer func() { _ = br.Close() }()

			pacer := pacing.New(cfg.Pacing, opts.Fast)
			session := linkedin.NewSession(br.Main(), pacer, a.logger)
			runner := &run.Runner{Cfg: cfg, Market: resolved, Site: session, Store: st, Pacer: pacer, Logger: a.logger, Events: a.printEvent}

			pf := pidFile{PID: os.Getpid(), Started: time.Now(), DryRun: opts.DryRun, Market: string(resolved.Name)}
			if data, err := json.Marshal(pf); err == nil {
				_ = os.WriteFile(a.pidPath(), data, 0o600)
			}
			defer func() { _ = os.Remove(a.pidPath()) }()

			sum, runErr := runner.Run(ctx, opts)
			a.printSummary(sum)
			if runErr != nil && !errors.Is(runErr, run.ErrApplyNotImplemented) {
				return runErr
			}
			if errors.Is(runErr, run.ErrApplyNotImplemented) {
				return &exitError{code: 2, msg: runErr.Error()}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.DryRun, "dry-run", false, "fill nothing in, list the jobs that would be applied to")
	f.BoolVar(&opts.Unattended, "unattended", false, "never pause for help; skip and remember instead")
	f.BoolVar(&opts.Fast, "fast", false, "shorten the long pauses (dry-run only)")
	f.IntVar(&opts.Max, "max", 0, "stop after this many applications (0: the pacing cap)")
	f.StringVar(&terms, "terms", "", "search terms for this run, comma-separated (overrides the config)")
	f.StringVar(&opts.Location, "location", "", "search location for this run (overrides the config)")
	f.StringVar(&market, "market", "", "market for this run: brasil or internacional")
	return cmd
}

func runMode(o run.Options) string {
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
	return strings.Join(parts, ", ")
}

func (a *App) stdoutIsTerminal() bool {
	f, ok := a.Stdout.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

func (a *App) printEvent(e run.Event) {
	title := ""
	if e.Card != nil {
		title = strings.TrimSpace(e.Card.Title + " | " + e.Card.Company)
	}
	switch e.Kind {
	case "login":
		a.ui.Warn("login", e.Text)
	case "term":
		a.ui.Println()
		a.ui.Title("Searching: " + e.Text)
	case "would-apply":
		a.ui.OK("would apply", title)
	case "skip":
		a.ui.Hint("skip  " + title + "  (" + e.Text + ")")
	case "limit":
		a.ui.Warn("stop", e.Text)
	case "warn":
		a.ui.Warn("warn", e.Text)
	default:
		a.ui.Println(e.Text)
	}
}

func (a *App) printSummary(s run.Summary) {
	a.ui.Println()
	a.ui.Title("Run summary")
	a.ui.KV("run", fmt.Sprintf("#%d (%s)", s.RunID, s.Market))
	if s.DryRun {
		a.ui.KV("would apply", fmt.Sprint(s.WouldApply))
	} else {
		a.ui.KV("applied", fmt.Sprint(s.Applied))
		a.ui.KV("external", fmt.Sprint(s.External))
		a.ui.KV("failed", fmt.Sprint(s.Failed))
	}
	a.ui.KV("skipped", fmt.Sprint(s.Skipped))
	a.ui.KV("ended", s.Reason)
}

// ------------------------------------------------------------------ status / logs

func (a *App) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether a run is in progress and the last runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if data, err := os.ReadFile(a.pidPath()); err == nil {
				var pf pidFile
				if json.Unmarshal(data, &pf) == nil {
					if processAlive(pf.PID) {
						a.ui.OK("running", fmt.Sprintf("pid %d since %s (%s)", pf.PID, pf.Started.Local().Format("15:04:05"), map[bool]string{true: "dry run", false: "live"}[pf.DryRun]))
					} else {
						a.ui.Warn("stale", fmt.Sprintf("pid %d is gone; the last run did not exit cleanly", pf.PID))
					}
				}
			} else {
				a.ui.Println("No run in progress.")
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
				a.ui.Printf("#%-4d %-25s %-5s %-14s applied %d  ext %d  failed %d  skipped %d  %s\n",
					r.ID, r.StartedAt, mode, r.Market, r.Applied, r.External, r.Failed, r.Skipped, firstNonEmpty(r.Reason, ended))
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

func (a *App) follow(ctx context.Context, path string, offset int64) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(500 * time.Millisecond):
		}
		f, err := os.Open(path)
		if err != nil {
			continue
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
