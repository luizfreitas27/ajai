package cli

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/luizfreitas27/aja/internal/tui"
)

func TestBareAjaPrintsTheHelpOutsideATerminal(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run()
	if code != 0 || !strings.Contains(out, "Available Commands:") || !strings.Contains(out, `"aja" with no command opens the TUI`) {
		t.Errorf("%d %q", code, out)
	}
	if code, _, errOut := h.run("bogus"); code == 0 || !strings.Contains(errOut, `unknown command "bogus"`) {
		t.Errorf("an unknown command is still an error: %d %q", code, errOut)
	}
}

func TestBareAjaOpensTheTUIInATerminal(t *testing.T) {
	h := newHarness(t)
	var got tui.Deps
	var noColor, called bool
	oldTerm, oldShow := inTerminal, showTUI
	t.Cleanup(func() { inTerminal, showTUI = oldTerm, oldShow })
	inTerminal = func(*App) bool { return true }
	showTUI = func(_ context.Context, d tui.Deps, _ io.Reader, _ io.Writer, plain bool) error {
		got, noColor, called = d, plain, true
		return nil
	}
	if code, out, errOut := h.run(); code != 0 || !called {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if got.Store == nil || got.Now == nil || got.Paths.DataDir != h.home || !noColor {
		t.Errorf("deps %+v, no colour %v", got.Paths, noColor)
	}
}
