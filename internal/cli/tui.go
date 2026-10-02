package cli

import (
	"context"

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
	d := tui.Deps{Paths: a.paths, Store: st, Env: a.Env, Logger: logger, Now: a.now}
	return showTUI(ctx, d, a.Stdin, a.Stdout, a.colorOff())
}
