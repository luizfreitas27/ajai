package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/run"
	"github.com/luizfreitas27/ajai/internal/schedule"
	"github.com/luizfreitas27/ajai/internal/store"
)

type notices struct {
	mu   sync.Mutex
	sent []string
}

func (n *notices) Send(_ context.Context, text string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, text)
	return true
}

func (n *notices) all() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.sent...)
}

// world is a scheduler over a real store and supervisor, a fake starter and a
// clock the test moves.
type world struct {
	t      *testing.T
	st     *store.Store
	sup    *Supervisor
	sched  *Scheduler
	runs   *fakeRuns
	notes  *notices
	now    time.Time
	ended  chan Ended
	ctx    context.Context
	cancel context.CancelFunc
}

func newWorld(t *testing.T, windows ...schedule.Window) *world {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "ajai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, w := range windows {
		if _, err := st.AddWindow(w); err != nil {
			t.Fatal(err)
		}
	}
	w := &world{t: t, st: st, runs: &fakeRuns{dataDir: dir}, notes: &notices{}, ended: make(chan Ended, 10)}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	t.Cleanup(func() { w.cancel(); w.sup.Wait() })
	w.sup = NewSupervisor(dir, w.runs.Start, w.notes, nil)
	w.sched = NewScheduler(st, w.sup, w.notes, nil, func() time.Time { return w.now })
	w.sup.OnEnd = func(e Ended) { w.sched.RunEnded(e); w.ended <- e }
	return w
}

// at moves the clock to "Mon 15:04" of the week of 2026-10-05 (a Monday) and ticks.
func (w *world) at(when string) {
	w.t.Helper()
	day, clock, _ := strings.Cut(when, " ")
	days := map[string]int{"Mon": 5, "Tue": 6, "Wed": 7, "Thu": 8, "Fri": 9, "Sat": 10, "Sun": 11}
	tm, err := time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("2026-10-%02d %s", days[day], clock), time.Local)
	if err != nil {
		w.t.Fatal(err)
	}
	w.now = tm
	if err := w.sched.Tick(w.ctx); err != nil {
		w.t.Fatal(err)
	}
}

// outcome is what was recorded for a window's opening on a date ("" when none).
func (w *world) outcome(windowID int64, date string) schedule.Outcome {
	w.t.Helper()
	rec, err := w.st.GetOpening(schedule.Key{WindowID: windowID, Date: date})
	if err != nil {
		w.t.Fatal(err)
	}
	if rec == nil {
		return ""
	}
	return rec.Outcome
}

func window(t *testing.T, days, start, end string) schedule.Window {
	t.Helper()
	d, err := schedule.ParseDays(days)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := schedule.ParseClock(start)
	e, _ := schedule.ParseClock(end)
	return schedule.Window{Days: d, Start: s, End: e, Enabled: true}
}

func TestAWindowStartsItsRunAndStopsItWhenItCloses(t *testing.T) {
	win := window(t, "mon-fri", "09:00", "11:00")
	win.Market, win.DryRun = "brasil", true
	w := newWorld(t, win)
	w.at("Mon 08:59")
	if len(w.runs.requests()) != 0 {
		t.Fatal("too early")
	}
	w.at("Mon 09:00")
	reqs := w.runs.requests()
	if len(reqs) != 1 || reqs[0].Origin != run.OriginSchedule || reqs[0].Market != "brasil" || !reqs[0].DryRun ||
		reqs[0].Opening != (schedule.Key{WindowID: 1, Date: "2026-10-05"}) {
		t.Fatalf("%+v", reqs)
	}
	if n := w.notes.all(); len(n) != 1 || !strings.Contains(n[0], "ajai started a run for schedule window 1: Mon-Fri 09:00-11:00, market brasil, dry run") {
		t.Errorf("Telegram says which window: %q", n)
	}
	w.at("Mon 10:30")
	if _, ok := w.sup.Current(); !ok || len(w.runs.requests()) != 1 {
		t.Error("the run goes on inside the window")
	}
	w.at("Mon 11:00")
	e := <-w.ended
	if stopReason(w.runs.causes[0]) != ReasonWindowClosed || e.Info.Stopping != ReasonWindowClosed {
		t.Errorf("stopped as the window closed: %v %+v", w.runs.causes, e)
	}
	w.at("Mon 11:01")
	if len(w.runs.requests()) != 1 {
		t.Error("no new run once closed")
	}
	w.at("Tue 09:00")
	if len(w.runs.requests()) != 2 {
		t.Error("the next day's opening starts")
	}
}

