package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/ajai/internal/daemon"
	"github.com/luizfreitas27/ajai/internal/service"
)

// Unit is the daemon's systemd user unit, as `ajai daemon install` and
// `uninstall` handle it.
type Unit interface {
	// Status is the unit as systemd sees it.
	Status() service.Status
	// Install writes, enables and starts the unit (or reinstalls it).
	Install() (service.Installed, error)
	// Uninstall stops and disables the unit and removes it.
	Uninstall() error
	// Start starts the installed unit; Stop stops it.
	Start() error
	Stop() error
}

// unitEvery: the unit's state (systemctl, a few calls) is read this often
// while the Schedule tab is shown, not every second.
const unitEvery = 5 // refreshes

// daemonAction is what the daemon section does, once confirmed.
type daemonAction int

const (
	noDaemonAction daemonAction = iota
	installUnit
	uninstallUnit
	startDaemon
	stopDaemon
	startOrStop // the daemon's key: start when the unit is not active, stop when it is
)

// daemonKeys are the Schedule tab's keys for the daemon.
var daemonKeys = map[string]daemonAction{"i": installUnit, "u": uninstallUnit, "t": startOrStop}

// daemonSection is the Schedule tab's daemon part: whether the daemon runs
// and how its systemd unit stands, with install, uninstall, start and stop,
// each after a question that says what it does.
type daemonSection struct {
	unit      service.Status
	read      bool
	reading   bool // a read is under way: reads never overlap, so an old one never wins
	unhandled bool // there is no unit to handle from here
	asking    daemonAction
	busy      daemonAction // under way
	notice    notice
}

// daemonProc is the daemon process, as the Dashboard's refresh reads it.
type daemonProc struct {
	process daemon.Process
	up      bool
}

type unitReadMsg struct {
	status    service.Status
	unhandled bool
}

type daemonDoneMsg struct{ notice notice }

func (s daemonSection) editing() bool { return s.asking != noDaemonAction }

// unitActive says whether systemd has the daemon's unit running (or on its
// way): t then stops it.
func (s daemonSection) unitActive() bool {
	switch s.unit.Active {
	case "active", "activating", "reloading":
		return true
	}
	return false
}

// readUnit reads the unit's state, unless a read or an action is under way.
func (s daemonSection) readUnit(d Deps) (daemonSection, tea.Cmd) {
	if s.reading || s.busy != noDaemonAction {
		return s, nil
	}
	s.reading = true
	if d.Unit == nil {
		return s, func() tea.Msg { return unitReadMsg{unhandled: true} }
	}
	return s, func() tea.Msg { return unitReadMsg{status: d.Unit.Status()} }
}

// ask starts the question for a, or says why a cannot be done.
func (s daemonSection) ask(a daemonAction) daemonSection {
	if a == startOrStop {
		a = startDaemon
		if s.unitActive() {
			a = stopDaemon
		}
	}
	switch {
	case s.unhandled:
		s.notice = notice{"The daemon's systemd unit cannot be handled from here.", levelWarn}
	case s.busy != noDaemonAction:
		s.notice = notice{"Wait: the last change to the daemon is under way.", levelWarn}
	case !s.read:
		s.notice = notice{"Still reading the unit's state: try again in a moment.", levelWarn}
	case a != installUnit && !s.unit.Installed:
		s.notice = notice{"The daemon's unit is not installed: i installs it.", levelWarn}
	default:
		s.asking, s.notice = a, notice{}
	}
	return s
}

func (s daemonSection) update(msg tea.Msg, d Deps) (daemonSection, tea.Cmd) {
	switch msg := msg.(type) {
	case unitReadMsg:
		s.unit, s.read, s.reading, s.unhandled = msg.status, true, false, msg.unhandled
	case daemonDoneMsg:
		s.busy, s.notice = noDaemonAction, msg.notice
		return s.readUnit(d)
	case tea.KeyMsg:
		switch msg.String() {
		case "y":
			a := s.asking
			s.asking, s.busy = noDaemonAction, a
			return s, runDaemonAction(d, a)
		case "n", "esc":
			s.asking = noDaemonAction
		}
	}
	return s, nil
}

