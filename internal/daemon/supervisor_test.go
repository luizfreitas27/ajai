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
	t.Helper()
	f := &fakeRuns{dataDir: t.TempDir()}
	s := NewSupervisor(f.dataDir, f.Start, nil)
	ended := make(chan Ended, 10)
	s.OnEnd = func(e Ended) { ended <- e }
	return s, f, ended
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
	if e := <-ended; e.Err == nil || !strings.Contains(e.Err.Error(), "the run crashed: a nil card") {
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
	terminal, err := run.AcquireLock(f.dataDir, run.OriginTerminal, time.Now())
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
