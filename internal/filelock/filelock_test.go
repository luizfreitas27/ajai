package filelock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOnlyOneHolderAtATimeAndTheDescriptionIsReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	if held, err := Held(path); held || err != nil {
		t.Fatalf("nothing holds a missing file: %v %v", held, err)
	}
	a, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Describe([]byte(`{"pid":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("a second open must not get the lock: %v", err)
	}
	if held, _ := Held(path); !held {
		t.Error("Held must see the holder")
	}
	if data, err := Read(path); err != nil || string(data) != `{"pid":1}` {
		t.Errorf("the description must be readable while held: %q %v", data, err)
	}
	a.Unlock()
	if held, _ := Held(path); held {
		t.Error("released")
	}
	if data, _ := Read(path); len(data) != 0 {
		t.Errorf("an unlocked file describes nobody: %q", data)
	}
	b, err := TryLock(path)
	if err != nil {
		t.Fatalf("free again: %v", err)
	}
	b.Unlock()
}

// TestTheLockDiesWithItsProcess: a helper process takes the lock and is killed;
// the lock is free at once, with no stale-lock clean-up.
func TestTheLockDiesWithItsProcess(t *testing.T) {
	if os.Getenv("FILELOCK_HELPER") != "" {
		l, err := TryLock(os.Getenv("FILELOCK_HELPER"))
		if err != nil {
			os.Exit(2)
		}
		_ = l.Describe([]byte("helper"))
		time.Sleep(time.Minute)
		return
	}
	path := filepath.Join(t.TempDir(), "x.lock")
	cmd := exec.Command(os.Args[0], "-test.run=TestTheLockDiesWithItsProcess")
	cmd.Env = append(os.Environ(), "FILELOCK_HELPER="+path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if data, _ := Read(path); strings.Contains(string(data), "helper") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper never took the lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("held by the helper: %v", err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	l, err := TryLock(path)
	if err != nil {
		t.Fatalf("the lock must be free once its process is gone: %v", err)
	}
	l.Unlock()
}

// TestAnotherReadersCheckIsNotAHolder: Held takes the lock for an instant, so a
// reader can find it taken with no description. ReadHeld looks again rather
// than report a holder nobody is; a holder that stays undescribed is believed.
func TestAnotherReadersCheckIsNotAHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	check, err := TryLock(path) // another reader's check, caught in the act
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(15 * time.Millisecond)
		_ = unlock(check.f)
		_ = check.f.Close()
	}()
	if desc, held, err := ReadHeld(path); held || err != nil {
		t.Errorf("a passing check is not a holder: %q %v %v", desc, held, err)
	}

	quiet, err := TryLock(path) // a holder that never describes itself
	if err != nil {
		t.Fatal(err)
	}
	defer quiet.Unlock()
	start := time.Now()
	if _, held, _ := ReadHeld(path); !held {
		t.Error("a holder that stays undescribed is still a holder")
	}
	if waited := time.Since(start); waited > 200*time.Millisecond {
		t.Errorf("looked again for %v", waited)
	}
}
