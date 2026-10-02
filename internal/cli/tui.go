package cli

import (
	"context"
	"os"
	"os/exec"
	"syscall"

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
		Probe: doctor.RealProbe(), Launcher: launcher{executable: self, configFlag: a.configFlag, noColor: a.noColor}, Home: home}
	return showTUI(ctx, d, a.Stdin, a.Stdout, a.colorOff())
}

// launcher runs aja for the TUI, with the same --config and --no-color.
type launcher struct {
	executable, configFlag string
	noColor                bool
}

// argv puts the TUI's own flags before args.
func (l launcher) argv(args []string) []string {
	var out []string
	if l.configFlag != "" {
		out = append(out, "--config", l.configFlag)
	}
	if l.noColor {
		out = append(out, "--no-color")
	}
	return append(out, args...)
}

// afterwards keeps a subcommand's last words on screen until Enter: a shell
// runs aja ("$0", its arguments "$@"), then waits, and exits with aja's status.
// Ctrl-C ends aja, not the shell (a trap is reset for aja, so aja still gets it).
const afterwards = `trap ':' INT; "$0" "$@"; status=$?; printf '\n[aja] Press Enter to go back to the TUI. '; read -r _; exit $status`

// InTerminal runs aja args in the TUI's terminal; Bubble Tea gives the terminal
// back to the TUI when it ends.
func (l launcher) InTerminal(args []string, done func(error) tea.Msg) tea.Cmd {
	sh := append([]string{"-c", afterwards, l.executable}, l.argv(args)...)
	return tea.ExecProcess(exec.Command("/bin/sh", sh...), done)
}

// Start runs aja args in a session of its own, so the terminal closing (or
// the TUI quitting) never reaches it; its standard streams go to the null
// device (it logs to the day's file) and it inherits the environment, the
// display Chrome needs included.
func (l launcher) Start(args []string) (int, <-chan error, error) {
	cmd := exec.Command(l.executable, l.argv(args)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return cmd.Process.Pid, exited, nil
}
