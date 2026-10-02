// Package filelock holds an operating-system lock on a file (the run lock). The
// kernel drops the lock when its process dies, so a crash
// never leaves a stale lock behind, and two processes can never both hold it.
package filelock

import (
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

// ErrLocked means another process (or another open of the file) holds the lock.
var ErrLocked = errors.New("locked by another process")

// File is a held lock. The file's content is free for the holder to describe
// itself; others can read it while the lock is held.
type File struct {
	f *os.File
	// Locked is when the lock was taken.
	Locked time.Time
}

// TryLock opens (creating if needed) and locks path without waiting:
// ErrLocked when someone else holds it.
func TryLock(path string) (*File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := tryLock(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &File{f: f, Locked: time.Now()}, nil
}

// Acquire takes the lock at path and describes its holder with desc. When the
// lock is taken, real says whether its description names a holder that exists
// (a live process): one that does not is a holder between taking the lock and
// describing itself, a probe (Held), or a crashed holder's leftover words under
// a probe, all over in microseconds, so Acquire looks again for up to settle.
// Otherwise it returns ErrLocked with the holder's description.
func Acquire(path string, desc []byte, settle time.Duration, real func(desc []byte) bool) (*File, []byte, error) {
	deadline := time.Now().Add(settle)
	for {
		l, err := TryLock(path)
		if err == nil {
			if err := l.Describe(desc); err != nil {
				l.Unlock()
				return nil, nil, err
			}
			return l, nil, nil
		}
		if !errors.Is(err, ErrLocked) {
			return nil, nil, err
		}
		holder, held, _ := ReadHeld(path)
		if (!held || !real(holder)) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		return nil, holder, ErrLocked
	}
}

// Held with no description may be another reader's own check (Held takes the
// lock for an instant) or a holder that has not described itself yet; both
// last microseconds, so ReadHeld looks again a few times before believing it.
const (
	undescribedChecks = 5
	undescribedPause  = 10 * time.Millisecond
)

// ReadHeld reports whether someone holds the lock on path, with the holder's
// description.
func ReadHeld(path string) ([]byte, bool, error) {
	for i := 1; ; i++ {
		held, err := Held(path)
		if err != nil || !held {
			return nil, false, err
		}
		desc, err := Read(path)
		if err != nil || len(desc) > 0 || i == undescribedChecks {
			return desc, true, err
		}
		time.Sleep(undescribedPause)
	}
}

// Describe replaces the file's content with data.
func (l *File) Describe(data []byte) error {
	if err := l.f.Truncate(0); err != nil {
		return err
	}
	_, err := l.f.WriteAt(data, 0)
	return err
}

// Unlock releases the lock and empties the file, so a later reader never takes
// a past holder for a present one.
func (l *File) Unlock() {
	_ = l.f.Truncate(0)
	_ = unlock(l.f)
	_ = l.f.Close()
}

// Read returns the file's content (the holder's description), or nil when the
// file does not exist.
func Read(path string) ([]byte, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// Held reports whether someone holds the lock on path, by trying to take it.
func Held(path string) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	l, err := TryLock(path)
	if errors.Is(err, ErrLocked) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	_ = unlock(l.f) // not Unlock: the file is not ours to empty
	_ = l.f.Close()
	return false, nil
}

// tryLock is flock(2): aja targets Linux only for now.
func tryLock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrLocked
	}
	return err
}

func unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
