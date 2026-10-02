// Package chrome finds the Chrome or Chromium installed on this machine and the
// default profile directory, for doctor and for the browser layer.
package chrome

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrNotFound means no Chrome executable was located.
var ErrNotFound = errors.New("Chrome was not found; install Google Chrome or set settings.chrome_binary")

// Candidates are the names and paths searched, per OS, in order.
func Candidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"google-chrome", "chromium",
		}
	case "windows":
		var out []string
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
			if base != "" {
				out = append(out, filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
		return append(out, "chrome.exe", "chromium.exe")
	default:
		return []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser", "chrome",
			"/usr/bin/google-chrome-stable", "/usr/bin/google-chrome", "/usr/bin/chromium", "/snap/bin/chromium",
			"/opt/google/chrome/chrome"}
	}
}

// Find returns the executable to use: the explicit path when given (it must
// exist), else the first candidate found.
func Find(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", errors.New("settings.chrome_binary does not exist: " + explicit)
		}
		return explicit, nil
	}
	for _, c := range Candidates() {
		if filepath.IsAbs(c) {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", ErrNotFound
}

// Version runs `<binary> --version` and returns its trimmed output, or "".
func Version(binary string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// DefaultProfileDir is where this OS keeps the user's Chrome user-data directory.
func DefaultProfileDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	case "windows":
		if l := os.Getenv("LocalAppData"); l != "" {
			return filepath.Join(l, "Google", "Chrome", "User Data")
		}
		return filepath.Join(home, "AppData", "Local", "Google", "Chrome", "User Data")
	default:
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			return filepath.Join(x, "google-chrome")
		}
		return filepath.Join(home, ".config", "google-chrome")
	}
}
