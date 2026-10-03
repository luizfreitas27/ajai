package run

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/ajai/internal/filelock"
)

func fastStopPoll(t *testing.T) {
	saved := stopPoll
	stopPoll = 10 * time.Millisecond
	t.Cleanup(func() { stopPoll = saved })
}

func TestOneRunHoldsTheLockAndTheRefusalNamesIt(t *testing.T) {
	dir := t.TempDir()
	if _, held, err := ReadLock(dir); held || err != nil {
		t.Fatalf("nothing holds it yet: %v %v", held, err)
	}
	at := time.Date(2026, 10, 2, 9, 30, 0, 0, time.Local)
	a, err := AcquireLock(dir, Holder{Origin: OriginTerminal, Started: at})
	if err != nil {
		t.Fatal(err)
	}
	h, held, err := ReadLock(dir)
	if err != nil || !held || h.Origin != OriginTerminal || h.PID != os.Getpid() || !h.Started.Equal(at) {
		t.Errorf("%+v %v %v", h, held, err)
	}
	_, err = AcquireLock(dir, Holder{Origin: OriginSchedule, Started: time.Now()})
	var he *HeldError
	if !errors.As(err, &he) || he.Holder.Origin != OriginTerminal {
		t.Fatalf("a second run must be refused, naming the first: %v", err)
	}
	want := "another run is in progress: a run started from the terminal (pid " + strconv.Itoa(os.Getpid()) + ") at 09:30; stop it with `ajai stop` or wait for it to end"
	if err.Error() != want {
		t.Errorf("%q", err)
	}
	a.Release()
	b, err := AcquireLock(dir, Holder{Origin: OriginTelegram, Started: time.Now()})
	if err != nil {
		t.Fatalf("free once released: %v", err)
	}
	b.Release()
}

func TestALockACrashedRunLeftNeverBlocksTheNext(t *testing.T) {
	dir := t.TempDir()
	// A run killed with SIGKILL never empties the file: its description stays,
	// naming a process that is gone, while the kernel has dropped its lock.
	stale := `{"pid":999999999,"origin":"terminal","started":"2026-10-01T09:00:00Z"}`
	if err := os.WriteFile(lockPath(dir), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, held, _ := ReadLock(dir); held {
		t.Error("a dead run's description is not a holder")
	}
	l, err := AcquireLock(dir, Holder{Origin: OriginTerminal, Started: time.Now()})
	if err != nil {
		t.Fatalf("a stale lock must not block a run: %v", err)
	}
	l.Release()

	// `ajai status` probing the lock at that very moment must not make the new run
	// refuse in the dead run's name.
	if err := os.WriteFile(lockPath(dir), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	probe, err := filelock.TryLock(lockPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(30 * time.Millisecond); probe.Unlock() }()
	l, err = AcquireLock(dir, Holder{Origin: OriginTerminal, Started: time.Now()})
	if err != nil {
		t.Fatalf("a probe over a dead run's description must not refuse the run: %v", err)
	}
	l.Release()
}

func TestAStopRequestNeedsARunAndOnlyOneMadeDuringTheRunCounts(t *testing.T) {
	fastStopPoll(t)
	dir := t.TempDir()
	if _, err := RequestStop(dir, "x"); !errors.Is(err, ErrNoRun) {
		t.Errorf("no run, nothing to stop: %v", err)
	}
	if _, err := os.Stat(StopPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("no request may be left behind when there was no run")
	}
	// A request that arrived as the previous run ended stays on disk.
	if err := os.WriteFile(StopPath(dir), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if err := os.Chtimes(StopPath(dir), past, past); err != nil {
		t.Fatal(err)
	}
	l, err := AcquireLock(dir, Holder{Origin: OriginTerminal, Started: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	go l.WatchStops(ctx, cancel)
	time.Sleep(60 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("a request from before the run must not stop it")
	}
	if _, err := os.Stat(StopPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("the old request is discarded")
	}
	if _, err := RequestStop(dir, "stopped with ajai stop"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a request made during the run must stop it")
	}
	var sr *StopRequested
	if !errors.As(context.Cause(ctx), &sr) || sr.Reason != "stopped with ajai stop" {
		t.Errorf("%v", context.Cause(ctx))
	}
}

func TestAStopRequestEndsTheRunLikeCtrlCWithItsReason(t *testing.T) {
	fastStopPoll(t)
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	c := cfg()
	c.Settings.RunNonStop = true // a request ends the whole session, not just this run
	r, st, events := runner(t, site, c)
	rec := &recorder{}
	r.Notifier = rec
	dir := t.TempDir()
	l, err := AcquireLock(dir, Holder{Origin: OriginTerminal, Started: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	r.AfterFilters = func(ctx context.Context, term string) bool {
		if h, err := RequestStop(dir, "stopped with ajai stop"); err != nil || h.Origin != OriginTerminal {
			t.Errorf("%+v %v", h, err)
		}
		<-ctx.Done()
		return true
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	go l.WatchStops(ctx, cancel)
	sums, err := r.Session(ctx, Options{DryRun: true})
	if err != nil || len(sums) != 1 || sums[0].End != EndStopped || sums[0].Reason != "stopped with ajai stop" {
		t.Fatalf("%+v %v", sums, err)
	}
	if ends := kinds(*events, EventRunEnd); len(ends) != 1 || ends[0].Summary.Reason != "stopped with ajai stop" {
		t.Errorf("the summary must say why: %+v", ends)
	}
	if runs, _ := st.ListRuns(1); len(runs) != 1 || runs[0].Reason != "stopped with ajai stop" {
		t.Errorf("recorded: %+v", runs)
	}
	if len(rec.messages) != 1 || !strings.Contains(rec.messages[0], "stopped with ajai stop") {
		t.Errorf("the Telegram summary must say why: %q", rec.messages)
	}
	if _, err := os.Stat(StopPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("a request is consumed once honoured")
	}
}
