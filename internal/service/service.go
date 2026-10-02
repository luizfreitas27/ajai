// Package service installs `aja daemon` as a systemd user service tied to the
// desktop session, so it starts at login and the Chrome it opens is visible.
// aja targets Linux only for now; systemd is the only service manager.
package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// UnitName is the service's name.
const UnitName = "aja.service"

// Options are what the unit runs.
type Options struct {
	Binary     string // absolute path of the aja binary
	ConfigFile string // --config, "" for the default location
	Home       string // AJA_HOME, "" when unset
}

// Unit is the systemd unit for o.
func Unit(o Options) string {
	exec := quote(o.Binary) + " daemon"
	if o.ConfigFile != "" {
		exec += " --config " + quote(o.ConfigFile)
	}
	var env string
	if o.Home != "" {
		env = "Environment=" + quote("AJA_HOME="+o.Home) + "\n"
	}
	return `# Written by aja daemon install; aja daemon uninstall removes it.
[Unit]
Description=aja: applies to LinkedIn jobs on your schedule windows
Documentation=https://github.com/luizfreitas27/aja
# The Chrome window the runs open needs the desktop session.
PartOf=graphical-session.target
After=graphical-session.target

[Service]
Type=simple
` + env + `ExecStart=` + exec + `
# Stopped like Ctrl-C: the run in progress ends cleanly first.
KillSignal=SIGINT
TimeoutStopSec=3min
Restart=on-failure
RestartSec=30

[Install]
WantedBy=graphical-session.target
`
}

// quote writes a word for a unit file: % doubled, and double quotes around it
// (with \ and " escaped) when it holds a space or a quote.
func quote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// Systemctl runs `systemctl --user args...` and returns its output.
type Systemctl func(args ...string) (string, error)

// RealSystemctl is Systemctl on this machine.
func RealSystemctl(args ...string) (string, error) {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Manager installs, removes and reports the service.
type Manager struct {
	UnitDir   string // ~/.config/systemd/user
	Systemctl Systemctl
}

// UnitPath is where the unit file lives.
func (m Manager) UnitPath() string { return filepath.Join(m.UnitDir, UnitName) }

// Installed is what Install did, for the user to see.
type Installed struct {
	Path     string
	Commands []string // the systemctl commands it ran
	Warnings []string
}

// Install writes the unit (replacing an older one, e.g. after the binary moved),
// reloads systemd, enables the service for the desktop session and (re)starts it.
func (m Manager) Install(o Options) (Installed, error) {
	in := Installed{Path: m.UnitPath()}
	if strings.Contains(o.Binary, "/go-build") {
		in.Warnings = append(in.Warnings, o.Binary+" is a temporary build (go run): install aja itself first, or the service will point at a file that disappears")
	}
	if err := os.MkdirAll(m.UnitDir, 0o755); err != nil {
		return in, err
	}
	if err := os.WriteFile(in.Path, []byte(Unit(o)), 0o644); err != nil {
		return in, err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", UnitName}, {"restart", UnitName}} {
		in.Commands = append(in.Commands, "systemctl --user "+strings.Join(args, " "))
		if out, err := m.Systemctl(args...); err != nil {
			return in, fmt.Errorf("systemctl --user %s: %v %s", strings.Join(args, " "), err, out)
		}
	}
	if out, _ := m.Systemctl("is-active", "graphical-session.target"); out != "active" {
		in.Warnings = append(in.Warnings, "your session has not reached graphical-session.target, so the service will not start at login;"+
			" start it from your session's autostart instead (e.g. Hyprland: exec-once = systemctl --user start "+UnitName+")")
	}
	if env, err := m.Systemctl("show-environment"); err == nil && !hasDisplay(env) {
		in.Warnings = append(in.Warnings, "systemd's environment has neither WAYLAND_DISPLAY nor DISPLAY, so Chrome cannot open a window;"+
			" add `systemctl --user import-environment WAYLAND_DISPLAY DISPLAY` to your session's startup")
	}
	return in, nil
}

func hasDisplay(env string) bool {
	for _, line := range strings.Split(env, "\n") {
		if strings.HasPrefix(line, "WAYLAND_DISPLAY=") || strings.HasPrefix(line, "DISPLAY=") {
			return true
		}
	}
	return false
}

// ErrNotInstalled: there is no unit to remove.
var ErrNotInstalled = errors.New("the aja service is not installed")

// Uninstall stops and disables the service and removes its unit.
func (m Manager) Uninstall() ([]string, error) {
	if _, err := os.Stat(m.UnitPath()); errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInstalled
	}
	var commands []string
	run := func(args ...string) error {
		commands = append(commands, "systemctl --user "+strings.Join(args, " "))
		if out, err := m.Systemctl(args...); err != nil {
			return fmt.Errorf("systemctl --user %s: %v %s", strings.Join(args, " "), err, out)
		}
		return nil
	}
	if err := run("disable", "--now", UnitName); err != nil {
		return commands, err
	}
	if err := os.Remove(m.UnitPath()); err != nil {
		return commands, err
	}
	return commands, run("daemon-reload")
}

// Status is the service as systemd sees it.
type Status struct {
	Installed bool
	Path      string
	Enabled   string // "enabled", "disabled", ... (systemctl is-enabled)
	Active    string // "active", "inactive", "failed", ... (systemctl is-active)
}

// Status asks systemd about the service. is-enabled and is-active answer with
// a word and a non-zero exit for anything but yes; the word is what counts.
func (m Manager) Status() Status {
	st := Status{Path: m.UnitPath()}
	if _, err := os.Stat(st.Path); err != nil {
		return st
	}
	st.Installed = true
	st.Enabled, _ = m.Systemctl("is-enabled", UnitName)
	st.Active, _ = m.Systemctl("is-active", UnitName)
	return st
}
