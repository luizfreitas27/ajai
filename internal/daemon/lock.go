package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/luizfreitas27/aja/internal/filelock"
)

const lockFileName = "daemon.lock"

func lockPath(dataDir string) string { return filepath.Join(dataDir, lockFileName) }

// Running describes the daemon that holds the daemon lock.
type Running struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// ErrRunning: a daemon is already running.
type ErrRunning struct{ Running Running }

// Error names the daemon that runs.
func (e *ErrRunning) Error() string {
	return fmt.Sprintf("aja daemon is already running (pid %d, since %s)", e.Running.PID, e.Running.Started.Local().Format("Mon 02 Jan 15:04"))
}

// Lock is held by the one daemon of a data directory.
type Lock struct{ f *filelock.File }

// AcquireLock makes this process the daemon, or returns *ErrRunning.
func AcquireLock(dataDir string, now time.Time) (*Lock, error) {
	f, err := filelock.TryLock(lockPath(dataDir))
	if errors.Is(err, filelock.ErrLocked) {
		r, _, _ := ReadLock(dataDir)
		return nil, &ErrRunning{Running: r}
	}
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(Running{PID: os.Getpid(), Started: now})
	if err := f.Describe(data); err != nil {
		f.Unlock()
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Release gives the lock up when the daemon exits.
func (l *Lock) Release() { l.f.Unlock() }

// ReadLock reports whether a daemon runs, and which.
func ReadLock(dataDir string) (Running, bool, error) {
	held, err := filelock.Held(lockPath(dataDir))
	if err != nil || !held {
		return Running{}, false, err
	}
	var r Running
	if data, err := filelock.Read(lockPath(dataDir)); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &r)
	}
	return r, true, nil
}
