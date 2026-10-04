package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/daemon"
	"github.com/luizfreitas27/ajai/internal/launch"
	"github.com/luizfreitas27/ajai/internal/logging"
	"github.com/luizfreitas27/ajai/internal/notify"
	"github.com/luizfreitas27/ajai/internal/paths"
	"github.com/luizfreitas27/ajai/internal/remote"
	"github.com/luizfreitas27/ajai/internal/run"
	"github.com/luizfreitas27/ajai/internal/service"
	"github.com/luizfreitas27/ajai/internal/store"
)

func (a *App) daemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start and stop runs on the schedule windows, until stopped",
		Long: `Runs in the foreground until Ctrl-C (or its systemd unit stops): every 30 seconds it
looks at the schedule windows (ajai schedule), starts one unattended run when a window opens
and stops it when the window closes. With AJAI_TELEGRAM_BOT_TOKEN and AJAI_TELEGRAM_CHAT_ID set
it also takes commands from that chat: /status, /stop, /run, /help. The configuration is
read afresh for every run. A run in progress when the daemon stops ends cleanly first.

Only one daemon runs at a time. Its log is the daemon file in the log directory
(ajai logs --daemon). To have it start with your desktop session: ajai daemon install.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := a.paths.EnsureDataDirs(); err != nil {
				return err
			}
			lock, err := daemon.AcquireLock(a.paths.DataDir, time.Now())
			var already *daemon.RunningError
			if errors.As(err, &already) {
				return &exitError{code: service.ExitAlreadyRunning, msg: err.Error()} // the systemd unit does not retry on it
			}
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
				a.ui.Hint("No schedule windows yet: add one with ajai schedule add.")
			} else {
				a.printNextOpening(windows)
			}
			// Remote commands need the bot's token and chat id when the daemon starts.
			var listening sync.WaitGroup
			if s := a.secrets(nil); s.TelegramBotToken != "" && s.TelegramChatID != "" {
				bot := notify.New(s.TelegramBotToken, s.TelegramChatID, logger)
				listener := remote.New(bot, sup, st, a.paths.DataDir, logger, a.now)
				listening.Add(1)
				go func() { defer listening.Done(); listener.Loop(ctx) }()
				a.ui.OK("telegram", "listening for commands: /status, /stop, /run, /help")
			} else {
				a.ui.Hint("Telegram commands are off: set AJAI_TELEGRAM_BOT_TOKEN and AJAI_TELEGRAM_CHAT_ID, then restart the daemon.")
			}
			logger.Info("daemon started", "pid", os.Getpid(), "windows", len(windows))
			sched.Loop(ctx, daemon.TickEvery)
			if _, ok := sup.Current(); ok {
				a.ui.Println("Stopping: the run in progress is ending…")
			}
			sup.Wait()
			listening.Wait()
			logger.Info("daemon stopped")
			a.ui.OK("daemon", "stopped")
			return nil
		},
	}
	cmd.AddCommand(a.daemonInstallCmd(), a.daemonUninstallCmd(), a.daemonStatusCmd())
	return cmd
}

// daemonStarter opens a run for the daemon: the configuration and secrets read
// afresh (with the request's market), unattended, through launch like ajai run.
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

// Seams for the systemd unit commands; tests replace them.
var (
	systemctl  service.Systemctl = service.RealSystemctl
	executable                   = os.Executable // on Linux, already the resolved path
	unitSettle                   = 5 * time.Second
)

// serviceManager is the systemd user unit, in the directory systemd looks in.
func (a *App) serviceManager() (service.Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return service.Manager{}, err
	}
	env, _ := systemctl("show-environment") // when systemd cannot be asked, the commands below say so
	return service.Manager{UnitDir: service.UnitDir(env, home), Systemctl: systemctl, Settle: unitSettle}, nil
}

// pinnedEnv is the environment the unit must carry for the daemon to find the
// same config and data as this shell: what is set here, made absolute.
func (a *App) pinnedEnv() (map[string]string, error) {
	env := map[string]string{}
	for _, k := range []string{paths.HomeEnv, "XDG_CONFIG_HOME", "XDG_DATA_HOME"} {
		if v := a.Env.Getenv(k); v != "" {
			abs, err := filepath.Abs(v)
			if err != nil {
				return nil, err
			}
			env[k] = abs
		}
	}
	return env, nil
}

// handStarted is a daemon running outside systemd (`ajai daemon` in a terminal)
// while the unit's is not: it keeps the unit's from starting, and the unit
// cannot stop it.
func (a *App) handStarted(m service.Manager) (daemon.Process, bool) {
	d, running, _ := daemon.ReadLock(a.paths.DataDir)
	return d, running && !m.Status().Running()
}

// installCheck refuses to install while a daemon runs outside systemd, and says
// what reinstalling a running unit ends: the run in progress, if any.
func (a *App) installCheck(m service.Manager) (restarting string, err error) {
	if d, ok := a.handStarted(m); ok {
		return "", fmt.Errorf("ajai daemon is already running outside systemd (%s): stop it (Ctrl-C in its terminal), then install", d)
	}
	if h, held, _ := run.ReadLock(a.paths.DataDir); held && m.Status().Running() {
		return "restarting the daemon ends " + h.String() + "; its window resumes once the daemon is back", nil
	}
	return "", nil
}

// installOptions are the unit's for this binary, config and environment.
func (a *App) installOptions() (service.Options, error) {
	o := service.Options{}
	var err error
	if o.Binary, err = executable(); err != nil {
		return o, err
	}
	if a.configFlag != "" {
		if o.ConfigFile, err = filepath.Abs(a.configFlag); err != nil {
			return o, err
		}
	}
	o.Env, err = a.pinnedEnv()
	return o, err
}

// daemonUnit is the daemon's systemd user unit for the TUI, with `ajai daemon
// install`'s checks and options. The manager is made on each call: making it
// asks systemd, which the TUI does only while it shows the unit.
type daemonUnit struct{ app *App }

