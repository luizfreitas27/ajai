// Package service installs `aja daemon` as a systemd user unit tied to the
// desktop session, so it starts at login and the Chrome it opens is visible.
// aja targets Linux only for now; systemd is the only service manager.
package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// UnitName is the unit's name.
const UnitName = "aja.service"

// ExitAlreadyRunning is the exit status of `aja daemon` when another daemon
// runs; the unit does not restart on it (RestartPreventExitStatus).
const ExitAlreadyRunning = 3

// Options are what the unit runs.
type Options struct {
	Binary     string            // absolute path of the aja binary
	ConfigFile string            // --config, "" for the default location
	Env        map[string]string // AJA_HOME, XDG_CONFIG_HOME, XDG_DATA_HOME as the installing shell has them
}

// Unit is the systemd unit for o.
func Unit(o Options) string {
	execStart := execQuote(o.Binary) + " daemon"
	if o.ConfigFile != "" {
		execStart += " --config " + execQuote(o.ConfigFile)
	}
	keys := make([]string, 0, len(o.Env))
	for k := range o.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var env strings.Builder
	for _, k := range keys {
		env.WriteString("Environment=" + quote(k+"="+o.Env[k]) + "\n")
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
` + env.String() + `ExecStart=` + execStart + `
# Stopped like Ctrl-C: only the daemon gets SIGINT, its run ends cleanly and it
# closes the Chrome it opened; whatever is left after it exits is killed.
KillSignal=SIGINT
KillMode=mixed
TimeoutStopSec=3min
Restart=on-failure
RestartSec=30
# Another aja daemon already runs: retrying would not help.
RestartPreventExitStatus=` + fmt.Sprint(ExitAlreadyRunning) + `

[Install]
WantedBy=graphical-session.target
`
}

// quote writes a value for a unit file: % doubled (systemd specifiers), and
// double quotes around it, with \ " and newlines escaped, when it holds
// whitespace or a quote.
func quote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	if !strings.ContainsAny(s, " \t\n\"'\\") {
		return s
	}
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
	return `"` + s + `"`
}

// execQuote is quote for ExecStart, which also expands $VARIABLES.
func execQuote(s string) string { return quote(strings.ReplaceAll(s, "$", "$$")) }

// Systemctl runs `systemctl --user args...` and returns what it printed on
// stdout; a failure's error carries stderr.
type Systemctl func(args ...string) (string, error)

// RealSystemctl is Systemctl on this machine.
func RealSystemctl(args ...string) (string, error) {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
	}
	return strings.TrimSpace(string(out)), err
}

// UnitDir is where systemd --user looks for units: $XDG_CONFIG_HOME/systemd/user
// as the systemd user manager has it (managerEnv: `systemctl --user
// show-environment`), else ~/.config/systemd/user.
func UnitDir(managerEnv, home string) string {
	for _, line := range strings.Split(managerEnv, "\n") {
		if v, ok := strings.CutPrefix(line, "XDG_CONFIG_HOME="); ok && filepath.IsAbs(v) {
			return filepath.Join(v, "systemd", "user")
		}
	}
	return filepath.Join(home, ".config", "systemd", "user")
}

// Manager installs, removes and reports the unit.
type Manager struct {
	UnitDir   string
	Systemctl Systemctl
	// Settle is how long Install waits for the started unit to stay up (default 5 s).
	Settle time.Duration
}

// UnitPath is where the unit file lives.
func (m Manager) UnitPath() string { return filepath.Join(m.UnitDir, UnitName) }

// systemctl runs one command, recording it for the user, and makes its error say which.
func (m Manager) systemctl(ran *[]string, args ...string) (string, error) {
	cmd := "systemctl --user " + strings.Join(args, " ")
	*ran = append(*ran, cmd)
	out, err := m.Systemctl(args...)
	if err != nil {
		return out, fmt.Errorf("%s: %w", cmd, err)
	}
	return out, nil
}

// Installed is what Install did, for the user to see.
type Installed struct {
	Path     string
	Unit     string   // the unit file it wrote
	Commands []string // the systemctl commands it ran
	Started  bool     // the unit was (re)started and stayed up
	Warnings []string
}

// Install writes the unit (replacing an older one, e.g. after the binary
// moved), reloads systemd and enables the unit for the desktop session. In a
// desktop session it also (re)starts it and checks it stays up; outside one it
// says so and leaves the start to the next login.
func (m Manager) Install(o Options) (Installed, error) {
	result := Installed{Path: m.UnitPath(), Unit: Unit(o)}
	if strings.Contains(o.Binary, "/go-build") {
		result.Warnings = append(result.Warnings, o.Binary+" is a temporary build (go run): install aja itself first, or the unit will point at a file that disappears")
	}
	if err := os.MkdirAll(m.UnitDir, 0o755); err != nil {
		return result, err
	}
	if err := os.WriteFile(result.Path, []byte(result.Unit), 0o644); err != nil {
		return result, err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", UnitName}} {
		if _, err := m.systemctl(&result.Commands, args...); err != nil {
			return result, err
		}
	}
	if env, err := m.Systemctl("show-environment"); err == nil && !hasDisplay(env) {
		result.Warnings = append(result.Warnings, "systemd's environment has neither WAYLAND_DISPLAY nor DISPLAY, so Chrome cannot open a window;"+
			" add `systemctl --user import-environment WAYLAND_DISPLAY DISPLAY` to your session's startup")
	}
	if state, _ := m.Systemctl("is-active", "graphical-session.target"); state != "active" {
		result.Warnings = append(result.Warnings, "your session has not reached graphical-session.target, so the daemon was not started and will not start at login;"+
			" start it from your session's autostart instead (e.g. Hyprland: exec-once = systemctl --user start "+UnitName+")")
		return result, nil
	}
	if _, err := m.systemctl(&result.Commands, "restart", UnitName); err != nil {
		return result, err
	}
	if !m.staysUp() {
		return result, errors.New("the daemon did not stay up; see journalctl --user -u " + UnitName)
	}
	result.Started = true
	return result, nil
}

// staysUp reports whether the unit is active after Settle (a daemon that
// cannot start fails within a second or two).
func (m Manager) staysUp() bool {
	settle := m.Settle
	if settle == 0 {
		settle = 5 * time.Second
	}
	deadline := time.Now().Add(settle)
	for {
		state, _ := m.Systemctl("is-active", UnitName)
		if state != "active" && state != "activating" {
			return false
		}
		if !time.Now().Before(deadline) {
			return state == "active"
		}
		time.Sleep(250 * time.Millisecond)
	}
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
var ErrNotInstalled = errors.New("the aja unit is not installed")

// Uninstall stops and disables the unit and removes it.
func (m Manager) Uninstall() ([]string, error) {
	if _, err := os.Stat(m.UnitPath()); errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInstalled
	}
	var ran []string
	if _, err := m.systemctl(&ran, "disable", "--now", UnitName); err != nil {
		return ran, err
	}
	if err := os.Remove(m.UnitPath()); err != nil {
		return ran, err
	}
	_, err := m.systemctl(&ran, "daemon-reload")
	return ran, err
}

// Start starts the installed unit now and checks the daemon stays up, as
// Install does.
func (m Manager) Start() ([]string, error) {
	ran, err := m.installedCommand("start")
	if err == nil && !m.staysUp() {
		err = errors.New("the daemon did not stay up; see journalctl --user -u aja.service")
	}
	return ran, err
}

// Stop stops the installed unit now; the daemon ends like Ctrl-C, and so does
// a run it started. It starts again with the next desktop session.
func (m Manager) Stop() ([]string, error) { return m.installedCommand("stop") }

func (m Manager) installedCommand(verb string) ([]string, error) {
	if _, err := os.Stat(m.UnitPath()); errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInstalled
	}
	var ran []string
	_, err := m.systemctl(&ran, verb, UnitName)
	return ran, err
}

// Status is the unit as systemd sees it.
type Status struct {
	Installed bool
	Path      string
	Enabled   string // "enabled", "disabled", ... (systemctl is-enabled)
	Active    string // "active", "inactive", "failed", ... (systemctl is-active)
}

// Status asks systemd about the unit. is-enabled and is-active print a word
// and exit non-zero for anything but yes: the word is the answer; with no word,
// systemd could not be asked and the error says why.
func (m Manager) Status() Status {
	st := Status{Path: m.UnitPath()}
	if _, err := os.Stat(st.Path); err != nil {
		return st
	}
	st.Installed = true
	ask := func(args ...string) string {
		out, err := m.Systemctl(args...)
		if out == "" && err != nil {
			return "unknown (" + err.Error() + ")"
		}
		return out
	}
	st.Enabled, st.Active = ask("is-enabled", UnitName), ask("is-active", UnitName)
	return st
}
