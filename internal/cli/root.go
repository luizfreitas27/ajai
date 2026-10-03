// Package cli is the cobra command tree. It holds no business logic: each
// command loads the configuration, calls a package, and prints through ui.
package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/luizfreitas27/ajai/internal/config"
	"github.com/luizfreitas27/ajai/internal/logging"
	"github.com/luizfreitas27/ajai/internal/paths"
	"github.com/luizfreitas27/ajai/internal/run"
	"github.com/luizfreitas27/ajai/internal/ui"
)

// App carries what every command needs. Tests build one with buffers and a
// fake environment; main builds one from the process.
type App struct {
	Version string
	Commit  string // short commit of a release build, "" otherwise
	Stdout  io.Writer
	Stderr  io.Writer
	Stdin   io.Reader
	Env     config.Env
	Now     func() time.Time // nil: time.Now

	// set by the root flags
	configFlag string
	noColor    bool

	ui       *ui.Printer
	paths    paths.Paths
	logger   *slog.Logger
	cleanups []func() error // closed in reverse order when the command ends

	eventsAsJSON bool // ajai run --json

	linesOnce   sync.Once   // starts the stdin reader (readLine)
	lines       chan string // lines typed on stdin; closed at end of input
	stdinClosed bool        // end of input was seen: nobody can answer anything any more
}

// NewApp wires the process environment.
func NewApp(version string) *App {
	return &App{Version: version, Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin, Env: config.OSEnv()}
}

// Command builds the root command and its subcommands.
func (a *App) Command() *cobra.Command {
	root := &cobra.Command{
		Use:   "ajai",
		Short: "Apply to LinkedIn Easy Apply jobs from your own Chrome",
		Long: `ajai drives your real, visible Chrome through LinkedIn job search and Easy Apply,
answering forms from your configuration, its answer memory and (optionally) an AI
model. Start with "ajai init", sign in once with "ajai login", check with "ajai doctor",
try "ajai run --dry-run". In a terminal, "ajai" with no command opens the TUI.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			a.ui = ui.New(a.Stdout, a.Stderr, a.colorOff())
			a.paths = paths.Resolve(a.configFlag, a.Env.Getenv)
			a.logger = logging.Discard
			return nil
		},
		// With no command: the TUI in a real terminal, the help anywhere else.
		RunE: func(cmd *cobra.Command, args []string) error {
			if !inTerminal(a) {
				return cmd.Help()
			}
			return a.openTUI(cmd.Context())
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			for i := len(a.cleanups) - 1; i >= 0; i-- {
				_ = a.cleanups[i]()
			}
			a.cleanups = nil
		},
	}
	// The root runs the TUI, which would add "ajai [flags]" to the help; the
	// help stays as it was, and subcommands keep their usage line.
	root.SetUsageTemplate(strings.Replace(root.UsageTemplate(), "{{if .Runnable}}", "{{if and .Runnable .HasParent}}", 1))
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	root.SetIn(a.Stdin)
	root.PersistentFlags().StringVar(&a.configFlag, "config", "", "config file (default: the OS config dir, or $AJAI_HOME/config.yaml)")
	root.PersistentFlags().BoolVar(&a.noColor, "no-color", false, "plain output (also honours NO_COLOR)")
	root.AddCommand(a.versionCmd(), a.initCmd(), a.configCmd(), a.doctorCmd(), a.importCmd(), a.jobsCmd(), a.exportCmd(), a.answersCmd(), a.loginCmd(), a.runCmd(), a.stopCmd(), a.statusCmd(), a.logsCmd(), a.scheduleCmd(), a.daemonCmd())
	return root
}

// colorOff: --no-color or NO_COLOR.
func (a *App) colorOff() bool { return a.noColor || a.Env.Get("NO_COLOR") != "" }

// Execute runs the tree and returns the exit code.
func (a *App) Execute(ctx context.Context, args []string) int {
	cmd := a.Command()
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(ctx); err != nil {
		if a.ui == nil {
			a.ui = ui.New(a.Stdout, a.Stderr, true)
		}
		a.ui.Error(err)
		var ee *exitError
		if errors.As(err, &ee) {
			return ee.code
		}
		return 1
	}
	return 0
}

// exitError ends a command with a message and a specific exit code; commands
// use it when the message has already been printed in detail above.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// load reads the configuration for a command. It returns the loaded value even
// when validation failed so callers can decide; the error says what is wrong.
func (a *App) load() (*config.Loaded, error) {
	return config.Load(a.paths, a.Env, nil)
}

// openLog starts the day's log file. Commands that write to the data directory
// call it; read-only commands (version, config get/show/path) stay silent.
func (a *App) openLog(level string) {
	logger, closer, err := logging.Open(a.paths.LogDir, logging.ParseLevel(level), nil)
	if err != nil {
		a.note(run.EventWarn, "log", "could not open the log file: "+err.Error())
		return
	}
	a.logger = logger
	a.cleanups = append(a.cleanups, closer.Close)
}

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			line := "ajai " + a.Version
			if a.Commit != "" {
				line += " (" + a.Commit + ")"
			}
			a.ui.Println(line + " " + runtime.GOOS + "/" + runtime.GOARCH)
		},
	}
}
