package daemon

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luizfreitas27/aja/internal/run"
)

func newSup(t *testing.T) (*Supervisor, *fakeRuns, chan Ended) {
	s, f, ended, _ := newSupWithNotices(t)
	return s, f, ended
}

func newSupWithNotices(t *testing.T) (*Supervisor, *fakeRuns, chan Ended, *notices) {
	t.Helper()
	f := &fakeRuns{dataDir: t.TempDir()}
	n := &notices{}
	s := NewSupervisor(f.dataDir, f.Start, n, nil)
	ended := make(chan Ended, 10)
	s.OnEnd = func(e Ended) { ended <- e }
	return s, f, ended, n
}

func TestTheSupervisorRunsOneRunAtATimeAndStopsItWithAReason(t *testing.T) {
	s, f, ended := newSup(t)
	ctx := context.Background()
	if err := s.Start(ctx, Request{Origin: run.OriginTelegram, Market: "brasil", DryRun: true}); err != nil {
		t.Fatal(err)
	}
	info, ok := s.Current()
	if !ok || info.Request.Origin != run.OriginTelegram || info.Request.Market != "brasil" || info.Stopping != "" {
		t.Errorf("%+v %v", info, ok)
	}
	err := s.Start(ctx, Request{Origin: run.OriginSchedule})
	var held *run.HeldError
	if !errors.As(err, &held) || held.Holder.Origin != run.OriginTelegram || held.Holder.PID != os.Getpid() {
		t.Fatalf("one run at a time: %v", err)
	}
	if h, err := s.Stop("stopped from Telegram"); err != nil || h.Origin != run.OriginTelegram {
		t.Fatal(h, err)
	}
	s.Wait()
	e := <-ended
	if e.Err != nil || len(e.Summaries) != 1 || e.Info.Stopping != "stopped from Telegram" || stopReason(f.causes[0]) != "stopped from Telegram" {
		t.Errorf("%+v %v", e, f.causes)
	}
	if _, ok := s.Current(); ok {
		t.Error("idle again")
	}
	if _, held, _ := run.ReadLock(f.dataDir); held {
		t.Error("the run lock must be released")
	}
	if err := s.Start(ctx, Request{Origin: run.OriginSchedule}); err != nil {
		t.Errorf("a new run may start: %v", err)
	}
	_, _ = s.Stop("x")
	s.Wait()
}

func TestARunThatEndsEarlyFailsToStartOrPanicsLeavesTheSupervisorReady(t *testing.T) {
	s, f, ended := newSup(t)
	ctx := context.Background()

	f.behave = func(context.Context, Request) ([]run.Summary, error) { return []run.Summary{{RunID: 3}}, nil }
	if err := s.Start(ctx, Request{Origin: run.OriginSchedule}); err != nil {
		t.Fatal(err)
	}
	if e := <-ended; e.Err != nil || e.Summaries[0].RunID != 3 {
		t.Errorf("early end: %+v", e)
	}

	f.behave = func(context.Context, Request) ([]run.Summary, error) { panic("a nil card") }
	if err := s.Start(ctx, Request{Origin: run.OriginSchedule}); err != nil {
		t.Fatal(err)
	}
	var crash *run.Crash
	if e := <-ended; !errors.As(e.Err, &crash) || crash.Value != "a nil card" {
		t.Errorf("panic: %+v", e)
	}
	if _, held, _ := run.ReadLock(f.dataDir); held {
		t.Error("a crashed run must still release the lock")
	}

	f.behave, f.startErr = nil, errors.New("Chrome was not found")
	if err := s.Start(ctx, Request{Origin: run.OriginSchedule}); err == nil || err.Error() != "Chrome was not found" {
		t.Errorf("start failure: %v", err)
	}
	if _, ok := s.Current(); ok {
		t.Error("a run that could not start leaves nothing in progress")
	}
	f.startErr = nil
	if err := s.Start(ctx, Request{Origin: run.OriginSchedule}); err != nil {
		t.Errorf("still ready: %v", err)
	}
	_, _ = s.Stop("x")
	s.Wait()
}

