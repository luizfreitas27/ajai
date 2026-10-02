// Package cli is the cobra command tree. It holds no business logic: each
// command loads the configuration, calls a package, and prints through ui.
package cli

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/luizfreitas27/aja/internal/config"
	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/paths"
	"github.com/luizfreitas27/aja/internal/ui"
)

// App carries what every command needs. Tests build one with buffers and a
// fake environment; main builds one from the process.
type App struct {
	Version string
	Stdout  io.Writer
	Stderr  io.Writer
	Stdin   io.Reader
	Getenv  func(string) string
	Env     config.Env
	Environ config.Environ

	// set by the root flags
	configFlag string
	noColor    bool

	ui     *ui.Printer
	paths  paths.Paths
	logger *slog.Logger
	closer io.Closer
}

// NewApp wires the process environment.
func NewApp(version string) *App {
	return &App{Version: version, Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin,
		Getenv: os.Getenv, Env: config.OSEnv(), Environ: os.Environ}
}

// Command builds the root command and its subcommands.
func (a *App) Command() *cobra.Command {
	root := &cobra.Command{
		Use:   "aja",
		Short: "Apply to LinkedIn Easy Apply jobs from your own Chrome",
		Long: `aja drives your real, visible Chrome through LinkedIn job search and Easy Apply,
answering forms from your configuration, its answer memory and (optionally) an AI
model. Start with "aja init", check with "aja doctor", try "aja run --dry-run".`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			a.ui = ui.New(a.Stdout, a.Stderr, a.noColor)
			a.paths = paths.Resolve(a.configFlag, a.Getenv)
			a.logger = logging.Discard
			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			if a.closer != nil {
				a.closer.Close()
			}
		},
	}
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	root.SetIn(a.Stdin)
	root.PersistentFlags().StringVar(&a.configFlag, "config", "", "config file (default: the OS config dir, or $AJA_HOME/config.yaml)")
	root.PersistentFlags().BoolVar(&a.noColor, "no-color", false, "plain output (also honours NO_COLOR)")
	root.AddCommand(a.versionCmd(), a.initCmd(), a.configCmd(), a.doctorCmd())
	return root
}

// Execute runs the tree and returns the exit code.
func (a *App) Execute(ctx context.Context, args []string) int {
	cmd := a.Command()
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(ctx); err != nil {
		if a.ui == nil {
			a.ui = ui.New(a.Stdout, a.Stderr, true)
		}
		a.ui.Error(err)
		var ec interface{ ExitCode() int }
		if asExit(err, &ec) {
			return ec.ExitCode()
		}
		return 1
	}
	return 0
}

// exitError carries a specific exit code (doctor: 1 on failures, 2 on warnings only).
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }
func (e *exitError) ExitCode() int { return e.code }

func asExit(err error, target *interface{ ExitCode() int }) bool {
	if e, ok := err.(*exitError); ok {
		*target = e
		return true
	}
	return false
}

// load reads the configuration for a command. It returns the loaded value even
// when validation failed so callers can decide; the error says what is wrong.
func (a *App) load(flags map[string]string) (*config.Loaded, error) {
	return config.Load(a.paths, a.Env, a.Environ, flags)
}

// openLog starts the day's log file; commands that write to the data directory call it.
func (a *App) openLog(level string) {
	logger, closer, err := logging.Open(a.paths.LogDir, logging.ParseLevel(level), nil)
	if err != nil {
		a.ui.Warn("log", "could not open the log file: "+err.Error())
		return
	}
	a.logger, a.closer = logger, closer
}

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			a.ui.Println("aja", a.Version)
		},
	}
}
