// Package daemon is what `aja daemon` runs: a Supervisor that owns the run in
// progress (started as a goroutine, never a child process), and a Scheduler
// that starts and stops runs on the schedule windows. Every run it starts is
// unattended and goes through internal/launch, so it is exactly a terminal run
// with nobody at the terminal.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

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

// Session is an opened run: launch.Opened.
type Session interface {
	Session(ctx context.Context) ([]run.Summary, error)
	Close()
}

// Starter opens a run for a request: the configuration read afresh, the run
// lock, Chrome. It fails with a *run.HeldError when another run has the lock.
type Starter func(ctx context.Context, req Request) (Session, error)

// Info is the supervisor's run in progress.
type Info struct {
	Request Request
	Started time.Time
	// Stopping is the reason a stop was asked for; "" while the run goes on.
	Stopping string
}

// Holder describes the run as the run lock would.
func (i Info) Holder() run.Holder {
	return run.Holder{PID: os.Getpid(), Origin: i.Request.Origin, Started: i.Started}
}

// Ended is a run of the supervisor that has finished.
type Ended struct {
	Info      Info
	Summaries []run.Summary
	Err       error
}

// Supervisor owns the run in progress inside the daemon: one at a time.
type Supervisor struct {
	start   Starter
	dataDir string
	logger  *slog.Logger
	now     func() time.Time
	// OnEnd, when set, is called once each run has ended and the supervisor is idle again.
	OnEnd func(Ended)

	mu      sync.Mutex
	current *Info // nil when idle; set while starting too
	cancel  context.CancelCauseFunc
	done    chan struct{} // closed when the current run has ended
}

// NewSupervisor makes a supervisor for the data directory's run lock.
func NewSupervisor(dataDir string, start Starter, logger *slog.Logger) *Supervisor {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Supervisor{start: start, dataDir: dataDir, logger: logger, now: time.Now}
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
	info := &Info{Request: req, Started: s.now()}
	done := make(chan struct{})
	s.current, s.done = info, done
	s.mu.Unlock()

	runCtx, cancel := context.WithCancelCause(ctx)
	sess, err := s.start(runCtx, req)
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
	go s.run(runCtx, cancel, sess, info, done)
	return nil
}

func (s *Supervisor) run(ctx context.Context, cancel context.CancelCauseFunc, sess Session, info *Info, done chan struct{}) {
	ended := Ended{}
	func() {
		defer func() {
			if p := recover(); p != nil {
				// The runner has already reported a panic inside a run; this keeps the
				// daemon, the scheduler and Telegram alive for the next one.
				ended.Err = fmt.Errorf("the run crashed: %v", p)
				s.logger.Error("run crashed", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			}
		}()
		ended.Summaries, ended.Err = sess.Session(ctx)
	}()
	sess.Close()
	cancel(nil)
	s.mu.Lock()
	ended.Info = *info
	s.current, s.cancel, s.done = nil, nil, nil
	onEnd := s.OnEnd
	s.mu.Unlock()
	close(done)
	s.logger.Info("run ended", "origin", info.Request.Origin, "error", errText(ended.Err))
	if onEnd != nil {
		onEnd(ended)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Stop asks the run in progress to end cleanly with reason: the supervisor's
// own run directly, a run of another process (a terminal) through a stop
// request. It returns who was asked, or run.ErrNoRun.
func (s *Supervisor) Stop(reason string) (run.Holder, error) {
	s.mu.Lock()
	if s.current != nil {
		holder := s.current.Holder()
		if s.current.Stopping == "" {
			s.current.Stopping = reason
			if s.cancel != nil {
				s.cancel(&run.StopRequested{Reason: reason})
			}
		}
		s.mu.Unlock()
		s.logger.Info("stop requested", "reason", reason, "origin", holder.Origin)
		return holder, nil
	}
	s.mu.Unlock()
	return run.RequestStop(s.dataDir, reason)
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
