package run

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/luizfreitas27/ajai/internal/store"
)

// deadPID is the pid of a process that has ended.
func deadPID(t *testing.T) int {
	t.Helper()
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Skip("no true(1):", err)
	}
	return gone.Process.Pid
}

func TestARunWhoseProcessDiedIsEndedOnceWithItsLastCounts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "ajai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	id, _ := st.StartRun(ctx, "brasil", true)
	dead := State{PID: deadPID(t), RunID: id, Market: "brasil", DryRun: true, Failed: 1, Skipped: 4}
	settle := func(held bool) bool {
		t.Helper()
		if err := WriteState(StatePath(dir), dead); err != nil {
			t.Fatal(err)
		}
		act, _ := ReadActivity(dir)
		act.Held = held
		ended, err := SettleStale(ctx, st, act, StatePath(dir))
		if err != nil {
			t.Fatal(err)
		}
		return ended
	}
	if !settle(false) {
		t.Fatal("the dead run was not ended")
	}
	r, _ := st.GetRun(id)
	if r.EndedAt == "" || r.Reason != StaleReason || r.Failed != 1 || r.Skipped != 4 {
		t.Errorf("%+v", r)
	}
	if _, err := ReadState(StatePath(dir)); !errors.Is(err, ErrNoState) {
		t.Errorf("the stale state file stays: %v", err)
	}
	if settle(false) {
		t.Error("ended twice")
	}
	if settle(true); !fileHasState(dir) {
		t.Error("the state file went while a run holds the lock: that run writes its own")
	}

	done, _ := st.StartRun(ctx, "brasil", false) // a run that ended properly
	_ = st.FinishRun(ctx, store.Run{ID: done, Applied: 2, Reason: "all search terms visited"})
	dead.RunID = done
	if settle(false) {
		t.Error("a run that ended properly was ended again")
	}
	if r, _ := st.GetRun(done); r.Reason != "all search terms visited" || r.Applied != 2 {
		t.Errorf("%+v", r)
	}
}

func TestARunStillAliveIsNeverEnded(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "ajai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	id, _ := st.StartRun(ctx, "brasil", true)
	if err := WriteState(StatePath(dir), State{PID: os.Getpid(), RunID: id}); err != nil { // this process: alive
		t.Fatal(err)
	}
	act, _ := ReadActivity(dir)
	if ended, err := SettleStale(ctx, st, act, StatePath(dir)); ended || err != nil {
		t.Errorf("a live run was ended: %v %v", ended, err)
	}
	if r, _ := st.GetRun(id); r.EndedAt != "" || !fileHasState(dir) {
		t.Errorf("a live run was touched: %+v", r)
	}
}

func fileHasState(dir string) bool {
	_, err := ReadState(StatePath(dir))
	return err == nil
}
