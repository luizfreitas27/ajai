package daemon

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/luizfreitas27/ajai/internal/run"
)

// fakeRuns is a Starter that takes the real run lock, as launch does, and runs
// sessions whose behaviour each test chooses.
type fakeRuns struct {
	dataDir string
	mu      sync.Mutex
	started []Request
	// behave runs the session: by default it blocks until stopped.
	behave   func(ctx context.Context, req Request) ([]run.Summary, error)
	startErr error
	opening  time.Duration // how long "Chrome" takes to open
	causes   []error       // the context cause each session ended with
}

func (f *fakeRuns) Start(ctx context.Context, req Request) (Opened, error) {
	if f.startErr != nil {
		return nil, f.startErr
	}
	lock, err := run.AcquireLock(f.dataDir, run.Holder{Origin: req.Origin, Window: req.Opening.WindowID, Started: time.Now()})
	if err != nil {
		return nil, err
	}
	time.Sleep(f.opening)
	f.mu.Lock()
	f.started = append(f.started, req)
	f.mu.Unlock()
	return &fakeSession{f: f, req: req, lock: lock}, nil
}

func (f *fakeRuns) requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.started...)
}

type fakeSession struct {
	f      *fakeRuns
	req    Request
	lock   *run.Lock
	closed bool
}

func (s *fakeSession) Session(ctx context.Context) ([]run.Summary, error) {
	defer func() {
		s.f.mu.Lock()
		s.f.causes = append(s.f.causes, context.Cause(ctx))
		s.f.mu.Unlock()
	}()
	if s.f.behave != nil {
		return s.f.behave(ctx, s.req)
	}
	<-ctx.Done()
	return []run.Summary{{RunID: 7}}, nil
}

func (s *fakeSession) Close() { s.closed = true; s.lock.Release() }

// stopReason is the reason a context cause carries, if it is a stop request.
func stopReason(err error) string {
	var sr *run.StopRequested
	if errors.As(err, &sr) {
		return sr.Reason
	}
	return ""
}
