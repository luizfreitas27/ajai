package cli

import (
	"context"
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
			l, _ := a.load() // the log level: fixed for the daemon's life; runs reread everything else
			logger, closer, err := logging.OpenDaily(a.paths.LogDir, logging.DaemonFileName, logging.ParseLevel(logLevel(l)))
			if err != nil {
				return err
			}
			defer func() { _ = closer.Close() }()
			a.logger = logger
			st, err := a.openStore()
			if err != nil {
				return err
			}
			telegram := freshTelegram{app: a, logger: logger}
			sup := daemon.NewSupervisor(a.paths.DataDir, a.daemonStarter(st, logger), telegram, logger)
			sched := daemon.NewScheduler(st, sup, telegram, logger, a.now)

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
	return func(ctx context.Context, req daemon.Request) (daemon.Opened, error) {
		l, err := config.Load(a.paths, a.Env, marketFlag(req.Market))
		if err := a.configProblem(l, err); err != nil {
			return nil, err
		}
		plan, err := launch.NewPlan(l, a.paths, logger)
		if err != nil {
			return nil, err
		}
		telegram := notify.New(l.Secrets.TelegramBotToken, l.Secrets.TelegramChatID, logger)
		opts := run.Options{DryRun: req.DryRun, Unattended: true}
		// No help pause and no filter pause to offer: a daemon run is unattended.
		opened, err := plan.Open(ctx, opts, launch.Deps{Origin: req.Origin, Window: req.Opening.WindowID, Paths: a.paths, Store: st,
			Logger: logger, Notifier: telegram, Events: a.printEvent, OpenBrowser: OpenBrowser})
		if err != nil {
			return nil, err
		}
		return opened, nil
	}
}

// freshTelegram sends the daemon's own notices with the secrets of the moment,
// so a token set while the daemon runs is used without a restart.
type freshTelegram struct {
	app    *App
	logger *slog.Logger
}

func (f freshTelegram) Send(ctx context.Context, text string) bool {
	s := f.app.secrets(nil)
	return notify.New(s.TelegramBotToken, s.TelegramChatID, f.logger).Send(ctx, text)
}
