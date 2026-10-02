//go:build windows

package cli

import "os"

// processAlive reports whether a process with this pid exists (best effort on Windows).
func processAlive(pid int) bool {
	_, err := os.FindProcess(pid)
	return err == nil
}
