package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/luizfreitas27/aja/internal/filelock"
	"github.com/luizfreitas27/aja/internal/procs"
)

const lockFileName = "daemon.lock"

// lockSettle: see filelock.Acquire; a probe or a starting daemon lasts microseconds.
const lockSettle = time.Second

func lockPath(dataDir string) string { return filepath.Join(dataDir, lockFileName) }

// Process is the daemon that holds the daemon lock.
type Process struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// String is "pid 42, since Mon 05 Oct 09:00".
func (p Process) String() string {
	return fmt.Sprintf("pid %d, since %s", p.PID, p.Started.Local().Format("Mon 02 Jan 15:04"))
}

// RunningError: a daemon is already running.
type RunningError struct{ Daemon Process }

// Error names the daemon that runs.
func (e *RunningError) Error() string {
	return "aja daemon is already running (" + e.Daemon.String() + ")"
}

// Lock is held by the one daemon of a data directory.
type Lock struct{ f *filelock.File }

// AcquireLock makes this process the daemon, or returns a *RunningError.
func AcquireLock(dataDir string, now time.Time) (*Lock, error) {
	data, _ := json.Marshal(Process{PID: os.Getpid(), Started: now})
	f, desc, err := filelock.Acquire(lockPath(dataDir), data, lockSettle, func(desc []byte) bool {
		p := parseProcess(desc)
		return p.PID != 0 && procs.Alive(p.PID)
	})
	if errors.Is(err, filelock.ErrLocked) {
		return nil, &RunningError{Daemon: parseProcess(desc)}
	}
	if err != nil {
		return nil, err
	}
	return &Lock{f: f}, nil
}

func parseProcess(desc []byte) Process {
	var p Process
	_ = json.Unmarshal(desc, &p)
	return p
}

// Release gives the lock up when the daemon exits.
func (l *Lock) Release() { l.f.Unlock() }

// ReadLock reports whether a daemon runs, and which.
func ReadLock(dataDir string) (Process, bool, error) {
	desc, held, err := filelock.ReadHeld(lockPath(dataDir))
	if err != nil || !held {
		return Process{}, false, err
	}
	return parseProcess(desc), true, nil
}
