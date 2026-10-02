package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/daemon"
	"github.com/luizfreitas27/aja/internal/launch"
	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/notify"
	"github.com/luizfreitas27/aja/internal/run"
	"github.com/luizfreitas27/aja/internal/service"
	"github.com/luizfreitas27/aja/internal/store"
)

func (a *App) daemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start and stop runs on the schedule windows, until stopped",
		Long: `Runs in the foreground until Ctrl-C (or a service stop): every 30 seconds it looks at
the schedule windows (aja schedule), starts one unattended run when a window opens and
stops it when the window closes. The configuration is read afresh for every run. A run in
progress when the daemon stops ends cleanly first.

Only one daemon runs at a time. Its log is the daemon file in the log directory
(aja logs --daemon). To have it start with your desktop session, aja daemon install.`,
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
	cmd.AddCommand(a.daemonInstallCmd(), a.daemonUninstallCmd(), a.daemonStatusCmd())
	return cmd
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

// Seams for the service commands; tests replace them.
var (
	systemctl  service.Systemctl = service.RealSystemctl
	executable                   = os.Executable
)

// serviceManager is the systemd user service in the user's config directory.
func (a *App) serviceManager() service.Manager {
	base := a.Env.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return service.Manager{UnitDir: filepath.Join(base, "systemd", "user"), Systemctl: systemctl}
}

func (a *App) daemonInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Run the daemon as a systemd user service that starts with your desktop session",
		Long: `Writes a systemd user unit for this aja binary (with AJA_HOME and --config when you use
them), enables it for your desktop session and (re)starts it. Run it again after moving or
upgrading the binary. The service is stopped like Ctrl-C, so a run in progress ends cleanly.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bin, err := executable()
			if err != nil {
				return err
			}
			if resolved, err := filepath.EvalSymlinks(bin); err == nil {
				bin = resolved
			}
			o := service.Options{Binary: bin}
			if a.configFlag != "" {
				if o.ConfigFile, err = filepath.Abs(a.configFlag); err != nil {
					return err
				}
			}
			if home := a.Env.Getenv("AJA_HOME"); home != "" {
				if o.Home, err = filepath.Abs(home); err != nil {
					return err
				}
			}
			in, err := a.serviceManager().Install(o)
			for _, c := range in.Commands {
				a.ui.Hint("ran: " + c)
			}
			if err != nil {
				return err
			}
			a.ui.OK("installed", in.Path)
			for _, w := range in.Warnings {
				a.ui.Warn("service", w)
			}
			a.ui.Hint("It starts with your desktop session. Check it with aja daemon status; follow it with aja logs --daemon -f.")
			return nil
		},
	}
}

func (a *App) daemonUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop the daemon service and remove it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m := a.serviceManager()
			commands, err := m.Uninstall()
			if errors.Is(err, service.ErrNotInstalled) {
				a.ui.Println("The aja service is not installed.")
				return nil
			}
			for _, c := range commands {
				a.ui.Hint("ran: " + c)
			}
			if err != nil {
				return err
			}
			a.ui.OK("removed", m.UnitPath())
			return nil
		},
	}
}

func (a *App) daemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Whether the daemon service is installed and running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st := a.serviceManager().Status()
			if st.Installed {
				a.ui.KV("service", fmt.Sprintf("%s, %s (%s)", st.Enabled, st.Active, st.Path))
			} else {
				a.ui.KV("service", "not installed (aja daemon install)")
			}
			if d, running, err := daemon.ReadLock(a.paths.DataDir); err != nil {
				return err
			} else if running {
				a.ui.KV("daemon", "running ("+d.String()+")")
			} else {
				a.ui.KV("daemon", "not running")
			}
			return nil
		},
	}
}
