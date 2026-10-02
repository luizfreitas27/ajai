package run

import "errors"

// ActivityKind is what the machine's run is doing.
type ActivityKind int

const (
	// Idle: no run in progress.
	Idle ActivityKind = iota
	// Starting: the run lock is held and no state is written yet.
	Starting
	// Running: a run is applying.
	Running
	// Waiting: a non-stop session waits between two runs.
	Waiting
	// Stale: the state file names a process that is gone; the last run did not
	// exit cleanly.
	Stale
)

// Activity is the run lock and the state file read together, the one
// description `aja status`, Telegram's /status and the TUI give of the run in
// progress.
type Activity struct {
	Kind   ActivityKind
	Held   bool   // the run lock is held
	Holder Holder // who holds it; PID 0 until it has described itself
	State  State  // Running, Waiting and Stale only
}

// ReadActivity reads the run lock and the state file in dataDir. On an error
// the activity is still what could be read.
func ReadActivity(dataDir string) (Activity, error) {
	holder, held, lockErr := ReadLock(dataDir)
	a := Activity{Kind: Idle, Held: held, Holder: holder}
	if held {
		a.Kind = Starting
	}
	state, err := ReadState(StatePath(dataDir))
	switch {
	case errors.Is(err, ErrNoState):
		err = nil
	case err != nil:
	case !state.Alive():
		a.Kind, a.State = Stale, state
	case !state.NextRun.IsZero():
		a.Kind, a.State = Waiting, state
	default:
		a.Kind, a.State = Running, state
	}
	return a, errors.Join(lockErr, err)
}

// From says who started the run in progress ("the terminal", "schedule window
// 3", "Telegram"), or "" while the lock does not say.
func (a Activity) From() string {
	if !a.Held || a.Holder.PID == 0 {
		return ""
	}
	return a.Holder.From()
}
