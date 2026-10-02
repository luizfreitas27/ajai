package cli

import (
	"context"

	"github.com/luizfreitas27/aja/internal/tui"
)

// inTerminal reports whether both ends of aja are a terminal; tests replace it.
var inTerminal = func(a *App) bool { return a.stdinIsTerminal() && a.stdoutIsTerminal() }

// showTUI runs the TUI; tests replace it.
var showTUI = tui.Run

// openTUI shows the TUI over the data directory and database. Its actions go to
// the day's log, at the configured level when there is a config.
func (a *App) openTUI(ctx context.Context) error {
	st, err := a.openStore()
	if err != nil {
		return err
	}
	level := ""
	if l, _ := a.load(); l != nil {
		level = l.Config.Settings.LogLevel
	}
	a.openLog(level)
	d := tui.Deps{Paths: a.paths, Store: st, Env: a.Env, Logger: a.logger, Now: a.now}
	return showTUI(ctx, d, a.Stdin, a.Stdout, a.colorOff())
}