func TestTheSupervisorDefersToARunOfAnotherProcess(t *testing.T) {
	s, f, _ := newSup(t)
	terminal, err := run.AcquireLock(f.dataDir, run.Holder{Origin: run.OriginTerminal, Started: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Release()
	var held *run.HeldError
	if err := s.Start(context.Background(), Request{Origin: run.OriginSchedule}); !errors.As(err, &held) || held.Holder.Origin != run.OriginTerminal {
		t.Errorf("%v", err)
	}
	// Stop reaches the terminal run through a stop request.
	if h, err := s.Stop("stopped from Telegram"); err != nil || h.Origin != run.OriginTerminal {
		t.Errorf("%+v %v", h, err)
	}
	if data, _ := os.ReadFile(run.StopPath(f.dataDir)); string(data) != "stopped from Telegram" {
		t.Errorf("%q", data)
	}
	terminal.Release()
	if _, err := s.Stop("x"); !errors.Is(err, run.ErrNoRun) {
		t.Errorf("nothing to stop: %v", err)
	}
}

func TestAStopAskedWhileChromeOpensStopsTheRunOnceItStarts(t *testing.T) {
	s, f, ended := newSup(t)
	f.opening = 100 * time.Millisecond
	go func() {
		time.Sleep(30 * time.Millisecond)
		_, _ = s.Stop(ReasonWindowClosed)
	}()
	if err := s.Start(context.Background(), Request{Origin: run.OriginSchedule}); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-ended:
		if e.Info.Stopping != ReasonWindowClosed {
			t.Errorf("%+v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the run was never stopped")
	}
}

func TestTheDaemonCtxEndsTheRunLikeCtrlC(t *testing.T) {
	s, f, ended := newSup(t)
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx, Request{Origin: run.OriginSchedule}); err != nil {
		t.Fatal(err)
	}
	cancel()
	s.Wait()
	if e := <-ended; e.Err != nil || !errors.Is(f.causes[0], context.Canceled) {
		t.Errorf("%+v %v", e, f.causes)
	}
}

func TestACrashIsReportedOnceAndLeavesNoPhantomRun(t *testing.T) {
	s, f, ended, notes := newSupWithNotices(t)
	ctx := context.Background()
	state := run.StatePath(f.dataDir)

	// A crash the runner did not see (the non-stop loop): the supervisor reports
	// it, and the live state it left behind (naming this very process) goes.
	f.behave = func(context.Context, Request) ([]run.Summary, error) {
		_ = run.WriteState(state, run.State{PID: os.Getpid()})
		panic("in the non-stop loop")
	}
	if err := s.Start(ctx, Request{Origin: run.OriginSchedule}); err != nil {
		t.Fatal(err)
	}
	<-ended
	if n := notes.all(); len(n) != 1 || !strings.Contains(n[0], "aja crashed: in the non-stop loop") {
		t.Errorf("%q", n)
	}
	if _, err := run.ReadState(state); !errors.Is(err, run.ErrNoState) {
		t.Error("aja status would show a run that is not there")
	}

	// A crash inside a run: the runner recorded and reported it (*run.Crash); not twice.
	f.behave = func(context.Context, Request) ([]run.Summary, error) { panic(&run.Crash{Value: "a nil card"}) }
	if err := s.Start(ctx, Request{Origin: run.OriginSchedule}); err != nil {
		t.Fatal(err)
	}
	<-ended
	if n := notes.all(); len(n) != 1 {
		t.Errorf("reported twice: %q", n)
	}
}

func TestAPanicWhileOpeningARunIsAnError(t *testing.T) {
	s, f, _ := newSup(t)
	s.start = func(context.Context, Request) (Opened, error) { panic("a nil config") }
	if err := s.Start(context.Background(), Request{Origin: run.OriginSchedule}); err == nil || !strings.Contains(err.Error(), "starting the run crashed: a nil config") {
		t.Errorf("%v", err)
	}
	if _, ok := s.Current(); ok {
		t.Error("nothing in progress")
	}
	s.start = f.Start
	if err := s.Start(context.Background(), Request{Origin: run.OriginSchedule}); err != nil {
		t.Errorf("ready again: %v", err)
	}
	_, _ = s.Stop("x")
	s.Wait()
}

func TestAnotherProcesssRunIsSeenBeforeTheStarterReadsAnything(t *testing.T) {
	s, f, _ := newSup(t)
	f.startErr = errors.New("fix the configuration first") // a broken config must not hide the skip
	terminal, err := run.AcquireLock(f.dataDir, run.Holder{Origin: run.OriginTerminal, Started: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Release()
	var held *run.HeldError
	if err := s.Start(context.Background(), Request{Origin: run.OriginSchedule}); !errors.As(err, &held) {
		t.Errorf("%v", err)
	}
}

func TestShutdownIsToldApartFromAStopRequest(t *testing.T) {
	s, _, ended := newSup(t)
	if err := s.Start(context.Background(), Request{Origin: run.OriginSchedule}); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Stop(ReasonWindowClosed)
	if e := <-ended; e.Shutdown {
		t.Error("a stop request is not the daemon stopping")
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx, Request{Origin: run.OriginSchedule}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if e := <-ended; !e.Shutdown {
		t.Error("the daemon stopping is a shutdown")
	}
	if _, ok := s.StopIf("x", func(Info) bool { return false }); ok {
		t.Error("StopIf with no run, or not the one meant, stops nothing")
	}
}
