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
	h.shows(fmt.Sprintf("Stop a run started from the terminal (pid %d) at 09:30? It ends once the job in hand is done.", os.Getpid()), "y stops the run · n keeps it")
	h.golden("stop-confirm")
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

func TestARunThatHeldTheLockThenFailedSaysWhyNotAnOldRunsSummary(t *testing.T) {
	h := newHarness(t)
	h.pastRun(h.now.Add(-24*time.Hour), "brasil", true, store.Run{Applied: 9, Reason: "an old run"})
	h.open(100, 30)
	h.press("r", "enter")
	lock, err := run.AcquireLock(h.p.DataDir, run.Holder{Origin: run.OriginTUI, Started: h.now})
	if err != nil {
		t.Fatal(err)
	}
	h.refresh()
	h.shows("Started: the run is in progress")
	// Chrome will not open: the run logs why and lets the lock go, recording nothing.
	h.logAt(logging.FileName, h.now.Add(time.Second), run.StartFailedMsg, `"error":"launching Chrome: no display"`)
	lock.Release()
	h.exit <- errors.New("exit status 1")
	h.refresh()
	h.shows("The run did not start: launching Chrome: no display")
	if strings.Contains(h.screen(), "The run ended (an old run)") {
		t.Errorf("an old run's summary:\n%s", h.screen())
	}
}

func TestAStartedRunThatEndsCleanlyIsNotAFailureAndEveryRunOfASessionIsAnnounced(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.press("r", "enter")
	lock, err := run.AcquireLock(h.p.DataDir, run.Holder{Origin: run.OriginTUI, Started: h.now})
	if err != nil {
		t.Fatal(err)
	}
	h.refresh()
	// A non-stop session: two runs end while the lock stays held.
	for i, applied := range []int{1, 2} {
		h.now = h.now.Add(time.Minute)
		id, _ := h.st.StartRun(context.Background(), "brasil", true)
		h.now = h.now.Add(time.Minute)
		if err := h.st.FinishRun(context.Background(), store.Run{ID: id, Applied: applied, Reason: fmt.Sprintf("run %d done", i+1)}); err != nil {
			t.Fatal(err)
		}
		h.refresh()
		h.shows(fmt.Sprintf("The run ended (run %d done): applied %d", i+1, applied))
	}
	lock.Release()
	h.exit <- nil
	h.refresh()
	if strings.Contains(h.screen(), "did not start") {
		t.Errorf("a clean end is not a failure:\n%s", h.screen())
	}
	h.shows("The run ended (run 2 done)")
}

func TestOneStartAtATimeAndASlowStartIsSaid(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.press("r", "enter", "r")
	h.shows("A run is starting already (pid")
	if len(h.started) != 1 {
		t.Fatalf("started %q", h.started)
	}
	h.now = h.now.Add(startWait + time.Second)
	h.refresh()
	h.shows("has not begun after 20s; today's log may say why.")
}

func TestStopLeavesAnotherRunAlone(t *testing.T) {
	h := newHarness(t)
	first, err := run.AcquireLock(h.p.DataDir, run.Holder{Origin: run.OriginTerminal, Started: h.now})
	if err != nil {
		t.Fatal(err)
	}
	h.open(100, 30)
	h.press("x")
	h.shows("Stop a run started from the terminal")
	// Before y, that run ends and a schedule window's starts.
	first.Release()
	second, err := run.AcquireLock(h.p.DataDir, run.Holder{Origin: run.OriginSchedule, Window: 2, Started: h.now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	h.press("y")
	h.shows("Not stopped: a run started from schedule window 2")
	if _, err := os.Stat(run.StopPath(h.p.DataDir)); err == nil {
		t.Error("the other run was asked to stop")
	}
}

func TestTheChecklistNeverOpensOverTheStartForm(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.press("r", "down", "down", "1")
	h.session = false
	h.send(runDoctor(h.deps())())
	h.press("2")
	h.shows("Start a run")
	if strings.Contains(h.screen(), "Setup checklist") {
		t.Fatalf("the checklist opened over the form:\n%s", h.screen())
	}
}

func TestARunThatBeganAndEndedBetweenTwoRefreshesIsNoFailure(t *testing.T) {
	h := newHarness(t)
	h.open(100, 30)
	h.press("r", "enter")
	// The whole run happens unseen (the TUI was busy, say, with a fix in the terminal).
	h.now = h.now.Add(time.Minute)
	id, _ := h.st.StartRun(context.Background(), "brasil", true)
	if err := h.st.FinishRun(context.Background(), store.Run{ID: id, Applied: 0, Skipped: 2, Reason: "the search ran out of jobs"}); err != nil {
		t.Fatal(err)
	}
	h.exit <- nil
	h.refresh()
	h.shows("The run ended (the search ran out of jobs)")
	if strings.Contains(h.screen(), "did not start") {
		t.Errorf("a clean end is no failure:\n%s", h.screen())
	}
}
