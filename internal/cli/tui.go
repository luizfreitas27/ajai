package cli

import (
	"context"

	"github.com/luizfreitas27/aja/internal/tui"
)

// inTerminal reports whether both ends of aja are a terminal; tests replace it.
var inTerminal = func(a *App) bool { return a.stdinIsTerminal() && a.stdoutIsTerminal() }

// showTUI runs the TUI; tests replace it.
var showTUI = tui.Run

// openTUI shows the TUI over the data directory and database.
func (a *App) openTUI(ctx context.Context) error {
	st, err := a.openStore()
	if err != nil {
		return err
	}
	return showTUI(ctx, tui.Deps{Paths: a.paths, Store: st, Now: a.now}, a.Stdin, a.Stdout, !a.ui.Color())
}
