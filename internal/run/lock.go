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
	"github.com/luizfreitas27/aja/internal/procs"
)

// Origin says who started a run.
type Origin string

// The origins of a run.
const (
	OriginTerminal Origin = "terminal"
	OriginSchedule Origin = "schedule"
	OriginTelegram Origin = "telegram"
	OriginTUI      Origin = "tui" // a run the TUI started, in a process of its own
)

// Holder is who holds the run lock.
type Holder struct {
	PID     int       `json:"pid"`
	Origin  Origin    `json:"origin"`
	Window  int64     `json:"window,omitempty"` // the schedule window, for OriginSchedule
	Started time.Time `json:"started"`
}

// String describes the holder for a message: "a run started from the terminal (pid 42) at 09:30".
func (h Holder) String() string {
	if h.PID == 0 {
		return "a run that is just starting"
	}
	return fmt.Sprintf("a run started from %s (pid %d) at %s", h.From(), h.PID, h.Started.Local().Format("15:04"))
}

// ByDaemon reports whether the daemon runs this run: a schedule window or
// Telegram started it.
func (h Holder) ByDaemon() bool { return h.Origin == OriginSchedule || h.Origin == OriginTelegram }

// From says where the run was started: "the terminal", "schedule window 3", "Telegram".
func (h Holder) From() string {
	switch {
	case h.Window != 0:
		return fmt.Sprintf("schedule window %d", h.Window)
	case h.Origin == OriginTelegram:
		return "Telegram"
	case h.Origin == OriginTUI:
		return "the TUI"
	}
	return "the " + string(h.Origin)
}

// HeldError: another run holds the lock.
type HeldError struct{ Holder Holder }

// Error names the run in progress and how to stop it.
func (e *HeldError) Error() string {
	return "another run is in progress: " + e.Holder.String() + "; stop it with `aja stop` or wait for it to end"
}

// Lock is the machine-wide run lock: one run, or non-stop session, at a time,
// whichever process starts it. The operating system releases it when its
// process dies, so it can never go stale.
type Lock struct {
	f       *filelock.File
	dataDir string
	since   time.Time // when this lock was taken: older stop requests are not for us
}

// File names inside the data directory.
const (
	lockFileName = "run.lock"
	stopFileName = "run.stop"
)

func lockPath(dataDir string) string { return filepath.Join(dataDir, lockFileName) }

// StopPath is the stop-request file for a data directory.
func StopPath(dataDir string) string { return filepath.Join(dataDir, stopFileName) }

// lockSettle is how long AcquireLock waits for a lock held by nobody real (a
// starting run, a probe) to settle; both last microseconds.
const lockSettle = time.Second

// AcquireLock takes the run lock for holder (its PID is this process's), or
// returns a *HeldError naming the run that has it.
func AcquireLock(dataDir string, holder Holder) (*Lock, error) {
	holder.PID = os.Getpid()
	data, _ := json.Marshal(holder)
	f, desc, err := filelock.Acquire(lockPath(dataDir), data, lockSettle, func(desc []byte) bool {
		h := parseHolder(desc)
		return h.PID != 0 && procs.Alive(h.PID)
	})
	if errors.Is(err, filelock.ErrLocked) {
		return nil, &HeldError{Holder: parseHolder(desc)}
	}
	if err != nil {
		return nil, err
	}
	return &Lock{f: f, dataDir: dataDir, since: f.Locked}, nil
}

func parseHolder(desc []byte) Holder {
	var h Holder
	_ = json.Unmarshal(desc, &h)
	return h
}

// Release frees the lock.
func (l *Lock) Release() { l.f.Unlock() }

// ReadLock reports whether a run holds the lock, and who.
func ReadLock(dataDir string) (Holder, bool, error) {
	desc, held, err := filelock.ReadHeld(lockPath(dataDir))
	if err != nil || !held {
		return Holder{}, false, err
	}
	return parseHolder(desc), true, nil
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

// Error is the reason the request gave.
func (s *StopRequested) Error() string { return s.Reason }

// stopPoll is how often a run looks for a stop request.
var stopPoll = 2 * time.Second

// WatchStops cancels ctx, with a *StopRequested cause, as soon as a stop
// request made while this lock is held appears. A request left from before the
// lock was taken (one that arrived as the previous run was ending) is discarded.
// It returns when ctx ends.
func (l *Lock) WatchStops(ctx context.Context, cancel context.CancelCauseFunc) {
	path := StopPath(l.dataDir)
	t := time.NewTicker(stopPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		_ = os.Remove(path)
		if err != nil || info.ModTime().Before(l.since) {
			continue
		}
		reason := strings.TrimSpace(string(data))
		if reason == "" {
			reason = "stopped by request"
		}
		cancel(&StopRequested{Reason: reason})
		return
	}
}
