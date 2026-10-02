// Package chrome finds the Chrome or Chromium installed on this machine, the
// user's everyday profile for it, and whether a profile is signed in to
// LinkedIn, for doctor, aja login and the browser layer.
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
var ErrNotFound = errors.New("no Chrome executable was found; install Google Chrome or set settings.chrome_binary")

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

// EverydayProfileDir is where the browser at binary keeps the user's everyday
// profile on Linux: ~/.config/chromium for Chromium, ~/.config/google-chrome
// (or its -beta / -unstable sibling) for Chrome. The aja profile is
// paths.Paths.ChromeProfileDir.
func EverydayProfileDir(binary string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	name := strings.ToLower(filepath.Base(binary))
	switch {
	case strings.Contains(name, "chromium"):
		return filepath.Join(base, "chromium")
	case strings.Contains(name, "beta"):
		return filepath.Join(base, "google-chrome-beta")
	case strings.Contains(name, "unstable"), strings.Contains(name, "dev"):
		return filepath.Join(base, "google-chrome-unstable")
	default:
		return filepath.Join(base, "google-chrome")
	}
}
