package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/luizfreitas27/ajai/internal/tui"
)

func TestBareAjaiPrintsTheHelpOutsideATerminal(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run()
	if code != 0 || !strings.Contains(out, "Available Commands:") || !strings.Contains(out, `"ajai" with no command opens the TUI`) {
		t.Errorf("%d %q", code, out)
	}
	if strings.Contains(out, "ajai [flags]") {
		t.Errorf("the help keeps its usage lines; the TUI adds none:\n%s", out)
	}
	if _, help, _ := h.run("--help"); help != out {
		t.Errorf("bare ajai and ajai --help print the same help:\n%s\n---\n%s", out, help)
	}
	if _, sub, _ := h.run("status", "--help"); !strings.Contains(sub, "ajai status [flags]") {
		t.Errorf("a subcommand keeps its usage line:\n%s", sub)
	}
	if code, _, errOut := h.run("bogus"); code == 0 || !strings.Contains(errOut, `unknown command "bogus"`) {
		t.Errorf("an unknown command is still an error: %d %q", code, errOut)
	}
}

func TestBareAjaiOpensTheTUIInATerminal(t *testing.T) {
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
// shell line the launcher hands to Bubble Tea, with echo and false for ajai.
func TestTheTerminalLauncherPassesArgumentsAsTheyAreAndWaitsForEnter(t *testing.T) {
	run := func(self string, args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command("/bin/sh", append([]string{"-c", afterwards, self}, args...)...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // its own group, as a terminal's job: Ctrl-C reaches only it
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
	if code != 0 || !strings.HasPrefix(out, "import /home/ana/my bot; rm -rf x\n") || !strings.Contains(out, "[ajai] Press Enter to go back to the TUI.") {
		t.Errorf("%d %q", code, out)
	}
	if _, code := run("/bin/false"); code != 1 {
		t.Errorf("ajai's exit status is kept: %d", code)
	}
	// Ctrl-C ends ajai (SIGINT to the whole group); the shell still waits for Enter.
	out, code = run("/bin/sh", "-c", `kill -INT 0; echo never`)
	if !strings.Contains(out, "[ajai] Press Enter") || strings.Contains(out, "never") || code == 0 {
		t.Errorf("after Ctrl-C: %d %q", code, out)
	}
}

// TestStartRunsAjaiInASessionOfItsOwnAndReportsItsEnd uses sh for ajai.
func TestStartRunsAjaiInASessionOfItsOwnAndReportsItsEnd(t *testing.T) {
	l := launcher{executable: "/bin/sh"}
	pid, exited, err := l.Start([]string{"-c", "sleep 0.3; exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Fatal(err)
	}
	// Fields after the command name ")": state ppid pgrp session …
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	if fields[3] != strconv.Itoa(pid) {
		t.Errorf("the run leads a session of its own: session %s, pid %d", fields[3], pid)
	}
	var exit *exec.ExitError
	if err := <-exited; !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Errorf("its end: %v", err)
	}
	withFlags := launcher{executable: "ajai", configFlag: "/x/c.yaml", noColor: true}.argv([]string{"run"})
	if strings.Join(withFlags, " ") != "--config /x/c.yaml --no-color run" {
		t.Errorf("%q", withFlags)
	}
}