// Status is the unit as systemd sees it.
func (u daemonUnit) Status() service.Status {
	m, err := u.app.serviceManager()
	if err != nil {
		return service.Status{}
	}
	return m.Status()
}

// Install writes, enables and starts the unit as `ajai daemon install` does;
// what reinstalling ends comes first among the warnings.
func (u daemonUnit) Install() (service.Installed, error) {
	m, err := u.app.serviceManager()
	if err != nil {
		return service.Installed{}, err
	}
	restarting, err := u.app.installCheck(m)
	if err != nil {
		return service.Installed{}, err
	}
	o, err := u.app.installOptions()
	if err != nil {
		return service.Installed{}, err
	}
	result, err := m.Install(o)
	if restarting != "" {
		result.Warnings = append([]string{restarting}, result.Warnings...)
	}
	return result, err
}

// Uninstall stops and disables the unit and removes it.
func (u daemonUnit) Uninstall() error {
	m, err := u.app.serviceManager()
	if err != nil {
		return err
	}
	_, err = m.Uninstall()
	return err
}

// Start starts the unit, unless a daemon started by hand runs (the unit's
// would exit at once).
func (u daemonUnit) Start() error {
	m, err := u.app.serviceManager()
	if err != nil {
		return err
	}
	if d, ok := u.app.handStarted(m); ok {
		return fmt.Errorf("ajai daemon is already running outside systemd (%s): stop it (Ctrl-C in its terminal) first", d)
	}
	_, err = m.Start()
	return err
}

// Stop stops the unit; a daemon started by hand is not the unit's to stop.
func (u daemonUnit) Stop() error {
	m, err := u.app.serviceManager()
	if err != nil {
		return err
	}
	if d, ok := u.app.handStarted(m); ok {
		return fmt.Errorf("the daemon running (%s) was started by hand, not by its unit: stop it with Ctrl-C in its terminal", d)
	}
	_, err = m.Stop()
	return err
}

func (a *App) daemonInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Run the daemon from a systemd user unit that starts with your desktop session",
		Long: `Writes a systemd user unit for this ajai binary (with AJAI_HOME, the XDG directories and
--config as this shell has them), enables it for your desktop session and (re)starts it. Run
it again after moving or upgrading the binary. The unit is stopped like Ctrl-C, so a run in
progress ends cleanly (and its window resumes when the daemon is back).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := a.serviceManager()
			if err != nil {
				return err
			}
			restarting, err := a.installCheck(m)
			if err != nil {
				return err
			}
			if restarting != "" {
				a.ui.Warn("run", restarting)
			}
			o, err := a.installOptions()
			if err != nil {
				return err
			}
			result, err := m.Install(o)
			if result.Unit != "" {
				a.ui.OK("wrote", result.Path)
				for _, line := range strings.Split(strings.TrimRight(result.Unit, "\n"), "\n") {
					a.ui.Hint("  " + line)
				}
			}
			for _, c := range result.Commands {
				a.ui.Hint("ran: " + c)
			}
			if err != nil {
				return err
			}
			for _, w := range result.Warnings {
				a.ui.Warn("unit", w)
			}
			if result.Started {
				a.ui.OK("started", "the daemon runs, and starts with your desktop session; ajai daemon status, ajai logs --daemon -f")
			}
			return nil
		},
	}
}

func (a *App) daemonUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop the daemon's systemd unit and remove it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := a.serviceManager()
			if err != nil {
				return err
			}
			commands, err := m.Uninstall()
			if errors.Is(err, service.ErrNotInstalled) {
				a.ui.Println("The ajai unit is not installed.")
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
		Short: "Whether the daemon's systemd unit is installed, and whether the daemon runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := a.serviceManager()
			if err != nil {
				return err
			}
			if st := m.Status(); st.Installed {
				a.ui.KV("unit", fmt.Sprintf("%s, %s (%s)", st.Enabled, st.Active, st.Path))
			} else {
				a.ui.KV("unit", "not installed (ajai daemon install)")
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
