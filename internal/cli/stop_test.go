package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/browser"
	"github.com/luizfreitas27/aja/internal/run"
)

func TestRunRefusesWhileAnotherRunHoldsTheLockWithoutOpeningChrome(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	lock, err := run.AcquireLock(h.home, run.OriginSchedule, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	saved := OpenBrowser
	t.Cleanup(func() { OpenBrowser = saved })
	opened := false
	var asked browser.Options
	OpenBrowser = func(ctx context.Context, o browser.Options) (browser.Browser, error) {
		opened, asked = true, o
		return nil, browser.ErrProfileInUse
	}
	code, out, errText := h.run("run", "--dry-run")
	if code == 0 || !strings.Contains(errText, "another run is in progress: a run started from the schedule") {
		t.Errorf("%d %s %s", code, out, errText)
	}
	if opened {
		t.Error("Chrome must not open when the run cannot have the lock")
	}
	if _, out, _ = h.run("status"); !strings.Contains(out, "a run started from the schedule") {
		t.Errorf("status names the run that holds the lock:\n%s", out)
	}

	lock.Release()
	if code, _, _ := h.run("run", "--dry-run"); code == 0 || !opened {
		t.Fatal("with the lock free the run gets as far as Chrome")
	}
	if _, held, _ := run.ReadLock(h.home); held {
		t.Error("a run that could not open Chrome must release the lock")
	}
	if asked.ProfileDir != filepath.Join(h.home, "chrome-profile") || !asked.QuitOnClose {
		t.Errorf("with chrome_profile_dir blank a run uses aja's own profile and closes Chrome after: %+v", asked)
	}
}

func TestStopAsksTheRunInProgressAndWaitsForItToEnd(t *testing.T) {
	h := newHarness(t)
	h.initValid()
	if code, _, errText := h.run("stop"); code == 0 || !strings.Contains(errText, "no run in progress") {
		t.Errorf("nothing to stop: %d %s", code, errText)
	}

	lock, err := run.AcquireLock(h.home, run.OriginTerminal, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	code, out, errText := h.run("stop", "--wait", "0")
	if code != 0 || !strings.Contains(out, "Asked a run started from the terminal") {
		t.Errorf("%d %s %s", code, out, errText)
	}
	if data, _ := os.ReadFile(run.StopPath(h.home)); string(data) != stopReasonCLI {
		t.Errorf("the request carries the reason the run records: %q", data)
	}

	saved := stopWaitPoll
	stopWaitPoll = 10 * time.Millisecond
	t.Cleanup(func() { stopWaitPoll = saved })
	go func() { time.Sleep(100 * time.Millisecond); lock.Release() }() // the run ends
	code, out, _ = h.run("stop", "--wait", "5s")
	if code != 0 || !strings.Contains(out, "stopped") {
		t.Errorf("stop waits for the run to end: %d %s", code, out)
	}
}