// runDaemonAction does a; the unit's commands can take seconds (Install and
// Start wait to see the daemon stay up), so it runs away from the screen.
func runDaemonAction(d Deps, a daemonAction) tea.Cmd {
	return func() tea.Msg {
		failed := func(what string, err error) tea.Msg {
			d.logger().Warn("could not "+what+" from the TUI", "error", err)
			return daemonDoneMsg{notice{"Could not " + what + ": " + err.Error(), levelFail}}
		}
		switch a {
		case installUnit:
			result, err := d.Unit.Install()
			if err != nil {
				return failed("install the daemon's unit", err)
			}
			d.logger().Info("daemon unit installed from the TUI", "unit", result.Path, "started", result.Started)
			n := notice{text: "Installed " + d.tilde(result.Path) + "; the daemon runs, and starts with your desktop session."}
			if !result.Started {
				n = notice{"Installed " + d.tilde(result.Path) + ", but the daemon did not start.", levelWarn}
			}
			if len(result.Warnings) > 0 {
				warnings := strings.Join(result.Warnings, "; ")
				n.level, n.text = levelWarn, n.text+" "+strings.ToUpper(warnings[:1])+warnings[1:]+"."
			}
			return daemonDoneMsg{n}
		case uninstallUnit:
			err := d.Unit.Uninstall()
			switch {
			case errors.Is(err, service.ErrNotInstalled):
				return daemonDoneMsg{notice{"The daemon's unit is not installed.", levelWarn}}
			case err != nil:
				return failed("uninstall the daemon's unit", err)
			}
			d.logger().Info("daemon unit uninstalled from the TUI")
			return daemonDoneMsg{notice{text: "Removed the daemon's unit."}}
		case startDaemon:
			if err := d.Unit.Start(); err != nil {
				return failed("start the daemon", err)
			}
			d.logger().Info("daemon started from the TUI")
			return daemonDoneMsg{notice{text: "Started the daemon."}}
		case stopDaemon:
			if err := d.Unit.Stop(); err != nil {
				return failed("stop the daemon", err)
			}
			d.logger().Info("daemon stopped from the TUI")
			return daemonDoneMsg{notice{text: "Stopped the daemon."}}
		}
		return nil
	}
}

// question is the confirmation of a.
func (s daemonSection) question(a daemonAction) string {
	switch a {
	case installUnit:
		if s.unit.Installed {
			return "Reinstall the daemon's unit for this ajai and restart it? A run it is running ends cleanly, and its window resumes."
		}
		return "Install the daemon as a systemd user unit? It starts now and with your desktop session."
	case uninstallUnit:
		return "Stop the daemon and remove its unit? Schedule windows stop opening runs."
	case startDaemon:
		return "Start the daemon now?"
	case stopDaemon:
		return "Stop the daemon? A run it started ends cleanly; schedule windows open no runs until it starts again."
	}
	return ""
}

// strip is the daemon's strip under the Schedule tab's title: the daemon and
// its unit, then its keys and the last outcome, or a question (or the change
// under way) in the keys' place.
func (s daemonSection) strip(st styles, width int, proc daemonProc) []string {
	running := st.warn.Render("not running")
	if proc.up {
		running = st.ok.Render("running") + " since " + proc.process.Started.Local().Format(dayTime)
	}
	unit := "systemd unit: reading…"
	switch {
	case !s.read:
	case s.unhandled:
		unit = "systemd unit: not handled here"
	case !s.unit.Installed:
		unit = "systemd unit: not installed"
	default:
		unit = fmt.Sprintf("systemd unit: %s, %s", s.unit.Enabled, s.unit.Active)
	}
	out := []string{st.label.Render("Daemon") + "  " + running + st.dim.Render(" · "+unit)}
	switch { // a question, or the change under way, takes the keys' line
	case s.asking != noDaemonAction:
		return append(out, wrapStyled(st.warn, s.question(s.asking), width)...)
	case s.busy != noDaemonAction:
		return append(out, st.dim.Render("Working…"))
	}
	keys := "i install · u uninstall · t start"
	if s.unitActive() {
		keys = "i install · u uninstall · t stop"
	}
	out = append(out, st.hints(keys))
	if s.notice.text != "" {
		out = append(out, wrapStyled(st.byLevel(s.notice.level), s.notice.text, width)...)
	}
	return out
}
