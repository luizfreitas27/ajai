package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luizfreitas27/aja/internal/daemon"
	"github.com/luizfreitas27/aja/internal/service"
)

// Service is the daemon's systemd user unit, as `aja daemon install`,
// `uninstall` and systemctl handle it.
type Service interface {
	Status() service.Status
	Install() (service.Installed, error)
	Uninstall() ([]string, error)
	Start() ([]string, error)
	Stop() ([]string, error)
}

// unitEvery: the unit's state (systemctl, twice) is read this often while the
// Schedule tab is shown, not every second.
const unitEvery = 5 // refreshes

// daemonAction is what the daemon section does, once confirmed.
type daemonAction int

const (
	noDaemonAction daemonAction = iota
	installUnit
	uninstallUnit
	startDaemon
	stopDaemon
)

// daemonSection is the Schedule tab's daemon part: whether the daemon runs
// and how its systemd unit stands, with install, uninstall, start and stop,
// each after a confirmation that says what it does.
type daemonSection struct {
	unit      service.Status
	read      bool
	unhandled bool
	asking    daemonAction // waiting for y or n
	busy      daemonAction // under way
	notice    notice
}

type unitReadMsg struct {
	status    service.Status
	unhandled bool // no unit is handled from here
}

type daemonDoneMsg struct{ notice notice }

func readUnit(d Deps) tea.Cmd {
	if d.Service == nil {
		return func() tea.Msg { return unitReadMsg{unhandled: true} }
	}
	return func() tea.Msg { return unitReadMsg{status: d.Service.Status()} }
}

func (s daemonSection) editing() bool { return s.asking != noDaemonAction }

// ask starts the confirmation of a, or says why a cannot be done.
func (s daemonSection) ask(a daemonAction, d Deps) daemonSection {
	switch {
	case d.Service == nil:
		s.notice = notice{"The daemon's systemd unit cannot be handled from here.", levelWarn}
	case s.busy != noDaemonAction:
		s.notice = notice{"Wait: the last change to the daemon is under way.", levelWarn}
	case a != installUnit && s.read && !s.unit.Installed:
		s.notice = notice{"The daemon's unit is not installed: i installs it.", levelWarn}
	default:
		s.asking, s.notice = a, notice{}
	}
	return s
}

func (s daemonSection) update(msg tea.Msg, d Deps) (daemonSection, tea.Cmd) {
	switch msg := msg.(type) {
	case unitReadMsg:
		s.unit, s.read, s.unhandled = msg.status, true, msg.unhandled
	case daemonDoneMsg:
		s.busy, s.notice = noDaemonAction, msg.notice
		return s, readUnit(d)
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

// runDaemonAction does a; the unit's own commands can take seconds (Install
// waits to see the daemon stay up), so it runs away from the screen.
func runDaemonAction(d Deps, a daemonAction) tea.Cmd {
	return func() tea.Msg {
		switch a {
		case installUnit:
			result, err := d.Service.Install()
			if err != nil {
				return daemonDoneMsg{notice{"Could not install the daemon's unit: " + err.Error(), levelFail}}
			}
			d.logger().Info("daemon unit installed from the TUI", "unit", result.Path, "started", result.Started)
			n := notice{text: "Installed " + d.tilde(result.Path) + "; the daemon runs, and starts with your desktop session."}
			if !result.Started {
				n = notice{"Installed " + d.tilde(result.Path) + ", but the daemon did not start.", levelWarn}
			}
			if len(result.Warnings) > 0 {
				n.level = levelWarn
				n.text += " " + strings.Join(result.Warnings, "; ") + "."
			}
			return daemonDoneMsg{n}
		case uninstallUnit:
			_, err := d.Service.Uninstall()
			switch {
			case errors.Is(err, service.ErrNotInstalled):
				return daemonDoneMsg{notice{"The daemon's unit is not installed.", levelWarn}}
			case err != nil:
				return daemonDoneMsg{notice{"Could not uninstall the daemon's unit: " + err.Error(), levelFail}}
			}
			d.logger().Info("daemon unit uninstalled from the TUI")
			return daemonDoneMsg{notice{text: "Removed the daemon's unit; the daemon is stopped."}}
		}
		verb, do := "start", d.Service.Start
		if a == stopDaemon {
			verb, do = "stop", d.Service.Stop
		}
		if _, err := do(); err != nil {
			return daemonDoneMsg{notice{"Could not " + verb + " the daemon: " + err.Error(), levelFail}}
		}
		d.logger().Info("daemon " + verb + " from the TUI")
		done := map[string]string{"start": "Started the daemon.", "stop": "Stopped the daemon."}[verb]
		return daemonDoneMsg{notice{text: done}}
	}
}

// question is the confirmation of a.
func (s daemonSection) question(a daemonAction) string {
	switch a {
	case installUnit:
		if s.unit.Installed {
			return "Reinstall the daemon's unit for this aja and restart it? A run it is running ends cleanly, and its window resumes."
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

// lines are the section: the daemon, its unit, the keys, a question or the
// last outcome.
func (s daemonSection) lines(st styles, width int, proc daemon.Process, up bool) []string {
	running := st.warn.Render("not running")
	if up {
		running = st.ok.Render("running") + " since " + proc.Started.Local().Format(dayTime)
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
	out := []string{st.dim.Render(strings.Repeat("─", width)),
		st.section.Render("Daemon") + "  " + running + st.dim.Render(" · "+unit),
		st.dim.Render("i install · u uninstall · s start · t stop")}
	switch {
	case s.asking != noDaemonAction:
		out = append(out, wrapStyled(st.warn, s.question(s.asking), width)...)
		out = append(out, st.key.Render("y")+" does it · "+st.key.Render("n")+" leaves it")
	case s.busy != noDaemonAction:
		out = append(out, st.dim.Render("Working…"))
	case s.notice.text != "":
		out = append(out, wrapStyled(st.byLevel(s.notice.level), s.notice.text, width)...)
	}
	return out
}