func TestARunThatEndsEarlyIsNotRestartedInTheSameOpening(t *testing.T) {
	w := newWorld(t, window(t, "mon", "09:00", "11:00"))
	w.runs.behave = func(context.Context, Request) ([]run.Summary, error) {
		return []run.Summary{{RunID: 0}}, nil // every term visited
	}
	w.at("Mon 09:00")
	<-w.ended
	w.at("Mon 09:00")
	w.at("Mon 10:00")
	if n := len(w.runs.requests()); n != 1 {
		t.Errorf("%d runs", n)
	}
}

func TestADaemonStartedMidWindowRunsItOnceAndRecordsTheRunID(t *testing.T) {
	w := newWorld(t, window(t, "mon", "09:00", "11:00"))
	runID, err := w.st.StartRun(context.Background(), "brasil", false) // the run the session records
	if err != nil {
		t.Fatal(err)
	}
	w.runs.behave = func(ctx context.Context, _ Request) ([]run.Summary, error) {
		<-ctx.Done()
		return []run.Summary{{RunID: runID}}, nil
	}
	w.at("Mon 10:15")
	if n := len(w.runs.requests()); n != 1 {
		t.Fatalf("%d runs", n)
	}
	_, _ = w.sup.Stop("stopped from Telegram") // the user stops it early
	<-w.ended
	w.at("Mon 10:16")
	if n := len(w.runs.requests()); n != 1 {
		t.Errorf("stopped by hand, the opening stays handled: %d runs", n)
	}
	rec, err := w.st.GetOpening(schedule.Key{WindowID: 1, Date: "2026-10-05"})
	if err != nil || rec == nil || rec.Outcome != schedule.OutcomeStarted || rec.RunID != runID {
		t.Errorf("the opening records its run: %+v %v", rec, err)
	}
}

func TestAnOpeningDuringAnotherRunIsSkippedRecordedAndNotified(t *testing.T) {
	w := newWorld(t, window(t, "mon", "09:00", "11:00"))
	terminal, err := run.AcquireLock(w.runs.dataDir, run.Holder{Origin: run.OriginTerminal, Started: w.now})
	if err != nil {
		t.Fatal(err)
	}
	w.at("Mon 09:00")
	terminal.Release()
	if len(w.runs.requests()) != 0 || w.outcome(1, "2026-10-05") != schedule.OutcomeSkipped {
		t.Fatal("skipped and recorded")
	}
	if n := w.notes.all(); len(n) != 1 || !strings.Contains(n[0], "ajai skipped schedule window 1 (Mon 09:00-11:00) today: a run started from the terminal") {
		t.Errorf("%q", n)
	}
	w.at("Mon 09:30")
	if len(w.runs.requests()) != 0 {
		t.Error("a skipped opening is not retried")
	}
}

func TestAFailedStartOrACrashIsReportedAndTheNextWindowStillStarts(t *testing.T) {
	w := newWorld(t, window(t, "mon", "09:00", "10:00"), window(t, "mon", "12:00", "13:00"), window(t, "mon", "14:00", "15:00"))
	w.runs.startErr = errors.New("Chrome was not found")
	w.at("Mon 09:00")
	if n := w.notes.all(); len(n) != 1 || !strings.Contains(n[0], "ajai could not start the run for schedule window 1") || !strings.Contains(n[0], "Chrome was not found") {
		t.Errorf("%q", n)
	}
	w.runs.startErr = nil
	w.at("Mon 09:01")
	if len(w.runs.requests()) != 0 || w.outcome(1, "2026-10-05") != schedule.OutcomeFailed {
		t.Error("a failed opening is recorded and not retried")
	}

	w.runs.behave = func(context.Context, Request) ([]run.Summary, error) { panic("boom") }
	w.at("Mon 12:00")
	if e := <-w.ended; e.Err == nil || !strings.Contains(e.Err.Error(), "crashed") {
		t.Errorf("%+v", e)
	}

	w.runs.behave = nil
	w.at("Mon 14:00")
	if reqs := w.runs.requests(); len(reqs) != 2 || reqs[1].Opening.WindowID != 3 {
		t.Errorf("after a failure and a crash the next window still starts: %+v", reqs)
	}
}

