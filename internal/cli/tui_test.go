package cli

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
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
	if strings.Contains(out, "aja [flags]") {
		t.Errorf("the help keeps its usage lines; the TUI adds none:\n%s", out)
	}
	if _, help, _ := h.run("--help"); help != out {
		t.Errorf("bare aja and aja --help print the same help:\n%s\n---\n%s", out, help)
	}
	if _, sub, _ := h.run("status", "--help"); !strings.Contains(sub, "aja status [flags]") {
		t.Errorf("a subcommand keeps its usage line:\n%s", sub)
	}
	if code, _, errOut := h.run("bogus"); code == 0 || !strings.Contains(errOut, `unknown command "bogus"`) {
		t.Errorf("an unknown command is still an error: %d %q", code, errOut)
	}
}

func TestBareAjaOpensTheTUIInATerminal(t *testing.T) {
	h := newHarness(t)
	type call struct {
		deps  tui.Deps
		plain bool
	}
	var calls []call
	oldTerm, oldShow := inTerminal, showTUI
	t.Cleanup(func() { inTerminal, showTUI = oldTerm, oldShow })
	inTerminal = func(*App) bool { return true }
	showTUI = func(_ context.Context, d tui.Deps, _ io.Reader, _ io.Writer, plain bool) error {
		calls = append(calls, call{d, plain})
		return nil
	}

	h.env["NO_COLOR"] = ""
	other := filepath.Join(t.TempDir(), "other.yaml")
	for _, args := range [][]string{{}, {"--no-color"}, {"--config", other}} {
		if code, out, errOut := h.run(args...); code != 0 {
			t.Fatalf("%v: %d %q %q", args, code, out, errOut)
		}
	}
	h.env["NO_COLOR"] = "1"
	h.run()
	if len(calls) != 4 {
		t.Fatalf("%d calls", len(calls))
	}
	first := calls[0]
	if first.deps.Store == nil || first.deps.Now == nil || first.deps.Logger == nil || first.deps.Env.Lookup == nil ||
		first.deps.Paths.DataDir != h.home || first.plain {
		t.Errorf("colour on in a terminal by default: %+v", first)
	}
	if !calls[1].plain || !calls[3].plain {
		t.Error("--no-color and NO_COLOR turn colour off")
	}
	if calls[2].deps.Paths.ConfigFile != other {
		t.Errorf("--config reaches the TUI: %q", calls[2].deps.Paths.ConfigFile)
	}
}

// TestTheTerminalLauncherPassesArgumentsAsTheyAreAndWaitsForEnter runs the
// shell line the launcher hands to Bubble Tea, with echo and false for aja.
func TestTheTerminalLauncherPassesArgumentsAsTheyAreAndWaitsForEnter(t *testing.T) {
	run := func(self string, args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command("/bin/sh", append([]string{"-c", afterwards, self}, args...)...)
		cmd.Stdin = strings.NewReader("\n")
		out, err := cmd.Output()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return string(out), exit.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(out), 0
	}
	out, code := run("/bin/echo", "import", "/home/ana/my bot; rm -rf x")
	if code != 0 || !strings.HasPrefix(out, "import /home/ana/my bot; rm -rf x\n") || !strings.Contains(out, "[aja] Press Enter to go back to the TUI.") {
		t.Errorf("%d %q", code, out)
	}
	if _, code := run("/bin/false"); code != 1 {
		t.Errorf("aja's exit status is kept: %d", code)
	}
}
