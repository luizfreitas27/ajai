package run

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestReadActivityTellsWhatTheRunIsDoing(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 5, 9, 30, 0, 0, time.Local)
	expect := func(want ActivityKind, from string) Activity {
		t.Helper()
		a, err := ReadActivity(dir)
		if err != nil || a.Kind != want || a.From() != from {
			t.Fatalf("got %+v (from %q) %v, want kind %d from %q", a, a.From(), err, want, from)
		}
		return a
	}
	expect(Idle, "")

	lock, err := AcquireLock(dir, Holder{PID: os.Getpid(), Origin: OriginSchedule, Window: 2, Started: now})
	if err != nil {
		t.Fatal(err)
	}
	if a := expect(Starting, "schedule window 2"); !a.Holder.ByDaemon() {
		t.Error("a schedule window's run is the daemon's")
	}
	state := State{PID: os.Getpid(), Started: now, Market: "brasil", Applied: 1}
	if err := WriteState(StatePath(dir), state); err != nil {
		t.Fatal(err)
	}
	if a := expect(Running, "schedule window 2"); a.State.Applied != 1 {
		t.Errorf("%+v", a.State)
	}
	state.NextRun = now.Add(time.Hour)
	_ = WriteState(StatePath(dir), state)
	expect(Waiting, "schedule window 2")
	lock.Release()

	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Skip("no true(1):", err)
	}
	state.PID, state.NextRun = gone.Process.Pid, time.Time{}
	_ = WriteState(StatePath(dir), state)
	expect(Stale, "")

	if from := (Activity{Kind: Starting, Held: true}).From(); from != "" {
		t.Errorf("a holder that has not described itself started from nowhere yet: %q", from)
	}
	if (Holder{Origin: OriginTerminal}).ByDaemon() || !(Holder{Origin: OriginTelegram}).ByDaemon() {
		t.Error("ByDaemon: Telegram's and the schedule's runs, not the terminal's")
	}
}
