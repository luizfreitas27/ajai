// Package filelock holds an operating-system lock on a file (the run lock). The
// kernel drops the lock when its process dies, so a crash
// never leaves a stale lock behind, and two processes can never both hold it.
package filelock

import (
	"errors"
	"io"
	"os"
	"syscall"
)

// ErrLocked means another process (or another open of the file) holds the lock.
var ErrLocked = errors.New("locked by another process")

// File is a held lock. The file's content is free for the holder to describe
// itself; others can read it while the lock is held.
type File struct {
	f *os.File
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
	return &File{f: f}, nil
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
