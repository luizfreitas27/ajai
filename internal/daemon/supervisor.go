// Package daemon is what `aja daemon` runs: a Supervisor that owns the run in
// progress (started as a goroutine, never a child process), and a Scheduler
// that starts and stops runs on the schedule windows. Every run it starts is
// unattended and goes through internal/launch, so it is exactly a terminal run
// with nobody at the terminal.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/luizfreitas27/aja/internal/logging"
	"github.com/luizfreitas27/aja/internal/run"
	"github.com/luizfreitas27/aja/internal/schedule"
)

// Request is what a run is started for.
type Request struct {
	Origin run.Origin
	Market string // "" runs markets.active
	DryRun bool
	// Opening is the schedule window opening that started the run (OriginSchedule).
	Opening schedule.Key
}

// Opened is a run ready to go, as launch.Opened: Session runs it, Close
// releases Chrome and the run lock.
type Opened interface {
	Session(ctx context.Context) ([]run.Summary, error)
	Close()
}

// Starter opens a run for a request: the configuration read afresh, the run
// lock, Chrome. It fails with a *run.HeldError when another run has the lock.
type Starter func(ctx context.Context, req Request) (Opened, error)

// Info is the supervisor's run in progress.
type Info struct {
	Request Request
	Started time.Time
	// Stopping is the reason a stop was asked for; "" while the run goes on.
	Stopping string
}

// Holder describes the run as the run lock does.
func (i Info) Holder() run.Holder {
	return run.Holder{PID: os.Getpid(), Origin: i.Request.Origin, Window: i.Request.Opening.WindowID, Started: i.Started}
}

// Ended is a run of the supervisor that has finished.
type Ended struct {
	Info      Info
	Summaries []run.Summary
	Err       error
	// Shutdown: the daemon was stopping, and the run ended because of it (not on
	// its own, nor by a stop request).
	Shutdown bool
}

// Supervisor owns the run in progress inside the daemon: one at a time.
type Supervisor struct {
	start    Starter
	dataDir  string
	notifier run.Notifier // for a crash the runner could not report
	logger   *slog.Logger
	// OnEnd, when set, is called once each run has ended, before Wait returns.
	OnEnd func(Ended)

	mu      sync.Mutex
	current *Info // nil when idle; set while starting too
	cancel  context.CancelCauseFunc
	done    chan struct{} // closed when the current run has ended and OnEnd returned
}

// NewSupervisor makes a supervisor for the data directory's run lock.
func NewSupervisor(dataDir string, start Starter, notifier run.Notifier, logger *slog.Logger) *Supervisor {
	if logger == nil {
		logger = logging.Discard
	}
	return &Supervisor{start: start, dataDir: dataDir, notifier: notifier, logger: logger}
}

// Start opens a run and starts it in the background. It returns once the run
// has started (lock held, Chrome open) or with why it could not: a
// *run.HeldError when a run is in progress here or in another process. The run
// ends with ctx (the daemon's), a stop request, or on its own.
func (s *Supervisor) Start(ctx context.Context, req Request) error {
	s.mu.Lock()
	if s.current != nil {
		held := &run.HeldError{Holder: s.current.Holder()}
		s.mu.Unlock()
		return held
	}
	// Another process's run first, before reading any configuration: a window
	// that opens during a terminal run is skipped, whatever state the config is in.
	if holder, held, _ := run.ReadLock(s.dataDir); held {
		s.mu.Unlock()
		return &run.HeldError{Holder: holder}
	}
	info := &Info{Request: req, Started: time.Now()}
	done := make(chan struct{})
	s.current, s.done = info, done
	s.mu.Unlock()

	runCtx, cancel := context.WithCancelCause(ctx)
	opened, err := s.open(runCtx, req)
	if err == nil && ctx.Err() != nil {
		opened.Close() // the daemon stopped while Chrome was opening
		err = ctx.Err()
	}
	if err != nil {
		cancel(nil)
		s.mu.Lock()
		s.current, s.done = nil, nil
		s.mu.Unlock()
		close(done)
		return err
	}
	s.mu.Lock()
	s.cancel = cancel
	stopping := info.Stopping // a stop asked while Chrome was opening
	s.mu.Unlock()
	if stopping != "" {
		cancel(&run.StopRequested{Reason: stopping})
	}
	s.logger.Info("run started", "origin", req.Origin, "market", req.Market, "dry_run", req.DryRun, "window", req.Opening.WindowID)
	go s.run(runCtx, ctx, cancel, opened, info, done)
	return nil
}

