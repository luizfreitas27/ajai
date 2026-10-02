//go:build !windows

// Package procs answers "is this process alive?", for pid files and Chrome's profile lock.
package procs

import (
	"os"
	"syscall"
)

// Alive reports whether a process with this pid exists.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
