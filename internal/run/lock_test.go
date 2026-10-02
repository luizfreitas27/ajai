package run

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestOneRunHoldsTheLockAndTheRefusalNamesIt(t *testing.T) {
	dir := t.TempDir()
	if _, held, err := ReadLock(dir); held || err != nil {
		t.Fatalf("nothing holds it yet: %v %v", held, err)
	}
	at := time.Date(2026, 10, 2, 9, 30, 0, 0, time.Local)
	a, err := AcquireLock(dir, OriginTerminal, at)
	if err != nil {
		t.Fatal(err)
	}
	h, held, err := ReadLock(dir)
	if err != nil || !held || h.Origin != OriginTerminal || h.PID != os.Getpid() || !h.Started.Equal(at) {
		t.Errorf("%+v %v %v", h, held, err)
	}
	_, err = AcquireLock(dir, OriginSchedule, time.Now())
	var he *HeldError
	if !errors.As(err, &he) || he.Holder.Origin != OriginTerminal {
		t.Fatalf("a second run must be refused, naming the first: %v", err)
	}
	if msg := err.Error(); msg != "another run is in progress: a run started from the terminal (pid "+itoa(os.Getpid())+") at 09:30; stop it with `aja stop` or wait for it to end" {
		t.Errorf("%q", msg)
	}
	a.Release()
	b, err := AcquireLock(dir, OriginTelegram, time.Now())
	if err != nil {
		t.Fatalf("free once released: %v", err)
	}
	b.Release()
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestAStopRequestNeedsARunAndALeftoverOneNeverStopsTheNext(t *testing.T) {
	dir := t.TempDir()
	if _, err := RequestStop(dir, "x"); !errors.Is(err, ErrNoRun) {
		t.Errorf("no run, nothing to stop: %v", err)
	}
	if _, err := os.Stat(StopPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("no request may be left behind when there was no run")
	}
	// A request that arrives as a run ends stays on disk; the next run must not see it.
	if err := os.WriteFile(StopPath(dir), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := AcquireLock(dir, OriginTerminal, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if _, err := os.Stat(StopPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("taking the lock must discard a leftover stop request")
	}
}

func TestAStopRequestEndsTheRunLikeCtrlCWithItsReason(t *testing.T) {
	saved := stopPoll
	stopPoll = 10 * time.Millisecond
	t.Cleanup(func() { stopPoll = saved })
	site := &fakeSite{jobs: map[string]fakeJob{"1": job("1", "Go Dev", "ACME", "Go")}, pages: [][]string{{"1"}}}
	c := cfg()
	c.Settings.RunNonStop = true // a request ends the whole session, not just this run
	r, st, events := runner(t, site, c)
	dir := t.TempDir()
	l, err := AcquireLock(dir, OriginTerminal, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	r.StopFile = StopPath(dir)
	r.AfterFilters = func(ctx context.Context, term string) bool {
		if h, err := RequestStop(dir, "stopped with aja stop"); err != nil || h.Origin != OriginTerminal {
			t.Errorf("%+v %v", h, err)
		}
		<-ctx.Done()
		return true
	}
	sums, err := r.Session(context.Background(), Options{DryRun: true})
	if err != nil || len(sums) != 1 || sums[0].End != EndStopped || sums[0].Reason != "stopped with aja stop" {
		t.Fatalf("%+v %v", sums, err)
	}
	if ends := kinds(*events, EventRunEnd); len(ends) != 1 || ends[0].Summary.Reason != "stopped with aja stop" {
		t.Errorf("the summary must say why: %+v", ends)
	}
	runs, _ := st.ListRuns(1)
	if len(runs) != 1 || runs[0].Reason != "stopped with aja stop" {
		t.Errorf("recorded: %+v", runs)
	}
	if _, err := os.Stat(StopPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("a request is consumed once honoured")
	}
}
