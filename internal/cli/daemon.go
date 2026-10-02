package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/daemon"
	"github.com/luizfreitas27/aja/internal/launch"
	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/notify"
	"github.com/luizfreitas27/aja/internal/run"
	"github.com/luizfreitas27/aja/internal/store"
)

func (a *App) daemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Start and stop runs on the schedule windows, until stopped",
		Long: `Runs in the foreground until Ctrl-C (or a service stop): every 30 seconds it looks at
the schedule windows (aja schedule), starts one unattended run when a window opens and
stops it when the window closes. The configuration is read afresh for every run. A run in
progress when the daemon stops ends cleanly first.

Only one daemon runs at a time. Its log is the daemon file in the log directory
(aja logs --daemon).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := a.paths.EnsureDataDirs(); err != nil {
				return err
			}
			lock, err := daemon.AcquireLock(a.paths.DataDir, time.Now())
			if err != nil {
				return err
			}
			defer lock.Release()
			level := config.Defaults().Settings.LogLevel
			if l, err := a.load(); l != nil && err == nil {
				level = l.Config.Settings.LogLevel
			}
			logger, closer, err := logging.OpenDaily(a.paths.LogDir, logging.DaemonFileName, logging.ParseLevel(level))
			if err != nil {
				return err
			}
			defer func() { _ = closer.Close() }()
			a.logger = logger
			st, err := a.openStore()
			if err != nil {
				return err
			}
			// The daemon's own notices use the secrets of the moment it started; every
			// run reads them afresh with the configuration.
			secrets := a.secrets(nil)
			telegram := notify.New(secrets.TelegramBotToken, secrets.TelegramChatID, logger)

			sup := daemon.NewSupervisor(a.paths.DataDir, a.daemonStarter(st, logger), logger)
			sched := &daemon.Scheduler{Store: st, Sup: sup, Notifier: telegram, Logger: logger, Now: a.now}
			sup.OnEnd = sched.RunEnded

			a.ui.Banner(a.Version)
			a.ui.OK("daemon", fmt.Sprintf("running (pid %d); Ctrl-C to stop", os.Getpid()))
			windows, err := st.ListWindows()
			if err != nil {
				return err
			}
			if len(windows) == 0 {
				a.ui.Hint("No schedule windows yet: add one with aja schedule add.")
			} else {
				a.printNextOpening(windows)
			}
			logger.Info("daemon started", "pid", os.Getpid(), "windows", len(windows))
			sched.Loop(ctx, daemon.TickEvery)
			if _, ok := sup.Current(); ok {
				a.ui.Println("Stopping: the run in progress is ending…")
			}
			sup.Wait()
			logger.Info("daemon stopped")
			a.ui.OK("daemon", "stopped")
			return nil
		},
	}
}

// daemonStarter opens a run for the daemon: the configuration and secrets read
// afresh (with the request's market), unattended, through launch like aja run.
func (a *App) daemonStarter(st *store.Store, logger *slog.Logger) daemon.Starter {
	return func(ctx context.Context, req daemon.Request) (daemon.Session, error) {
		flags := map[string]string{}
		if req.Market != "" {
			flags["markets.active"] = req.Market
		}
		l, err := config.Load(a.paths, a.Env, flags)
		switch {
		case l == nil:
			return nil, err
		case !l.FileExisted:
			return nil, errors.New("no config file at " + a.paths.ConfigFile + "; run `aja init` first")
		case err != nil:
			return nil, fmt.Errorf("fix the configuration first (`aja config validate`): %w", err)
		}
		plan, err := launch.NewPlan(l, a.paths, logger)
		if err != nil {
			return nil, err
		}
		telegram := notify.New(l.Secrets.TelegramBotToken, l.Secrets.TelegramChatID, logger)
		opts := run.Options{DryRun: req.DryRun, Unattended: true}
		opened, err := plan.Open(ctx, opts, launch.Deps{Origin: req.Origin, Paths: a.paths, Store: st, Logger: logger,
			Notifier: telegram, Events: a.printEvent, OpenBrowser: OpenBrowser})
		if err != nil {
			return nil, err
		}
		return opened, nil
	}
}
