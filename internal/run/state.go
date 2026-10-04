package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/luizfreitas27/ajai/internal/procs"
	"github.com/luizfreitas27/ajai/internal/store"
)

// State is the run in progress, written to a file in the data directory after
// every change so `ajai status` can show live counters.
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
	// NextRun is set while a non-stop session waits between runs.
	NextRun time.Time `json:"next_run,omitempty"`
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

// StaleReason is what a run its process never ended (killed, crashed) is ended with.
const StaleReason = "did not exit cleanly: its process ended without closing the run"

// EndStale ends, in the store, the run a dead process left in progress (s, as
// its state file last had it), with the counts it last saved, once: a run
// already ended stays as it is. Once the run is ended its state file goes too,
// unless keepFile (a run holding the lock writes its own). It reports whether
// it ended the run.
func EndStale(ctx context.Context, st *store.Store, statePath string, s State, keepFile bool) (bool, error) {
	ended, err := st.EndAbandonedRun(ctx, s.row(StaleReason))
	if err != nil {
		return false, err // the state file keeps the counts for the next try
	}
	if again, rerr := ReadState(statePath); !keepFile && rerr == nil && again.PID == s.PID && !again.Alive() {
		RemoveState(statePath)
	}
	return ended, nil
}

// EndIfStale is EndStale for the run the activity found stale, if it did.
func (a Activity) EndIfStale(ctx context.Context, st *store.Store, dataDir string) (bool, error) {
	if a.Kind != Stale {
		return false, nil
	}
	return EndStale(ctx, st, StatePath(dataDir), a.State, a.Held)
}

// row is the run the state describes, as far as the state knows it.
func (s State) row(reason string) store.Run {
	return store.Run{ID: s.RunID, Applied: s.Applied, External: s.External, Failed: s.Failed, Skipped: s.Skipped, Reason: reason}
}