func TestBackToBackWindowsHandOverInsteadOfSkipping(t *testing.T) {
	w := newWorld(t, window(t, "sat", "22:00", "24:00"), window(t, "sun", "00:00", "02:00"))
	slowEnd := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(slowEnd) }) }
	t.Cleanup(release) // runs before the world's cleanup waits for the run
	w.runs.behave = func(ctx context.Context, req Request) ([]run.Summary, error) {
		<-ctx.Done()
		if req.Opening.WindowID == 1 {
			<-slowEnd // the Saturday run is still finishing its job at midnight
		}
		return nil, nil
	}
	w.at("Sat 22:00")
	w.at("Sun 00:00") // stops Saturday's run; Sunday's opening must wait for it
	if got := w.outcome(2, "2026-10-11"); got != "" {
		t.Fatalf("Sunday's opening must not be skipped while our own run is ending: %q", got)
	}
	release()
	<-w.ended
	w.now = w.now.Add(time.Second) // the loop's wake-up tick after the run ended
	if err := w.sched.Tick(w.ctx); err != nil {
		t.Fatal(err)
	}
	reqs := w.runs.requests()
	if len(reqs) != 2 || reqs[1].Opening.WindowID != 2 || w.outcome(2, "2026-10-11") != schedule.OutcomeStarted {
		t.Errorf("Sunday's window starts once Saturday's run has ended: %+v", reqs)
	}
	for _, n := range w.notes.all() {
		if strings.Contains(n, "skipped") {
			t.Errorf("no skip notice: %q", n)
		}
	}
}

func TestDisablingOrRemovingAnOpenWindowStopsItsRunWithItsOwnReason(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*store.Store)
		reason string
	}{
		{"disabled", func(st *store.Store) { _, _ = st.SetWindowEnabled(1, false) }, ReasonWindowDisabled},
		{"removed", func(st *store.Store) { _, _ = st.DeleteWindow(1) }, ReasonWindowRemoved},
	} {
		w := newWorld(t, window(t, "mon", "09:00", "11:00"))
		w.at("Mon 09:00")
		tc.change(w.st)
		w.at("Mon 09:30")
		<-w.ended
		if got := stopReason(w.runs.causes[0]); got != tc.reason {
			t.Errorf("%s: %q", tc.name, got)
		}
	}
}

func TestLoopTicksAtOnceAndWhenWoken(t *testing.T) {
	w := newWorld(t, window(t, "mon", "09:00", "11:00"))
	w.now = time.Date(2026, 10, 5, 9, 0, 0, 0, time.Local)
	ctx, cancel := context.WithCancel(w.ctx)
	done := make(chan struct{})
	go func() { w.sched.Loop(ctx, time.Hour); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(w.runs.requests()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(w.runs.requests()) != 1 {
		t.Error("the first tick is immediate")
	}
	cancel()
	<-done
}

func TestAWindowInterruptedByTheDaemonStoppingResumesWhenItIsBack(t *testing.T) {
	w := newWorld(t, window(t, "mon", "09:00", "11:00"))
	w.at("Mon 09:00")
	w.cancel() // a reboot
	if e := <-w.ended; !e.Shutdown {
		t.Fatalf("%+v", e)
	}
	if got := w.outcome(1, "2026-10-05"); got != "" {
		t.Errorf("the interrupted opening is not handled: %q", got)
	}
	w.ctx, w.cancel = context.WithCancel(context.Background()) // the daemon is back
	w.at("Mon 09:40")
	if n := len(w.runs.requests()); n != 2 {
		t.Errorf("the window resumes: %d runs", n)
	}
}
