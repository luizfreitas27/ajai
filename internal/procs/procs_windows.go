//go:build windows

// Package procs answers "is this process alive?", for pid files and Chrome's profile lock.
package procs

import "os"

// Alive reports whether a process with this pid exists (best effort on Windows).
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.FindProcess(pid)
	return err == nil
}
