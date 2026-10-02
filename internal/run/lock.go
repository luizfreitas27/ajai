package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luizfreitas27/aja/internal/filelock"
)

// Origin says who started a run.
type Origin string

// The origins of a run.
const (
	OriginTerminal Origin = "terminal"
	OriginSchedule Origin = "schedule"
	OriginTelegram Origin = "telegram"
)

// Holder is who holds the run lock.
type Holder struct {
	PID     int       `json:"pid"`
	Origin  Origin    `json:"origin"`
	Started time.Time `json:"started"`
}

func (h Holder) String() string {
	if h.PID == 0 {
		return "a run that is just starting"
	}
	return fmt.Sprintf("a run started from the %s (pid %d) at %s", h.Origin, h.PID, h.Started.Local().Format("15:04"))
}

// HeldError: another run holds the lock.
type HeldError struct{ Holder Holder }

func (e *HeldError) Error() string {
	return "another run is in progress: " + e.Holder.String() + "; stop it with `aja stop` or wait for it to end"
}

// Lock is the machine-wide run lock: one run, or non-stop session, at a time,
// whichever process starts it. The operating system releases it when its
// process dies, so it can never go stale.
type Lock struct{ f *filelock.File }

// File names inside the data directory.
const (
	LockFileName = "run.lock"
	StopFileName = "run.stop"
)

// LockPath is the run lock for a data directory.
func LockPath(dataDir string) string { return filepath.Join(dataDir, LockFileName) }

// StopPath is the stop-request file for a data directory.
func StopPath(dataDir string) string { return filepath.Join(dataDir, StopFileName) }

// AcquireLock takes the run lock for origin, or returns a *HeldError naming the
// run that has it. A stop request left over from an earlier run is discarded,
// so it cannot stop this one.
func AcquireLock(dataDir string, origin Origin, now time.Time) (*Lock, error) {
	path := LockPath(dataDir)
	for attempt := 0; ; attempt++ {
		f, err := filelock.TryLock(path)
		if err == nil {
			data, _ := json.Marshal(Holder{PID: os.Getpid(), Origin: origin, Started: now})
			if err := f.Describe(data); err != nil {
				f.Unlock()
				return nil, err
			}
			_ = os.Remove(StopPath(dataDir))
			return &Lock{f: f}, nil
		}
		if !errors.Is(err, filelock.ErrLocked) {
			return nil, err
		}
		h, held, _ := ReadLock(dataDir)
		// An empty description is a holder between taking the lock and describing
		// itself, or `aja status` probing the lock for an instant: look again.
		if (!held || h.PID == 0) && attempt < 5 {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		return nil, &HeldError{Holder: h}
	}
}

// Release frees the lock.
func (l *Lock) Release() { l.f.Unlock() }

// ReadLock reports whether a run holds the lock, and who.
func ReadLock(dataDir string) (Holder, bool, error) {
	path := LockPath(dataDir)
	held, err := filelock.Held(path)
	if err != nil || !held {
		return Holder{}, false, err
	}
	var h Holder
	if data, err := filelock.Read(path); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &h)
	}
	return h, true, nil
}

// ErrNoRun means there is no run in progress to stop.
var ErrNoRun = errors.New("no run in progress")

// RequestStop asks the run in progress, whichever process runs it, to end
// cleanly as Ctrl-C would; reason becomes the run's ending reason.
func RequestStop(dataDir, reason string) (Holder, error) {
	h, held, err := ReadLock(dataDir)
	if err != nil {
		return Holder{}, err
	}
	if !held {
		return Holder{}, ErrNoRun
	}
	path := StopPath(dataDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(reason), 0o600); err != nil {
		return h, err
	}
	return h, os.Rename(tmp, path)
}

// StopRequested is the cause of a run's context when a stop request ended it.
type StopRequested struct{ Reason string }

func (s *StopRequested) Error() string { return s.Reason }

// stopPoll is how often a run looks for a stop request.
var stopPoll = 2 * time.Second

// watchStop cancels the session's context, with a *StopRequested cause, as soon
// as a stop request appears; it returns when ctx ends.
func watchStop(ctx context.Context, path string, cancel context.CancelCauseFunc) {
	t := time.NewTicker(stopPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		_ = os.Remove(path)
		reason := strings.TrimSpace(string(data))
		if reason == "" {
			reason = "stopped by request"
		}
		cancel(&StopRequested{Reason: reason})
		return
	}
}
