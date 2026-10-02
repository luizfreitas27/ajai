package cli

import (
	"context"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/aja/internal/doctor"
	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/tui"
)

// inTerminal reports whether both ends of aja are a terminal; tests replace it.
var inTerminal = func(a *App) bool { return a.stdinIsTerminal() && a.stdoutIsTerminal() }

// showTUI runs the TUI; tests replace it.
var showTUI = tui.Run

// openTUI shows the TUI over the data directory and database. Its actions go to
// the log of the day they happen (the TUI may stay open past midnight), at the
// configured level.
func (a *App) openTUI(ctx context.Context) error {
	st, err := a.openStore()
	if err != nil {
		return err
	}
	l, _ := a.load()
	logger, closer, err := logging.OpenDaily(a.paths.LogDir, logging.FileName, logging.ParseLevel(logLevel(l)))
	if err != nil {
		logger = logging.Discard
	} else {
		a.cleanups = append(a.cleanups, closer.Close)
	}
	self, err := executable()
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	d := tui.Deps{Paths: a.paths, Store: st, Env: a.Env, Logger: logger, Now: a.now,
		Probe: doctor.RealProbe(), Launcher: terminalLauncher{executable: self, configFlag: a.configFlag}, Home: home}
	return showTUI(ctx, d, a.Stdin, a.Stdout, a.colorOff())
}

// terminalLauncher runs aja's subcommands for the TUI, with the same --config.
type terminalLauncher struct{ executable, configFlag string }

// afterwards keeps a subcommand's last words on screen until Enter: a shell
// runs aja ("$0", its arguments "$@"), then waits, and exits with aja's status.
const afterwards = `"$0" "$@"; status=$?; printf '\n[aja] Press Enter to go back to the TUI. '; read -r _; exit $status`

// InTerminal runs aja args in the TUI's terminal; Bubble Tea gives the terminal
// back to the TUI when it ends.
func (l terminalLauncher) InTerminal(args []string, done func(error) tea.Msg) tea.Cmd {
	argv := []string{"-c", afterwards, l.executable}
	if l.configFlag != "" {
		argv = append(argv, "--config", l.configFlag)
	}
	cmd := exec.Command("/bin/sh", append(argv, args...)...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return done(err) })
}
