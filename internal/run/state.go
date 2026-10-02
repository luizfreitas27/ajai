package run

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/luizfreitas27/aja/internal/procs"
)

// State is the run in progress, written to a file in the data directory after
// every change so `aja status` can show live counters.
type State struct {
	PID        int       `json:"pid"`
	Started    time.Time `json:"started"`
	RunID      int64     `json:"run_id"`
	DryRun     bool      `json:"dry_run"`
	Market     string    `json:"market"`
	Applied    int       `json:"applied"`
	External   int       `json:"external"`
	Failed     int       `json:"failed"`
	Skipped    int       `json:"skipped"`
	WouldApply int       `json:"would_apply"`
	Term       string    `json:"term"`
}

// StateFileName is the file's name inside the data directory.
const StateFileName = "run.pid"

// StatePath is the state file for a data directory.
func StatePath(dataDir string) string { return filepath.Join(dataDir, StateFileName) }

// Alive reports whether the process that wrote the state still runs.
func (s State) Alive() bool { return procs.Alive(s.PID) }

// WriteState saves the state atomically.
func WriteState(path string, s State) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ErrNoState means no run has written a state file.
var ErrNoState = errors.New("no run state")

// ReadState loads the state file; ErrNoState when absent, another error when corrupt.
func ReadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, ErrNoState
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, errors.New("the run state file is unreadable: " + err.Error())
	}
	return s, nil
}

// RemoveState deletes the state file.
func RemoveState(path string) { _ = os.Remove(path) }