// open calls the starter; a panic there is an error, not the end of the daemon.
func (s *Supervisor) open(ctx context.Context, req Request) (opened Opened, err error) {
	defer func() {
		if p := recover(); p != nil {
			s.logger.Error("starting a run crashed", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			opened, err = nil, fmt.Errorf("starting the run crashed: %v", p)
		}
	}()
	return s.start(ctx, req)
}

func (s *Supervisor) run(ctx, daemonCtx context.Context, cancel context.CancelCauseFunc, opened Opened, info *Info, done chan struct{}) {
	ended := Ended{}
	func() {
		defer func() {
			if p := recover(); p != nil {
				var crash *run.Crash
				if c, ok := p.(*run.Crash); ok {
					crash = c // the runner recorded and reported it
				} else {
					crash = &run.Crash{Value: p}
					s.notify("aja's run crashed.\n\n" + crash.Error())
				}
				ended.Err = crash
				s.logger.Error("run crashed", "panic", fmt.Sprint(crash.Value), "stack", string(debug.Stack()))
				// A run that crashed outside a recorded run (the non-stop loop) may
				// leave the live state behind, naming this very process.
				run.RemoveState(run.StatePath(s.dataDir))
			}
		}()
		ended.Summaries, ended.Err = opened.Session(ctx)
	}()
	var stopped *run.StopRequested
	ended.Shutdown = daemonCtx.Err() != nil && !errors.As(context.Cause(ctx), &stopped)
	opened.Close()
	cancel(nil)
	s.mu.Lock()
	ended.Info = *info
	s.current, s.cancel, s.done = nil, nil, nil
	onEnd := s.OnEnd
	s.mu.Unlock()
	s.logger.Info("run ended", "origin", info.Request.Origin, "error", errText(ended.Err), "shutdown", ended.Shutdown)
	if onEnd != nil {
		onEnd(ended)
	}
	close(done) // last: Wait returns only once everything about this run is recorded
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Supervisor) notify(text string) {
	if s.notifier != nil {
		s.notifier.Send(context.Background(), text)
	}
}

// Stop asks the run in progress to end cleanly with reason: the supervisor's
// own run directly, a run of another process (a terminal) through a stop
// request. It returns who was asked, or run.ErrNoRun.
func (s *Supervisor) Stop(reason string) (run.Holder, error) {
	if holder, ok := s.StopIf(reason, func(Info) bool { return true }); ok {
		return holder, nil
	}
	return run.RequestStop(s.dataDir, reason)
}

// StopIf stops the supervisor's own run with reason when match says it is the
// one meant, deciding under the same lock as runs start and end: a run that
// ended a moment ago is never mistaken for the next one. It never touches
// another process's run.
func (s *Supervisor) StopIf(reason string, match func(Info) bool) (run.Holder, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil || !match(*s.current) {
		return run.Holder{}, false
	}
	if s.current.Stopping == "" {
		s.current.Stopping = reason
		if s.cancel != nil {
			s.cancel(&run.StopRequested{Reason: reason})
		}
		s.logger.Info("stop requested", "reason", reason, "origin", s.current.Request.Origin)
	}
	return s.current.Holder(), true
}

// Current returns the supervisor's run in progress (or being started).
func (s *Supervisor) Current() (Info, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return Info{}, false
	}
	return *s.current, true
}

// Wait returns once the supervisor's run in progress, if any, has ended.
func (s *Supervisor) Wait() {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}
