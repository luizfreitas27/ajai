package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/run"
	"github.com/luizfreitas27/aja/internal/store"
)

func TestStartOpensAFormAndStartsARunOfItsOwn(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.shows("r run · x stop · s setup")
	h.press("r")
	h.shows("Start a run", "closing the TUI never ends it", "> Market    ‹ the config's ›", "Mode      ‹ dry run: fills every form, sends none ›",
		"↑↓ field · ←→ change · enter starts · esc cancels")
	h.golden("start-form")

	h.press("right", "down", "space", "down", "q", "enter") // q is typed into the maximum, not a key
	h.shows("Refused: the maximum is a whole number")
	h.press("backspace", "7", "enter")
	want := []string{"run", "--unattended", "--origin", "tui", "--market", "brasil", "--max", "7"}
	if len(h.started) != 1 || !reflect.DeepEqual(h.started[0], want) {
		t.Fatalf("started %q, want %q", h.started, want)
	}
	pid := os.Getpid()
	h.shows(fmt.Sprintf("Starting a run (pid %d)…", pid))
	if !strings.Contains(h.logs.String(), fmt.Sprintf(`"msg":"run started from the TUI","pid":%d,"args":"--unattended --origin tui --market brasil --max 7"`, pid)) {
		t.Errorf("not logged: %s", h.logs)
	}

	// The process takes the run lock: started.
	h.holdRun(run.Holder{Origin: run.OriginTUI, Started: h.now})
	h.refresh()
	h.shows(fmt.Sprintf("Started: the run is in progress (pid %d).", pid), "starting, started from the TUI")
}

func TestARunThatCannotStartSaysWhyAndAHeldLockRefusesAStart(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.press("r", "enter")
	if !reflect.DeepEqual(h.started, [][]string{{"run", "--unattended", "--origin", "tui", "--dry-run"}}) {
		t.Fatalf("a dry run of the config's market by default: %q", h.started)
	}
	h.logAt(logging.FileName, h.now.Add(time.Second), run.StartFailedMsg, `"error":"the Chrome profile is in use: close the Chrome that uses it"`)
	h.exit <- errors.New("exit status 1")
	h.refresh()
	h.shows("The run did not start: the Chrome profile is in use: close the Chrome that uses it")

	// A run another way: Start refuses and names it, starting nothing.
	h.holdRun(run.Holder{PID: os.Getpid(), Origin: run.OriginTerminal, Started: h.now})
	h.press("r", "enter")
	h.shows("Not started: a run started from the terminal", "is in progress; x stops it.")
	if len(h.started) != 1 {
		t.Errorf("started again: %q", h.started)
	}
}

func TestStopAsksFirstThenStopsAnyRunAndSaysWhenItEnded(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.press("x")
	h.shows("No run in progress.")

	lock, err := run.AcquireLock(h.p.DataDir, run.Holder{PID: os.Getpid(), Origin: run.OriginTerminal, Started: h.now})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := h.st.StartRun(context.Background(), "brasil", false)
	h.refresh()
	h.press("x")
	h.shows("Stop the run started from the terminal at 09:30? It ends once the job in hand is done.", "y stops the run · n keeps it")
	h.press("n")
	if _, err := os.Stat(run.StopPath(h.p.DataDir)); err == nil {
		t.Fatal("n keeps the run")
	}
	h.press("x", "y")
	if _, err := os.Stat(run.StopPath(h.p.DataDir)); err != nil {
		t.Fatalf("no stop request: %v", err)
	}
	h.shows("Stopping a run started from the terminal")
	if !strings.Contains(h.logs.String(), "stop requested from the TUI") {
		t.Errorf("not logged: %s", h.logs)
	}

	// The run records its end and lets the lock go.
	if err := h.st.FinishRun(context.Background(), store.Run{ID: id, Applied: 2, Skipped: 3, Reason: reasonStopped}); err != nil {
		t.Fatal(err)
	}
	lock.Release()
	h.refresh()
	h.shows("The run ended (stopped from the TUI): applied 2 · external 0 · failed 0 · skipped 3.", "No run in progress.")
}
